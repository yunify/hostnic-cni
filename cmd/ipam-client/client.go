package main

import (
	"flag"
	"fmt"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
	"k8s.io/client-go/tools/clientcmd"
)

var handleID, pool, masterURL, kubeconfig string

func main() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&handleID, "handleID", "", "handleID")
	flag.StringVar(&pool, "pool", "", "pool name")
	flag.Parse()

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Printf("Error building kubeconfig: %v", err)
		return
	}

	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		fmt.Printf("Error building example clientset: %v", err)
		return
	}

	c := ipam.NewIPAMClient(client, networkv1alpha1.IPPoolTypeLocal)

	if handleID != "" {
		if err := c.ReleaseByHandle(handleID); err != nil {
			fmt.Printf("Release %s failed: %v\n", handleID, err)
		} else {
			fmt.Printf("Release %s OK\n", handleID)
		}
	}

	var args ipam.GetUtilizationArgs
	if pool != "" {
		args.Pools = []string{pool}
	}
	if utils, err := c.GetPoolBlocksUtilization(args); err != nil {
		fmt.Printf("GetUtilization failed: %v\n", err)
	} else {
		fmt.Printf("GetUtilization:\n")
		for _, util := range utils {
			fmt.Printf("\t%s: Capacity %3d Unallocated %3d Allocate %3d Reserved %3d\n",
				util.Name, util.Capacity, util.Unallocated, util.Allocate, util.Reserved)
			for _, block := range util.Blocks {
				fmt.Printf("\t%24s: Capacity %3d Unallocated %3d Allocate %3d Reserved %3d\n",
					block.Name, block.Capacity, block.Unallocated, block.Allocate, block.Reserved)
			}
		}
	}
}
