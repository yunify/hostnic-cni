package server

import (
	"net"

	cmap "github.com/orcaman/concurrent-map"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/types"
	"k8s.io/klog"
)

type GatewayManager struct {
	gatewayMgr cmap.ConcurrentMap
	qcClient   qcclient.QingCloudAPI
}

func NewGatewayManager(qcClient qcclient.QingCloudAPI) *GatewayManager {
	return &GatewayManager{gatewayMgr: cmap.New(), qcClient: qcClient}
}

//CollectGatewayNic collect nic on host
func (pool *GatewayManager) CollectGatewayNic() ([]*types.HostNic, error) {
	klog.V(1).Infoln("Collect existing nic as gateway cadidate")
	var nicidList []string
	if linklist, err := netlink.LinkList(); err != nil {
		return nil, err
	} else {
		for _, link := range linklist {
			if link.Attrs().Flags&net.FlagLoopback == 0 {
				nicid := link.Attrs().HardwareAddr.String()
				nicidList = append(nicidList, nicid)
				klog.V(2).Infof("Found nic %s on host", nicid)
			}
		}
	}
	niclist, err := pool.qcClient.GetNics(nicidList)
	if err != nil {
		return nil, err
	}
	var unusedList []*types.HostNic
	for _, nic := range niclist {
		niclink, err := types.LinkByMacAddr(nic.ID)
		if err != nil {
			return nil, err
		}
		if niclink.Attrs().Flags&net.FlagUp != 0 {
			if ok := pool.gatewayMgr.SetIfAbsent(nic.VxNetID, nic.Address); ok {
				continue
			} else {
				netlink.LinkSetDown(niclink)
			}
		}
		klog.V(2).Infof("nic %s is unused,status is up: %t", nic.ID, niclink.Attrs().Flags&netlink.OperUp != 0)
		unusedList = append(unusedList, nic)
	}
	klog.V(1).Infof("Found following nic as gateway")
	for key, value := range pool.gatewayMgr.Items() {
		klog.V(1).Infof("vxnet: %s gateway: %s", key, value.(string))
	}
	return unusedList, nil
}

//GetOrAllocateGateway find one nic for gateway usage
func (pool *GatewayManager) GetOrAllocateGateway(vxnetid string) (string, error) {
	var gatewayIP string
	if item, ok := pool.gatewayMgr.Get(vxnetid); !ok {
		//allocate nic

		upsertcb := func(exist bool, valueInMap interface{}, newValue interface{}) interface{} {
			if exist {
				return valueInMap
			}
			nic, err := pool.qcClient.CreateNic(vxnetid)
			if err != nil {
				return nil
			}
			niclink, err := types.LinkByMacAddr(nic.HardwareAddr)
			if err != nil {
				pool.qcClient.DeleteNic(nic.HardwareAddr)
				return nil
			}

			if err = netlink.LinkSetDown(niclink); err != nil {
				klog.Errorf("LinkSetDown err %v, delete Nic %s", err, nic.HardwareAddr)
				pool.qcClient.DeleteNic(nic.HardwareAddr)
				return nil
			}
			v, err := pool.qcClient.GetVxNet(vxnetid)
			if err != nil {
				klog.Errorf("Failed to get vxvet of %s, err: ", vxnetid, err.Error())
				return nil
			}
			_, netcidr, err := net.ParseCIDR(v.Network)
			if err != nil {
				klog.Errorf("Failed to parse gateway network.%s Please check your config", err)
				pool.qcClient.DeleteNic(nic.HardwareAddr)
				return nil
			}
			addr := &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(nic.Address), Mask: netcidr.Mask}, Label: ""}
			if err = netlink.AddrAdd(niclink, addr); err != nil {
				klog.Errorf("AddrAdd err %s, delete Nic %s", err.Error(), nic.HardwareAddr)
				pool.qcClient.DeleteNic(nic.HardwareAddr)
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
