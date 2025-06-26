/*
Copyright 2017 The Kubernetes Authors.

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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	poolscheme "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned/scheme"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	networklisters "github.com/yunify/hostnic-cni/pkg/client/listers/network/v1alpha1"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool/ipam"
	"github.com/yunify/hostnic-cni/pkg/timer"
)

const controllerAgentName = "hostnic-controller"

// Controller is the controller implementation for vxnetpool resources
type VxNetPoolController struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// clientset is a clientset for our own API group
	clientset clientset.Interface

	ipamClient ipam.IPAMClient

	ippoolsLister networklisters.IPPoolLister
	ippoolsSynced cache.InformerSynced

	poolsLister networklisters.VxNetPoolLister
	poolsSynced cache.InformerSynced

	ipamblocksLister networklisters.IPAMBlockLister
	ipamblocksSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	// qingcloud cluster config
	conf *conf.ClusterConfig
	// qingcloud info
	rwLock sync.RWMutex
	// vxnet cache data
	vxNetCache map[string]*rpc.VxNet
	// vip cache data
	vipCache map[string][]*rpc.VIP
	// securityGroup cache data
	sgCache map[string]*rpc.SecurityGroupRule
	// job info
	jobs map[string]string

	timer *timer.Timer
}

func NewVxNetPoolController(
	conf *conf.ClusterConfig,
	kubeclientset kubernetes.Interface,
	clientset clientset.Interface,
	informers informers.SharedInformerFactory,
	k8sInformers k8sinformers.SharedInformerFactory,
) *VxNetPoolController {

	utilruntime.Must(poolscheme.AddToScheme(scheme.Scheme))

	ipamblocskInformer := informers.Network().V1alpha1().IPAMBlocks()
	ippoolInformer := informers.Network().V1alpha1().IPPools()
	vxnetpoolInformer := informers.Network().V1alpha1().VxNetPools()

	controller := &VxNetPoolController{
		kubeclientset:    kubeclientset,
		clientset:        clientset,
		ipamClient:       ipam.NewIPAMClient(clientset, networkv1alpha1.IPPoolTypeLocal, informers, k8sInformers),
		ippoolsLister:    ippoolInformer.Lister(),
		ippoolsSynced:    ippoolInformer.Informer().HasSynced,
		poolsLister:      vxnetpoolInformer.Lister(),
		poolsSynced:      vxnetpoolInformer.Informer().HasSynced,
		ipamblocksLister: ipamblocskInformer.Lister(),
		ipamblocksSynced: ipamblocskInformer.Informer().HasSynced,
		workqueue:        workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), controllerAgentName),

		// iaas
		conf:       conf,
		vxNetCache: make(map[string]*rpc.VxNet),
		vipCache:   make(map[string][]*rpc.VIP),
		sgCache:    make(map[string]*rpc.SecurityGroupRule),
		jobs:       make(map[string]string),
	}

	controller.timer = timer.NewTimer(controllerAgentName, 10, controller.qingCloudSync)

	klog.Info("Setting up event handlers")
	vxnetpoolInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueuePool,
		UpdateFunc: func(old, new interface{}) {
			oldVxnetPool := old.(*networkv1alpha1.VxNetPool)
			newVxnetPool := new.(*networkv1alpha1.VxNetPool)
			controller.clearVxnet(oldVxnetPool, newVxnetPool)
			controller.enqueuePool(new)
		},
	})

	ippoolInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newIPPool := new.(*networkv1alpha1.IPPool)
			oldIPPool := old.(*networkv1alpha1.IPPool)
			if newIPPool.ResourceVersion == oldIPPool.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *VxNetPoolController) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Infof("Starting %s", controllerAgentName)

	go c.timer.Run(stopCh)

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.ippoolsSynced, c.poolsSynced, c.ipamblocksSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *VxNetPoolController) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *VxNetPoolController) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.V(4).Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the resource
// with the current status of the resource.
func (c *VxNetPoolController) syncHandler(name string) error {
	if name != constants.IPAMVxnetPoolName {
		klog.V(3).Infof("Don't care about %s", name)
		return nil
	}

	pool, err := c.poolsLister.Get(name)
	if err != nil {
		// The resource may no longer exist, in which case we stop processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("vxnetpool '%s' in work queue no longer exists", name))
			return nil
		}
		return err
	}
	if pool.DeletionTimestamp != nil {
		klog.V(3).Infof("Graceful delete vxnetpool %s", name)
		return nil
	}

	// takeup QCloud resource
	if ok, err := c.prepareQcloudResource(pool); err != nil {
		return err
	} else if !ok {
		return c.updatePoolStatus(pool, false)
	}

	// allocator k8s resource
	if ok, err := c.prepareK8SResource(pool); err != nil {
		return err
	} else if !ok {
		return c.updatePoolStatus(pool, false)
	}

	err = c.updatePoolStatus(pool, true)
	if err != nil {
		return err
	}

	return nil
}

func (c *VxNetPoolController) getPools(pool *networkv1alpha1.VxNetPool) ([]networkv1alpha1.PoolInfo, error) {
	var pools []networkv1alpha1.PoolInfo
	for _, vxnet := range pool.Spec.Vxnets {
		// if blocks, err := c.clientset.NetworkV1alpha1().IPAMBlocks().List(context.Background(), metav1.ListOptions{
		req, err := labels.NewRequirement(networkv1alpha1.IPPoolNameLabel, selection.In, []string{vxnet.Name})
		if err != nil {
			klog.Errorf("new requirement for vxnet %s error: %v", vxnet.Name, err)
			continue
		}
		blocks, err := c.ipamblocksLister.List(labels.NewSelector().Add(*req))
		if err != nil {
			return nil, err
		} else {
			var subnets []string
			for _, item := range blocks {
				if item != nil {
					subnets = append(subnets, item.Name)
				}
			}
			pools = append(pools, networkv1alpha1.PoolInfo{
				Name:    vxnet.Name,
				IPPool:  vxnet.Name,
				Subnets: subnets,
			})
		}
	}

	return pools, nil
}

func (c *VxNetPoolController) updatePoolStatus(pool *networkv1alpha1.VxNetPool, ready bool) error {
	pools, err := c.getPools(pool)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(pool.Status.Pools, pools) && pool.Status.Ready == ready {
		return nil
	}

	poolCopy := pool.DeepCopy()
	poolCopy.Status.Ready = ready
	poolCopy.Status.Pools = pools

	_, err = c.clientset.NetworkV1alpha1().VxNetPools().UpdateStatus(context.TODO(), poolCopy, metav1.UpdateOptions{})
	return err
}

func (c *VxNetPoolController) enqueuePool(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// clearVxnet try to clear the related resources with the vxnet deleted from vxnetpool named v-pool
// clear related resources if this vxnet was not being used by pod (ippool.status.allocations == 0)
// 1. release vip
// 2. delete ippool
func (c *VxNetPoolController) clearVxnet(old, new *networkv1alpha1.VxNetPool) {
	if new.Name != constants.IPAMVxnetPoolName {
		klog.V(3).Infof("Don't care about %s", new.Name)
		return
	}

	newVxnetsMap := make(map[string]bool)
	for _, newVxnet := range new.Spec.Vxnets {
		newVxnetsMap[newVxnet.Name] = true
	}

	for _, oldVxnet := range old.Spec.Vxnets {
		if !newVxnetsMap[oldVxnet.Name] {
			ippool, err := c.ippoolsLister.Get(oldVxnet.Name)
			if err != nil {
				klog.Errorf("get ippool %s error: %v", oldVxnet.Name, err)
				continue
			}

			if ippool.Status.Allocations > 0 {
				klog.Infof("vxnet %s was being deleted from vxnetpool, but this vxnet was already used by some pods, skip clear vip and ippool!", oldVxnet.Name)
				continue
			}

			klog.Infof("vxnet %s was being deleted from vxnetpool and not being used by pod, going to clear vip and ippool", oldVxnet.Name)

			// release vip
			go c.deleteVIPsByVxnetID(oldVxnet.Name)

			// delete ippool
			err = c.clientset.NetworkV1alpha1().IPPools().Delete(context.TODO(), ippool.Name, metav1.DeleteOptions{})
			if err != nil {
				klog.Errorf("delete ippool %s error: %v", ippool.Name, err)
			} else {
				klog.Infof("delete ippool %s success", ippool.Name)
			}
		}
	}
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *VxNetPoolController) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a vxnetpool, we should not do anything more with it.
		if ownerRef.Kind != "VxNetPool" {
			return
		}

		pool, err := c.poolsLister.Get(ownerRef.Name)
		if err != nil {
			klog.V(4).Infof("ignoring orphaned object '%s' of pool '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		c.enqueuePool(pool)
		return
	}
}

// newIPPool creates a new IPPool for a vxnet resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the vxnet resource that 'owns' it.
func (c *VxNetPoolController) createIPPool(name string, blockSize int, customReservedIPCount int64, vxnet *rpc.VxNet) (*networkv1alpha1.IPPool, error) {
	ippool := &networkv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"controller": name,
			},
		},
		Spec: networkv1alpha1.IPPoolSpec{
			Type:                  networkv1alpha1.IPPoolTypeLocal,
			CIDR:                  vxnet.Network,
			Gateway:               vxnet.Gateway,
			BlockSize:             blockSize,
			CustomReservedIPCount: customReservedIPCount,
			RangeStart:            vxnet.IPStart,
			RangeEnd:              vxnet.IPEnd,
		},
	}
	return c.clientset.NetworkV1alpha1().IPPools().Create(context.TODO(), ippool, metav1.CreateOptions{})
}

// newBlocks creates a list of IPAMBlocks from ippool. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the ippool resource that 'owns' it.
func (c *VxNetPoolController) createBlocksFromIPPool(ippool *networkv1alpha1.IPPool) error {
	return c.ipamClient.AutoGenerateBlocksFromPool(ippool.Name)
}

func keyForVxNetVIP(vxnet string) string {
	return "VIP|" + vxnet
}

func keyForVxNetSG(sg, vxnet string) string {
	return "SG|" + sg + "/" + vxnet
}

func (c *VxNetPoolController) prepareQcloudResource(pool *networkv1alpha1.VxNetPool) (bool, error) {
	// 1. check vxnet
	for _, vxnet := range pool.Spec.Vxnets {
		if _, ok := c.getVxNetInfo(vxnet.Name); !ok {
			klog.Warningf("vxnet %s not sync", vxnet.Name)
			return false, nil
		}
	}

	// 2. check and pre-Allocator vips
	for _, vxnet := range pool.Spec.Vxnets {
		if _, ok := c.getVxNetVIPInfo(vxnet.Name); !ok {
			klog.Warningf("vips for vxnet %s not ready", vxnet.Name)
			v, _ := c.getVxNetInfo(vxnet.Name)
			if job, ok := c.getJob(keyForVxNetVIP(v.ID)); ok {
				klog.Warningf("vips for vxnet %s: wait job %s", vxnet.Name, job)
				return false, nil
			}
			if job, err := qcclient.QClient.CreateVIPs(v); err != nil {
				return false, err
			} else {
				c.setJob(keyForVxNetVIP(v.ID), job)
				klog.V(3).Infof("CreateVIPs for vxnet %s: %s", vxnet.Name, job)
			}
			return false, nil
		}
	}

	// 3. check and add security-group for vxnet
	if sg := c.conf.SecurityGroup; sg != "" {
		for _, vxnet := range pool.Spec.Vxnets {
			id := keyForVxNetSG(sg, vxnet.Name)
			if _, ok := c.getSecurityGroupRule(id); !ok {
				klog.Warningf("SecurityGroupRule for vxnet %s not ready", vxnet.Name)
				if job, ok := c.getJob(id); ok {
					klog.Warningf("sg for vxnet %s: wait job %s", vxnet.Name, job)
					return false, nil
				}
				v, _ := c.getVxNetInfo(vxnet.Name)
				if job, err := qcclient.QClient.CreateSecurityGroupRuleForVxNet(sg, v); err != nil {
					return false, err
				} else {
					c.setJob(id, job)
					klog.V(3).Infof("CreateSecurityGroupRuleForVxNet for vxnet %s: %s", vxnet.Name, job)
				}
				return false, nil
			}
		}
	}

	return true, nil
}

// create ippool and ipamblock
func (c *VxNetPoolController) prepareK8SResource(pool *networkv1alpha1.VxNetPool) (bool, error) {
	for _, vxnet := range pool.Spec.Vxnets {
		ippool, err := c.ippoolsLister.Get(vxnet.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				// create ippool
				if v, ok := c.getVxNetInfo(vxnet.Name); ok {
					if _, err := c.createIPPool(vxnet.Name, pool.Spec.BlockSize, pool.Spec.CustomReservedIPCount, v); err != nil {
						return false, err
					} else {
						// handle it's block at next event
						return false, nil
					}
				} else {
					return false, nil
				}
			}
			return false, err
		}
		// prepare blocks for ippool
		if err := c.createBlocksFromIPPool(ippool); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (c *VxNetPoolController) getVxNetInfo(vxnet string) (*rpc.VxNet, bool) {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	result, ok := c.vxNetCache[vxnet]
	return result, ok
}

func (c *VxNetPoolController) setVxNetInfo(vxnet string, v *rpc.VxNet) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.vxNetCache[vxnet] = v
}

func (c *VxNetPoolController) getVxNetVIPInfo(vxnet string) ([]*rpc.VIP, bool) {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	result, ok := c.vipCache[vxnet]
	return result, ok
}

func (c *VxNetPoolController) setVxNetVIPInfo(vxnet string, v []*rpc.VIP) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.vipCache[vxnet] = v
}

func (c *VxNetPoolController) getSecurityGroupRule(id string) (*rpc.SecurityGroupRule, bool) {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	result, ok := c.sgCache[id]
	return result, ok
}

func (c *VxNetPoolController) setSecurityGroupRule(id string, v *rpc.SecurityGroupRule) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.sgCache[id] = v
}

func (c *VxNetPoolController) deleteJob(id string) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	delete(c.jobs, id)
}

func (c *VxNetPoolController) getJob(id string) (string, bool) {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	job, ok := c.jobs[id]
	return job, ok
}

func (c *VxNetPoolController) setJob(id, job string) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.jobs[id] = job
}

func (c *VxNetPoolController) deleteVIPsByVxnetID(vxnetID string) {
	vxnets, err := qcclient.QClient.GetVxNets([]string{vxnetID}, 0)
	if err != nil {
		klog.Errorf("Get info for vxnet %s failed: %v\n", vxnetID, err)
		return
	}
	if len(vxnets) == 0 {
		klog.Infof("VxNet %s: not found, skip delete vip", vxnetID)
		return
	}

	vxnet := vxnets[vxnetID]

	for {
		if vips, err := qcclient.QClient.DescribeVIPs(vxnet); err != nil {
			klog.Errorf("Get vips for vxnet %s failed: %v\n", vxnet.ID, err)
			return
		} else {
			if len(vips) == 0 {
				break
			}
			var vipsToDel []string
			for _, vip := range vips {
				vipsToDel = append(vipsToDel, vip.ID)
			}
			if job, err := qcclient.QClient.DeleteVIPs(vipsToDel); err != nil {
				klog.Errorf("Clear vips for VxNet %s failed: %v, will retry", vxnet.ID, err)
			} else {
				klog.Infof("Clear vips for VxNet %s: count %d, job id %s", vxnet.ID, len(vips), job)
			}
		}
		time.Sleep(2 * time.Second)
	}
	// delete vxnet cache
	delete(c.vxNetCache, vxnetID)
	// delete vip cache
	delete(c.vipCache, vxnetID)
	klog.Infof("Clear vips for VxNet %s success\n", vxnet.ID)
}

func (c *VxNetPoolController) qingCloudSync() {
	klog.V(4).Infof("qingCloudSync: start")
	defer klog.V(4).Infof("qingCloudSync: end")

	var change bool
	pool, err := c.poolsLister.Get(constants.IPAMVxnetPoolName)
	if err != nil {
		klog.Errorf("Get VxNetPool failed: %v", err)
		return
	}

	// 1. update vxnets
	var needUpdate []string
	for _, vxnet := range pool.Spec.Vxnets {
		if _, ok := c.getVxNetInfo(vxnet.Name); !ok {
			needUpdate = append(needUpdate, vxnet.Name)
		}
	}
	if len(needUpdate) > 0 {
		if result, err := qcclient.QClient.GetVxNets(needUpdate, pool.Spec.CustomReservedIPCount); err != nil {
			klog.Errorf("Get vxnet %v from QingCloud failed: %v", needUpdate, err)
			return
		} else {
			for k, v := range result {
				c.setVxNetInfo(k, v)
				klog.V(4).Infof("Get vxnet %s from QingCloud: %v", k, v)
			}
			if len(result) == len(needUpdate) {
				change = true
			} else {
				klog.Warningf("Get some vxnet from QingCloud failed: %v, %+v", needUpdate, result)
			}
		}
	}

	// 2. update vips
	for _, vxnet := range c.vxNetCache {
		if _, ok := c.getVxNetVIPInfo(vxnet.ID); !ok {
			if result, err := qcclient.QClient.DescribeVIPs(vxnet); err != nil {
				klog.Errorf("Get vxnet %s's vips from QingCloud failed: %v", vxnet.ID, err)
				return
			} else {
				if len(result) > 0 {
					c.setVxNetVIPInfo(vxnet.ID, result)
					c.deleteJob(keyForVxNetVIP(vxnet.ID))
					klog.V(4).Infof("Get vips for vxnet %s from QingCloud: %v", vxnet.ID, result)
					change = true
				} else {
					klog.Warningf("Get vips for vxnet %s from QingCloud failed: not prepare", vxnet.ID)
				}
			}
		}
	}

	// 3. update securityGroup rule
	klog.V(4).Infof("qingCloudSync: DescribeClusterSecurityGroup")
	if c.conf.ClusterID != "" {
		if result, err := qcclient.QClient.DescribeClusterSecurityGroup(c.conf.ClusterID); err != nil {
			klog.Errorf("Get SecurityGroup for Cluster %s from QingCloud failed: %v", c.conf.ClusterID, err)
			return
		} else {
			if result != c.conf.SecurityGroup {
				change = true
				klog.V(3).Infof("SecurityGroup for Cluster %s change from %s to %s", c.conf.ClusterID, c.conf.SecurityGroup, result)
			}
			c.conf.SecurityGroup = result
		}
	}
	if c.conf.SecurityGroup != "" {
		for _, vxnet := range c.vxNetCache {
			id := keyForVxNetSG(c.conf.SecurityGroup, vxnet.ID)
			if _, ok := c.getSecurityGroupRule(id); !ok {
				if result, err := qcclient.QClient.GetSecurityGroupRuleForVxNet(c.conf.SecurityGroup, vxnet); err != nil {
					klog.Errorf("Get SecurityGroupRule for vxnet %s in %s from QingCloud failed: %v", vxnet.ID, c.conf.SecurityGroup, err)
					return
				} else {
					if result != nil {
						c.setSecurityGroupRule(id, result)
						c.deleteJob(id)
						klog.V(4).Infof("Get SecurityGroupRule for vxnet %s in %s from QingCloud: %v", vxnet.ID, c.conf.SecurityGroup, result)
						change = true
					} else {
						klog.Warningf("Get SecurityGroupRule for vxnet %s in %s from QingCloud failed: not prepare", vxnet.ID, c.conf.SecurityGroup)
					}
				}
			}
		}
	}

	// 4. sync to controller
	if change {
		c.enqueuePool(pool)
	}
}
