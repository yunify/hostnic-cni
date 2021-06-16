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

	"github.com/sirupsen/logrus"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/allocator"
	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/config"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/db"
	"github.com/yunify/hostnic-cni/pkg/log"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/server"
	"github.com/yunify/hostnic-cni/pkg/signals"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

func main() {
	//parse flag and setup logrus
	logOpts := log.NewLogOptions()
	logOpts.AddFlags()
	dbOpts := db.NewLevelDBOptions()
	dbOpts.AddFlags()
	flag.Parse()
	db.SetupLevelDB(dbOpts)
	defer func() {
		db.CloseDB()
	}()
	log.Setup(logOpts)

	// set up signals so we handle the first shutdown signals gracefully
	stopCh := signals.SetupSignalHandler()

	// load ipam server config
	conf, err := conf.TryLoadFromDisk(constants.DefaultConfigName, constants.DefaultConfigPath)
	if err != nil {
		logrus.WithError(err).Fatalf("failed to load config")
	}
	logrus.Infof("hostnic config is %v", conf)

	// setup qcclient, k8s
	qcclient.SetupQingCloudClient(qcclient.Options{
		Tag: conf.Pool.Tag,
	})

	cfg, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building example clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
	clusterConfig := config.NewClusterConfig(kubeInformerFactory.Core().V1().ConfigMaps())
	kubeInformerFactory.Start(stopCh)
	clusterConfig.Sync(stopCh)

	networkutils.SetupNetworkHelper()
	allocator.SetupAllocator(conf.Pool)

	logrus.Info("all setup done, startup daemon")
	allocator.Alloc.Start(stopCh)
	server.NewIPAMServer(conf.Server, clusterConfig, ipam.NewIPAMClient(client, networkv1alpha1.IPPoolTypeLocal)).Start(stopCh)

	<-stopCh
	logrus.Info("daemon exited")
}
