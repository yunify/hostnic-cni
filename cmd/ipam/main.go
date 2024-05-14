//
// =========================================================================
// Copyright (C) 2017 by Yunify, Inc...
// -------------------------------------------------------------------------
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this work except in compliance with the License.
// You may obtain a copy of the License in the LICENSE file, or at:
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// =========================================================================
//

package main

import (
	"flag"
	"time"

	k8sinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	log "k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/allocator"
	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/config"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/db"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/server"
	"github.com/yunify/hostnic-cni/pkg/signals"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

var qps, burst, metricsPort int

func main() {
	//parse flag and setup klog
	log.InitFlags(flag.CommandLine)
	flag.IntVar(&qps, "k8s-api-qps", 80, "maximum QPS to k8s apiserver from this client.")
	flag.IntVar(&burst, "k8s-api-burst", 100, "maximum burst for throttle from this client.")
	flag.IntVar(&metricsPort, "metrics-port", 9191, "metrics port")
	dbOpts := db.NewLevelDBOptions()
	dbOpts.AddFlags()
	flag.Parse()
	db.SetupLevelDB(dbOpts)
	defer func() {
		db.CloseDB()
	}()

	// set up signals so we handle the first shutdown signals gracefully
	stopCh := signals.SetupSignalHandler()

	// load ipam server config
	conf, err := conf.TryLoadIpamConfFromDisk(constants.DefaultConfigName, constants.DefaultConfigPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	log.Infof("hostnic config is %v", conf)

	// setup qcclient, k8s
	qcclient.SetupQingCloudClient(qcclient.Options{
		Tag: conf.Pool.Tag,
	})

	cfg, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		log.Fatalf("Error building kubeconfig: %v", err)
	}

	cfg.QPS = float32(qps)
	cfg.Burst = burst
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Error building kubernetes clientset: %v", err)
	}

	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Error building example clientset: %v", err)
	}

	k8sInformerFactory := k8sinformers.NewSharedInformerFactory(k8sClient, time.Second*30)
	informerFactory := informers.NewSharedInformerFactory(client, time.Second*30)

	clusterConfig := config.NewClusterConfig(k8sInformerFactory.Core().V1().ConfigMaps())
	ipamClient := ipam.NewIPAMClient(client, networkv1alpha1.IPPoolTypeLocal, informerFactory, k8sInformerFactory)

	k8sInformerFactory.Start(stopCh)
	informerFactory.Start(stopCh)

	if err = clusterConfig.Sync(stopCh); err != nil {
		log.Fatalf("clusterConfig sync error: %v", err)
	}

	if err = ipamClient.Sync(stopCh); err != nil {
		log.Fatalf("ipamclient sync error: %v", err)
	}

	networkutils.SetupNetworkHelper()
	allocator.SetupAllocator(conf.Pool)

	log.Info("all setup done, startup daemon")
	allocator.Alloc.Start(stopCh)
	server.NewIPAMServer(conf.Server, clusterConfig, k8sClient, ipamClient, metricsPort).Start(stopCh)

	<-stopCh
	log.Info("daemon exited")
}
