// Copyright © 2017 NAME HERE <EMAIL ADDRESS>
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/yunify/hostnic-cni/pkg/messages"
	"github.com/yunify/hostnic-cni/pkg/provider/qingcloud"
	"github.com/yunify/hostnic-cni/pkg/server"
	"google.golang.org/grpc"
)

const (
	gracefulTimeout = 120 * time.Second
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start daemon process",
	Long: `hostnic-cni is a Container Network Interface plugin.

This plugin will create a new nic by IaaS api and attach to host,
then move the nic to container network namespace`,
	Run: func(cmd *cobra.Command, args []string) {
		resourceProvider, err := qingcloud.NewQCNicProvider(viper.GetString("QyAccessFilePath"), viper.GetStringSlice("vxnets"))
		if err != nil {
			log.Errorf("Failed to initiate resource provider, %v", err)
			return
		}

		//setup nic pool
		nicpool, err := server.NewNicPool(viper.GetInt("PoolSize"), resourceProvider)
		if err != nil {
			log.Errorf("Failed to create pool. %v", err)
			return
		}
		//start up server rpc routine
		listener, err := net.Listen("tcp", viper.GetString("bindAddr"))
		if err != nil {
			log.Errorf("Failed to listen to assigned port, %v", err)
			return
		}
		grpcServer := grpc.NewServer()
		messages.RegisterNicservicesServer(grpcServer, server.NewDaemonServerHandler(nicpool))
		go grpcServer.Serve(listener)

		signalCh := make(chan os.Signal, 4)
		signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		var sig os.Signal

	WAIT:
		select {
		case s := <-signalCh:
			sig = s
		}

		if sig == syscall.SIGHUP {
			//TODO implement refresh config logic
			goto WAIT
		}
		//Attempt a graceful shutdown

		log.Infof("Got interrupt call, graceful shutdown...")
		gracefulCh := make(chan struct{})
		go func() {
			log.Infof("Shutdown grpc server")
			grpcServer.GracefulStop()
			log.Infof("Shutdown nic pool server")
			nicpool.ShutdownNicPool()
			close(gracefulCh)
		}()

		select {
		case <-signalCh:
			return
		case <-time.After(gracefulTimeout):
			return
		case <-gracefulCh:
			return
		}
	},
}

func init() {
	RootCmd.AddCommand(startCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// startCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// startCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	//server routine properties
	startCmd.Flags().String("bindAddr", ":31080", "port of daemon process(e.g. socket port 127.0.0.1:31080 [fe80::1%lo0]:80 )")

	//sdk properties
	startCmd.Flags().String("QyAccessFilePath", "/etc/qingcloud/client.yaml", "Path of QingCloud Access file")
	startCmd.Flags().StringSlice("vxnets", []string{}, "ids of vxnet")

	//pool properties
	startCmd.Flags().Int("PoolSize", 3, "The size of nic pool")
	viper.BindPFlags(startCmd.Flags())
}
