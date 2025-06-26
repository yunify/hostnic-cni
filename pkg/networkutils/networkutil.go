package networkutils

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/insomniacslk/dhcp/dhcpv4/client4"
	"github.com/insomniacslk/dhcp/netboot"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

const (
	ebtablesLock = "/var/run/hostnic/hostnic.lock"
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

	return setArpReply(constants.GetHostNicBridgeName(int(nic.RouteTableNum)), ip, nic.HardwareAddr, "-I")
}

// After the Response is uninstalled, the relevant routes are cleared, so you only need to delete the rule.
func (n NetworkUtils) CleanupPodNetwork(nic *rpc.HostNic, podIP string) error {
	ip := net.ParseIP(podIP)
	dstRules, err := getRuleListByDst(ip)
	if err != nil {
		return fmt.Errorf("get rule list by ip %s error: %v", podIP, err)
	}

	for _, rule := range dstRules {
		//delete not exists rule return: RTNETLINK answers: No such file or directory
		err := netlink.RuleDel(&rule)
		if err != nil && !strings.Contains(err.Error(), "no such file or directory") {
			return fmt.Errorf("failed to del rule %v : %v", rule, err)
		}
	}

	//delete not exists rule return: Sorry, rule does not exist.(ebtables v2.0.11)
	//delete not exists rule return: Bad rule (does a matching rule exist in that chain?) (ebtables v1.0.8)
	err = setArpReply(constants.GetHostNicBridgeName(int(nic.RouteTableNum)), podIP, nic.HardwareAddr, "-D")
	if err != nil && !strings.Contains(err.Error(), "rule does not exist") && !strings.Contains(err.Error(), "does a matching rule exist in that chain") {
		return fmt.Errorf("delete ebtables rule for ip %s error: %v", podIP, err)
	}

	return nil
}

// Note: setup NetworkManager to disable dhcp on nic
// SetupNicNetwork adds default route to route table (nic-<nic_table>)
func (n NetworkUtils) SetupNetwork(nic *rpc.HostNic) (rpc.Phase, error) {
	devName := constants.GetHostNicName(nic.VxNet.ID)
	brName := constants.GetHostNicBridgeName(int(nic.RouteTableNum))
	master, slave, err := n.getLinksByMacAddr(nic.HardwareAddr)
	if master == nil && slave == nil {
		return rpc.Phase_Init, fmt.Errorf("failed to get link %s: %v %v %v", nic.HardwareAddr, master, slave, err)
	}

	// nic has attached, but bridge not set
	if slave == nil {
		if err := n.setupNicNetwork(devName, master); err != nil {
			return rpc.Phase_CreateAndAttach, err
		}
		if err := n.setupBridgeNetwork(master, brName, nic.VxNet.TunnelType); err != nil {
			return rpc.Phase_JoinBridge, err
		}
		if err := n.setupRouteTable(nic); err != nil {
			return rpc.Phase_SetRouteTable, err
		}
	} else {
		// do nothing: nic has attached and bridge is ready
		// hostnic_todo: should check if ip is ready for hostnic br or do this in the ip addr new timer
	}

	return rpc.Phase_Succeeded, nil
}

func (n NetworkUtils) CheckAndRepairNetwork(nic *rpc.HostNic) (rpc.Phase, error) {
	devName := constants.GetHostNicName(nic.VxNet.ID)
	brName := constants.GetHostNicBridgeName(int(nic.RouteTableNum))
	master, slave, err := n.getLinksByMacAddr(nic.HardwareAddr)

	if err != nil {
		if err != constants.ErrNicNotFound {
			return rpc.Phase_Init, fmt.Errorf("failed to get link %s: %v", nic.HardwareAddr, err)
		} else {
			// slave link not found, try to attach again
			nicKey := fmt.Sprintf("%s/%s", nic.VxNet.ID, nic.ID)

			//describe nic to get status; if can be attached, then attach it to this node;
			klog.Infof("hostNic %s was not found, try to attach it", nicKey)
			nicCur, err := qcclient.QClient.GetNics([]string{nic.ID})
			if err != nil {
				return rpc.Phase_Init, fmt.Errorf("failed to get nic %s from api: %v", nicKey, err)
			}
			if _, ok := nicCur[nic.ID]; !ok {
				return rpc.Phase_Init, fmt.Errorf("nic %s was not found from api, can't repair this hostnic", nicKey)
			}
			if nicCur[nic.ID].Using {
				return rpc.Phase_Init, fmt.Errorf("nic %s status is in-use, please deattach it from other instance", nicKey)
			}

			//attach nic
			klog.Infof("waiting for hostNic %s attach", nicKey)
			_, err = qcclient.QClient.AttachNics([]string{nic.ID}, true)
			if err != nil {
				return rpc.Phase_Init, fmt.Errorf("attach hostNic %s error: %v", nicKey, err)
			}

			klog.Infof("attach hostNic %s success", nicKey)

			//get link
			slave, err = n.LinkByMacAddr(nic.ID)
			if err != nil {
				return rpc.Phase_Init, fmt.Errorf("failed to get hostNic %s after attach", nicKey)
			}
		}
	}

	if slave == nil {
		// nic has attached, but bridge not set: maybe node reboot, just rebuild all
		if err := n.setupNicNetwork(devName, master); err != nil {
			return rpc.Phase_CreateAndAttach, err
		}
		if err := n.setupBridgeNetwork(master, brName, nic.VxNet.TunnelType); err != nil {
			return rpc.Phase_JoinBridge, err
		}
		if err := n.setupRouteTable(nic); err != nil {
			return rpc.Phase_SetRouteTable, err
		}
	} else {
		// maybe agent reboot, then check all and do some repair
		if err := n.setupNicNetwork(devName, slave); err != nil {
			return rpc.Phase_CreateAndAttach, err
		}
		if err := n.setupBridgeNetwork(slave, brName, nic.VxNet.TunnelType); err != nil {
			return rpc.Phase_JoinBridge, err
		}
		if err := n.setupRouteTable(nic); err != nil {
			return rpc.Phase_SetRouteTable, err
		}
	}

	return rpc.Phase_Succeeded, nil
}

// After the Response is uninstalled, the relevant routes are cleared, so you only need to delete the rule.
func (n NetworkUtils) CleanupNetwork(nic *rpc.HostNic) error {
	if err := n.clearRouteTable(nic); err != nil {
		return err
	}
	return n.clearBridgeNetwork(nic)
}

// Note: setup NetworkManager to disable dhcp on nic
// SetupNicNetwork adds default route to route table (nic-<nic_table>)
func (n NetworkUtils) setupNicNetwork(name string, link netlink.Link) error {
	var err error
	if link.Attrs().Name != name {
		err = netlink.LinkSetName(link, name)
		if err != nil {
			return fmt.Errorf("failed to set link %d name to %s: %v", link.Attrs().Index, name, err)
		}
	}
	if link.Attrs().Alias != name {
		err = netlink.LinkSetAlias(link, name)
		if err != nil {
			return fmt.Errorf("failed to set link %d alias to %s: %v", link.Attrs().Index, name, err)
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

// create br and add hostnic to br
func (n NetworkUtils) setupBridgeNetwork(link netlink.Link, brName, tunnelType string) error {
	la := netlink.NewLinkAttrs()
	la.Name = brName
	br := &netlink.Bridge{LinkAttrs: la}
	err := netlink.LinkAdd(br)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to add %s to %s: %v", link.Attrs().Name, la.Name, err)
	}
	err = netlink.LinkSetMaster(link, br)
	if err != nil {
		return fmt.Errorf("faild to set link %s: %v", la.Name, err)
	}
	err = netlink.LinkSetUp(br)
	if err != nil {
		return fmt.Errorf("failed to set link %s up: %v", la.Name, err)
	}
	if tunnelType == constants.TunnelTypeVlan {
		// get an ip addr and add to br
		// 1.get an ip addr from dhcp server
		bootConf, err := getIPAddrFromDHCPServer(brName)
		if err != nil {
			return fmt.Errorf("failed to get ip address for link %s: %v", brName, err)
		}
		klog.Infof("get ip addr %+v success from dhcp server for link %s", bootConf.Addresses, brName)

		// 2. replace addr to br
		err = replaceLinkIPAddr(br, bootConf.Addresses)
		if err != nil {
			return fmt.Errorf("failed to replace ip address for link %s: %v", brName, err)
		}
	}

	return nil
}

func (n NetworkUtils) clearBridgeNetwork(nic *rpc.HostNic) error {
	brName := constants.GetHostNicBridgeName(int(nic.RouteTableNum))
	br, err := netlink.LinkByName(brName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return nil
		}
		return fmt.Errorf("failed to lookup br %s: %v", brName, err)
	}

	if err = netlink.LinkDel(br); err != nil {
		return fmt.Errorf("failed to del br %s: %v", brName, err)
	}
	return nil
}

func (n NetworkUtils) setupRouteTable(nic *rpc.HostNic) error {
	master, slave, err := n.getLinksByMacAddr(nic.HardwareAddr)
	if master == nil || slave == nil || err != nil {
		return fmt.Errorf("failed to get link %s: %v %v %v", nic.HardwareAddr, master, slave, err)
	}

	_, dst, _ := net.ParseCIDR(nic.VxNet.Network)
	routes := []netlink.Route{
		// Add a direct link route for Pods in the same vxnet
		{
			LinkIndex: master.Attrs().Index,
			Dst:       dst,
			Scope:     netlink.SCOPE_LINK,
			Table:     int(nic.RouteTableNum),
		},
		{
			LinkIndex: master.Attrs().Index,
			Dst: &net.IPNet{
				IP:   net.IPv4zero,
				Mask: net.CIDRMask(0, 32),
			},
			Scope: netlink.SCOPE_UNIVERSE,
			Gw:    net.ParseIP(nic.VxNet.Gateway),
			Table: int(nic.RouteTableNum),
		},
	}

	// if tunnel type is vlan, clear route to repair hostnic
	if nic.VxNet.TunnelType == constants.TunnelTypeVlan {
		for _, r := range routes {
			// netlink.RouteDel return error if route not exists: no such process
			if err := netlink.RouteDel(&r); err != nil && !strings.Contains(err.Error(), constants.RouteNotExistsError) {
				return fmt.Errorf("failed to del route %v: %v", r, err)
			}
		}

	} else {
		for _, r := range routes {
			// netlink.RouteAdd return error if route already exists: file exists ; shouldn't use os.IsExist here
			if err := netlink.RouteAdd(&r); err != nil && !strings.Contains(err.Error(), constants.RouteExistsError) {
				return fmt.Errorf("failed to add route %v: %v", r, err)
			}
		}
	}

	fromPodRule := netlink.NewRule()
	fromPodRule.Priority = constants.FromContainerRulePriority
	fromPodRule.Table = int(nic.RouteTableNum)
	fromPodRule.Src = dst
	err = netlink.RuleAdd(fromPodRule)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to add rule %s: %v", fromPodRule, err)
	}

	return nil
}

// Note: When br was deleted, associated rules in route table will be deleted by kernel, so skip route table clear.
func (n NetworkUtils) clearRouteTable(nic *rpc.HostNic) error {
	_, dst, _ := net.ParseCIDR(nic.VxNet.Network)
	fromPodRule := netlink.NewRule()
	fromPodRule.Priority = constants.FromContainerRulePriority
	fromPodRule.Table = int(nic.RouteTableNum)
	fromPodRule.Src = dst
	err := netlink.RuleDel(fromPodRule)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to del rule %s: %v", fromPodRule, err)
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

func (n NetworkUtils) getLinksByMacAddr(macAddr string) (netlink.Link, netlink.Link, error) {
	var master, slave netlink.Link
	links, err := netlink.LinkList()
	if err != nil {
		return nil, nil, err
	}

	for _, link := range links {
		attr := link.Attrs()
		if attr.HardwareAddr.String() == macAddr {
			if attr.MasterIndex != 0 {
				slave = link
			} else {
				master = link
			}
		}
	}
	// attention: br has the same mac addr with hostnic link
	if master == nil && slave == nil {
		return nil, nil, constants.ErrNicNotFound
	}
	return master, slave, nil
}

func setArpReply(br string, ip string, macAddress string, action string) error {
	rule := fmt.Sprintf("flock %s /sbin/ebtables -t nat %s PREROUTING -p ARP --logical-in %s --arp-op Request --arp-ip-dst %s -j arpreply --arpreply-mac %s",
		ebtablesLock, action, br, ip, macAddress)

	_, err := ExecuteCommand(rule)
	return err
}

func ListArpReply() (string, error) {
	cmd := fmt.Sprintf("flock %s /sbin/ebtables -t nat -L PREROUTING", ebtablesLock)
	out, err := ExecuteCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to list ebtables rules, execute command error: %s", err)
	}
	return out, err
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

func getIPAddrFromDHCPServer(ifname string) (*netboot.BootConf, error) {
	// client := client4.NewClient()
	client := &client4.Client{
		ReadTimeout:  client4.DefaultReadTimeout * 5,
		WriteTimeout: client4.DefaultWriteTimeout * 5,
	}
	conv, err := client.Exchange(ifname)
	if err != nil {
		return nil, fmt.Errorf("dhcp client exchange error: %v", err)
	}
	// extract the network configuration
	netconf, err := netboot.ConversationToNetconfv4(conv)
	if err != nil {
		return nil, fmt.Errorf("dhcp client ConversationToNetconfv4 error: %v", err)
	}
	return netconf, nil
}

func replaceLinkIPAddr(link netlink.Link, addres []netboot.AddrConf) error {
	var shortestLeaseTime time.Duration
	ifName := link.Attrs().Name
	if len(addres) < 1 {
		return fmt.Errorf("there is no avaliable addr for link %s", ifName)
	}
	for _, addrConf := range addres {
		if shortestLeaseTime.Seconds() == 0 {
			shortestLeaseTime = addrConf.ValidLifetime
		}
		if addrConf.ValidLifetime.Seconds() < shortestLeaseTime.Seconds() {
			shortestLeaseTime = addrConf.ValidLifetime
		}

		addr := &netlink.Addr{
			IPNet:       &addrConf.IPNet,
			ValidLft:    int(addrConf.ValidLifetime.Seconds()),
			PreferedLft: int(addrConf.ValidLifetime.Seconds()),
		}
		err := netlink.AddrReplace(link, addr)
		if err != nil {
			return fmt.Errorf("replace addr %+v to link %s error: %v", addr, ifName, err)
		}
	}

	if shortestLeaseTime.Seconds()/2 != constants.LastIPAddrRenewPeriod.Seconds() && shortestLeaseTime != 0 {
		klog.Infof("update LastIPAddrRenewPeriod from %v to %v", constants.LastIPAddrRenewPeriod, shortestLeaseTime/2)
		constants.LastIPAddrRenewPeriod = shortestLeaseTime / 2
		constants.IpAddrReNewTicker.Reset(shortestLeaseTime / 2)
	}

	return nil
}

func UpdateLinkIPAddrAndLease(nic *rpc.HostNic) error {
	brName := constants.GetHostNicBridgeName(int(nic.RouteTableNum))
	la := netlink.NewLinkAttrs()
	la.Name = brName
	br := &netlink.Bridge{LinkAttrs: la}

	// 1. get an ip form dhcp server
	bootConf, err := getIPAddrFromDHCPServer(brName)
	if err != nil || bootConf.Addresses == nil {
		return fmt.Errorf("failed to get ip address for link %s: %v", brName, err)
	}
	klog.Infof("get ip addr %+v success from dhcp server for link %s", bootConf.Addresses, brName)

	// 2. replace addr to br
	err = replaceLinkIPAddr(br, bootConf.Addresses)
	if err != nil {
		return fmt.Errorf("failed to replace ip address for link %s: %v", brName, err)
	}
	return nil
}
