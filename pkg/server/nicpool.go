package server

import (
	"sync"

	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/pkg/provider/qingcloud"
	"net"
)

const (
	AllocationRetryTimes = 3
)

//NicPool nic cached pool
type NicPool struct {
	sync.RWMutex
	nicDict map[string]*pkg.HostNic

	nicpool chan string

	nicGenerator NICGenerator

	nicValidator NicValidator

	nicStopGeneratorChann chan struct{}

	gatewayMgr map[string]string

	resoureStub *qingcloud.QCNicProvider

	sync.WaitGroup

	gatewayLock sync.RWMutex
}

//NewNicPool new nic pool
func NewNicPool(size int, resoureStub *qingcloud.QCNicProvider) (*NicPool, error) {
	pool := &NicPool{
		nicDict:               make(map[string]*pkg.HostNic),
		nicpool:               make(chan string, size),
		nicStopGeneratorChann: make(chan struct{}),
		gatewayMgr:            make(map[string]string),
		resoureStub:           resoureStub,
	}
	err := pool.init()
	if err != nil {
		return nil, err
	}
	return pool, nil
}

//NICGenerator nic generator function
type NICGenerator func() (*pkg.HostNic, error)

//NicValidator validate object
type NicValidator func(nicid string) bool

func (pool *NicPool) init() error {
	err := pool.collectGatewayNic()
	if err != nil {
		return err
	}

	// build generator
	pool.nicGenerator = pool.resoureStub.CreateNic

	pool.nicValidator = func(nicid string) bool {
		link, err := pkg.LinkByMacAddr(nicid)
		if err != nil {
			log.Errorf("Validation failed :%v", err)
			return false
		}
		if link.Attrs().Flags&net.FlagUp != 0 {
			log.Errorf("Validation failed :Nic is up, not available")
			return false
		}
		return true
	}
	//start generator
	pool.StartEventloop()
	return nil
}

func (pool *NicPool) collectGatewayNic() error {
	log.Infof("Collect existing nic as gateway cadidate")
	var nicidList []*string
	if linklist, err := netlink.LinkList(); err != nil {
		return err
	} else {
		for _, link := range linklist {
			if link.Attrs().Flags&net.FlagLoopback == 0 {
				nicid := link.Attrs().HardwareAddr.String()
				nicidList = append(nicidList, pkg.StringPtr(nicid))
				log.Debugf("Found nic %s on host", nicid)
			}
		}
	}
	niclist, err := pool.resoureStub.GetNics(nicidList)
	if err != nil {
		return err
	}
	var unusedList []*pkg.HostNic
	for _, nic := range niclist {
		niclink, err := pkg.LinkByMacAddr(nic.ID)
		if err != nil {
			return err
		}
		if niclink.Attrs().Flags&net.FlagUp != 0 {
			if _, ok := pool.gatewayMgr[nic.VxNet.ID]; !ok {
				pool.gatewayMgr[nic.VxNet.ID] = nic.Address
				continue
			} else {
				netlink.LinkSetDown(niclink)
			}
		}
		log.Debugf("nic %s is unused,status is %d", nic.ID, niclink.Attrs().Flags&net.FlagUp)
		unusedList = append(unusedList, nic)
	}

	//move unused nics to nic pool
	if len(unusedList) > 0 {
		pool.Add(1)
		go func() {
			defer pool.Done()
			log.Debug("Found unused nics and put them to nic pool %v", unusedList)
			pool.addNicsToPool(unusedList...)
		}()
	}
	log.Infof("Found following nic as gateway")
	for key, value := range pool.gatewayMgr {
		log.Infof("vxnet: %s gateway: %s", key, value)
	}
	return nil
}

func (pool *NicPool) getOrAllocateGateway(vxnetid string) (string, error) {
	var gatewayIp string
	pool.gatewayLock.RLock()
	var ok bool
	if gatewayIp, ok = pool.gatewayMgr[vxnetid]; !ok {
		pool.gatewayLock.RUnlock()

		//allocate nic
		pool.gatewayLock.Lock()
		defer pool.gatewayLock.Unlock()

		nic, err := pool.resoureStub.CreateNicInVxnet(vxnetid)
		if err != nil {
			return "", err
		}
		niclink, err := pkg.LinkByMacAddr(nic.HardwareAddr)
		if err != nil {
			return "", err
		}
		err = netlink.LinkSetUp(niclink)
		if err != nil {
			return "", err
		}
		pool.gatewayMgr[nic.VxNet.ID] = nic.Address

		return nic.Address, nil
	} else {
		pool.gatewayLock.RUnlock()
		return gatewayIp, nil
	}
}

//addNicsToPool may block current process until channel is not empty
func (pool *NicPool) addNicsToPool(nics ...*pkg.HostNic) {
	for _, nic := range nics {
		pool.Lock()
		pool.nicDict[nic.ID] = nic
		pool.Unlock()

		log.Debugln("start to wait until channel is not full.")
		pool.nicpool <- nic.ID
		log.Debugf("put %s into channel", nic.ID)
	}
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

			default:
				nic, err := pool.nicGenerator()
				if err != nil {
					log.Errorf("Failed to get nic from generator", err)
					continue
				}
				pool.addNicsToPool(nic)
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
		log.Infoln("start to delete nics")
		var cachedlist []*string
		for nic := range pool.nicpool {
			cachedlist = append(cachedlist, pkg.StringPtr(nic))
			log.Debugf("Got nic %s in nic pool", nic)
		}
		err := pool.resoureStub.DeleteNics(cachedlist)
		log.Infof("clean up nic pool,delete nics %v: %v \n", cachedlist, err)
		stopch <- struct{}{}
		close(stopch)
	}(stopChannel)
	pool.Wait() // wait until all of requests are processed
	close(pool.nicpool)
	log.Infof("closed nic pool")
	<-stopChannel
}

func (pool *NicPool) ReturnNic(nicid string) error {
	log.Debugf("Return %s to nic pool", nicid)
	pool.Add(1)
	go func() {
		defer pool.Done()
		//check if nic is in dict
		pool.RLock()
		if _, ok := pool.nicDict[nicid]; ok {
			pool.RUnlock()
			pool.nicpool <- nicid
		} else {
			pool.RUnlock()

			nics, err := pool.resoureStub.GetNics([]*string{&nicid})
			if err != nil {
				log.Errorf("Failed to get nics %s, %v", nicid, err)
				return
			}
			pool.addNicsToPool(nics[0])
		}

	}()

	return nil
}

func (pool *NicPool) BorrowNic(autoAssignGateway bool) (*pkg.HostNic, error) {
	nicid := <-pool.nicpool
	times := 0
	for ; !pool.nicValidator(nicid) && times < AllocationRetryTimes; times++ {
		log.Errorf("Validation Failed. retry allocation")
		nicid = <-pool.nicpool
	}
	if times == AllocationRetryTimes {
		return nil, fmt.Errorf("Failed to allocate nic. retried %d times ", times)
	}

	pool.RLock()
	nic := pool.nicDict[nicid]
	pool.RUnlock()

	if autoAssignGateway {
		gateway, err := pool.getOrAllocateGateway(nic.VxNet.ID)
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
