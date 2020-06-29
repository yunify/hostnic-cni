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
	"github.com/yunify/hostnic-cni/pkg/allocator"
	"github.com/yunify/hostnic-cni/pkg/db"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/server"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/k8s"
	"github.com/yunify/hostnic-cni/pkg/log"
	"github.com/yunify/hostnic-cni/pkg/signals"
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
	k8s.SetupK8sHelper()
	networkutils.SetupNetworkHelper()
	allocator.SetupAllocator(conf.Pool)

	// add daemon
	k8s.K8sHelper.Mgr.Add(allocator.Alloc)
	k8s.K8sHelper.Mgr.Add(server.NewIPAMServer(conf.Server))

	logrus.Info("all setup done, startup daemon")
	if err := k8s.K8sHelper.Mgr.Start(stopCh); err != nil {
		logrus.WithError(err).Errorf("failed to start daemon")
		os.Exit(1)
	}
	logrus.Info("daemon exited")
}
