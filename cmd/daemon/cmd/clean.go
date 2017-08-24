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
	"fmt"

	"github.com/spf13/cobra"
	"github.com/yunify/hostnic-cni/pkg/messages"
	"google.golang.org/grpc"
	"github.com/spf13/viper"
	log "github.com/sirupsen/logrus"

	"context"
)

// cleanCmd represents the clean command
var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean up nic cache",
	Long: `hostnic-cni is a Container Network Interface plugin.

This plugin will create a new nic by IaaS api and attach to host,
then move the nic to container network namespace`,
	Run: func(cmd *cobra.Command, args []string) {
		conn, err := grpc.Dial(viper.GetString("manageAddr"), grpc.WithInsecure())
		if err != nil {
			log.Error(fmt.Errorf("Failed to open socket %v", err))
			return
		}
		defer conn.Close()
		client := messages.NewManagementClient(conn)
		client.CleanUpNic(context.Background(),&messages.CleanUpRequest{})
	},
}

func init() {
	RootCmd.AddCommand(cleanCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// cleanCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// cleanCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
