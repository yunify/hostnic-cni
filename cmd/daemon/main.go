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
	"github.com/yunify/hostnic-cni/pkg/types"
	"os"
	"os/signal"
	"syscall"

	"github.com/coreos/go-systemd/daemon"
	"github.com/yunify/hostnic-cni/pkg/ipam"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

func init() {
	klog.InitFlags(nil)
	flag.Parse()
}

func main() {
	stopCh := make(chan struct{})
	stopSignal := make(chan os.Signal)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	daemon.SdNotify(false, "READY=1")
	go func() {
		defer close(stopSignal)
		for range stopSignal {
			stopCh <- struct{}{}
		}
	}()

	var err error
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to get k8s config, err:%v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to get k8s clientset, err:%v", err)
	}
	err = ipam.Start(clientset, types.StopCh)
	if err != nil {
		klog.Fatalf("Failed to start ipamd, err:%v", err)
	}

	select {
	case <- stopCh:
		klog.Info("Daemon exit")
		os.Exit(0)
	}
}
