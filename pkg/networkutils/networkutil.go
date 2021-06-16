package networkutils

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

type NetworkUtils struct {
}

func (n NetworkUtils) IsNSorErr(nspath string) error {
	return ns.IsNSorErr(nspath)
}

func (n NetworkUtils) SetupPodNetwork(nic *rpc.HostNic, ip string) error {
	toPodRule := netlink.NewRule()
	toPodRule.Priority = constants.ToContainerRulePriority
	toPodRule.Table = constants.MainTable
	toPodRule.Dst = &net.IPNet{
		IP:   net.ParseIP(ip),
		Mask: net.CIDRMask(32, 32),
	}
	if err := netlink.RuleAdd(toPodRule); err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to add rule %s : %v", toPodRule, err)
	}

	return setArpReply(constants.GetHostNicBridgeName(int(nic.RouteTableNum)), ip, nic.HardwareAddr, "-A")
}

//After the Response is uninstalled, the relevant routes are cleared, so you only need to delete the rule.
func (n NetworkUtils) CleanupPodNetwork(nic *rpc.HostNic, podIP string) error {
	ip := net.ParseIP(podIP)
	dstRules, err := getRuleListByDst(ip)
	if err != nil {
		return err
	}

	for _, rule := range dstRules {
		err := netlink.RuleDel(&rule)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to del rule %v : %v", rule, err)
		}
	}

	return setArpReply(constants.GetHostNicBridgeName(int(nic.RouteTableNum)), podIP, nic.HardwareAddr, "-D")
}

//Note: setup NetworkManager to disable dhcp on nic
// SetupNicNetwork adds default route to route table (nic-<nic_table>)
func (n NetworkUtils) SetupNetwork(nic *rpc.HostNic) (rpc.Phase, error) {
	// Get links by addrs
	// case 1: only vxnet-hostnic
	// case 2: bridge and bridge_slave
	name := constants.GetHostNicName(nic.VxNet.ID)
	links, err := n.linksByMacAddr(nic.HardwareAddr)
	log.Infof("links: %v", links)
	if len(links) == 0 {
		return rpc.Phase_Init, fmt.Errorf("failed to get link %s: %v", name, err)
	}
	if len(links) > 2 {
		return rpc.Phase_Init, fmt.Errorf("failed to get link %s: %d unexpected", nic.HardwareAddr, len(links))
	}

	if len(links) == 1 {
		if err := n.setupNicNetwork(nic); err != nil {
			return rpc.Phase_CreateAndAttach, err
		}
		if err := n.setupBridgeNetwork(links[0], constants.GetHostNicBridgeName(int(nic.RouteTableNum))); err != nil {
			return rpc.Phase_JoinBridge, err
		}
		if err := n.setupRouteTable(nic); err != nil {
			return rpc.Phase_SetRouteTable, err
		}
	}

	return rpc.Phase_Succeeded, nil
}

//After the Response is uninstalled, the relevant routes are cleared, so you only need to delete the rule.
func (n NetworkUtils) CleanupNetwork(nic *rpc.HostNic) error {
	return nil
}

//Note: setup NetworkManager to disable dhcp on nic
// SetupNicNetwork adds default route to route table (nic-<nic_table>)
func (n NetworkUtils) setupNicNetwork(nic *rpc.HostNic) error {
	name := constants.GetHostNicName(nic.VxNet.ID)
	link, err := n.LinkByMacAddr(nic.HardwareAddr)
	if err != nil {
		return fmt.Errorf("failed to get link %s: %v", name, err)
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
	return nil
}

func (n NetworkUtils) setupBridgeNetwork(link netlink.Link, brName string) error {
	//create br, then add hostnic to br, and set route to br
	la := netlink.NewLinkAttrs()
	la.Name = brName
	br := &netlink.Bridge{LinkAttrs: la}
	err := netlink.LinkAdd(br)
	if err != nil {
		return fmt.Errorf("failed to add %s to %s: %v\n", link.Attrs().Name, la.Name, err)
	}
	err = netlink.LinkSetMaster(link, br)
	if err != nil {
		return fmt.Errorf("faild to set link %s: %v\n", la.Name, err)
	}
	err = netlink.LinkSetUp(br)
	if err != nil {
		return fmt.Errorf("failed to set link %s up: %v", la.Name, err)
	}

	return nil
}

func (n NetworkUtils) setupRouteTable(nic *rpc.HostNic) error {
	ls, err := n.linksByMacAddr(nic.HardwareAddr)
	if len(ls) != 2 || err != nil {
		return fmt.Errorf("failed to get link %s: %v", nic.HardwareAddr, ls)
	}

	var link netlink.Link
	log.Infof("setupRouteTable: %d", len(ls))
	for _, l := range ls {
		if l.Type() == "bridge" {
			link = l
		}
	}
	if link == nil {
		return fmt.Errorf("failed to found link %s for bridge", nic.HardwareAddr)
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
		if err := netlink.RouteAdd(&r); err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to add route %v : %v", r, err)
		}
	}

	fromPodRule := netlink.NewRule()
	fromPodRule.Priority = constants.FromContainerRulePriority
	fromPodRule.Table = int(nic.RouteTableNum)
	fromPodRule.Src = dst
	err = netlink.RuleAdd(fromPodRule)
	log.Infof("add from pod rule for vxnet %s: %s", nic.VxNet.ID, fromPodRule)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to add rule %s : %v", fromPodRule, err)
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

func (n NetworkUtils) linksByMacAddr(macAddr string) ([]netlink.Link, error) {
	var ls []netlink.Link
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}

	for _, link := range links {
		attr := link.Attrs()
		if attr.HardwareAddr.String() == macAddr {
			ls = append(ls, link)
		}
	}
	return ls, nil
}

func setArpReply(br string, ip string, macAddress string, action string) error {
	rule := fmt.Sprintf("ebtables -t nat %s PREROUTING -p ARP --logical-in %s --arp-op Request --arp-ip-dst %s -j arpreply --arpreply-mac %s",
		action, br, ip, macAddress)

	// TODO: delete later
	log.Infof("ebtables rule: %s", rule)
	_, err := ExecuteCommand(rule)
	return err
}

func getRuleListByDst(dst net.IP) ([]netlink.Rule, error) {
	var dstRuleList []netlink.Rule
	ruleList, err := netlink.RuleList(unix.AF_INET)
	if err != nil {
		return nil, err
	}
	for _, rule := range ruleList {
		// TODO: delete later
		log.Infof("dst rule %s", rule)
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

func ExecuteCommand(command string) (string, error) {
	var stderr bytes.Buffer
	var out bytes.Buffer
	cmd := exec.Command("sh", "-c", command)
	cmd.Stderr = &stderr
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s:%s", err.Error(), stderr.String())
	}

	return out.String(), nil
}
