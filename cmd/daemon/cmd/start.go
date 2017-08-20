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
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/pkg/messages"
	"github.com/yunify/hostnic-cni/pkg/server"
	"google.golang.org/grpc"
	"net"
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {

		//setup nic generator
		nicGeneratorFunc := func() (*pkg.HostNic, error) {
			return nil, nil
		}
		//setup nic pool
		nicpool := server.NewNicPool(viper.GetInt("PoolSize"), nicGeneratorFunc)

		//setup

		//start up server rpc routine
		listener, err := net.Listen(viper.GetString("bindType"), viper.GetString("bindAddr"))
		if err != nil {
			log.Errorf("Failed to listen to assigned port, %v", err)
			return
		}
		grpcServer := grpc.NewServer()
		messages.RegisterNicservicesServer(grpcServer, server.NewDaemonServer(nicpool))
		grpcServer.Serve(listener)

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
	startCmd.Flags().String("bindAddr", ":31080", "port of daemon process(e.g. socket port 127.0.0.1:31080 [fe80::1%lo0]:80 unix port /var/run/daemon/port")
	startCmd.Flags().String("bindType", "tcp", "Type of socket, tcp and unix are supported")

	//sdk properties
	startCmd.Flags().String("QyAccessFilePath", "/etc/qingcloud/client.yaml", "Path of QingCloud Access file")

	//pool properties
	startCmd.Flags().Int("PoolSize", 3, "The size of nic pool")
	viper.BindPFlags(startCmd.Flags())
}
