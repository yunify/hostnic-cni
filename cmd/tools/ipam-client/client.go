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

func main() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&handleID, "handleID", "", "handleID")
	flag.StringVar(&pool, "pool", "", "pool name")
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

	var args ipam.GetUtilizationArgs
	if pool != "" {
		args.Pools = []string{pool}
	}
	utils, err := ipamClient.GetPoolBlocksUtilization(args)
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

	// GetSubnets
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
