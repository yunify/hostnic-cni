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
	"fmt"

	"github.com/spf13/cobra"
	"github.com/yunify/hostnic-cni/pkg"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Version number of hostnic plugin",
	Long: `hostnic-cni is a Container Network Interface plugin.

This plugin will create a new nic by IaaS api and attach to host,
then move the nic to container network namespace`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("VERSION: %s\n", pkg.VERSION)
		fmt.Printf("GIT_SHA1: %s\n", pkg.GIT_SHA1)
		fmt.Printf("BUILD_LABEL: %s\n", pkg.BUILD_LABEL)
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// versionCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// versionCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
