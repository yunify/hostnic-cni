package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"google.golang.org/grpc"

	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

func usage() {
	fmt.Println("This tool is used to display the hostnics of the current node and manually remove the unused hostnics from the iaas.")
	fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
	fmt.Println("\t./hostnic-client")
	fmt.Println("\t./hostnic-client -clear true")
}

func main() {
	var clear bool
	flag.BoolVar(&clear, "clear", false, "clear free hostnics")
	flag.Usage = usage
	flag.Parse()

	conn, err := grpc.Dial(constants.DefaultUnixSocketPath, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("failed to connect ipam: %v\n", err)
		return
	}
	defer conn.Close()

	client := rpc.NewCNIBackendClient(conn)
	result, err := client.ShowNics(context.Background(), &rpc.Nothing{})
	if err != nil {
		fmt.Printf("failed to get nics: %v\n", err)
		return
	}

	fmt.Println("********************* current node nics *********************")
	for _, nic := range result.Items {
		fmt.Printf("%s %s %s %s %d\n", nic.Vxnet, nic.Id, nic.Phase, nic.Status, nic.Pods)
	}

	if clear {
		if _, err := client.ClearNics(context.Background(), &rpc.Nothing{}); err != nil {
			fmt.Printf("ClearNics failed: %v\n", err)
		} else {
			fmt.Printf("ClearNics OK\n")
		}
	}
}
