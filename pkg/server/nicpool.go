package server

import (
	"sync"

	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/pkg/provider/qingcloud"
	"net"
	"github.com/orcaman/concurrent-map"
)

const (
	AllocationRetryTimes = 3
	ReadyPoolSize =2
)

//NicPool nic cached pool
type NicPool struct {
	nicDict cmap.ConcurrentMap

	nicpool chan string

	nicReadyPool chan string

	nicGenerator NICGenerator

	nicValidator NicValidator

	nicStopGeneratorChann chan struct{}

	gatewayMgr cmap.ConcurrentMap

	resoureStub *qingcloud.QCNicProvider

	sync.WaitGroup
}

//NewNicPool new nic pool
func NewNicPool(size int, resoureStub *qingcloud.QCNicProvider) (*NicPool, error) {
	pool := &NicPool{
		nicDict:               cmap.New(),
		nicpool:               make(chan string, size),
		nicReadyPool:          make(chan string,ReadyPoolSize),
		nicStopGeneratorChann: make(chan struct{}),
		gatewayMgr:            cmap.New(),
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
			if _, ok := pool.gatewayMgr.Get(nic.VxNet.ID); !ok {
				pool.gatewayMgr.Set(nic.VxNet.ID, nic.Address)
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
	if item, ok := pool.gatewayMgr.Get(vxnetid); !ok {

		//allocate nic

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
		pool.gatewayMgr.Set(nic.VxNet.ID,nic.Address)

		return nic.Address, nil
	} else {
		gatewayIp = item.(string)
		return gatewayIp, nil
	}
}

//addNicsToPool may block current process until channel is not empty
func (pool *NicPool) addNicsToPool(nics ...*pkg.HostNic) {
	for _, nic := range nics {
		pool.nicDict.Set(nic.ID,nic)

		log.Debugln("start to wait until ready pool is not full.")
		pool.nicReadyPool <- nic.ID
		log.Debugf("put %s into ready pool", nic.ID)
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

			case nic :=<-pool.nicReadyPool:
				pool.nicpool <-nic

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
		var cachedlist []*string
		log.Infoln("start to delete nics in ready pool")
		for nic := range pool.nicReadyPool {
			cachedlist = append(cachedlist, pkg.StringPtr(nic))
			log.Debugf("Got nic %s in nic pool", nic)
		}
		log.Infoln("start to delete nics in cache pool")
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
	close(pool.nicReadyPool)
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
		if _, ok := pool.nicDict.Get(nicid); ok {
			pool.nicReadyPool <- nicid
		} else {

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

	var nic *pkg.HostNic
	if item,ok := pool.nicDict.Get(nicid);ok {
		nic = item.(*pkg.HostNic)
	}

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
