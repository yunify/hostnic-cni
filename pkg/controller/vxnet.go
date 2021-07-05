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
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	vxnetv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/vxnet/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	poolscheme "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned/scheme"
	networkinformers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions/network/v1alpha1"
	vxnetinformers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions/vxnet/v1alpha1"
	networklisters "github.com/yunify/hostnic-cni/pkg/client/listers/network/v1alpha1"
	vxnetlisters "github.com/yunify/hostnic-cni/pkg/client/listers/vxnet/v1alpha1"
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

	poolsLister vxnetlisters.VxNetPoolLister
	poolsSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	// qingcloud info
	rwLock    sync.RWMutex
	vxnetPool string
	// vxnet cache data
	vxNetCache map[string]*rpc.VxNet
	// vip cache data
	vipCache map[string][]*rpc.VIP
	// job info
	// vips in vxnet -> jobID
	jobs map[string]string

	timer *timer.Timer
}

func NewVxNetPoolController(
	kubeclientset kubernetes.Interface,
	clientset clientset.Interface,
	vxnetPool string,
	ippoolInformer networkinformers.IPPoolInformer,
	poolInformer vxnetinformers.VxNetPoolInformer) *VxNetPoolController {

	utilruntime.Must(poolscheme.AddToScheme(scheme.Scheme))

	controller := &VxNetPoolController{
		kubeclientset: kubeclientset,
		clientset:     clientset,
		ipamClient:    ipam.NewIPAMClient(clientset, networkv1alpha1.IPPoolTypeLocal),
		ippoolsLister: ippoolInformer.Lister(),
		ippoolsSynced: ippoolInformer.Informer().HasSynced,
		poolsLister:   poolInformer.Lister(),
		poolsSynced:   poolInformer.Informer().HasSynced,
		workqueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), controllerAgentName),

		// iaas
		vxnetPool:  vxnetPool,
		vxNetCache: make(map[string]*rpc.VxNet),
		vipCache:   make(map[string][]*rpc.VIP),
		jobs:       make(map[string]string),
	}

	controller.timer = timer.NewTimer(controllerAgentName, 10, controller.qingCloudSync)

	klog.Info("Setting up event handlers")
	poolInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueuePool,
		UpdateFunc: func(old, new interface{}) {
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
	if ok := cache.WaitForCacheSync(stopCh, c.ippoolsSynced, c.poolsSynced); !ok {
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
		klog.Infof("Successfully synced '%s'", key)
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
	if name != c.vxnetPool {
		klog.Infof("Don't care about %s", name)
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
		klog.Infof("Graceful delete vxnetpool %s", name)
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

func (c *VxNetPoolController) getPools(pool *vxnetv1alpha1.VxNetPool) ([]vxnetv1alpha1.PoolInfo, error) {
	var pools []vxnetv1alpha1.PoolInfo
	for _, vxnet := range pool.Spec.Vxnets {
		if blocks, err := c.clientset.NetworkV1alpha1().IPAMBlocks().List(context.Background(), metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(labels.Set{
				networkv1alpha1.IPPoolNameLabel: vxnet.Name,
			}).String()}); err != nil {
			return nil, err
		} else {
			var subnets []string
			for _, item := range blocks.Items {
				subnets = append(subnets, item.Name)
			}
			pools = append(pools, vxnetv1alpha1.PoolInfo{
				Name:    vxnet.Name,
				IPPool:  vxnet.Name,
				Subnets: subnets,
			})
		}
	}

	return pools, nil
}

func (c *VxNetPoolController) updatePoolStatus(pool *vxnetv1alpha1.VxNetPool, ready bool) error {
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

	_, err = c.clientset.VxnetV1alpha1().VxNetPools().UpdateStatus(context.TODO(), poolCopy, metav1.UpdateOptions{})
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
func (c *VxNetPoolController) createIPPool(name string, blockSize int, vxnet *rpc.VxNet) (*networkv1alpha1.IPPool, error) {
	ippool := &networkv1alpha1.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"controller": name,
			},
		},
		Spec: networkv1alpha1.IPPoolSpec{
			Type:       networkv1alpha1.IPPoolTypeLocal,
			CIDR:       vxnet.Network,
			Gateway:    vxnet.Gateway,
			BlockSize:  blockSize,
			RangeStart: vxnet.IPStart,
			RangeEnd:   vxnet.IPEnd,
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

func (c *VxNetPoolController) prepareQcloudResource(pool *vxnetv1alpha1.VxNetPool) (bool, error) {
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
			if job, ok := c.getJob(v.ID); ok {
				klog.Warningf("vips for vxnet %s: wait job %s", vxnet.Name, job)
				return false, nil
			}
			if job, err := qcclient.QClient.CreateVIPs(v); err != nil {
				return false, err
			} else {
				c.setJob(v.ID, job)
			}
			return false, nil
		}
	}

	return true, nil
}

// create ippool and ipamblock
func (c *VxNetPoolController) prepareK8SResource(pool *vxnetv1alpha1.VxNetPool) (bool, error) {
	for _, vxnet := range pool.Spec.Vxnets {
		ippool, err := c.ippoolsLister.Get(vxnet.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				// create ippool
				if v, ok := c.getVxNetInfo(vxnet.Name); ok {
					if _, err := c.createIPPool(vxnet.Name, pool.Spec.BlockSize, v); err != nil {
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

func (c *VxNetPoolController) setVxNetInfo(k string, v *rpc.VxNet) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.vxNetCache[k] = v
	return
}

func (c *VxNetPoolController) getVxNetVIPInfo(vxnet string) ([]*rpc.VIP, bool) {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	result, ok := c.vipCache[vxnet]
	return result, ok
}

func (c *VxNetPoolController) setVxNetVIPInfo(k string, v []*rpc.VIP) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.vipCache[k] = v
	return
}

func (c *VxNetPoolController) deleteJobByVxNet(vxnet string) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	delete(c.jobs, vxnet)
}

func (c *VxNetPoolController) deleteJobByJob(job string) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	for k, v := range c.jobs {
		if v == job {
			delete(c.jobs, k)
		}
	}
}

func (c *VxNetPoolController) getJob(vxnet string) (string, bool) {
	c.rwLock.RLock()
	defer c.rwLock.RUnlock()

	job, ok := c.jobs[vxnet]
	return job, ok
}

func (c *VxNetPoolController) setJob(vips, job string) {
	c.rwLock.Lock()
	defer c.rwLock.Unlock()

	c.jobs[vips] = job
	return
}

func (c *VxNetPoolController) qingCloudSync() {
	var change bool
	pool, err := c.poolsLister.Get(c.vxnetPool)
	if err != nil {
		klog.Errorf("Get VxNetPool failed: %v", err)
		return
	}

	// 1. get vxnets
	var needUpdate []string
	for _, vxnet := range pool.Spec.Vxnets {
		if _, ok := c.getVxNetInfo(vxnet.Name); !ok {
			needUpdate = append(needUpdate, vxnet.Name)
		}
	}
	if len(needUpdate) > 0 {
		if result, err := qcclient.QClient.GetVxNets(needUpdate); err != nil {
			klog.Errorf("Get vxnet %v from QingCloud failed: %v", needUpdate, err)
			return
		} else {
			for k, v := range result {
				c.setVxNetInfo(k, v)
				klog.Infof("Get vxnet %s from QingCloud: %v", k, v)
			}
			if len(result) == len(needUpdate) {
				change = true
			} else {
				klog.Warningf("Get some vxnet from QingCloud failed: %v", needUpdate, result)
			}
		}
	}

	// 2. create vips
	for _, vxnet := range c.vxNetCache {
		if _, ok := c.getVxNetVIPInfo(vxnet.ID); !ok {
			if result, err := qcclient.QClient.DescribeVIPs(vxnet); err != nil {
				klog.Errorf("Get vxnet %s's vips from QingCloud failed: %v", vxnet.ID, err)
				return
			} else {
				if len(result) > 0 {
					c.setVxNetVIPInfo(vxnet.ID, result)
					c.deleteJobByVxNet(vxnet.ID)
					klog.Infof("Get vips for vxnet %s from QingCloud: %v", vxnet.ID, result)
					change = true
				} else {
					klog.Warningf("Get vips for vxnet %s from QingCloud failed: not prepare", vxnet.ID)
				}
			}
		}
	}

	// 3. sync to controller
	if change {
		c.enqueuePool(pool)
	}
}
