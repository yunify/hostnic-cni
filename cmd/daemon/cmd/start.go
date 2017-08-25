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
	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/yunify/hostnic-cni/pkg/messages"
	"github.com/yunify/hostnic-cni/pkg/provider/qingcloud"
	"github.com/yunify/hostnic-cni/pkg/server"
	"google.golang.org/grpc"
	"net/http"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
		resourceStub, err := qingcloud.NewQCNicProvider(viper.GetString("QyAccessFilePath"), viper.GetStringSlice("vxnets"))
		if err != nil {
			log.Errorf("Failed to initiate resource provider, %v", err)
			return
		}

		//setup nic pool
		nicpool, err := server.NewNicPool(viper.GetInt("PoolSize"), server.NewQingCloudNicProvider(resourceStub),server.NewGatewayManager(resourceStub),server.NicPoolConfig{CleanUpCache:viper.GetBool("CleanUpCacheOnExit")})
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
		grpcServer := grpc.NewServer(
			grpc.UnaryInterceptor(grpc_prometheus.UnaryServerInterceptor),
		)
		handlers:= server.NewDaemonServerHandler(nicpool)
		messages.RegisterNicservicesServer(grpcServer,handlers)
		grpc_prometheus.Register(grpcServer)
		http.Handle("/metrics", promhttp.Handler())
		http.HandleFunc("/clearcache", func(writer http.ResponseWriter, request *http.Request) {
			nicpool.CleanUpReadyPool()
			writer.Header().Set("Content-Type", "text/plain")
			writer.Write([]byte("Nic ready pool is cleared .\n"))
		})
		http.HandleFunc("/shutdown", func(writer http.ResponseWriter, request *http.Request) {
			syscall.Kill(syscall.Getpid(),syscall.SIGTERM)
			writer.Header().Set("Content-Type", "text/plain")
			writer.Write([]byte("terminate signal is sent .\n"))
		})


		go grpcServer.Serve(listener)
		go http.ListenAndServe(viper.GetString("manageAddr"),nil)
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


	//sdk properties
	startCmd.Flags().String("QyAccessFilePath", "/etc/qingcloud/client.yaml", "Path of QingCloud Access file")
	startCmd.Flags().StringSlice("vxnets", []string{}, "ids of vxnet")

	//pool properties
	startCmd.Flags().Int("PoolSize", 3, "The size of nic pool")
	startCmd.Flags().Bool("CleanUpCacheOnExit",false,"Delete cached nic on exit")
	viper.BindPFlags(startCmd.Flags())
}
