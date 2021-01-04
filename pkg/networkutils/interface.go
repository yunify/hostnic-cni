package networkutils

import (
	"net"

	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

type NetworkUtilsWrap interface {
	CleanupNicNetwork(nic *rpc.HostNic) error
	SetupNicNetwork(nic *rpc.HostNic) error
	LinkByMacAddr(macAddr string) (netlink.Link, error)
	IsNSorErr(nspath string) error
}

var (
	NetworkHelper NetworkUtilsWrap
)

type NetworkUtilsFake struct {
	Links  map[string]netlink.Link
	Rules  map[string]netlink.Rule
	Routes map[int][]netlink.Route
}

func newNetworkUtilsFake() NetworkUtilsFake {
	return NetworkUtilsFake{
		Links:  make(map[string]netlink.Link),
		Rules:  make(map[string]netlink.Rule),
		Routes: make(map[int][]netlink.Route),
	}
}

var _ NetworkUtilsWrap = NetworkUtilsFake{}

func (n NetworkUtilsFake) IsNSorErr(nspath string) error {
	return nil
}

func (n NetworkUtilsFake) LinkByMacAddr(macAddr string) (netlink.Link, error) {
	return n.Links[macAddr], nil
}

func (n NetworkUtilsFake) CleanupNicNetwork(nic *rpc.HostNic) error {
	delete(n.Rules, nic.PrimaryAddress)
	delete(n.Routes, int(nic.RouteTableNum))
	return nil
}

func (n NetworkUtilsFake) SetupNicNetwork(nic *rpc.HostNic) error {
	link := n.Links[nic.ID]

	_, dst, _ := net.ParseCIDR(nic.VxNet.Network)
	routes := []netlink.Route{
		{
			LinkIndex: link.Attrs().Index,
			Dst:       dst,
			Scope:     netlink.SCOPE_LINK,
			Table:     int(nic.RouteTableNum),
		},
		{
			LinkIndex: link.Attrs().Index,
			Dst: &net.IPNet{
				IP:   net.IPv4zero,
				Mask: net.CIDRMask(0, 32),
			},
			Scope: netlink.SCOPE_UNIVERSE,
			Gw:    net.ParseIP(nic.VxNet.Gateway),
			Table: int(nic.RouteTableNum),
		},
	}
	n.Routes[int(nic.RouteTableNum)] = routes

	rule := netlink.NewRule()
	rule.Table = int(nic.RouteTableNum)
	rule.Src = &net.IPNet{
		IP:   net.ParseIP(nic.PrimaryAddress),
		Mask: net.CIDRMask(32, 32),
	}
	rule.Priority = fromContainerRulePriority

	n.Rules[nic.PrimaryAddress] = *rule

	return nil
}

func SetupNetworkFakeHelper() {
	NetworkHelper = newNetworkUtilsFake()
}
