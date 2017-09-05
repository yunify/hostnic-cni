package server

import (
	"fmt"
	"sync"

	"time"

	"github.com/orcaman/concurrent-map"
	log "github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg"
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
		log.Infoln("Initialize gateway manager")
		nicids, err := pool.gatewayMgr.CollectGatewayNic()
		if err != nil {
			return err
		}
		pool.addNicsToPool(nicids...)
	}
	//start eventloop
	log.Infoln("Start nic pool event loop")
	pool.StartEventloop()
	return nil
}

//addNicsToPool may block current process until channel is not empty
func (pool *NicPool) addNicsToPool(nics ...*pkg.HostNic) {
	pool.Add(1)
	go func() {
		defer pool.Done()
		for _, nic := range nics {
			pool.nicDict.Set(nic.ID, nic)
			log.Debugln("start to wait until ready pool is not full.")
			pool.nicReadyPool <- nic.ID
			log.Debugf("put %s into ready pool", nic.ID)
		}
	}()
}

//CleanUpReadyPool clear recycled nic cache
func (pool *NicPool) CleanUpReadyPool() {
	log.Infoln("Start to clean up ready pool")
	timer := time.NewTimer(DeleteWaitTimeout)
	var niclist []*string
Cleanerloop:
	for {
		select {
		case <-timer.C:
			break Cleanerloop
		case nicid, ok := <-pool.nicReadyPool:
			if ok {
				niclist = append(niclist, pkg.StringPtr(nicid))
				timer.Reset(DeleteWaitTimeout)
			} else {
				break Cleanerloop
			}
		}
	}
	timer.Stop()
	log.Infof("Cleaned up ready pool ")
	if len(niclist) > 0 {
		log.Infof("Deleting reclaimed nics ...")
		nicids := ""
		err := pool.nicProvider.ReclaimNic(niclist)
		for _, item := range niclist {
			nicids = nicids + "[" + *item + "]"
			pool.nicDict.Remove(*item)
		}
		log.Infof("Deleted nic %s , error : %v", nicids, err)
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
				log.Debugf("move %s from ready pool to nic pool", nic)
				pool.nicpool <- nic

			default:
				pool.nicStopLock.RLock()
				stopFlag := pool.nicStopFlag
				pool.nicStopLock.RUnlock()
				if !stopFlag {
					nic, err := pool.nicProvider.GenerateNic()
					if err != nil {
						log.Errorf("Failed to get nic from generator:%s ", err)
						continue
					}
					pool.nicDict.Set(nic.ID, nic)

					log.Debugln("start to wait until nic pool is not full.")
					pool.nicpool <- nic.ID
					log.Debugf("put %s into nic pool", nic.ID)
				} else {
					log.Debugln("Stopped generator")
					break CLEANUP
				}
			}
		}
		log.Info("Event loop stopped")
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
		var cachedlist []*string

		log.Infoln("start to delete nics in cache pool")
		for nic := range pool.nicpool {
			cachedlist = append(cachedlist, pkg.StringPtr(nic))
			log.Debugf("Got nic %s in nic pool", nic)
		}
		log.Infoln("start to delete nics in ready pool")
		for nic := range pool.nicReadyPool {
			cachedlist = append(cachedlist, pkg.StringPtr(nic))
			log.Debugf("Got nic %s in nic pool", nic)
		}
		log.Infof("clean up nic pool")
		if pool.config.CleanUpCache {
			log.Infof("Deleting cached nics...")
			err := pool.nicProvider.ReclaimNic(cachedlist)
			var niclist string
			for _, nicitem := range cachedlist {
				niclist = niclist + "[" + *nicitem + "] "
			}
			log.Infof("Deleted nics %s,error:%v", niclist, err)
		}
		stopch <- struct{}{}
		close(stopch)
	}(stopChannel)
	pool.Wait() // wait until all of requests are processed
	close(pool.nicReadyPool)
	close(pool.nicpool)
	log.Infof("closed nic pool")
	<-stopChannel
}

//ReturnNic recycle deleted nic
func (pool *NicPool) ReturnNic(nicid string) error {
	log.Debugf("Return %s to nic ready pool", nicid)
	//check if nic is in dict
	if _, ok := pool.nicDict.Get(nicid); ok {
		pool.nicReadyPool <- nicid
	} else {
		nics, err := pool.nicProvider.GetNicsInfo([]*string{&nicid})
		if err != nil {
			log.Errorf("Failed to get nics %s, %v", nicid, err)
			return err
		}
		pool.addNicsToPool(nics[0])
	}
	return nil
}

//BorrowNic allocate nic for client
func (pool *NicPool) BorrowNic(autoAssignGateway bool) (*pkg.HostNic, *string, error) {
	nicid := <-pool.nicpool
	times := 0
	for ; !pool.nicProvider.ValidateNic(nicid) && times < AllocationRetryTimes; times++ {
		log.Errorf("Validation Failed. retry allocation")
		nicid = <-pool.nicpool
	}
	if times == AllocationRetryTimes {
		return nil, nil, fmt.Errorf("Failed to allocate nic. retried %d times ", times)
	}

	var nic *pkg.HostNic
	if item, ok := pool.nicDict.Get(nicid); ok {
		nic = item.(*pkg.HostNic)
	}

	if pool.gatewayMgr != nil && autoAssignGateway {
		gateway, err := pool.gatewayMgr.GetOrAllocateGateway(nic.VxNet.ID)
		if err != nil {
			return nil, nil, err
		}
		return nic, &gateway, nil
	}
	log.Debugf("Borrow nic from nic pool")
	return nic, nil, nil
}
