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
var listBrokenBlocks, fixBrokenBlocks, debug, missingHandles, misMatchedHandles bool
var releaseip, recordip, fixip string

func main() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&handleID, "handleID", "", "release ip by handleID")
	flag.StringVar(&releaseip, "releaseip", "", "release specified leak ip, if ip was used by exesting pod, just return error and do nothing")
	flag.StringVar(&recordip, "recordip", "", "allocated an used ip in ipamblock, if ip was not used by any pod or alread allocated in ipamblock, just return error and do nothing")
	// flag.StringVar(&fixip, "fixip", "", "fix an used ip in ipamblock, if ip attribute correct, just return and do nothing")

	flag.StringVar(&pool, "pool", "", "pool name")
	flag.BoolVar(&listBrokenBlocks, "lb", false, "whether to list broken ipamblocks")
	flag.BoolVar(&fixBrokenBlocks, "fb", false, "whether to fix all broken ipamblocks")
	flag.BoolVar(&debug, "d", false, "show debug info for broken ipamblocks")
	flag.BoolVar(&missingHandles, "missingHandles", false, "show used ipamhandle missing info, this issue no need to fix")
	flag.BoolVar(&misMatchedHandles, "misMatchedHandles", false, "show mismatched ipamhandle info, this issue no need to fix")

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
			fmt.Printf("Release %s success\n", handleID)
		}
		return
	}

	//release ip and return
	if releaseip != "" {
		if err := ipamClient.ReleaseLeakIP(releaseip, "", false); err != nil {
			fmt.Printf("Release ip %s failed: %v\n", releaseip, err)
		} else {
			fmt.Printf("Release ip %s success\n", releaseip)
		}
		return
	}

	//record ip and return
	if recordip != "" {
		if err := ipamClient.RecordUsedIP(recordip, ipam.UsedIPOption{}, false); err != nil {
			fmt.Printf("Record ip %s failed: %v\n", recordip, err)
		} else {
			fmt.Printf("Record ip %s success\n", recordip)
		}
		return
	}

	//TODO:record ip and return; if record already exists, check if the attribute is correct
	if fixip != "" {
		if err := ipamClient.RecordUsedIP(recordip, ipam.UsedIPOption{}, true); err != nil {
			fmt.Printf("Record ip %s failed: %v\n", recordip, err)
		} else {
			fmt.Printf("Record ip %s success\n", recordip)
		}
		return
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
	var blockUtils []*ipam.PoolBlocksUtilization
	if listBrokenBlocks || fixBrokenBlocks {
		blockUtils, err = ipamClient.GetBrokenBlocks(args, missingHandles, misMatchedHandles)
		if err != nil {
			fmt.Printf("GetAndFixBrokenBlocks failed: %v\n", err)
			return
		}
		fmt.Printf("\nBroken blocks for each ippool:\n")
		for _, ippool := range blockUtils {
			if len(ippool.BrokenBlocks) == 0 {
				continue
			}

			fmt.Printf("\t%s: %v\n", ippool.Name, ippool.BrokenBlockNames)
			if debug {
				// print debug msg
				for _, block := range ippool.BrokenBlocks {
					fmt.Printf("\tblock: %s\n", block.Name)
					printTitle := true
					for ipStr, podNames := range block.IpToPods {
						if len(podNames) > 1 {
							if printTitle {
								fmt.Println("\t\tip was allocated to multiple pods, shoule check and recreate these pods manually:")
								printTitle = false
							}
							fmt.Printf("\t\tip %s was allocated more than one pods: %v\n", ipStr, podNames)
						}
					}

					if len(block.IpNotAllocExistsPod) > 0 {
						fmt.Println("\t\tip was allocated to pod, but not record in ipamblock:")
						for ipStr, item := range block.IpNotAllocExistsPod {
							msg := fmt.Sprintf("\t\tip %s was allocated to pod %s/%s, but not record in ipamblock", ipStr, item.PodNamespace, item.PodName)
							if item.HandleID == "" {
								msg = fmt.Sprintf("%s, and can not found ipamhandle for the pod", msg)
							}
							fmt.Println(msg)
						}
					}

					if len(block.IpAllocNotExistsPod) > 0 {
						fmt.Println("\t\tip was allocated in ipamblock, but pod not exists:")
						for ipStr, podName := range block.IpAllocNotExistsPod {
							fmt.Printf("\t\tip %s was allocated to %s, but pod not exists\n", ipStr, podName)
						}
					}

					if missingHandles {
						if len(block.UsedHandlesMissing) > 0 {
							fmt.Println("\t\tipamhandle was used in ipamblock, but ipamhandle resource not exists, this issue has no effect, ignore:")
							for ipStr, handleID := range block.UsedHandlesMissing {
								fmt.Printf("\t\tipamhandle %s was used by ip %s in ipamblock, but ipamhandle resource not exists\n", handleID, ipStr)
							}
						}
					}
					if misMatchedHandles {
						if len(block.IpAllocRecordNotMatch) > 0 {
							fmt.Println("\t\tip was allocated to one pod in ipamblock, but used by another pod, this issue has no effect, ignore:")
							for ipStr, info := range block.IpAllocRecordNotMatch {
								fmt.Printf("\t\tip %s was allocated to %s in ipamblock, but was used by another pod %s/%s\n", ipStr, info.RecordHandleID, info.CurrentUsedPod.PodNamespace, info.CurrentUsedPod.PodName)
							}
						}
					}
				}
			}
			fmt.Println()
		}

	}

	if fixBrokenBlocks {
		//fix broken blocks
		fmt.Printf("wait 30s and going to fix all broken blocks...\n")
		time.Sleep(30 * time.Second)

		for _, ippool := range blockUtils {
			if len(ippool.BrokenBlocks) == 0 {
				continue
			}
			printTitle := true

			// release leak ip and record used ip
			for _, block := range ippool.BrokenBlocks {
				if len(block.IpNotAllocExistsPod) > 0 || len(block.IpAllocNotExistsPod) > 0 {
					if printTitle {
						fmt.Printf("fix blocks for ippool %s\n", ippool.Name)
						printTitle = false
					}
					fmt.Printf("fix block: %s %s\n", ippool.Name, block.Name)
					//record used ip to ipamblock
					for ipStr, ipOption := range block.IpNotAllocExistsPod {
						fmt.Printf("going to record ip %s used by pod %s/%s but not record in ipamblock\n", ipStr, ipOption.PodNamespace, ipOption.PodName)
						if err := ipamClient.RecordUsedIP(ipStr, ipOption, false); err != nil {
							fmt.Printf("record ip %s failed: %v\n\n", ipStr, err)
							continue
						}
						fmt.Printf("record ip %s success\n\n", ipStr)
					}

					//relese leaked ip in ipamblock
					for ipStr, handleID := range block.IpAllocNotExistsPod {
						fmt.Printf("going to release ip %s allocated in ipamblock but pod %s not exists\n", ipStr, handleID)
						if err := ipamClient.ReleaseLeakIP(ipStr, block.Name, true); err != nil {
							fmt.Printf("release ip %s failed: %v\n\n", ipStr, err)
							continue
						}
						fmt.Printf("release ip %s success\n\n", ipStr)
					}
					fmt.Println()
				}
			}
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
