package server

import (
	"fmt"
	"sync"

	"time"

	cmap "github.com/orcaman/concurrent-map"
	"github.com/yunify/hostnic-cni/pkg/types"
	"k8s.io/klog"
)

const (
	//AllocationRetryTimes maximum allocation retry times
	AllocationRetryTimes = 3
	//ReadyPoolSize size of recycled nic pool
	ReadyPoolSize = 64
	//DeleteWaitTimeout delete nic time out
	DeleteWaitTimeout = 5 * time.Second
)

//NicPool nic cached pool
type NicPool struct {
	nicDict cmap.ConcurrentMap

	nicpool chan string

	nicReadyPool chan string

	nicStopFlag bool
	nicStopLock sync.RWMutex

	nicProvider NicProvider

	gatewayMgr *GatewayManager

	config NicPoolConfig

	sync.WaitGroup
}

//NicPoolConfig nicpool configuration
type NicPoolConfig struct {
	CleanUpCache bool
}

//NewNicPoolConfig nicconfig factory func
func NewNicPoolConfig() NicPoolConfig {
	return NicPoolConfig{CleanUpCache: false}
}

//NewNicPool new nic pool
func NewNicPool(size int, nicProvider NicProvider, gatewayMgr *GatewayManager, option ...NicPoolConfig) (*NicPool, error) {
	config := NewNicPoolConfig()
	if len(option) > 1 {
		return nil, fmt.Errorf("More than one option objects are found")
	}
	for _, item := range option {
		config.CleanUpCache = item.CleanUpCache
	}
	if nicProvider == nil {
		return nil, fmt.Errorf("NicProvider is nil, Please provide nic provider")
	}
	pool := &NicPool{
		nicDict:      cmap.New(),
		nicpool:      make(chan string, size),
		nicReadyPool: make(chan string, ReadyPoolSize),
		nicStopFlag:  false,
		nicProvider:  nicProvider,
		gatewayMgr:   gatewayMgr,
		config:       config,
	}
	err := pool.init()
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func (pool *NicPool) init() error {
	if pool.gatewayMgr != nil {
		klog.V(1).Infoln("Initialize gateway manager")
		nicids, err := pool.gatewayMgr.CollectGatewayNic()
		if err != nil {
			return err
		}
		pool.addNicsToPool(nicids...)
	}
	//start eventloop
	klog.V(1).Infoln("Start nic pool event loop")
	pool.StartEventloop()
	return nil
}

//addNicsToPool may block current process until channel is not empty
func (pool *NicPool) addNicsToPool(nics ...*types.HostNic) {
	pool.Add(1)
	go func() {
		defer pool.Done()
		for _, nic := range nics {
			pool.nicDict.Set(nic.ID, nic)
			klog.V(2).Infoln("start to wait until ready pool is not full.")
			pool.nicReadyPool <- nic.ID
			klog.V(2).Infof("put %s into ready pool", nic.ID)
		}
	}()
}

//CleanUpReadyPool clear recycled nic cache
func (pool *NicPool) CleanUpReadyPool() {
	klog.V(1).Infoln("Start to clean up ready pool")
	timer := time.NewTimer(DeleteWaitTimeout)
	var niclist []string
Cleanerloop:
	for {
		select {
		case <-timer.C:
			break Cleanerloop
		case nicid, ok := <-pool.nicReadyPool:
			if ok {
				niclist = append(niclist, nicid)
				timer.Reset(DeleteWaitTimeout)
			} else {
				break Cleanerloop
			}
		}
	}
	timer.Stop()
	klog.V(1).Infof("Cleaned up ready pool ")
	if len(niclist) > 0 {
		klog.V(1).Infof("Deleting reclaimed nics ...")
		nicids := ""
		err := pool.nicProvider.ReclaimNic(niclist)
		for _, item := range niclist {
			nicids = nicids + "[" + item + "]"
			pool.nicDict.Remove(item)
		}
		klog.V(1).Infof("Deleted nic %s , error : %v", nicids, err)
	}
}

//StartEventloop start nicpool event loop
func (pool *NicPool) StartEventloop() {
	pool.Add(1)
	go func() {
		defer pool.Done()
	CLEANUP:
		for {
			select {

			case nic := <-pool.nicReadyPool:
				klog.V(2).Infof("move %s from ready pool to nic pool", nic)
				pool.nicpool <- nic

			default:
				pool.nicStopLock.RLock()
				stopFlag := pool.nicStopFlag
				pool.nicStopLock.RUnlock()
				if !stopFlag {
					var err error
					var nic *types.HostNic
					var timer int
					for timer = 0; timer < 5; timer++ {
						nic, err = pool.nicProvider.GenerateNic()
						if err == nil {
							break
						}
						klog.Errorf("Failed to get nic from generator:%s ", err)
					}
					if timer == 5 {
						klog.Errorf("Failed to generate nic %v", err)
						go pool.ShutdownNicPool()
						break CLEANUP
					}
					pool.nicDict.Set(nic.ID, nic)

					klog.V(2).Infoln("start to wait until nic pool is not full.")
					pool.nicpool <- nic.ID
					klog.V(2).Infof("put %s into nic pool", nic.ID)
				} else {
					klog.V(2).Infoln("Stopped generator")
					break CLEANUP
				}
			}
		}
		klog.V(1).Infoln("Event loop stopped")
	}()
}

//ShutdownNicPool tear down nic pool and free up resources
func (pool *NicPool) ShutdownNicPool() {

	//recollect nics
	stopChannel := make(chan struct{})
	go func(stopch chan struct{}) {
		pool.nicStopLock.Lock()
		pool.nicStopFlag = true
		pool.nicStopLock.Unlock()
		if pool.config.CleanUpCache {
			var cachedlist []string
			klog.V(1).Infoln("start to delete nics in cache pool")
			for nic := range pool.nicpool {
				cachedlist = append(cachedlist, nic)
				klog.V(2).Infof("Got nic %s in nic pool", nic)
			}
			klog.V(1).Infoln("start to delete nics in ready pool")
			for nic := range pool.nicReadyPool {
				cachedlist = append(cachedlist, nic)
				klog.V(2).Infof("Got nic %s in nic pool", nic)
			}
			klog.V(1).Infof("Deleting cached nics...")
			err := pool.nicProvider.ReclaimNic(cachedlist)
			var niclist string
			for _, nicitem := range cachedlist {
				niclist = niclist + "[" + nicitem + "] "
			}
			klog.V(1).Infof("Deleted nics %s,error:%v", niclist, err)
		}
		stopch <- struct{}{}
		close(stopch)
	}(stopChannel)
	pool.Wait() // wait until all of requests are processed
	close(pool.nicReadyPool)
	close(pool.nicpool)
	klog.V(1).Infof("closed nic pool")
	<-stopChannel
}

//ReturnNic recycle deleted nic
func (pool *NicPool) ReturnNic(nicid string) error {
	klog.V(2).Infof("Return %s to nic ready pool", nicid)
	err := pool.nicProvider.DisableNic(nicid)
	if err != nil {
		klog.Errorf("Failed to disable nic %s, %v", nicid, err)
		return err
	}
	//check if nic is in dict
	if _, ok := pool.nicDict.Get(nicid); ok {
		pool.nicReadyPool <- nicid
	} else {
		nics, err := pool.nicProvider.GetNicsInfo([]string{nicid})
		if err != nil {
			klog.Errorf("Failed to get nic %s, %v", nicid, err)
			return err
		}
		pool.addNicsToPool(nics[0])
	}
	return nil
}

//BorrowNic allocate nic for client
func (pool *NicPool) BorrowNic(autoAssignGateway bool) (*types.HostNic, *string, error) {
	nicid := <-pool.nicpool
	times := 0
	for ; !pool.nicProvider.ValidateNic(nicid) && times < AllocationRetryTimes; times++ {
		klog.Errorf("Validation Failed. retry allocation")
		nicid = <-pool.nicpool
	}
	if times == AllocationRetryTimes {
		return nil, nil, fmt.Errorf("Failed to allocate nic. retried %d times ", times)
	}

	var nic *types.HostNic
	if item, ok := pool.nicDict.Get(nicid); ok {
		nic = item.(*types.HostNic)
	}

	if pool.gatewayMgr != nil && autoAssignGateway {
		gateway, err := pool.gatewayMgr.GetOrAllocateGateway(nic.VxNet.ID)
		if err != nil {
			return nil, nil, err
		}
		return nic, &gateway, nil
	}
	klog.V(2).Infof("Borrow nic from nic pool")
	return nic, nil, nil
}
