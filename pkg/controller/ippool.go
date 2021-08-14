package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	cnet "github.com/projectcalico/libcalico-go/lib/net"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sinformers "k8s.io/client-go/informers"
	coreinfomers "k8s.io/client-go/informers/core/v1"
	k8sclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	networkv1alpha1 "github.com/yunify/hostnic-cni/pkg/apis/network/v1alpha1"
	clientset "github.com/yunify/hostnic-cni/pkg/client/clientset/versioned"
	informers "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions"
	networkInformer "github.com/yunify/hostnic-cni/pkg/client/informers/externalversions/network/v1alpha1"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/controller/utils"
	"github.com/yunify/hostnic-cni/pkg/simple/client/network/ippool"
)

type workItem struct {
	Event string
	Name  string
}

type IPPoolController struct {
	eventBroadcaster record.EventBroadcaster
	eventRecorder    record.EventRecorder

	provider ippool.Provider

	ippoolInformer networkInformer.IPPoolInformer
	ippoolSynced   cache.InformerSynced
	ippoolQueue    workqueue.RateLimitingInterface

	nsInformer coreinfomers.NamespaceInformer
	nsSynced   cache.InformerSynced
	nsQueue    workqueue.RateLimitingInterface

	ipamblockInformer networkInformer.IPAMBlockInformer
	ipamblockSynced   cache.InformerSynced

	k8sclient k8sclientset.Interface
	client    clientset.Interface
}

func (c *IPPoolController) enqueueIPPools(obj interface{}) {
	pool, ok := obj.(*networkv1alpha1.IPPool)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("IPPool informer returned non-ippool object: %#v", obj))
		return
	}

	c.ippoolQueue.Add(pool.Name)
}

func (c *IPPoolController) addFinalizer(pool *networkv1alpha1.IPPool) error {
	clone := pool.DeepCopy()
	controllerutil.AddFinalizer(clone, networkv1alpha1.IPPoolFinalizer)
	if clone.Labels == nil {
		clone.Labels = make(map[string]string)
	}
	clone.Labels[networkv1alpha1.IPPoolNameLabel] = clone.Name
	clone.Labels[networkv1alpha1.IPPoolTypeLabel] = clone.Spec.Type
	clone.Labels[networkv1alpha1.IPPoolIDLabel] = fmt.Sprintf("%d", clone.ID())
	pool, err := c.client.NetworkV1alpha1().IPPools().Update(context.TODO(), clone, metav1.UpdateOptions{})
	if err != nil {
		klog.V(3).Infof("Error adding  finalizer to pool %s: %v", pool.Name, err)
		return err
	}
	klog.V(3).Infof("Added finalizer to pool %s", pool.Name)
	return nil
}

func (c *IPPoolController) removeFinalizer(pool *networkv1alpha1.IPPool) error {
	clone := pool.DeepCopy()
	controllerutil.RemoveFinalizer(clone, networkv1alpha1.IPPoolFinalizer)
	pool, err := c.client.NetworkV1alpha1().IPPools().Update(context.TODO(), clone, metav1.UpdateOptions{})
	if err != nil {
		klog.V(3).Infof("Error removing  finalizer from pool %s: %v", pool.Name, err)
		return err
	}
	klog.V(3).Infof("Removed protection finalizer from pool %s", pool.Name)
	return nil
}

func (c *IPPoolController) ValidateCreate(obj runtime.Object) error {
	b := obj.(*networkv1alpha1.IPPool)
	ip, cidr, err := cnet.ParseCIDR(b.Spec.CIDR)
	if err != nil {
		return fmt.Errorf("invalid cidr")
	}

	size, _ := cidr.Mask.Size()
	if ip.IP.To4() != nil && size == 32 {
		return fmt.Errorf("the cidr mask must be less than 32")
	}
	if b.Spec.BlockSize > 0 && b.Spec.BlockSize < size {
		return fmt.Errorf("the blocksize should be larger than the cidr mask")
	}

	if b.Spec.RangeStart != "" || b.Spec.RangeEnd != "" {
		iStart := cnet.ParseIP(b.Spec.RangeStart)
		iEnd := cnet.ParseIP(b.Spec.RangeEnd)
		if iStart == nil || iEnd == nil {
			return fmt.Errorf("invalid rangeStart or rangeEnd")
		}
		offsetStart, err := b.IPToOrdinal(*iStart)
		if err != nil {
			return err
		}
		offsetEnd, err := b.IPToOrdinal(*iEnd)
		if err != nil {
			return err
		}
		if offsetEnd < offsetStart {
			return fmt.Errorf("rangeStart should not big than rangeEnd")
		}
	}

	pools, err := c.client.NetworkV1alpha1().IPPools().List(context.TODO(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			networkv1alpha1.IPPoolIDLabel: fmt.Sprintf("%d", b.ID()),
		}).String(),
	})
	if err != nil {
		return err
	}

	for _, p := range pools.Items {
		if b.Overlapped(p) {
			return fmt.Errorf("ippool cidr is overlapped with %s", p.Name)
		}
	}

	return nil
}

func (c *IPPoolController) validateDefaultIPPool(p *networkv1alpha1.IPPool) error {
	pools, err := c.client.NetworkV1alpha1().IPPools().List(context.TODO(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(
			labels.Set{
				networkv1alpha1.IPPoolDefaultLabel: "",
			}).String(),
	})
	if err != nil {
		return err
	}

	poolLen := len(pools.Items)
	if poolLen != 1 || pools.Items[0].Name != p.Name {
		return nil
	}

	return fmt.Errorf("Must ensure that there is at least one default ippool")
}

func (c *IPPoolController) ValidateUpdate(old runtime.Object, new runtime.Object) error {
	oldP := old.(*networkv1alpha1.IPPool)
	newP := new.(*networkv1alpha1.IPPool)

	if newP.Spec.CIDR != oldP.Spec.CIDR {
		return fmt.Errorf("cidr cannot be modified")
	}

	if newP.Spec.Type != oldP.Spec.Type {
		return fmt.Errorf("ippool type cannot be modified")
	}

	if newP.Spec.BlockSize != oldP.Spec.BlockSize {
		return fmt.Errorf("ippool blockSize cannot be modified")
	}

	if newP.Spec.RangeEnd != oldP.Spec.RangeEnd || newP.Spec.RangeStart != oldP.Spec.RangeStart {
		return fmt.Errorf("ippool rangeEnd/rangeStart cannot be modified")
	}

	_, defaultOld := oldP.Labels[networkv1alpha1.IPPoolDefaultLabel]
	_, defaultNew := newP.Labels[networkv1alpha1.IPPoolDefaultLabel]
	if !defaultNew && defaultOld != defaultNew {
		err := c.validateDefaultIPPool(newP)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *IPPoolController) ValidateDelete(obj runtime.Object) error {
	p := obj.(*networkv1alpha1.IPPool)

	if p.Status.Allocations > 0 {
		return fmt.Errorf("ippool is in use, please remove the workload before deleting")
	}

	return c.validateDefaultIPPool(p)
}

func (c *IPPoolController) disableIPPool(old *networkv1alpha1.IPPool) error {
	if old.Spec.Disabled {
		return nil
	}

	clone := old.DeepCopy()
	clone.Spec.Disabled = true

	_, err := c.client.NetworkV1alpha1().IPPools().Update(context.TODO(), clone, metav1.UpdateOptions{})

	return err
}

func (c *IPPoolController) updateIPPoolStatus(old *networkv1alpha1.IPPool) error {
	new, err := c.provider.GetIPPoolStats(old)
	if err != nil {
		return fmt.Errorf("failed to get ippool %s status %v", old.Name, err)
	}

	if reflect.DeepEqual(old.Status, new.Status) {
		return nil
	}

	_, err = c.client.NetworkV1alpha1().IPPools().UpdateStatus(context.TODO(), new, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ippool %s status  %v", old.Name, err)
	}

	return nil
}

func (c *IPPoolController) processIPPool(name string) (*time.Duration, error) {
	klog.V(4).Infof("Processing IPPool %s", name)
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished processing IPPool %s (%v)", name, time.Since(startTime))
	}()

	pool, err := c.ippoolInformer.Lister().Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get ippool %s: %v", name, err)
	}

	if pool.Type() != c.provider.Type() {
		klog.V(4).Infof("pool %s type not match, ignored", pool.Name)
		return nil, nil
	}

	if utils.IsDeletionCandidate(pool, networkv1alpha1.IPPoolFinalizer) {
		err = c.disableIPPool(pool)
		if err != nil {
			return nil, err
		}

		// Pool should be deleted. Check if it's used and remove finalizer if
		// it's not.
		canDelete, err := c.provider.DeleteIPPool(pool)
		if err != nil {
			return nil, err
		}

		if canDelete {
			return nil, c.removeFinalizer(pool)
		}

		//The  ippool is being used, update status and try again later.
		delay := time.Second * 3
		return &delay, c.updateIPPoolStatus(pool)
	}

	if utils.NeedToAddFinalizer(pool, networkv1alpha1.IPPoolFinalizer) {
		err = c.addFinalizer(pool)
		if err != nil {
			return nil, err
		}

		err = c.provider.CreateIPPool(pool)
		if err != nil {
			klog.V(4).Infof("Provider failed to create IPPool %s, err=%v", pool.Name, err)
			return nil, err
		}

		return nil, c.updateIPPoolStatus(pool)
	}

	err = c.provider.UpdateIPPool(pool)
	if err != nil {
		klog.V(4).Infof("Provider failed to update IPPool %s, err=%v", pool.Name, err)
		return nil, err
	}

	return nil, c.updateIPPoolStatus(pool)
}

func (c *IPPoolController) Start(stopCh <-chan struct{}) error {
	go c.provider.SyncStatus(stopCh, c.ippoolQueue)
	return c.Run(5, stopCh)
}

func (c *IPPoolController) Run(workers int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.ippoolQueue.ShutDown()

	klog.Info("starting ippool controller")
	defer klog.Info("shutting down ippool controller")

	if !cache.WaitForCacheSync(stopCh, c.ippoolSynced, c.ipamblockSynced, c.nsSynced) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	for i := 0; i < workers; i++ {
		go wait.Until(c.runIPPoolWorker, time.Second, stopCh)
	}

	// only one thread to handle ns
	go wait.Until(c.runNSWorker, time.Second, stopCh)

	<-stopCh
	return nil
}

func (c *IPPoolController) runIPPoolWorker() {
	for c.processIPPoolItem() {
	}
}

func (c *IPPoolController) processIPPoolItem() bool {
	key, quit := c.ippoolQueue.Get()
	if quit {
		return false
	}
	defer c.ippoolQueue.Done(key)

	delay, err := c.processIPPool(key.(string))
	if err == nil {
		c.ippoolQueue.Forget(key)
		return true
	}

	if delay != nil {
		c.ippoolQueue.AddAfter(key, *delay)
	} else {
		c.ippoolQueue.AddRateLimited(key)
	}
	utilruntime.HandleError(fmt.Errorf("error processing ippool %v (will retry): %v", key, err))
	return true
}

func (c *IPPoolController) runNSWorker() {
	for c.processNSItem() {
	}
}

func contains(items []string, item string) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}

func (c *IPPoolController) getFreeIPAMBlock(apps map[string][]string) (string, error) {
	vxnetpool, err := c.client.NetworkV1alpha1().VxNetPools().Get(context.TODO(), constants.IPAMVxnetPoolName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	status := vxnetpool.Status
	if !status.Ready {
		return "", fmt.Errorf("waiting for VxNetPool %s to be ready", constants.IPAMVxnetPoolName)
	}

	used := make(map[string]struct{})
	for k, v := range apps {
		if k == constants.IPAMDefaultPoolKey {
			for _, pool := range status.Pools {
				if contains(v, pool.IPPool) {
					for _, subnet := range pool.Subnets {
						used[subnet] = struct{}{}
					}
				}
			}
		} else {
			for _, subnet := range v {
				used[subnet] = struct{}{}
			}
		}
	}

	for _, pool := range status.Pools {
		for _, subnet := range pool.Subnets {
			if _, ok := used[subnet]; !ok {
				return subnet, nil
			}
		}
	}

	return "", fmt.Errorf("no free subnet was found")
}

func (c *IPPoolController) getRelationNSFromBlock(block string) (string, error) {
	cm, err := c.k8sclient.CoreV1().ConfigMaps(constants.IPAMConfigNamespace).Get(context.TODO(), constants.IPAMConfigName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	var apps map[string][]string
	if err := json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &apps); err != nil {
		return "", err
	}

	for ns, blocks := range apps {
		if contains(blocks, block) {
			return ns, nil
		}
	}
	return "", nil
}

func (c *IPPoolController) isBlocksEmpty(blocks []string) (bool, error) {
	for _, block := range blocks {
		blockObj, exists, err := c.ipamblockInformer.Informer().GetStore().GetByKey(block)
		if err != nil {
			return false, err
		}
		if !exists {
			klog.Warningf("block %s not found", block)
			continue
		}

		block := blockObj.(*networkv1alpha1.IPAMBlock)
		if block.NumFreeAddresses() != 0 {
			return false, nil
		}
	}
	return true, nil
}

func (c *IPPoolController) processNS(item workItem) error {
	klog.V(4).Infof("Processing namespace %v", item)
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished processing namespace %v (%v)", item, time.Since(startTime))
	}()

	_, err := c.nsInformer.Lister().Get(item.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	cm, err := c.k8sclient.CoreV1().ConfigMaps(constants.IPAMConfigNamespace).Get(context.TODO(), constants.IPAMConfigName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if cm.Data[constants.IPAMAutoAssignForNamespace] != "on" {
		klog.V(4).Infof("Skip handle %v: autoAssign is off", item)
		return nil
	}

	var apps map[string][]string
	if err := json.Unmarshal([]byte(cm.Data[constants.IPAMConfigDate]), &apps); err != nil {
		return err
	}

	change := false
	if item.Event == constants.EventADD {
		if subnets, ok := apps[item.Name]; ok && len(subnets) > 0 {
			// ns has sunbet config, do nothing
			klog.V(4).Infof("Namespace %s has subnets %v", item.Name, subnets)
			return nil
		}

		// ns has no sunbet, pick one for it
		subnet, err := c.getFreeIPAMBlock(apps)
		if err != nil {
			return err
		}
		klog.V(4).Infof("Namespace %s get subnet %s", item.Name, subnet)
		change = true
		apps[item.Name] = []string{subnet}
	}

	// ns's blocks may be empty, and should be expanded
	if item.Event == constants.EventUpdate {
		if subnets, ok := apps[item.Name]; ok {
			if ok, err := c.isBlocksEmpty(subnets); err != nil {
				return err
			} else if ok {
				// ns has no free ip, expand subnet for it
				subnet, err := c.getFreeIPAMBlock(apps)
				if err != nil {
					return err
				}
				klog.V(4).Infof("Namespace %s get subnet %s", item.Name, subnet)
				change = true
				apps[item.Name] = append(subnets, subnet)
			}
		}
	}

	if item.Event == constants.EventDelete {
		// delete ns's subnet config
		if _, ok := apps[item.Name]; ok {
			change = true
			delete(apps, item.Name)
		}
	}

	if change {
		data, _ := json.Marshal(apps)
		clone := cm.DeepCopy()
		clone.Data[constants.IPAMConfigDate] = string(data)
		_, err := c.k8sclient.CoreV1().ConfigMaps(constants.IPAMConfigNamespace).Update(context.TODO(), clone, metav1.UpdateOptions{})
		return err
	}

	return nil
}

func (c *IPPoolController) processNSItem() bool {
	obj, quit := c.nsQueue.Get()
	if quit {
		return false
	}
	defer c.nsQueue.Done(obj)

	err := c.processNS(obj.(workItem))
	if err == nil {
		c.nsQueue.Forget(obj)
		return true
	}

	c.nsQueue.AddRateLimited(obj)
	utilruntime.HandleError(fmt.Errorf("error processing ns %v (will retry): %v", obj, err))
	return true
}

func (c *IPPoolController) enqueueIPAMBlocks(obj interface{}) {
	block, ok := obj.(*networkv1alpha1.IPAMBlock)
	if !ok {
		return
	}

	// notify ippool controller to update status
	c.ippoolQueue.Add(block.Labels[networkv1alpha1.IPPoolNameLabel])

	// notify ns controller to update subnets
	if block.NumFreeAddresses() == 0 {
		ns, err := c.getRelationNSFromBlock(block.Name)
		klog.Infof("subnet %s has no free addresses: (%s, %v)", block.Name, ns, err)
		if err != nil || ns == "" {
			return
		}

		c.enqueueNamespace(workItem{
			Name:  ns,
			Event: constants.EventUpdate,
		})
	}
}

func (c *IPPoolController) enqueueNamespace(obj interface{}) {
	c.nsQueue.Add(obj)
}

func NewIPPoolController(
	k8sclient k8sclientset.Interface,
	client clientset.Interface,
	k8sInformers k8sinformers.SharedInformerFactory,
	informers informers.SharedInformerFactory,
	provider ippool.Provider) *IPPoolController {

	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(func(format string, args ...interface{}) {
		klog.Info(fmt.Sprintf(format, args))
	})
	broadcaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: k8sclient.CoreV1().Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "ippool-controller"})

	c := &IPPoolController{
		eventBroadcaster: broadcaster,
		eventRecorder:    recorder,
		ippoolQueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ippool"),
		nsQueue:          workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ippool-ns"),
		k8sclient:        k8sclient,
		client:           client,
		provider:         provider,
	}
	c.ippoolInformer = informers.Network().V1alpha1().IPPools()
	c.ippoolSynced = c.ippoolInformer.Informer().HasSynced
	c.ipamblockInformer = informers.Network().V1alpha1().IPAMBlocks()
	c.ipamblockSynced = c.ipamblockInformer.Informer().HasSynced
	c.nsInformer = k8sInformers.Core().V1().Namespaces()
	c.nsSynced = c.nsInformer.Informer().HasSynced

	c.ippoolInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueueIPPools,
		UpdateFunc: func(old, new interface{}) {
			c.enqueueIPPools(new)
		},
		DeleteFunc: nil,
	})

	//just for update ippool status
	c.ipamblockInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueueIPAMBlocks,
		UpdateFunc: func(old, new interface{}) {
			c.enqueueIPAMBlocks(new)
		},
		DeleteFunc: c.enqueueIPAMBlocks,
	})

	c.nsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.enqueueNamespace(workItem{
				Name:  obj.(*corev1.Namespace).Name,
				Event: constants.EventADD,
			})
		},
		UpdateFunc: nil,
		DeleteFunc: func(obj interface{}) {
			c.enqueueNamespace(workItem{
				Name:  obj.(*corev1.Namespace).Name,
				Event: constants.EventDelete,
			})
		},
	})

	return c
}
