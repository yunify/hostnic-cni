/*
Copyright 2020 KubeSphere Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ipam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"slices"
	"strings"

	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/davecgh/go-spew/spew"
	cnet "github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/set"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	k8sinformers "k8s.io/client-go/informers"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	"github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	"github.com/yunify/hostnic-cni/pkg/client/clientset/versioned/scheme"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	networklisters "github.com/yunify/hostnic-cni/pkg/client/listers/network/v1alpha1"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/utils"
)

const (
	// Number of retries when we have an error writing data to etcd.
	datastoreRetries = 10

	// Common attributes which may be set on allocations by clients.
	IPAMBlockAttributePod       = "pod"
	IPAMBlockAttributeNamespace = "namespace"
	IPAMBlockAttributeNode      = "node"
	IPAMBlockAttributeIP        = "ip"
	IPAMBlockAttributeTimestamp = "timestamp"
)

var (
	ErrNoQualifiedPool  = errors.New("cannot find a qualified ippool")
	ErrNoFreeBlocks     = errors.New("no free blocks in ippool")
	ErrMaxRetry         = errors.New("Max retries hit - excessive concurrent IPAM requests")
	ErrUnknowIPPoolType = errors.New("unknow ippool type")
)

func (c IPAMClient) getAllPools() ([]v1alpha1.IPPool, error) {
	req, err := labels.NewRequirement(v1alpha1.IPPoolTypeLabel, selection.In, []string{c.typeStr})
	if err != nil {
		err = fmt.Errorf("new requirement for list ippool error: %v", err)
		return nil, err
	}
	pools, err := c.ippoolsLister.List(labels.NewSelector().Add(*req))
	if err != nil {
		return nil, err
	}

	var result []v1alpha1.IPPool
	for _, pool := range pools {
		if pool != nil {
			result = append(result, *pool)
		}
	}

	return result, nil
}

// NewIPAMClient returns a new IPAMClient, which implements Interface.
func NewIPAMClient(client versioned.Interface, typeStr string, informers informers.SharedInformerFactory, k8sInformers k8sinformers.SharedInformerFactory) IPAMClient {
	ippoolInformer := informers.Network().V1alpha1().IPPools()
	ipamblocksInformer := informers.Network().V1alpha1().IPAMBlocks()
	ipamhandleInformer := informers.Network().V1alpha1().IPAMHandles()
	configMapInformer := k8sInformers.Core().V1().ConfigMaps()
	podInformer := k8sInformers.Core().V1().Pods()
	nodeInformer := k8sInformers.Core().V1().Nodes()
	return IPAMClient{
		typeStr:          typeStr,
		client:           client,
		ippoolsLister:    ippoolInformer.Lister(),
		ippoolsSynced:    ippoolInformer.Informer().HasSynced,
		ipamblocksLister: ipamblocksInformer.Lister(),
		ipamblocksSynced: ipamblocksInformer.Informer().HasSynced,
		ipamhandleLister: ipamhandleInformer.Lister(),
		ipamhandleSynced: ipamhandleInformer.Informer().HasSynced,
		configMapLister:  configMapInformer.Lister(),
		configMapSynced:  configMapInformer.Informer().HasSynced,
		podLister:        podInformer.Lister(),
		podSynced:        podInformer.Informer().HasSynced,
		nodeLister:       nodeInformer.Lister(),
		nodeListerSynced: nodeInformer.Informer().HasSynced,
	}
}

// IPAMClient implements Interface
type IPAMClient struct {
	typeStr string
	client  versioned.Interface

	ippoolsLister networklisters.IPPoolLister
	ippoolsSynced cache.InformerSynced

	ipamblocksLister networklisters.IPAMBlockLister
	ipamblocksSynced cache.InformerSynced

	ipamhandleLister networklisters.IPAMHandleLister
	ipamhandleSynced cache.InformerSynced

	configMapLister corelisters.ConfigMapLister
	configMapSynced cache.InformerSynced

	podLister corelisters.PodLister
	podSynced cache.InformerSynced

	nodeLister       corelisters.NodeLister
	nodeListerSynced cache.InformerSynced
}

func (c IPAMClient) Sync(stopCh <-chan struct{}) error {
	klog.Info("Waiting for ippools and ipamblocks caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.ippoolsSynced, c.ipamblocksSynced, c.ipamhandleSynced, c.configMapSynced, c.podSynced, c.nodeListerSynced); !ok {
		return fmt.Errorf("failed to wait for ippools and ipamblocks caches to sync")
	}
	return nil
}

// AutoAssign automatically assigns one or more IP addresses as specified by the
// provided AutoAssignArgs.  AutoAssign returns the list of the assigned IPv4 addresses,
// and the list of the assigned IPv6 addresses.
//
// In case of error, returns the IPs allocated so far along with the error.
func (c IPAMClient) AutoAssign(args AutoAssignArgs) (*current.Result, error) {
	var (
		result current.Result
		err    error
		ip     *cnet.IPNet
		pool   *v1alpha1.IPPool
		block  *v1alpha1.IPAMBlock
	)

	for i := 0; i < datastoreRetries; i++ {
		pool, err = c.ippoolsLister.Get(args.Pool)
		if err != nil {
			return nil, ErrNoQualifiedPool
		}

		if pool.Disabled() {
			klog.Infof("provided ippool %s should be enabled", pool.Name)
			return nil, ErrNoQualifiedPool
		}

		if pool.TypeInvalid() {
			return nil, ErrUnknowIPPoolType
		}

		block, ip, err = c.autoAssign(args.HandleID, args.Attrs, pool)
		if err != nil {
			if errors.Is(err, ErrNoFreeBlocks) {
				return nil, err
			}
			continue
		}
		break
	}

	if err != nil {
		klog.Infof("AutoAssign: args=%s, err=%v", spew.Sdump(args), err)
		return nil, ErrMaxRetry
	}

	args.Info.IPPool = pool.Name
	args.Info.Block = block.Name

	version := 4
	if ip.IP.To4() == nil {
		version = 6
	}

	result.IPs = append(result.IPs, &current.IPConfig{
		Version: fmt.Sprintf("%d", version),
		Address: net.IPNet{IP: ip.IP, Mask: ip.Mask},
		Gateway: net.ParseIP(pool.Spec.Gateway),
	})

	for _, route := range pool.Spec.Routes {
		_, dst, _ := net.ParseCIDR(route.Dst)
		result.Routes = append(result.Routes, &cnitypes.Route{
			Dst: *dst,
			GW:  net.ParseIP(route.GW),
		})
	}
	result.DNS.Domain = pool.Spec.DNS.Domain
	result.DNS.Options = pool.Spec.DNS.Options
	result.DNS.Nameservers = pool.Spec.DNS.Nameservers
	result.DNS.Search = pool.Spec.DNS.Search

	poolType := pool.Spec.Type
	switch poolType {
	case v1alpha1.VLAN:
		result.Interfaces = append(result.Interfaces, &current.Interface{
			Mac: utils.EthRandomAddr(ip.IP),
		})
	}

	return &result, nil
}

// findOrClaimBlock find an address block with free space, and if it doesn't exist, create it.
func (c IPAMClient) findOrClaimBlock(pool *v1alpha1.IPPool, minFreeIps int) (*v1alpha1.IPAMBlock, error) {
	remainingBlocks, err := c.ListBlocks(pool.Name)
	if err != nil {
		return nil, err
	}

	// First, we try to find a block from one of the existing blocks.
	for len(remainingBlocks) > 0 {
		// Pop first cidr.
		block := remainingBlocks[0]
		remainingBlocks = remainingBlocks[1:]

		// Pull out the block.
		if block.NumFreeAddresses() >= minFreeIps {
			return &block, nil
		} else {
			continue
		}
	}

	//Second, create unused Address Blocks
	b, err := c.findUnclaimedBlock(pool)
	if err != nil {
		return nil, err
	}
	blockName := b.BlockName()
	controllerutil.SetControllerReference(pool, b, scheme.Scheme)
	b, err = c.client.NetworkV1alpha1().IPAMBlocks().Create(context.Background(), b, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			b, err = c.queryBlock(blockName)
		}
		if err != nil {
			return nil, err
		}
	}

	if b.NumFreeAddresses() >= minFreeIps {
		return b, nil
	} else {
		errString := fmt.Sprintf("Block '%s' has %d free ips which is less than %d ips required.", b.BlockName(), b.NumFreeAddresses(), minFreeIps)
		return nil, errors.New(errString)
	}
}

func (c IPAMClient) autoAssign(handleID string, attrs map[string]string, requestedPool *v1alpha1.IPPool) (*v1alpha1.IPAMBlock, *cnet.IPNet, error) {
	var (
		block  *v1alpha1.IPAMBlock
		result *cnet.IPNet
		err    error
	)

	block, err = c.findOrClaimBlock(requestedPool, 1)
	if err != nil {
		return nil, nil, err
	}

	result, err = c.autoAssignFromBlock(handleID, attrs, block)
	return block, result, err
}

func (c IPAMClient) autoAssignFromBlock(handleID string, attrs map[string]string, requestedBlock *v1alpha1.IPAMBlock) (*cnet.IPNet, error) {
	var (
		result *cnet.IPNet
		err    error
	)

	for i := 0; i < datastoreRetries; i++ {
		result, err = c.assignFromExistingBlock(requestedBlock, handleID, attrs)
		if err != nil {
			if k8serrors.IsConflict(err) {
				requestedBlock, err = c.queryBlock(requestedBlock.Name)
				if err != nil {
					return nil, err
				}

				// Block b is in sync with datastore. Retry assigning IP.
				continue
			}
			return nil, err
		}
		return result, nil
	}

	return nil, ErrMaxRetry
}

func (c IPAMClient) assignFromExistingBlock(block *v1alpha1.IPAMBlock, handleID string, attrs map[string]string) (*cnet.IPNet, error) {
	ips := block.AutoAssign(1, handleID, attrs)
	if len(ips) == 0 {
		return nil, fmt.Errorf("block %s has no availabe IP", block.BlockName())
	}

	err := c.incrementHandle(handleID, block, 1)
	if err != nil {
		return nil, err
	}

	_, err = c.client.NetworkV1alpha1().IPAMBlocks().Update(context.Background(), block, metav1.UpdateOptions{})
	if err != nil {
		if err := c.decrementHandle(handleID, block, 1); err != nil {
			klog.Errorf("Failed to decrement handle %s", handleID)
		}
		return nil, err
	}

	return &ips[0], nil
}

// ReleaseByHandle releases all IP addresses that have been assigned
// using the provided handle.
func (c IPAMClient) ReleaseByHandle(handleID string) error {
	handle, err := c.queryHandle(handleID)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		//TODO: should release ip even if handle resource not found
		return err
	}

	for blockStr := range handle.Spec.Block {
		blockName := v1alpha1.ConvertToBlockName(blockStr)
		if err := c.releaseByHandle(handleID, blockName); err != nil {
			return err
		}
	}
	return nil
}

func (c IPAMClient) releaseByHandle(handleID string, blockName string) error {
	for i := 0; i < datastoreRetries; i++ {
		block, err := c.queryBlock(blockName)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// Block doesn't exist, so all addresses are already
				// unallocated.  This can happen when a handle is
				// overestimating the number of assigned addresses.
				return nil
			} else {
				return err
			}
		}

		num := block.ReleaseByHandle(handleID)
		if num == 0 {
			// Block has no addresses with this handle, so
			// all addresses are already unallocated.
			return nil
		}

		if block.Empty() {
			// TODO: reserve block
			/*
				if err = c.DeleteBlock(block); err != nil {
					if k8serrors.IsConflict(err) {
						// Update conflict - retry.
						continue
					} else if !k8serrors.IsNotFound(err) {
						return err
					}
				}
			*/
			_, err = c.client.NetworkV1alpha1().IPAMBlocks().Update(context.Background(), block, metav1.UpdateOptions{})
			if err != nil {
				if k8serrors.IsConflict(err) {
					// Comparison failed - retry.
					continue
				} else {
					// Something else - return the error.
					return err
				}
			}
		} else {
			// Compare and swap the AllocationBlock using the original
			// KVPair read from before.  No need to update the Value since we
			// have been directly manipulating the value referenced by the KVPair.
			_, err = c.client.NetworkV1alpha1().IPAMBlocks().Update(context.Background(), block, metav1.UpdateOptions{})
			if err != nil {
				if k8serrors.IsConflict(err) {
					// Comparison failed - retry.
					continue
				} else {
					// Something else - return the error.
					return err
				}
			}
		}

		if err = c.decrementHandle(handleID, block, num); err != nil {
			klog.Errorf("Failed to decrement handle %s, err=%s", handleID, err)
		}

		return nil
	}
	return ErrMaxRetry
}

func (c IPAMClient) incrementHandle(handleID string, block *v1alpha1.IPAMBlock, num int) error {
	for i := 0; i < datastoreRetries; i++ {
		create := false
		handle, err := c.queryHandle(handleID)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// Handle doesn't exist - create it.
				handle = &v1alpha1.IPAMHandle{
					ObjectMeta: metav1.ObjectMeta{
						Name: handleID,
					},
					Spec: v1alpha1.IPAMHandleSpec{
						HandleID: handleID,
						Block:    map[string]int{},
					},
				}
				create = true
			} else {
				// Unexpected error reading handle.
				return err
			}
		}

		// Increment the handle for this block.
		handle.IncrementBlock(block, num)

		if create {
			_, err = c.client.NetworkV1alpha1().IPAMHandles().Create(context.Background(), handle, metav1.CreateOptions{})
		} else {
			_, err = c.client.NetworkV1alpha1().IPAMHandles().Update(context.Background(), handle, metav1.UpdateOptions{})
		}
		if err != nil {
			if k8serrors.IsAlreadyExists(err) || k8serrors.IsConflict(err) {
				continue
			}
			return err
		}

		return nil
	}

	return ErrMaxRetry
}

func (c IPAMClient) decrementHandle(handleID string, block *v1alpha1.IPAMBlock, num int) error {
	for i := 0; i < datastoreRetries; i++ {
		handle, err := c.queryHandle(handleID)
		if err != nil {
			return err
		}

		_, err = handle.DecrementBlock(block, num)
		if err != nil {
			klog.Errorf("decrementHandle: %v", err)
			return err
		}

		// Update / Delete as appropriate.  Since we have been manipulating the
		// data in the KVPair, just pass this straight back to the client.
		if handle.Empty() {
			if err = c.deleteHandle(handle); err != nil {
				if k8serrors.IsConflict(err) {
					// Update conflict - retry.
					continue
				} else if !k8serrors.IsNotFound(err) {
					return err
				}
			}
		} else {
			if _, err = c.client.NetworkV1alpha1().IPAMHandles().Update(context.Background(), handle, metav1.UpdateOptions{}); err != nil {
				if k8serrors.IsConflict(err) {
					// Update conflict - retry.
					continue
				}
				return err
			}
		}

		return nil
	}

	return ErrMaxRetry
}

// GetUtilization returns IP utilization info for the specified pools, or for all pools.
func (c IPAMClient) GetUtilization(args GetUtilizationArgs) ([]*PoolUtilization, error) {
	var usage []*PoolUtilization

	// Read all pools.
	allPools, err := c.getAllPools()
	if err != nil {
		return nil, err
	}

	if len(allPools) <= 0 {
		return nil, fmt.Errorf("not found pool")
	}

	// Identify the ones we want and create a PoolUtilization for each of those.
	wantAllPools := len(args.Pools) == 0
	wantedPools := set.FromArray(args.Pools)
	for _, pool := range allPools {
		if wantAllPools || wantedPools.Contains(pool.Name) {
			cap := pool.NumAddresses()
			reserved := pool.NumReservedAddresses()
			usage = append(usage, &PoolUtilization{
				Name:        pool.Name,
				Capacity:    cap,
				Reserved:    reserved,
				Allocate:    0,
				Unallocated: cap - reserved,
			})
		}
	}

	// Find which pool this block belongs to.
	for _, poolUse := range usage {
		blocks, err := c.ListBlocks(poolUse.Name)
		if err != nil {
			return nil, err
		}

		if len(blocks) <= 0 {
			continue
		} else {
			poolUse.Reserved = 0
			poolUse.Allocate = 0
		}

		for _, block := range blocks {
			poolUse.Allocate += block.NumAddresses() - block.NumFreeAddresses() - block.NumReservedAddresses()
			poolUse.Reserved += block.NumReservedAddresses()
		}

		poolUse.Unallocated = poolUse.Capacity - poolUse.Allocate - poolUse.Reserved
	}

	return usage, nil
}

func (c IPAMClient) GetPoolBlocksUtilization(args GetUtilizationArgs) ([]*PoolBlocksUtilization, error) {
	var usage []*PoolBlocksUtilization

	// Read all pools.
	allPools, err := c.getAllPools()
	if err != nil {
		return nil, err
	}

	if len(allPools) <= 0 {
		return nil, fmt.Errorf("not found pool")
	}

	// Identify the ones we want and create a PoolUtilization for each of those.
	wantAllPools := len(args.Pools) == 0
	wantedPools := set.FromArray(args.Pools)
	for _, pool := range allPools {
		if wantAllPools || wantedPools.Contains(pool.Name) {
			cap := pool.NumAddresses()
			reserved := pool.NumReservedAddresses()
			usage = append(usage, &PoolBlocksUtilization{
				Name:        pool.Name,
				Capacity:    cap,
				Reserved:    reserved,
				Allocate:    0,
				Unallocated: cap - reserved,
			})
		}
	}

	// Find which pool this block belongs to.
	for _, poolUse := range usage {
		blocks, err := c.ListBlocks(poolUse.Name)
		if err != nil {
			return nil, err
		}

		if len(blocks) <= 0 {
			continue
		} else {
			poolUse.Reserved = 0
			poolUse.Allocate = 0
		}

		for _, block := range blocks {
			cap := block.NumAddresses()
			free := block.NumFreeAddresses()
			reserved := block.NumReservedAddresses()
			poolUse.Allocate += cap - free - reserved
			poolUse.Reserved += reserved
			poolUse.Blocks = append(poolUse.Blocks, &BlockUtilization{
				Name:        block.Name,
				Capacity:    cap,
				Reserved:    reserved,
				Allocate:    cap - free - reserved,
				Unallocated: free,
			})
		}

		poolUse.Unallocated = poolUse.Capacity - poolUse.Allocate - poolUse.Reserved
	}

	return usage, nil
}

func (c IPAMClient) GetBrokenBlocks(args GetUtilizationArgs, missingHandles, mismatchHandles bool) ([]*PoolBlocksUtilization, error) {
	var usage []*PoolBlocksUtilization

	// Read all pools.
	allPools, err := c.getAllPools()
	if err != nil {
		return nil, err
	}

	if len(allPools) <= 0 {
		return nil, fmt.Errorf("not found pool")
	}

	// Identify the ones we want and create a PoolUtilization for each of those.
	wantAllPools := len(args.Pools) == 0
	wantedPools := set.FromArray(args.Pools)
	for _, pool := range allPools {
		if wantAllPools || wantedPools.Contains(pool.Name) {
			cap := pool.NumAddresses()
			reserved := pool.NumReservedAddresses()
			usage = append(usage, &PoolBlocksUtilization{
				Name:        pool.Name,
				Capacity:    cap,
				Reserved:    reserved,
				Allocate:    0,
				Unallocated: cap - reserved,
			})
		}
	}

	// get configmap to get ns for this ipamblock
	cm, err := c.configMapLister.ConfigMaps(constants.IPAMConfigNamespace).Get(constants.IPAMConfigName)
	if err != nil {
		err = fmt.Errorf("get configmap %s/%s failed: %v", constants.IPAMConfigNamespace, constants.IPAMConfigName, err)
		return nil, err
	}

	var nsToBlocks map[string][]string
	blockToNs := make(map[string][]string)
	err = json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &nsToBlocks)
	if err != nil {
		err = fmt.Errorf("unmarshal configmap %s/%s data failed: %v", constants.IPAMConfigNamespace, constants.IPAMConfigName, err)
		return nil, err
	}
	for ns, blocks := range nsToBlocks {
		for _, block := range blocks {
			if !slices.Contains(blockToNs[block], ns) {
				blockToNs[block] = append(blockToNs[block], ns)
			}
		}
	}

	// Find which pool this block belongs to.
	for _, poolUse := range usage {
		blocks, err := c.ListBlocks(poolUse.Name)
		if err != nil {
			return nil, err
		}

		if len(blocks) <= 0 {
			continue
		} else {
			poolUse.Reserved = 0
			poolUse.Allocate = 0
		}

		for _, block := range blocks {
			var broken bool

			//get ipamblock again to get latest data
			newBlock, err := c.ipamblocksLister.Get(block.Name)
			if err != nil {
				fmt.Printf("get block %s err: %v, skip block %s\n", block.Name, err, block.Name)
				continue
			}
			block = *newBlock

			blockName := (&block).BlockName()
			blockNs := blockToNs[blockName]
			if len(blockNs) == 0 {
				// ignore free subnet, we do not know which ns it belong to
				continue
			}

			// list all pods in these ns to get all used ip of this block
			var blockPods []*corev1.Pod
			for _, ns := range blockNs {
				pods, err := c.podLister.Pods(ns).List(labels.Everything())
				if err != nil {
					fmt.Printf("get pod for ns %s err: %v, skip ns %s for block %s \n", ns, err, ns, blockName)
					continue
				}
				blockPods = append(blockPods, pods...)
			}

			// all ipam handle
			allHandlesMap := make(map[string]struct{})
			ipamHandles, err := c.ipamhandleLister.List(labels.Everything())
			if err != nil {
				fmt.Printf("list ipam handle error: %v,  skip block %s\n", err, blockName)
				continue
			}
			for _, ipamHandle := range ipamHandles {
				allHandlesMap[ipamHandle.Name] = struct{}{}
			}

			// parse block cidr
			_, cidrNet, err := cnet.ParseCIDR(block.Spec.CIDR)
			if err != nil {
				fmt.Printf("parse block %s cidr err: %v, skip block %s \n", blockName, err, blockName)
				continue
			}

			// podIPIndexToAttrIndex := make(map[int]int)
			// podIPToPodNames := make(map[string][]string)
			ipToPods := make(map[string][]string)
			ipNotAllocExistsPod := make(map[string]UsedIPOption) // maybe allocated to more than one pod
			ipAllocNotExistsPod := make(map[string]string)
			usedHandlesMissing := make(map[string]string)
			IpAllocRecordNotMatch := make(map[string]IPAllocatedInfo)
			blockPodsMap := make(map[string]*corev1.Pod) //key: ns-podMame value:pod

			ipToPodsMap := make(map[string]*corev1.Pod)

			// covert podinfo to map
			for _, pod := range blockPods {
				if pod != nil {
					//should pay attention to pod status ContainerCreating, these pod maybe have no ip for now but allocated in ipamblock
					blockPodsMap[pod.Namespace+"-"+pod.Name] = pod
					if pod.Status.PodIP != "" {
						podIPStr := pod.Status.PodIP
						podIP := cnet.ParseIP(pod.Status.PodIP)
						// should ignore completed/failed pod there; ignore dumplicate pod name
						if cidrNet.Contains(podIP.IP) && !slices.Contains(ipToPods[podIPStr], pod.Name) &&
							pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
							ipToPods[podIPStr] = append(ipToPods[podIPStr], pod.Name)
							ipToPodsMap[podIPStr] = pod
						}
					}
				}
			}

			//check all ip in this block
			for ordinal, attrIndex := range block.Spec.Allocations {
				if attrIndex != nil {
					if *attrIndex == 0 {
						// ignore reserved ip
						continue
					}
				}

				ip, err := block.OrdinalToIP(ordinal)
				if err != nil {
					fmt.Printf("parse ordinal to ip error: %v, skip ordinal %d\n", err, ordinal)
					continue
				}

				ipStr := ip.String()
				ipPods := ipToPods[ipStr]

				// ip allocated more than one pod
				if len(ipPods) > 1 {
					broken = true
					// we can not fix this, just continue, caller will print this
					continue
				}

				// ip not allocated
				if attrIndex == nil {
					//ip not allocated, but pod exists
					if len(ipPods) > 0 {
						//check if pod is deleting, ignore this pod
						pod := ipToPodsMap[ipStr]
						if pod.DeletionTimestamp != nil {
							continue
						}

						broken = true

						//try to get handle id
						var handleID string
						for _, ipamHandle := range ipamHandles {
							if strings.Contains(ipamHandle.Name, pod.Namespace+"-"+pod.Name) {
								handleID = ipamHandle.Name
							}
						}

						ipNotAllocExistsPod[ipStr] = UsedIPOption{
							PodName:      pod.Name,
							PodNamespace: pod.Namespace,
							NodeName:     pod.Spec.NodeName,
							BlockName:    blockName,
							HandleID:     handleID,
						}
					}
				} else {
					// ip allocated
					var handldID string
					if *attrIndex <= len(block.Spec.Attributes)-1 {
						handldID = block.Spec.Attributes[*attrIndex].AttrPrimary
					} else {
						fmt.Printf("block %s attrIndex %d is nil\n", blockName, *attrIndex)
						continue
					}

					//ip allocated, but pod with this ip not exists
					var found bool
					if len(ipPods) == 0 {
						// compare handle id with pod namespace + pod name, try to get pod
						// maybe the pod for this ip is pending status, just allocate ip in ipamblock, but creating containers and not return ip to k8s
						for key, pod := range blockPodsMap {
							if strings.Contains(handldID, key) && pod != nil {
								// maybe pod is pending status, just ignore this ip and continue
								if pod.Status.Phase == corev1.PodPending {
									found = true
									break
								}
							}
						}
						if found {
							continue
						}

						broken = true
						ipAllocNotExistsPod[ipStr] = handldID
					} else {
						// ip allocated and pod exists
						// check ipam handle exists, check if handle id is correct
						// ignore handle id we make it as <pod namespace + pod name>
						pod := ipToPodsMap[ipStr]
						if handldID != pod.Namespace+"-"+pod.Name {
							// check ipam handle exists
							if missingHandles {
								if _, ok := allHandlesMap[handldID]; !ok {
									broken = true
									usedHandlesMissing[ipStr] = handldID
								}
							}

							// check if handle id is correct
							if mismatchHandles {
								if !strings.HasPrefix(handldID, pod.Namespace+"-"+pod.Name) {
									broken = true
									IpAllocRecordNotMatch[ipStr] = IPAllocatedInfo{
										RecordHandleID: handldID,
										CurrentUsedPod: UsedIPOption{
											PodName:      pod.Name,
											PodNamespace: pod.Namespace,
											NodeName:     pod.Spec.NodeName,
											BlockName:    blockName,
										},
									}
								}
							}
						}

					}
				}

			}

			if broken {
				// result broken blocks in array
				poolUse.BrokenBlocks = append(poolUse.BrokenBlocks, &BrokenBlockUtilization{
					Name:                  blockName,
					IpToPods:              ipToPods,
					IpNotAllocExistsPod:   ipNotAllocExistsPod,
					IpAllocNotExistsPod:   ipAllocNotExistsPod,
					UsedHandlesMissing:    usedHandlesMissing,
					IpAllocRecordNotMatch: IpAllocRecordNotMatch,
				})
				poolUse.BrokenBlockNames = append(poolUse.BrokenBlockNames, blockName)
			}

		}
	}

	return usage, nil
}

// findUnclaimedBlock finds a block cidr which does not yet exist within the given list of pools. The provided pools
// should already be sanitized and only include existing, enabled pools. Note that the block may become claimed
// between receiving the cidr from this function and attempting to claim the corresponding block as this function
// does not reserve the returned IPNet.
func (c IPAMClient) findUnclaimedBlock(pool *v1alpha1.IPPool) (*v1alpha1.IPAMBlock, error) {
	var result *v1alpha1.IPAMBlock

	// List blocks up front to reduce number of queries.
	// We will try to write the block later to prevent races.
	existingBlocks, err := c.ListBlocks(pool.Name)
	if err != nil {
		return nil, err
	}

	/// Build a map for faster lookups.
	exists := map[string]bool{}
	for _, e := range existingBlocks {
		exists[fmt.Sprintf("%s", e.Spec.CIDR)] = true
	}

	// Iterate through pools to find a new block.
	_, cidr, _ := cnet.ParseCIDR(pool.Spec.CIDR)
	poolType := pool.Spec.Type
	switch poolType {
	case v1alpha1.VLAN:
		if _, ok := exists[cidr.String()]; !ok {
			var reservedAttr *v1alpha1.ReservedAttr
			if pool.Spec.RangeStart != "" && pool.Spec.RangeEnd != "" {
				reservedAttr = &v1alpha1.ReservedAttr{
					StartOfBlock: pool.StartReservedAddressed(),
					EndOfBlock:   pool.EndReservedAddressed(),
					Handle:       v1alpha1.ReservedHandle,
					Note:         v1alpha1.ReservedNote,
				}
			}
			result = v1alpha1.NewBlock(pool, *cidr, reservedAttr)
		}
	default:
		blocks := blockGenerator(pool)
		for subnet := blocks(); subnet != nil; subnet = blocks() {
			// Check if a block already exists for this subnet.
			if _, ok := exists[fmt.Sprintf("%s", subnet.String())]; !ok {
				reservedAttr := &v1alpha1.ReservedAttr{
					StartOfBlock: StartReservedAddressed(*subnet, pool.Spec.RangeStart),
					EndOfBlock:   EndReservedAddressed(*subnet, pool.Spec.RangeEnd),
					Handle:       v1alpha1.ReservedHandle,
					Note:         v1alpha1.ReservedNote,
				}
				result = v1alpha1.NewBlock(pool, *subnet, reservedAttr)
				break
			}
		}
	}

	if result != nil {
		return result, nil
	} else {
		return nil, ErrNoFreeBlocks
	}
}

func (c IPAMClient) ListBlocks(pool string) ([]v1alpha1.IPAMBlock, error) {
	req, err := labels.NewRequirement(v1alpha1.IPPoolNameLabel, selection.In, []string{pool})
	if err != nil {
		err = fmt.Errorf("new requirement for list ipamblocks error: %v", err)
		return nil, err
	}
	blocks, err := c.ipamblocksLister.List(labels.NewSelector().Add(*req))
	if err != nil {
		return nil, err
	}

	var result []v1alpha1.IPAMBlock
	for _, block := range blocks {
		if block != nil {
			result = append(result, *block)
		}
	}

	return result, nil
}

// DeleteBlock deletes the given block.
func (c IPAMClient) DeleteBlock(b *v1alpha1.IPAMBlock) error {
	if !b.IsDeleted() {
		b.MarkDeleted()
		_, err := c.client.NetworkV1alpha1().IPAMBlocks().Update(context.Background(), b, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return c.client.NetworkV1alpha1().IPAMBlocks().Delete(context.Background(), b.Name, metav1.DeleteOptions{})
}

func (c IPAMClient) queryBlock(blockName string) (*v1alpha1.IPAMBlock, error) {
	block, err := c.client.NetworkV1alpha1().IPAMBlocks().Get(context.Background(), blockName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if block.IsDeleted() {
		err := c.DeleteBlock(block)
		if err != nil {
			return nil, err
		}

		return nil, k8serrors.NewNotFound(v1alpha1.Resource(v1alpha1.ResourcePluralIPAMBlock), blockName)
	}

	return block, nil
}

// queryHandle gets a handle for the given handleID key.
func (c IPAMClient) queryHandle(handleID string) (*v1alpha1.IPAMHandle, error) {
	handle, err := c.client.NetworkV1alpha1().IPAMHandles().Get(context.Background(), handleID, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if handle.IsDeleted() {
		err := c.deleteHandle(handle)
		if err != nil {
			return nil, err
		}

		return nil, k8serrors.NewNotFound(v1alpha1.Resource(v1alpha1.ResourcePluralIPAMHandle), handleID)
	}

	return handle, nil
}

// deleteHandle deletes the given handle.
func (c IPAMClient) deleteHandle(h *v1alpha1.IPAMHandle) error {
	if !h.IsDeleted() {
		h.MarkDeleted()
		_, err := c.client.NetworkV1alpha1().IPAMHandles().Update(context.Background(), h, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return c.client.NetworkV1alpha1().IPAMHandles().Delete(context.Background(), h.Name, metav1.DeleteOptions{})
}

func (c IPAMClient) AutoAssignFromPools(args AutoAssignArgs) (*current.Result, error) {
	utils, err := c.GetUtilization(GetUtilizationArgs{args.Pools})
	if err != nil {
		return nil, err
	}

	for _, util := range utils {
		if util.Unallocated > 1 {
			args.Pool = util.Name
			if r, err := c.AutoAssign(args); err != nil {
				klog.Warningf("AutoAssign from pool %s failed: %v", util.Name, err)
				continue
			} else {
				return r, nil
			}
		}
	}

	return nil, fmt.Errorf("no appropriate ippool found")
}

func (c IPAMClient) AutoAssignFromBlocks(args AutoAssignArgs) (*current.Result, error) {
	var blocks []*v1alpha1.IPAMBlock
	for _, block := range args.Blocks {
		if b, err := c.client.NetworkV1alpha1().IPAMBlocks().Get(context.Background(), block, metav1.GetOptions{}); err == nil {
			blocks = append(blocks, b)
		} else {
			klog.Warningf("Get block %s failed: %v", block, err)
		}
	}

	for _, block := range blocks {
		if block.NumFreeAddresses() >= 1 {
			if ip, err := c.autoAssignFromBlock(args.HandleID, args.Attrs, block); err == nil {
				poolName := block.Labels[networkv1alpha1.IPPoolNameLabel]
				if pool, err := c.ippoolsLister.Get(poolName); err == nil {
					args.Info.IPPool = poolName
					args.Info.Block = block.Name
					return IP2Resutl(ip, pool), nil
				} else {
					klog.Errorf("autoAssignFromBlock %s failed to find ippool %s: %v", block.Name, poolName, err)
				}
			} else {
				klog.Warningf("autoAssignFromBlock %s failed: %v", block.Name, err)
			}
		} else {
			continue
		}
	}

	return nil, ErrMaxRetry
}

func (c IPAMClient) AutoGenerateBlocksFromPool(poolName string) error {
	pool, err := c.ippoolsLister.Get(poolName)
	if err != nil {
		return ErrNoQualifiedPool
	}

	// List blocks up front to reduce number of queries.
	// We will try to write the block later to prevent races.
	existingBlocks, err := c.ListBlocks(pool.Name)
	if err != nil {
		return err
	}

	/// Build a map for faster lookups.
	exists := map[string]bool{}
	for _, e := range existingBlocks {
		exists[fmt.Sprintf("%s", e.Spec.CIDR)] = true
	}

	blocks := blockGenerator(pool)
	for subnet := blocks(); subnet != nil; subnet = blocks() {
		// Check if a block already exists for this subnet.
		if _, ok := exists[fmt.Sprintf("%s", subnet.String())]; !ok {
			reservedAttr := &v1alpha1.ReservedAttr{
				StartOfBlock: StartReservedAddressed(*subnet, pool.Spec.RangeStart),
				EndOfBlock:   EndReservedAddressed(*subnet, pool.Spec.RangeEnd),
				Handle:       v1alpha1.ReservedHandle,
				Note:         v1alpha1.ReservedNote,
			}
			block := v1alpha1.NewBlock(pool, *subnet, reservedAttr)
			blockName := block.BlockName()
			controllerutil.SetControllerReference(pool, block, scheme.Scheme)
			err = c.setBlockAttributes(blockName, block)
			if err != nil {
				return err
			}
			block, err = c.client.NetworkV1alpha1().IPAMBlocks().Create(context.Background(), block, metav1.CreateOptions{})
			if err != nil {
				if k8serrors.IsAlreadyExists(err) {
					block, err = c.queryBlock(blockName)
				}
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Generator to get list of block CIDRs which
// fall within the given cidr. The passed in pool
// must contain the passed in block cidr.
// Returns nil when no more blocks can be generated.
func blockGenerator(pool *v1alpha1.IPPool) func() *cnet.IPNet {
	tmp, cidr, _ := cnet.ParseCIDR(pool.Spec.CIDR)
	ip := *tmp

	var blockMask net.IPMask
	if ip.Version() == 4 {
		blockMask = net.CIDRMask(pool.Spec.BlockSize, 32)
	} else {
		blockMask = net.CIDRMask(pool.Spec.BlockSize, 128)
	}

	ones, size := blockMask.Size()
	blockSize := new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(size-ones)), nil)

	return func() *cnet.IPNet {
		returnIP := ip

		if cidr.Contains(ip.IP) {
			ipnet := net.IPNet{IP: returnIP.IP, Mask: blockMask}
			cidr := cnet.IPNet{IPNet: ipnet}
			ip = cnet.IncrementIP(ip, blockSize)
			return &cidr
		} else {
			return nil
		}
	}
}

func StartReservedAddressed(cidr cnet.IPNet, r string) int {
	if r == "" {
		return 0
	}

	ip := *cnet.ParseIP(r)
	ipAsInt := cnet.IPToBigInt(ip)
	baseInt := cnet.IPToBigInt(cnet.IP{IP: cidr.IP})
	ord := big.NewInt(0).Sub(ipAsInt, baseInt).Int64()

	ones, size := cidr.Mask.Size()
	numAddresses := 1 << uint(size-ones)

	if ord < 0 || ord >= int64(numAddresses) {
		// TODO: handle error
		return 0
	}
	return int(ord)
}

func EndReservedAddressed(cidr cnet.IPNet, r string) int {
	ones, size := cidr.Mask.Size()
	total := 1 << uint(size-ones)
	if r == "" {
		return 0
	}

	ip := *cnet.ParseIP(r)
	ipAsInt := cnet.IPToBigInt(ip)
	baseInt := cnet.IPToBigInt(cnet.IP{IP: cidr.IP})
	ord := big.NewInt(0).Sub(ipAsInt, baseInt).Int64()
	if ord < 0 {
		return total
	}
	if ord >= int64(total) {
		// TODO: handle error
		return 0
	}

	return total - int(ord) - 1
}

func IP2Resutl(ip *cnet.IPNet, pool *v1alpha1.IPPool) *current.Result {
	var result current.Result

	version := 4
	if ip.IP.To4() == nil {
		version = 6
	}

	result.IPs = append(result.IPs, &current.IPConfig{
		Version: fmt.Sprintf("%d", version),
		Address: net.IPNet{IP: ip.IP, Mask: ip.Mask},
		Gateway: net.ParseIP(pool.Spec.Gateway),
	})

	for _, route := range pool.Spec.Routes {
		_, dst, _ := net.ParseCIDR(route.Dst)
		result.Routes = append(result.Routes, &cnitypes.Route{
			Dst: *dst,
			GW:  net.ParseIP(route.GW),
		})
	}
	result.DNS.Domain = pool.Spec.DNS.Domain
	result.DNS.Options = pool.Spec.DNS.Options
	result.DNS.Nameservers = pool.Spec.DNS.Nameservers
	result.DNS.Search = pool.Spec.DNS.Search

	poolType := pool.Spec.Type
	switch poolType {
	case v1alpha1.VLAN:
		result.Interfaces = append(result.Interfaces, &current.Interface{
			Mac: utils.EthRandomAddr(ip.IP),
		})
	}

	return &result
}

// AssignFixIps fix ip form assign pool or block
func (c IPAMClient) AssignFixIps(handleID string, ipList, pools, blocks []string, info *PoolInfo, attrs map[string]string) (*current.Result, error) {
	fixArgs := FixIpArgs{
		AutoAssignArgs: AutoAssignArgs{
			HandleID: handleID,
			Pools:    pools,
			Blocks:   blocks,
			Info:     info,
			Attrs:    attrs,
		},
		TarGetIpList: ipList,
	}
	if len(fixArgs.Pools) > 0 {
		return c.FixIpsFromPool(fixArgs)
	} else if len(fixArgs.Blocks) > 0 {
		return c.FixIpsFromBlock(fixArgs)
	}
	return nil, fmt.Errorf("no suitable pool and block")
}

// FixIpsFromPool fix ip from assign pool
func (c IPAMClient) FixIpsFromPool(args FixIpArgs) (*current.Result, error) {
	for i := 0; i < len(args.Pools); i++ {
		args.Pool = args.Pools[i]
		blocks, err := c.ListBlocks(args.Pool)
		if err != nil {
			return nil, fmt.Errorf("%s get listBlock err: %v", args.Pool, err)
		}

		for j := 0; j < len(blocks); j++ {
			block := blocks[j]
			if block.NumFreeAddresses() < 1 {
				continue
			}
			args.Pool = block.Labels[networkv1alpha1.IPPoolNameLabel]
			if result, err := c.retryFixIP(&block, args); err == nil {
				args.Info.IPPool = args.Pool
				args.Info.Block = block.Name
				return result, nil
			} else {
				klog.Warningf("FixIpsFromPool for %s from %s failed: %v", args.TarGetIpList, block.Name, err)
			}
		}
	}

	return nil, fmt.Errorf("fixIP for %s from pool %s failed", args.TarGetIpList, args.Pools)
}

// FixIpsFromBlock fix ip form assign block
func (c IPAMClient) FixIpsFromBlock(args FixIpArgs) (*current.Result, error) {
	var blocks []*v1alpha1.IPAMBlock
	for _, block := range args.Blocks {
		if b, err := c.ipamblocksLister.Get(block); err == nil {
			blocks = append(blocks, b)
		} else {
			klog.Warningf("Get block %s failed: %v", block, err)
		}
	}

	for i := 0; i < len(blocks); i++ {
		block := blocks[i]
		args.Pool = block.Labels[networkv1alpha1.IPPoolNameLabel]
		if block.NumFreeAddresses() > 0 {
			if result, err := c.retryFixIP(block, args); err == nil {
				args.Info.IPPool = args.Pool
				args.Info.Block = block.Name
				return result, nil
			} else {
				klog.Warningf("FixIpsFromBlock for %s from %s failed: %v", args.TarGetIpList, block.Name, err)
			}
		}
	}

	return nil, fmt.Errorf("fixIP for %s from block %s failed", args.TarGetIpList, args.Blocks)
}

func (c IPAMClient) retryFixIP(requestBlock *networkv1alpha1.IPAMBlock, args FixIpArgs) (*current.Result, error) {
	var result *current.Result
	for i := 0; i < datastoreRetries; i++ {
		pool, err := c.ippoolsLister.Get(args.Pool)
		if err != nil {
			return nil, fmt.Errorf("get pool err: %v", err)
		}
		if pool.Disabled() {
			return nil, fmt.Errorf("get pool err: %v", ErrNoQualifiedPool)
		}
		if pool.TypeInvalid() {
			return nil, fmt.Errorf("get pool err: %v", ErrUnknowIPPoolType)
		}

		ip, err := c.fixIP(requestBlock, args.HandleID, args.TarGetIpList, args.Attrs)
		if err != nil {
			if k8serrors.IsConflict(err) {
				requestBlock, err = c.queryBlock(requestBlock.Name)
				if err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}

		result = IP2Resutl(&cnet.IPNet{
			IPNet: net.IPNet{IP: ip},
		}, pool)
		return result, nil
	}
	return nil, ErrMaxRetry
}

// fixIP check annotation ip is unallocated
func (c IPAMClient) fixIP(block *v1alpha1.IPAMBlock, handleID string, ipList []string, attrs map[string]string) (net.IP, error) {
	_, cidr, err := cnet.ParseCIDR(block.Spec.CIDR)
	if err != nil {
		return nil, fmt.Errorf("parse cidr err: %v", err)
	}

	if attrs == nil {
		attrs = make(map[string]string)
	}

	for _, tarIp := range ipList {
		ip := cnet.ParseIP(tarIp)
		if ip != nil && !cidr.Contains(ip.IP) {
			continue
		}
		ordinal, err := block.IPToOrdinal(*ip)
		if err != nil {
			continue
		}

		// add ip to attr
		attrs[IPAMBlockAttributeIP] = ip.String()

		for j, unUsedIp := range block.Spec.Unallocated {
			if ordinal == unUsedIp {
				block.Spec.Unallocated = append(block.Spec.Unallocated[:j], block.Spec.Unallocated[j+1:]...)
				attribute := updateAttribute(block, handleID, attrs)
				block.Spec.Allocations[ordinal] = &attribute

				err = c.incrementHandle(handleID, block, 1)
				if err != nil {
					return nil, err
				}
				_, err = c.client.NetworkV1alpha1().IPAMBlocks().Update(context.Background(), block, metav1.UpdateOptions{})
				if err != nil {
					if err := c.decrementHandle(handleID, block, 1); err != nil {
						klog.Errorf("Failed to decrement handle %s", handleID)
					}
					return nil, err
				}

				return ip.IP, nil
			}
		}
	}
	return nil, fmt.Errorf("not free ip in this block: %s", block.Name)
}

func updateAttribute(b *networkv1alpha1.IPAMBlock, handleID string, attrs map[string]string) int {
	attr := networkv1alpha1.AllocationAttribute{AttrPrimary: handleID, AttrSecondary: attrs}
	for idx, existing := range b.Spec.Attributes {
		if reflect.DeepEqual(attr, existing) {
			return idx
		}
	}

	// Does not exist - add it.
	attrIndex := len(b.Spec.Attributes)
	b.Spec.Attributes = append(b.Spec.Attributes, attr)
	return attrIndex
}

// setBlockAttributes set attributes for already used ip in the block
func (c IPAMClient) setBlockAttributes(blockName string, block *networkv1alpha1.IPAMBlock) error {
	// 1. get ns for this ipamblock from configmap
	// 2. get ip being used in the ns, also get handle_id(pause container id for this pod) for this ip
	// 3. update block  attributes

	// 1. get configmap to get ns
	cm, err := c.configMapLister.ConfigMaps(constants.IPAMConfigNamespace).Get(constants.IPAMConfigName)
	if err != nil {
		klog.Errorf("Get configmap %s/%s failed: %v, skip set block %s attribute", constants.IPAMConfigNamespace, constants.IPAMConfigName, err, blockName)
		return err
	}
	var apps map[string][]string
	err = json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &apps)
	if err != nil {
		klog.Errorf("unmarshal configmap %s/%s data failed: %v, skip set block %s attribute", constants.IPAMConfigNamespace, constants.IPAMConfigName, err, blockName)
		return nil
	}
	// 2. get pod in ns
	var blockNs []string
	for ns, blocks := range apps {
		if utils.SliceContains(blocks, blockName) {
			blockNs = append(blockNs, ns)
			// break //should not break here, need to check all ns, because one block may belong to multiple ns
		}
	}
	if len(blockNs) == 0 {
		klog.Infof("block %s not belong to any ns, skip set block attribute", blockName)
		return nil
	}

	// 3. update block
	allBlockPods := []*corev1.Pod{}
	blockNSList := []string{}
	for _, ns := range blockNs {
		nsPods, err := c.podLister.Pods(ns).List(labels.Everything())
		if err != nil {
			klog.Errorf("get pod for ns %s err: %v, skip set block %s attribute", ns, err, blockName)
			continue
		}
		allBlockPods = append(allBlockPods, nsPods...)
		blockNSList = append(blockNSList, ns)
	}

	if len(allBlockPods) == 0 {
		klog.Infof("no pod in ns %v, skip set block %s attribute", blockNSList, blockName)
		return nil
	}

	ipamHandles, err := c.ipamhandleLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("list ipam handle error: %v,  skip set block %s attribute", err, blockName)
		return nil
	}

	// 4. check if pods ip belong to block
	_, cidrNet, err := cnet.ParseCIDR(block.Spec.CIDR)
	if err != nil {
		klog.Errorf("parse block %s cidr err: %v, skip set block %s attribute", blockName, err, blockName)
		return nil
	}

	for _, pod := range allBlockPods {
		if pod != nil && pod.Status.PodIP != "" {
			podIP := cnet.ParseIP(pod.Status.PodIP)
			// should ignore comleted/failed pod there
			if cidrNet.Contains(podIP.IP) && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
				// get handle id,allocations index ,delete from unallocated slice
				// handle id for ipamhandle
				var handleID string
				for _, ipamHandle := range ipamHandles {
					if strings.Contains(ipamHandle.Name, pod.Namespace+"-"+pod.Name) {
						handleID = ipamHandle.Name
					}
				}
				if handleID == "" {
					handleID = pod.Namespace + "-" + pod.Name
					klog.Infof("ip %s of pod %s/%s belong to block %s, but can not found handle id, use handld id %s", pod.Status.PodIP, pod.Namespace, pod.Name, blockName, handleID)
				}

				// get index
				ordinal, err := block.IPToOrdinal(*podIP)
				if err != nil {
					klog.Errorf("get pod ip %s index err: %v, you should try to repair this block %s laster!", pod.Status.PodIP, err, blockName)
					continue
				}

				// attr
				attrs := map[string]string{
					IPAMBlockAttributeNamespace: pod.Namespace,
					IPAMBlockAttributePod:       pod.Name,
					IPAMBlockAttributeNode:      pod.Spec.NodeName,
					IPAMBlockAttributeIP:        pod.Status.PodIP,
					IPAMBlockAttributeTimestamp: pod.CreationTimestamp.String(),
				}
				for index, unUsedOrdinal := range block.Spec.Unallocated {
					if ordinal == unUsedOrdinal {
						block.Spec.Unallocated = append(block.Spec.Unallocated[:index], block.Spec.Unallocated[index+1:]...)
						attribute := updateAttribute(block, handleID, attrs)
						block.Spec.Allocations[ordinal] = &attribute
					}
				}

			}
		}
	}

	return nil
}

func (c IPAMClient) ListInstancePods(instanceID string) (pods []*corev1.Pod, nodeName string, err error) {

	//list all nodes to find the node with instanceID
	nodes, err := c.nodeLister.List(labels.NewSelector())
	if err != nil {
		return nil, "", fmt.Errorf("list nodes error: %v", err)
	}
	for _, node := range nodes {
		if instanceIDAnnotation, ok := node.GetAnnotations()[constants.InstanceIDAnnotation]; ok && instanceIDAnnotation == instanceID {
			nodeName = node.GetName()
			break
		}
	}
	if nodeName == "" {
		return nil, "", fmt.Errorf("no such node with annotation %s=%s", constants.InstanceIDAnnotation, instanceID)
	}

	//list all pods on the node with nodeName
	podsList, err := c.podLister.List(labels.NewSelector())
	if err != nil {
		return nil, "", fmt.Errorf("list pods error: %v", err)
	}

	for _, pod := range podsList {
		if pod.Spec.NodeName == nodeName && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			pods = append(pods, pod)
		}
	}
	return pods, nodeName, nil
}

// release an allocated leaked ip in ipamblock; param ip required; if ip was used by existing pod, return error and do nothing
// if give blockName, should check if the ip belongs to the block
// if not give blockName, should check all blocks in the pool to get which block the ip belongs to
func (c IPAMClient) ReleaseLeakIP(ip, blockName string, ingoreError bool) error {
	//check ip format
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid ip format: %s", ip)
	}

	// check if ip was used by an existing pod
	pods, err := c.podLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("list pods error: %v", err)
	}
	for _, pod := range pods {
		if pod.Status.PodIP == ip && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			if ingoreError {
				fmt.Printf("ip %s was used by pod %s/%s,  this issue has no effect, skip release and ignore\n", ip, pod.Namespace, pod.Name)
				return nil
			}
			return fmt.Errorf("ip %s was used by pod %s/%s, skip release and ignore", ip, pod.Namespace, pod.Name)
		}
	}

	var ipBlock *networkv1alpha1.IPAMBlock
	var handldID string
	// 1. if blockName is given, check if the ip belongs to the block
	if blockName != "" {
		block, err := c.queryBlock(blockName)
		if err != nil {
			return fmt.Errorf("query block %s error: %v", blockName, err)
		}

		//check if the ip belongs to the block
		_, cidr, err := cnet.ParseCIDR(block.Spec.CIDR)
		if err != nil {
			return fmt.Errorf("parse block %s cidr err: %v", block.Name, err)
		}
		if !cidr.Contains(cnet.ParseIP(ip).IP) {
			return fmt.Errorf("ip %s not belong to block %s", ip, blockName)
		}
		ipBlock = block
	} else {
		// 2. if blockName is not given, check all blocks in the pool to get which block the ip belongs to
		ipBlock, err = c.getBlockForIP(ip)
		if err != nil {
			return fmt.Errorf("get block for ip %s error: %v", ip, err)
		}
	}

	// parse ip to ordinal and get handle id for this ip
	ordinal, err := ipBlock.IPToOrdinal(*cnet.ParseIP(ip))
	if err != nil {
		return fmt.Errorf("get ip %s ordinal err: %v", ip, err)
	}
	if ipBlock.Spec.Allocations[ordinal] == nil {
		if ingoreError {
			fmt.Printf("ip %s was not allocated in ipamblock %s,skip release and ignore\n", ip, ipBlock.Name)
			return nil
		}
		return fmt.Errorf("ip %s was not allocated in ipamblock %s,skip release and ignore", ip, ipBlock.Name)
	}

	attrIndex := *ipBlock.Spec.Allocations[ordinal]
	if attrIndex <= len(ipBlock.Spec.Attributes)-1 {
		handldID = ipBlock.Spec.Attributes[attrIndex].AttrPrimary
	} else {
		//just release ip directly
		fmt.Printf("block %s attribute %d is nil, just release ip %s directly", ipBlock.Name, attrIndex, ip)
		return c.ReleaseIP(ip, ipBlock.Name)
	}

	if handldID == "" {
		fmt.Printf("handldID for ip %s in block %s is empty, just release ip directly\n", ip, ipBlock.Name)
		err = c.ReleaseIP(ip, ipBlock.Name)
		if err != nil {
			return fmt.Errorf("release ip %s error: %v", ip, err)
		}
		return nil
	}

	_, err = c.queryHandle(handldID)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// if ipamhandle not exits, release ip directly
			fmt.Printf("ipamhandle %s not exits, release ip %s directly\n", handldID, ip)
			err = c.ReleaseIP(ip, ipBlock.Name)
			if err != nil {
				return fmt.Errorf("release ip %s error: %v", ip, err)
			}
			return nil
		}
		return fmt.Errorf("query ipamhandle %s error: %v", handldID, err)
	}

	//if ipamhandle exits, release by handle id
	fmt.Printf("found ipamhandle %s for ip %s, release by handle id\n", handldID, ip)
	err = c.ReleaseByHandle(handldID)
	if err != nil {
		fmt.Printf("ReleaseByHandle %s for ip %s error: %v, try to release ip directly\n", handldID, ip, err)

	}
	return nil
}

func (c IPAMClient) ReleaseIP(ip, blockName string) error {
	//query block
	block, err := c.queryBlock(blockName)
	if err != nil {
		return fmt.Errorf("query block %s error: %v", blockName, err)
	}

	//check if the ip belongs to the block
	_, cidr, err := cnet.ParseCIDR(block.Spec.CIDR)
	if err != nil {
		return fmt.Errorf("parse block %s cidr err: %v", block.Name, err)
	}
	if !cidr.Contains(cnet.ParseIP(ip).IP) {
		return fmt.Errorf("ip %s not belong to block %s", ip, blockName)
	}

	//release ip
	err = block.ReleaseByIP(ip)
	if err != nil {
		return fmt.Errorf("release by ip %s error: %v", ip, err)
	}

	//update block
	_, err = c.client.NetworkV1alpha1().IPAMBlocks().Update(context.Background(), block, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update block %s error: %v", blockName, err)
	}

	return nil
}

// RecordUsedIP record used ip in ipamblock
func (c IPAMClient) RecordUsedIP(ip string, usedIPOption UsedIPOption, fix bool) error {
	// get blockName for the ip
	ipBlock, err := c.getBlockForIP(ip)
	if err != nil {
		return fmt.Errorf("get block for ip %s error: %v", ip, err)
	}

	// check if ip already allocated in the block
	ordinal, err := ipBlock.IPToOrdinal(*cnet.ParseIP(ip))
	if err != nil {
		return fmt.Errorf("get ip %s ordinal err: %v", ip, err)
	}
	if ipBlock.Spec.Allocations[ordinal] != nil {
		return fmt.Errorf("ip %s already allocated in block %s, attribute for the ip: %+v, can not record again", ip, ipBlock.Name, ipBlock.Spec.Attributes[ordinal])
		// TODO:check if ipamhandle exits; check if attribute is correct
		// if fix {
		// 	fmt.Printf("checking if ipamhandle %s exits...\n", ipBlock.Spec.Attributes[ordinal].AttrPrimary)
		// 	_, err = c.queryHandle(ipBlock.Spec.Attributes[ordinal].AttrPrimary)
		// }
	}
	usedIPOption.BlockName = ipBlock.Name

	// if do not give blockName, podNamespace, podName, handleID, we should get them from pod info
	if usedIPOption.BlockName == "" || usedIPOption.PodNamespace == "" ||
		usedIPOption.PodName == "" || usedIPOption.HandleID == "" {
		allPods, err := c.podLister.List(labels.Everything())
		if err != nil {
			return fmt.Errorf("get pods list error: %v", err)
		}

		ipPods := []*corev1.Pod{}
		ipPodsNameList := []string{}
		for _, pod := range allPods {
			if pod.Status.PodIP == ip {
				ipPods = append(ipPods, pod)
				ipPodsNameList = append(ipPodsNameList, pod.Namespace+"/"+pod.Name)
			}
		}

		if len(ipPods) == 0 {
			return fmt.Errorf("ip %s not belong to any pod, skip", ip)
		}
		if len(ipPods) > 1 {
			return fmt.Errorf("ip %s belong to multiple pods %v, can not determine which pod to record,shoule check and recreate these pods manually", ip, ipPodsNameList)
		}
		pod := ipPods[0]
		usedIPOption.PodNamespace = pod.Namespace
		usedIPOption.PodName = pod.Name
		usedIPOption.HandleID = pod.Namespace + "-" + pod.Name

	}
	fmt.Printf("get useIPOption for ip %s success: %v, going to record ip...\n", ip, usedIPOption)
	return c.recordUsedIP(ip, usedIPOption.BlockName, usedIPOption.PodNamespace, usedIPOption.PodName, usedIPOption.HandleID)
}

// recordUsedIP record used ip in ipamblock
func (c IPAMClient) recordUsedIP(ip, blockName, podNamespace, podName, handldID string) error {
	//get pod info to ensure ip was allocated to this pod
	pod, err := c.podLister.Pods(podNamespace).Get(podName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			fmt.Printf("pod %s/%s not found, no need to record, skip\n", podNamespace, podName)
			return nil
		}
		return fmt.Errorf("get pod %s/%s error: %v", podNamespace, podName, err)
	}
	if pod.Status.PodIP != ip {
		return fmt.Errorf("ip %s was not allocated to pod %s/%s", ip, podNamespace, podName)
	}

	//allocate ip to pod in ipamblock
	block, err := c.queryBlock(blockName)
	if err != nil {
		return fmt.Errorf("query block %s error: %v", blockName, err)
	}

	//parse ip to ordinal
	ordinal, err := block.IPToOrdinal(*cnet.ParseIP(ip))
	if err != nil {
		return fmt.Errorf("get ip %s ordinal err: %v", ip, err)
	}

	//attr
	attrs := map[string]string{
		IPAMBlockAttributeNamespace: podNamespace,
		IPAMBlockAttributePod:       podName,
		IPAMBlockAttributeNode:      pod.Spec.NodeName,
		IPAMBlockAttributeIP:        ip,
		IPAMBlockAttributeTimestamp: pod.CreationTimestamp.String(),
	}

	//allocate ip from block and update block
	for index, unUsedOrdinal := range block.Spec.Unallocated {
		if ordinal == unUsedOrdinal {
			block.Spec.Unallocated = append(block.Spec.Unallocated[:index], block.Spec.Unallocated[index+1:]...)
			attribute := updateAttribute(block, handldID, attrs)
			block.Spec.Allocations[ordinal] = &attribute
		}
	}

	_, err = c.client.NetworkV1alpha1().IPAMBlocks().Update(context.Background(), block, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update block %s error: %v", blockName, err)
	}

	return nil
}

func (c IPAMClient) getBlockForIP(ip string) (ipBlock *networkv1alpha1.IPAMBlock, err error) {
	pools, err := c.getAllPools()
	if err != nil {
		return nil, fmt.Errorf("get all pools error: %v", err)
	}
	//find which ippool the ip belongs to
	var poolName string
	for _, pool := range pools {
		_, cidr, err := cnet.ParseCIDR(pool.Spec.CIDR)
		if err != nil {
			return nil, fmt.Errorf("parse pool %s cidr err: %v", pool.Name, err)
		}
		if cidr.Contains(cnet.ParseIP(ip).IP) {
			poolName = pool.Name
		}
	}
	if poolName == "" {
		return nil, fmt.Errorf("ip %s not belong to any ippool", ip)
	}

	//get all ipamblocks in the ippool
	blocks, err := c.ListBlocks(poolName)
	if err != nil {
		return nil, fmt.Errorf("list blocks error: %v", err)
	}

	//find which ipamblock the ip belongs to and get handle id for this ip
	for _, block := range blocks {
		_, cidr, err := cnet.ParseCIDR(block.Spec.CIDR)
		if err != nil {
			return nil, fmt.Errorf("parse block %s cidr err: %v", block.Name, err)
		}
		if cidr.Contains(cnet.ParseIP(ip).IP) {
			ipBlock = &block
			return ipBlock, nil
		}
	}

	return nil, fmt.Errorf("ip %s not belong to any exists ipamblock", ip)
}

func (c IPAMClient) GetHandleIDForIP(ip string) (handleID string, err error) {
	//get block for ip
	ipBlock, err := c.getBlockForIP(ip)
	if err != nil {
		return "", fmt.Errorf("get block for ip %s error: %v", ip, err)
	}

	//get handld id for this ip
	ordinal, err := ipBlock.IPToOrdinal(*cnet.ParseIP(ip))
	if err != nil {
		return "", fmt.Errorf("get ip %s ordinal err: %v", ip, err)
	}
	if ipBlock.Spec.Allocations[ordinal] == nil {
		return "", fmt.Errorf("ip %s not allocated in ipamblock %s", ip, ipBlock.Name)
	}
	attrIndex := *ipBlock.Spec.Allocations[ordinal]
	if attrIndex <= len(ipBlock.Spec.Attributes)-1 {
		handleID = ipBlock.Spec.Attributes[attrIndex].AttrPrimary
	} else {
		return "", fmt.Errorf("ip %s not allocated in ipamblock %s", ip, ipBlock.Name)
	}
	return handleID, nil
}

func (c IPAMClient) GetIPByHandleID(handleID string) (ips []string, err error) {
	handle, err := c.queryHandle(handleID)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query handle %s error: %v", handleID, err)
	}
	for blockStr := range handle.Spec.Block {
		blockName := v1alpha1.ConvertToBlockName(blockStr)
		block, err := c.queryBlock(blockName)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// Block doesn't exist, so all addresses are already
				// unallocated.  This can happen when a handle is
				// overestimating the number of assigned addresses.
				return nil, nil
			} else {
				return nil, fmt.Errorf("query block %s error: %v", blockName, err)
			}
		}

		ordinals := block.GetHandleOrdinals(handleID)
		if len(ordinals) == 0 {
			return nil, nil
		}

		for _, ordinal := range ordinals {
			ip, _ := block.OrdinalToIP(ordinal)
			ips = append(ips, ip.String())
		}
	}
	return
}
