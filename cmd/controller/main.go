/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	goflag "flag"
	"sync"
	"time"

	flag "github.com/spf13/pflag"
	k8sinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/controller"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/signals"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool"
)

var qps, burst int

func main() {
	klog.InitFlags(goflag.CommandLine)
	goflag.Set("logtostderr", "false")
	goflag.Set("alsologtostderr", "true")
	flag.IntVar(&qps, "k8s-api-qps", 80, "maximum QPS to k8s apiserver from this client.")
	flag.IntVar(&burst, "k8s-api-burst", 100, "maximum burst for throttle from this client.")
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Parse()

	qcclient.SetupQingCloudClient(qcclient.Options{})

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	clusterConfig, err := conf.TryLoadClusterConfFromDisk(constants.DefaultClusterConfigPath)
	if err != nil {
		klog.Fatalf("Error building clusterConfig: %s", err.Error())
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	cfg.QPS = float32(qps)
	cfg.Burst = burst
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building example clientset: %s", err.Error())
	}

	k8sInformerFactory := k8sinformers.NewSharedInformerFactory(k8sClient, time.Second*30)
	informerFactory := informers.NewSharedInformerFactory(client, time.Second*30)

	c1 := controller.NewVxNetPoolController(clusterConfig, k8sClient, client,
		informerFactory.Network().V1alpha1().IPPools(), informerFactory.Network().V1alpha1().VxNetPools())

	c2 := controller.NewIPPoolController(k8sClient, client,
		k8sInformerFactory, informerFactory, ippool.NewProvider(client, networkv1alpha1.IPPoolTypeLocal))

	// notice that there is no need to run Start methods in a separate goroutine. (i.e. go kubeInformerFactory.Start(stopCh)
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	k8sInformerFactory.Start(stopCh)
	informerFactory.Start(stopCh)

	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		if err = c1.Run(2, stopCh); err != nil {
			klog.Errorf("Error running controller: %s", err.Error())
			wg.Done()
		}
	}()

	go func() {
		if err = c2.Run(2, stopCh); err != nil {
			klog.Errorf("Error running controller: %s", err.Error())
			wg.Done()
		}
	}()

	wg.Wait()
	klog.Fatalf("Error running controller")
}
