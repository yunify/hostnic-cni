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
	"os"
	"os/signal"
	"syscall"

	"github.com/coreos/go-systemd/daemon"
	"github.com/yunify/hostnic-cni/pkg/ipam"
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
	klog.V(1).Infoln("Starting IPAMD")
	ipamd := ipam.NewIpamD()
	err := ipamd.StartIPAMD(stopCh)
	if err != nil {
		klog.Fatalf("Failed to start ipamd, err: %s", err.Error())
	}
	go ipamd.StartReconcileIPPool(stopCh)
	klog.V(1).Infoln("Starting Grpc server")
	err = ipamd.StartGrpcServer()
	if err != nil {
		klog.Fatalf("Failed to start grpc server, err: %s", err.Error())
	}
	select {}
}
