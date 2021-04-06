package networkutils

import (
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"golang.org/x/sys/unix"
)

type NetworkUtils struct {
}

func (n NetworkUtils) IsNSorErr(nspath string) error {
	return ns.IsNSorErr(nspath)
}

//After the Response is uninstalled, the relevant routes are cleared, so you only need to delete the rule.
func (n NetworkUtils) CleanupNicNetwork(nic *rpc.HostNic) error {
	link, err := n.LinkByMacAddr(nic.HardwareAddr)
	if err != nil && err != constants.ErrNicNotFound {
		return err
	}
	if link != nil {
		err = netlink.LinkSetDown(link)
		if err != nil {
			return fmt.Errorf("failed to set link %s down", nic.ID)
		}
	}

	var rules []netlink.Rule

	ip := net.ParseIP(nic.PrimaryAddress)
	dstRules, err := getRuleListByDst(ip)
	if err != nil {
		return err
	}
	rules = append(rules, dstRules...)
	srcRules, err := getRuleListBySrc(ip)
	if err != nil {
		return err
	}
	rules = append(rules, srcRules...)

	for _, rule := range rules {
		err := netlink.RuleDel(&rule)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to del rule %v : %v", rule, err)
		}
	}

	return nil
}

//Note: setup NetworkManager to disable dhcp on nic
// SetupNicNetwork adds default route to route table (nic-<nic_table>)
func (n NetworkUtils) SetupNicNetwork(nic *rpc.HostNic) error {
	name := constants.GetHostNicName(int(nic.RouteTableNum))

	link, err := n.LinkByMacAddr(nic.HardwareAddr)
	if err != nil {
		return fmt.Errorf("failed to get link %s: %v", name, err)
	}

	err = n.CleanupNicNetwork(nic)
	if err != nil {
		return err
	}

	if link.Attrs().Name != name {
		err = netlink.LinkSetName(link, name)
		if err != nil {
			return fmt.Errorf("failed to set link %s name to %s: %v", nic.ID, name, err)
		}
	}
	if link.Attrs().Alias != name {
		err = netlink.LinkSetAlias(link, name)
		if err != nil {
			return fmt.Errorf("failed to set link %s alias to %s: %v", nic.HardwareAddr, name, err)
		}
	}

	_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv4/conf/%s/arp_ignore", name), "1")

	_, _ = sysctl.Sysctl("net/ipv4/conf/all/rp_filter", "0")
	_, _ = sysctl.Sysctl("net/ipv4/conf/default/rp_filter", "0")
	_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv4/conf/%s/rp_filter", name), "0")

	_, _ = sysctl.Sysctl("net/ipv4/conf/all/accept_local", "1")
	_, _ = sysctl.Sysctl("net/ipv4/conf/default/accept_local", "1")
	_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv4/conf/%s/accept_local", name), "1")

	err = netlink.LinkSetUp(link)
	if err != nil {
		return fmt.Errorf("failed to set link %s up: %v", link.Attrs().Name, err)
	}

	addrs, err := netlink.AddrList(link, unix.AF_INET)
	if err != nil {
		return fmt.Errorf("failed to list addr on link %s : %v", link.Attrs().Name, err)
	}
	for _, addr := range addrs {
		err = netlink.AddrDel(link, &addr)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete add %s on %s : %v ", addr.IP, link.Attrs().Name, err)
		}
	}

	_, dst, _ := net.ParseCIDR(nic.VxNet.Network)
	routes := []netlink.Route{
		// Add a direct link route for the host's NIC PodIP only
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

	for _, r := range routes {
		err = netlink.RouteAdd(&r)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to add route %v : %v", r, err)
		}
	}

	return nil
}

// should check result, maybe empty
func (n NetworkUtils) LinkByMacAddr(macAddr string) (netlink.Link, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}

	for _, link := range links {
		attr := link.Attrs()
		if attr.HardwareAddr.String() == macAddr {
			return link, nil
		}
	}
	return nil, constants.ErrNicNotFound
}

func getRuleListByDst(dst net.IP) ([]netlink.Rule, error) {
	var dstRuleList []netlink.Rule
	ruleList, err := netlink.RuleList(unix.AF_INET)
	if err != nil {
		return nil, err
	}
	for _, rule := range ruleList {
		if rule.Dst != nil && rule.Dst.IP.Equal(dst) {
			dstRuleList = append(dstRuleList, rule)
		}
	}
	return dstRuleList, nil
}

func getRuleListBySrc(src net.IP) ([]netlink.Rule, error) {
	var srcRuleList []netlink.Rule
	ruleList, err := netlink.RuleList(unix.AF_INET)
	if err != nil {
		return nil, err
	}
	for _, rule := range ruleList {
		if rule.Src != nil && rule.Src.IP.Equal(src) {
			srcRuleList = append(srcRuleList, rule)
		}
	}
	return srcRuleList, nil
}

func SetupNetworkHelper() {
	NetworkHelper = NetworkUtils{}
}
