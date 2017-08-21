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
			return false
		}
		if link.Attrs().Flags|net.FlagUp != 0 {
			return false
		}
		return true
	}
	//start generator
	pool.StartEventloop()
	return nil
}

func (pool *NicPool) collectGatewayNic() error {

	var nicidList []*string
	if linklist, err := netlink.LinkList(); err != nil {
		for _, link := range linklist {
			nicid := link.Attrs().HardwareAddr.String()
			nicidList = append(nicidList, &nicid)
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
		if niclink.Attrs().Flags|net.FlagUp != 0 {
			if _, ok := pool.gatewayMgr[nic.VxNet.ID]; !ok {
				pool.gatewayMgr[nic.VxNet.ID] = nic.Address
				continue
			} else {
				netlink.LinkSetDown(niclink)
			}
		}
		unusedList = append(unusedList, nic)
	}

	//move unused nics to nic pool
	pool.Add(1)
	go func() {
		defer pool.Done()
		pool.addNicsToPool(unusedList...)
	}()

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

		fmt.Println("start to wait until channel is not full.")
		pool.nicpool <- nic.ID
		fmt.Printf("put %s into channel", nic.ID)
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
	}()
}

func (pool *NicPool) ShutdownNicPool() {
	stopChannel := make(chan struct{})
	go func(stopch chan struct{}) {
		var cachedlist []*string
		for nic := range pool.nicpool {
			cachedlist = append(cachedlist, &nic)
		}
		pool.resoureStub.DeleteNics(cachedlist)
		stopch <- struct{}{}
		close(stopch)
	}(stopChannel)
	pool.Wait()
	<-stopChannel
}

func (pool *NicPool) ReturnNic(nicid string) error {
	pool.Add(1)
	go func() {
		defer pool.Done()
		//check if nic is in dict
		pool.RLock()
		if _, ok := pool.nicDict[nicid]; ok {
			defer pool.RUnlock()
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
	for ; !pool.nicValidator(nicid) || times < AllocationRetryTimes; times++ {
		nicid = <-pool.nicpool
		log.Errorf("Validation Failed. retry allocation")
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
	return nic, nil
}
