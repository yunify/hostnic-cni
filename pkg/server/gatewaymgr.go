package server

import (
	"net"

	"github.com/orcaman/concurrent-map"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/pkg/provider/qingcloud"
)

type GatewayManager struct {
	gatewayMgr   cmap.ConcurrentMap
	resourceStub *qingcloud.QCNicProvider
}

func NewGatewayManager(qcstub *qingcloud.QCNicProvider) *GatewayManager {
	return &GatewayManager{gatewayMgr: cmap.New(), resourceStub: qcstub}
}

//CollectGatewayNic collect nic on host
func (pool *GatewayManager) CollectGatewayNic() ([]*pkg.HostNic, error) {
	log.Infof("Collect existing nic as gateway cadidate")
	var nicidList []*string
	if linklist, err := netlink.LinkList(); err != nil {
		return nil, err
	} else {
		for _, link := range linklist {
			if link.Attrs().Flags&net.FlagLoopback == 0 {
				nicid := link.Attrs().HardwareAddr.String()
				nicidList = append(nicidList, pkg.StringPtr(nicid))
				log.Debugf("Found nic %s on host", nicid)
			}
		}
	}
	niclist, err := pool.resourceStub.GetNics(nicidList)
	if err != nil {
		return nil, err
	}
	var unusedList []*pkg.HostNic
	for _, nic := range niclist {
		niclink, err := pkg.LinkByMacAddr(nic.ID)
		if err != nil {
			return nil, err
		}
		if niclink.Attrs().Flags&net.FlagUp != 0 {
			if ok := pool.gatewayMgr.SetIfAbsent(nic.VxNet.ID, nic.Address); ok {
				continue
			} else {
				netlink.LinkSetDown(niclink)
			}
		}
		log.Debugf("nic %s is unused,status is up: %t", nic.ID, niclink.Attrs().Flags&netlink.OperUp != 0)
		unusedList = append(unusedList, nic)
	}
	log.Infof("Found following nic as gateway")
	for key, value := range pool.gatewayMgr.Items() {
		log.Infof("vxnet: %s gateway: %s", key, value.(string))
	}
	return unusedList, nil
}

//GetOrAllocateGateway find one nic for gateway usage
func (pool *GatewayManager) GetOrAllocateGateway(vxnetid string) (string, error) {
	var gatewayIP string
	if item, ok := pool.gatewayMgr.Get(vxnetid); !ok {
		//allocate nic

		upsertcb := func (exist bool, valueInMap interface{}, newValue interface{}) interface{} {
			if exist {
				return valueInMap
			}
			nic, err := pool.resourceStub.CreateNicInVxnet(vxnetid)
			if err != nil {
				return nil
			}
			niclink, err := pkg.LinkByMacAddr(nic.HardwareAddr)
			if err != nil {
				return nil

			}

			if err = netlink.LinkSetDown(niclink); err != nil {
				log.Errorf("LinkSetDown err %v, delete Nic %s", err, nic.HardwareAddr)
				pool.resourceStub.DeleteNic(nic.HardwareAddr)
				return nil
			}

			_, netcidr, err := net.ParseCIDR(nic.VxNet.Network)
			if err != nil {
				log.Errorf("Failed to parse gateway network.%s Please check your config", err)
				pool.resourceStub.DeleteNic(nic.HardwareAddr)
				return nil
			}
			addr := &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(nic.Address), Mask: netcidr.Mask}, Label: ""}
			if err = netlink.AddrAdd(niclink, addr); err != nil {
				log.Errorf("AddrAdd err %s, delete Nic %s", err.Error(), nic.HardwareAddr)
				pool.resourceStub.DeleteNic(nic.HardwareAddr)
				return nil
			}
			err = netlink.LinkSetUp(niclink)
			if err != nil {
				return nil
			}
			return nic.Address
		}
		item = pool.gatewayMgr.Upsert(vxnetid, nil, upsertcb)
		gatewayIP = item.(string)
	} else {
		gatewayIP = item.(string)
	}
	return gatewayIP, nil
}
