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
	fmt.Println("This tool is used to display the hostnics of the current node and manually remove the unused hostnics from the iaas. If add force, it will remove all hostnics.")
	fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
	fmt.Println("\t./hostnic-client")
	fmt.Println("\t./hostnic-client -clear true")
	fmt.Println("\t./hostnic-client -clear true -force true")
}

func main() {
	var clear, force bool
	flag.BoolVar(&clear, "clear", false, "clear free hostnics")
	flag.BoolVar(&force, "force", false, "force clear all hostnics, be careful, it will remove all hostnics, including the hostnics that are in use")
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
		fmt.Printf("%s %s %s %s %d\n\n", nic.Vxnet, nic.Id, nic.Phase, nic.Status, nic.Pods)
	}

	if clear {
		ctx := context.Background()
		forceKey := constants.ForceKey("force")
		ctx = context.WithValue(ctx, forceKey, force)
		if _, err := client.ClearNics(ctx, &rpc.Nothing{}); err != nil {
			fmt.Printf("ClearNics failed: %v\n", err)
		} else {
			fmt.Printf("ClearNics OK\n\n")
		}

		// show again
		result, err := client.ShowNics(context.Background(), &rpc.Nothing{})
		if err != nil {
			fmt.Printf("failed to get nics: %v\n", err)
			return
		}

		fmt.Println("-------------------- after clear node nics --------------------")
		for _, nic := range result.Items {
			fmt.Printf("%s %s %s %s %d\n\n", nic.Vxnet, nic.Id, nic.Phase, nic.Status, nic.Pods)
		}
	}
}
