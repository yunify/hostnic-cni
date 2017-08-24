package server

import (
	"fmt"
	"sync"

	"github.com/orcaman/concurrent-map"
	log "github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/pkg/provider/qingcloud"
)

const (
	AllocationRetryTimes = 3
	ReadyPoolSize        = 2
)

//NicPool nic cached pool
type NicPool struct {
	nicDict cmap.ConcurrentMap

	nicpool chan string

	nicReadyPool chan string

	nicStopGeneratorChann chan struct{}

	nicProvider NicProvider

	gatewayMgr *GatewayManager

	sync.WaitGroup
}

//NewNicPool new nic pool
func NewNicPool(size int, resoureStub *qingcloud.QCNicProvider) (*NicPool, error) {
	pool := &NicPool{
		nicDict:               cmap.New(),
		nicpool:               make(chan string, size),
		nicReadyPool:          make(chan string, ReadyPoolSize),
		nicStopGeneratorChann: make(chan struct{}),
		nicProvider:           NewQingCloudNicProvider(resoureStub),
		gatewayMgr:            NewGatewayManager(resoureStub),
	}
	err := pool.init()
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func (pool *NicPool) init() error {
	nicids, err := pool.gatewayMgr.CollectGatewayNic()
	if err != nil {
		return err
	}
	pool.addNicsToPool(nicids...)

	//start eventloop
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

func (pool *NicPool) StartEventloop() {
	pool.Add(1)
	go func() {
		defer pool.Done()
	CLEANUP:
		for {
			select {
			case <-pool.nicStopGeneratorChann:
				break CLEANUP

			case nic := <-pool.nicReadyPool:
				log.Debugf("move %s from ready pool to nic pool", nic)
				pool.nicpool <- nic

			default:
				nic, err := pool.nicProvider.GenerateNic()
				if err != nil {
					log.Errorf("Failed to get nic from generator", err)
					continue
				}
				pool.nicDict.Set(nic.ID, nic)

				log.Debugln("start to wait until nic pool is not full.")
				pool.nicpool <- nic.ID
				log.Debugf("put %s into nic pool", nic.ID)
			}
		}
		log.Info("Event loop stopped")
	}()
}

func (pool *NicPool) ShutdownNicPool() {
	//send terminate signal
	go func() {
		log.Infoln("send kill pool event loop signal ")
		pool.nicStopGeneratorChann <- struct{}{}
	}()
	//recollect nics
	stopChannel := make(chan struct{})
	go func(stopch chan struct{}) {
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
		err := pool.nicProvider.ReclaimNic(cachedlist)
		log.Infof("clean up nic pool,delete nics %v: %v \n", cachedlist, err)
		stopch <- struct{}{}
		close(stopch)
	}(stopChannel)
	pool.Wait() // wait until all of requests are processed
	close(pool.nicReadyPool)
	close(pool.nicpool)
	log.Infof("closed nic pool")
	<-stopChannel
}

func (pool *NicPool) ReturnNic(nicid string) error {
	log.Debugf("Return %s to nic pool", nicid)
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

func (pool *NicPool) BorrowNic(autoAssignGateway bool) (*pkg.HostNic, error) {
	nicid := <-pool.nicpool
	times := 0
	for ; !pool.nicProvider.ValidateNic(nicid) && times < AllocationRetryTimes; times++ {
		log.Errorf("Validation Failed. retry allocation")
		nicid = <-pool.nicpool
	}
	if times == AllocationRetryTimes {
		return nil, fmt.Errorf("Failed to allocate nic. retried %d times ", times)
	}

	var nic *pkg.HostNic
	if item, ok := pool.nicDict.Get(nicid); ok {
		nic = item.(*pkg.HostNic)
	}

	if autoAssignGateway {
		gateway, err := pool.gatewayMgr.GetOrAllocateGateway(nic.VxNet.ID)
		if err != nil {
			return nil, err
		}
		return &pkg.HostNic{
			ID:           nic.ID,
			HardwareAddr: nic.HardwareAddr,
			Address:      nic.Address,
			VxNet: &pkg.VxNet{
				ID:      nic.VxNet.ID,
				GateWay: gateway,
				Network: nic.VxNet.Network,
			},
		}, nil
	}
	log.Debugf("Borrow nic from nic pool")
	return nic, nil
}
