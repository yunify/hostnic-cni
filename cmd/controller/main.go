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
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	"github.com/yunify/hostnic-cni/pkg/controller"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/signals"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool"
)

var (
	masterURL  string
	kubeconfig string
)

func main() {
	var vxnetPool string
	flag.StringVar(&vxnetPool, "vxnet-pool", "qingcloud-pool", "This field instructs vxnetpool name.")
	klog.InitFlags(nil)
	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Parse()

	qcclient.SetupQingCloudClient(qcclient.Options{})

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
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
	informerFactory := informers.NewSharedInformerFactory(client, time.Second*30)

	c1 := controller.NewVxNetPoolController(kubeClient, client, vxnetPool,
		informerFactory.Network().V1alpha1().IPPools(),
		informerFactory.Vxnetpool().V1alpha1().VxNetPools())

	c2 := controller.NewIPPoolController(kubeClient, client, informerFactory, kubeInformerFactory, ippool.NewProvider(client, networkv1alpha1.IPPoolTypeLocal))

	// notice that there is no need to run Start methods in a separate goroutine. (i.e. go kubeInformerFactory.Start(stopCh)
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	kubeInformerFactory.Start(stopCh)
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

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
