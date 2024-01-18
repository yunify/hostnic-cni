package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/yunify/hostnic-cni/pkg/signals"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
)

var handleID, pool, masterURL, kubeconfig string
var listBrokenBlocks, fixBrokenBlocks, debug bool

func main() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&handleID, "handleID", "", "handleID")
	flag.StringVar(&pool, "pool", "", "pool name")
	flag.BoolVar(&listBrokenBlocks, "lb", false, "whether to list broken ipamblocks")
	flag.BoolVar(&fixBrokenBlocks, "fb", false, "whether to fix broken ipamblocks")
	flag.BoolVar(&debug, "d", false, "show debug info for broken ipamblocks")
	flag.Parse()

	// set up signals so we handle the first shutdown signals gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Printf("Error building kubeconfig: %v", err)
		return
	}

	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Printf("Error building kubernetes clientset: %v", err)
		return
	}

	client, err := clientset.NewForConfig(cfg)
	if err != nil {
		fmt.Printf("Error building example clientset: %v", err)
		return
	}

	k8sInformerFactory := k8sinformers.NewSharedInformerFactory(k8sClient, time.Second*10)
	informerFactory := informers.NewSharedInformerFactory(client, time.Second*30)

	ipamClient := ipam.NewIPAMClient(client, networkv1alpha1.IPPoolTypeLocal, informerFactory, k8sInformerFactory)

	k8sInformerFactory.Start(stopCh)
	informerFactory.Start(stopCh)

	if err = ipamClient.Sync(stopCh); err != nil {
		fmt.Printf("ipamclient sync error: %v", err)
		return
	}

	// c := ipam.NewIPAMClient(client, networkv1alpha1.IPPoolTypeLocal, informerFactory)

	if handleID != "" {
		if err := ipamClient.ReleaseByHandle(handleID); err != nil {
			fmt.Printf("Release %s failed: %v\n", handleID, err)
		} else {
			fmt.Printf("Release %s OK\n", handleID)
		}
	}

	// Get all ippool and ipamblocks
	// Get and fix broken ipamblock here
	var args ipam.GetUtilizationArgs
	var utils []*ipam.PoolBlocksUtilization

	if pool != "" {
		args.Pools = []string{pool}
	}
	utils, err = ipamClient.GetPoolBlocksUtilization(args)
	if err != nil {
		fmt.Printf("GetUtilization failed: %v\n", err)
		return
	} else {
		fmt.Printf("GetUtilization:\n")
		for _, util := range utils {
			fmt.Printf("\t%s: Capacity %3d, Unallocated %3d, Allocate %3d, Reserved %3d\n",
				util.Name, util.Capacity, util.Unallocated, util.Allocate, util.Reserved)
			for _, block := range util.Blocks {
				fmt.Printf("\t%24s: Capacity %3d, Unallocated %3d, Allocate %3d, Reserved %3d\n",
					block.Name, block.Capacity, block.Unallocated, block.Allocate, block.Reserved)
			}
		}
	}

	// Get configmap
	cm, err := k8sClient.CoreV1().ConfigMaps(constants.IPAMConfigNamespace).Get(context.TODO(), constants.IPAMConfigName, metav1.GetOptions{})
	if err != nil {
		fmt.Printf("GetSubnets failed: %v\n", err)
		return
	}

	var apps map[string][]string
	if err := json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &apps); err != nil {
		fmt.Printf("GetSubnets failed: %v\n", err)
		return
	}

	// GetSubnets
	autoSign := "off"
	if cm.Data[constants.IPAMAutoAssignForNamespace] == "on" {
		autoSign = "on"
	}
	fmt.Printf("GetSubnets: autoSign[%s]\n", autoSign)
	for ns, subnets := range apps {
		if ns != constants.IPAMDefaultPoolKey || autoSign == "off" {
			fmt.Printf("\t%s: %v\n", ns, subnets)
		}
	}
	fmt.Printf("FreeSubnets: %v\n", getFreeSubnets(autoSign, apps, utils))

	// broken blocks
	if listBrokenBlocks || fixBrokenBlocks {
		utils, err = ipamClient.GetAndFixBrokenBlocks(args, fixBrokenBlocks)
		if err != nil {
			fmt.Printf("GetAndFixBrokenBlocks failed: %v\n", err)
			return
		}
		fmt.Printf("\nBroken blocks for each ippool:\n")
		for _, ippool := range utils {
			fmt.Printf("\t%s: %v\n", ippool.Name, ippool.BrokenBlockNames)
			if debug {
				// print debug msg
				for _, block := range ippool.BrokenBlocks {
					for ipStr, podNames := range block.IpToPods {
						if len(podNames) > 1 {
							fmt.Printf("\t\tip %s was allocated more than noce to pods %v\n", ipStr, podNames)
						}
					}

					for ipStr, podNames := range block.IpWithoutRecord {
						fmt.Printf("\t\tip %s was allocated to pods %v, but not record in ipamblock\n", ipStr, podNames)
					}

					// for ipStr, podNames := range block.IpToPodsWithWrongRecord {
					// 	if len(podNames) == 1 {
					// 		fmt.Printf("\t\tip %s was recorded to wrong pods %v\n", ipStr, podNames)
					// 	}
					// }
				}
			}
			if fixBrokenBlocks {
				fmt.Printf("\tfix success: %v\n", ippool.BrokenBlockFixSucceed)
			}
			fmt.Println()
		}

	}
}

func getFreeSubnets(autoSign string, apps map[string][]string, ippool []*ipam.PoolBlocksUtilization) []string {
	all := make(map[string]struct{})
	for _, pool := range ippool {
		for _, block := range pool.Blocks {
			all[block.Name] = struct{}{}
		}
	}

	for ns, subnets := range apps {
		if ns == constants.IPAMDefaultPoolKey {
			if autoSign != "on" {
				for _, pool := range ippool {
					if contains(subnets, pool.Name) {
						for _, block := range pool.Blocks {
							delete(all, block.Name)
						}
					}
				}
			}
		} else {
			for _, subnet := range subnets {
				delete(all, subnet)
			}
		}
	}

	free := []string{}
	for subnet := range all {
		free = append(free, subnet)
	}
	return free
}

func contains(items []string, item string) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}
