package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"google.golang.org/grpc"
	log "k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

func usage() {
	fmt.Println("This tool is used to display the hostnics of the current node and manually remove the unused hostnics from the iaas.")
	fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
	fmt.Println("\t./tool")
	fmt.Println("\t./tool -clear true")
}

func main() {
	var clear bool
	flag.BoolVar(&clear, "clear", false, "clear free hostnics")
	flag.Usage = usage
	flag.Parse()

	conn, err := grpc.Dial(constants.DefaultUnixSocketPath, grpc.WithInsecure())
	if err != nil {
		log.Infof("failed to connect server, err=%v", err)
		return
	}
	defer conn.Close()

	client := rpc.NewCNIBackendClient(conn)
	result, err := client.ShowNics(context.Background(), &rpc.Nothing{})

	log.Info("********************* current node nics *********************")
	for _, nic := range result.Items {
		log.Infof("id:%s, vxnet:%s, phase:%s, status:%s, pods:%d", nic.Id, nic.Vxnet, nic.Phase, nic.Status, nic.Pods)
	}

	if clear {
		_, err = client.ClearNics(context.Background(), &rpc.Nothing{})
		log.Infof("ClearNics error:%v", err)
	}
}
