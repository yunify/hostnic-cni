package networkutils

import (
	"fmt"
	"net"
	"time"

	coreosiptables "github.com/coreos/go-iptables/iptables"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	neterror "github.com/yunify/hostnic-cni/pkg/errors"
	"github.com/yunify/hostnic-cni/pkg/netlinkwrapper"
	"github.com/yunify/hostnic-cni/pkg/networkutils/iptables"
	"github.com/yunify/hostnic-cni/pkg/nswrapper"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
)

const (
	// 0- 511 can be used other higher priorities
	toPodRulePriority = 512

	// 513 - 1023, can be used priority lower than toPodRulePriority but higher than default nonVPC CIDR rule

	// 1024 is reserved for (ip rule not to <vpc's subnet> table main)
	hostRulePriority = 1024

	// 1025 - 1535 can be used priority lower than fromPodRulePriority but higher than default nonVPC CIDR rule
	fromPodRulePriority = 1536

	mainRoutingTable = unix.RT_TABLE_MAIN

	// This environment is used to specify whether an external NAT gateway will be used to provide SNAT of
	// secondary NIC IP addresses.  If set to "true", the SNAT iptables rule and off-VPC ip rule will not
	// be installed and will be removed if they are already installed.  Defaults to false.
	envExternalSNAT = "QINGCLOUD_VPC_K8S_CNI_EXTERNALSNAT"

	// This environment is used to specify weather the SNAT rule added to iptables should randomize port
	// allocation for outgoing connections. If set to "hashrandom" the SNAT iptables rule will have the "--random" flag
	// added to it. Set it to "prng" if you want to use a pseudo random numbers, i.e. "--random-fully".
	// Defaults to hashrandom.
	envRandomizeSNAT = "QINGCLOUD_VPC_K8S_CNI_RANDOMIZESNAT"

	// envNodePortSupport is the name of environment variable that configures whether we implement support for
	// NodePorts on the primary NIC.  This requires that we add additional iptables rules and loosen the kernel's
	// RPF check as described below.  Defaults to true.
	envNodePortSupport = "QINGCLOUD_VPC_CNI_NODE_PORT_SUPPORT"

	envVPNSupport = "QINGCLOUD_VPC_CNI_VPN_SUPPORT"
	// envConnmark is the name of the environment variable that overrides the default connection mark, used to
	// mark traffic coming from the primary NIC so that return traffic can be forced out of the same interface.
	// Without using a mark, NodePort DNAT and our source-based routing do not work together if the target pod
	// behind the node port is not on the main NIC.  In that case, the un-DNAT is done after the source-based
	// routing, resulting in the packet being sent out of the pod's NIC, when the NodePort traffic should be
	// sent over the main NIC.
	envConnmark = "QINGCLOUD_VPC_K8S_CNI_CONNMARK"

	// defaultConnmark is the default value for the connmark described above.  Note: the mark space is a little crowded,
	// - kube-proxy uses 0x0000c000
	// - Calico uses 0xffff0000.
	defaultConnmark     = 0x80
	defaultVnicConnmark = 0x81

	// MTU of NIC - veth MTU defined in plugins/routed-nic/driver/driver.go
	ethernetMTU = 9001

	// number of retries to add a route
	maxRetryRouteAdd      = 5
	retryRouteAddInterval = 5 * time.Second

	// number of attempts to find an NIC by MAC address after it is attached
	maxAttemptsLinkByMac   = 5
	retryLinkByMacInterval = 5 * time.Second
)

// NetworkAPIs defines the host level and the nic level network related operations
type NetworkAPIs interface {
	// SetupNodeNetwork performs node level network configuration
	SetupHostNetwork(primaryMAC string, primaryAddr *net.IP) error
	// SetupNICNetwork performs nic level network configuration
	SetupNICNetwork(nicIP string, mac string, table int, subnetCIDR string) error
}

type linuxNetwork struct {
	err error

	useExternalSNAT        bool
	typeOfSNAT             snatType
	nodePortSupportEnabled bool
	connmark               uint32
	vpnSupportEnabled      bool

	netLink     netlinkwrapper.NetLink
	ns          nswrapper.NS
	newIptables func() (iptables.IptablesIface, error)
	mainNICMark uint32
	vnicMark    uint32
	setProcSys  func(string, string) error
}

// New creates a linuxNetwork object
func New() NetworkAPIs {
	InitIpset()

	return &linuxNetwork{
		useExternalSNAT:        useExternalSNAT(),
		typeOfSNAT:             typeOfSNAT(),
		nodePortSupportEnabled: nodePortSupportEnabled(),
		mainNICMark:            getConnmark(),
		vnicMark:               defaultVnicConnmark,

		netLink: netlinkwrapper.NewNetLink(),
		ns:      nswrapper.NewNS(),
		//TODO: ipv6? now only ipv4
		newIptables: func() (iptables.IptablesIface, error) {
			ipt, err := coreosiptables.New()
			return ipt, err
		},
		setProcSys: setProcSysByWritingFile,
	}
}

// SetupHostNetwork performs node level network configuration
func (n *linuxNetwork) SetupHostNetwork(primaryMAC string, primaryAddr *net.IP) error {
	klog.V(1).Info("Setting up host network... ")

	n.netLink.Init()

	// If node port support is enabled, configure the kernel's reverse path filter check on eth0 for "loose"
	// filtering.  This is required because
	// - NodePorts are exposed on eth0
	// - The kernel's RPF check happens after incoming packets to NodePorts are DNATted to the pod IP.
	// - For pods assigned to secondary NICs, the routing table includes source-based routing.  When the kernel does
	//   the RPF check, it looks up the route using the pod IP as the source.
	// - Thus, it finds the source-based route that leaves via the secondary NIC.
	// - In "strict" mode, the RPF check fails because the return path uses a different interface to the incoming
	//   packet.  In "loose" mode, the check passes because some route was found.
	primaryIntfRPFilter := "/proc/sys/net/ipv4/conf/all/rp_filter"
	err := n.setProcSys(primaryIntfRPFilter, "0")
	if err != nil {
		return errors.Wrapf(err, "failed to configure RPF check")
	}

	primaryIntf := "eth0"
	if n.nodePortSupportEnabled {
		link, err := n.netLink.LinkByMac(primaryMAC)
		if err != nil {
			return errors.Wrapf(err, "failed to SetupHostNetwork")
		}

		primaryIntf = link.Attrs().Name
		primaryIntfRPFilter := "/proc/sys/net/ipv4/conf/" + primaryIntf + "/rp_filter"
		err = n.setProcSys(primaryIntfRPFilter, "2")
		if err != nil {
			return errors.Wrapf(err, "failed to configure RPF check")
		}
	}

	// If node port support is enabled, add a rule that will force marked traffic out of the main NIC.  We then
	// add iptables rules below that will mark traffic that needs this special treatment.  In particular NodePort
	// traffic always comes in via the main NIC but response traffic would go out of the pod's assigned NIC if we
	// didn't handle it specially. This is because the routing decision is done before the NodePort's DNAT is
	// reversed so, to the routing table, it looks like the traffic is pod traffic instead of NodePort traffic.
	mainNICRule := n.netLink.NewRule()
	mainNICRule.Mark = int(n.mainNICMark)
	mainNICRule.Mask = int(n.mainNICMark)
	mainNICRule.Table = mainRoutingTable
	mainNICRule.Priority = hostRulePriority
	// If this is a restart, cleanup previous rule first
	n.netLink.RuleDel(mainNICRule)

	if n.nodePortSupportEnabled {
		n.netLink.RuleAdd(mainNICRule)
	}

	if err = n.netLink.Error(); err != nil {
		klog.Error("Failed to add ip rule for snat")
		return err
	}

	//Below set up iptables rule
	ipt, err := n.newIptables()
	if err != nil {
		return errors.Wrap(err, "host network setup: failed to create iptables")
	}

	// build IPTABLES chain for SNAT of non-VPC outbound traffic
	snatChain := fmt.Sprintf("QINGCLOUD-SNAT-CHAIN")
	if err = ipt.NewChain("nat", snatChain); err != nil {
		if neterror.ContainChainExistErr(err) {
			klog.V(1).Infof("Clear chain %s before insert rule", snatChain)
			err = ipt.ClearChain("nat", snatChain)
			if err != nil {
				klog.Errorf("Failed to clear chain %s", snatChain)
				return err
			}
		} else {
			klog.Errorf("ipt.NewChain error for chain [%s]: %v", snatChain, err)
			return errors.Wrapf(err, "host network setup: failed to add chain")
		}
	}

	mangleChain := fmt.Sprintf("QINGCLOUD-PREROUTING-CHAIN")
	if err = ipt.NewChain("mangle", mangleChain); err != nil {
		if neterror.ContainChainExistErr(err) {
			klog.V(1).Infof("Clear chain %s before insert rule", mangleChain)
			err = ipt.ClearChain("mangle", mangleChain)
			if err != nil {
				klog.Errorf("Failed to clear chain %s", mangleChain)
				return err
			}
		} else {
			klog.Errorf("ipt.NewChain error for chain [%s]: %v", mangleChain, err)
			return errors.Wrapf(err, "host network setup: failed to add chain")
		}
	}

	// build SNAT rules for outbound non-VPC traffic
	klog.V(2).Infof("Setup Host Network: iptables -A POSTROUTING -m comment --comment \"QINGCLOUD SNAT CHAIN\" -j QINGCLOUD-SNAT-CHAIN-0")

	var iptableRules []iptables.IptablesRule
	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "first SNAT rules for non-VPC outbound traffic",
		ShouldExist: !n.useExternalSNAT,
		Table:       "nat",
		Chain:       "POSTROUTING",
		Rule: []string{
			"-m", "comment", "--comment", "QINGCLOUD SNAT CHAIN", "-j", snatChain,
		}})

	// Prepare the Desired Rule for SNAT Rule
	snatRule := []string{"-m", "comment", "--comment", "QINGCLOUD SNAT",
		"-m", "set", "!", "--match-set", ipsetName, "dst",
		"-j", "SNAT", "--to-source", primaryAddr.String()}
	if n.typeOfSNAT == randomHashSNAT {
		snatRule = append(snatRule, "--random")
	}
	if n.typeOfSNAT == randomPRNGSNAT {
		if ipt.HasRandomFully() {
			snatRule = append(snatRule, "--random-fully")
		} else {
			klog.Warningf("prng (--random-fully) requested, but iptables version does not support it. " +
				"Falling back to hashrandom (--random)")
			snatRule = append(snatRule, "--random")
		}
	}
	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "last SNAT rule for non-VPC outbound traffic",
		ShouldExist: !n.useExternalSNAT,
		Table:       "nat",
		Chain:       snatChain,
		Rule:        snatRule,
	})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "accept traffic to/from nics in chain Forward",
		ShouldExist: true,
		Table:       "filter",
		Chain:       "FORWARD",
		Rule:        []string{"-m", "comment", "--comment", "QINGCLOUD FORWARD", "-i", "nic+", "-j", "ACCEPT"},
	})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "accept traffic to/from nics in chain Forward",
		ShouldExist: true,
		Table:       "filter",
		Chain:       "FORWARD",
		Rule:        []string{"-m", "comment", "--comment", "QINGCLOUD FORWARD", "-o", "nic+", "-j", "ACCEPT"},
	})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "first rules for VPC outbound traffic",
		ShouldExist: true,
		Table:       "mangle",
		Chain:       "PREROUTING",
		Rule: []string{
			"-m", "comment", "--comment", "QINGCLOUD MANGLE CHAIN", "-j", mangleChain,
		}})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "connmark for pod to pod",
		ShouldExist: true,
		Table:       "mangle",
		Chain:       mangleChain,
		Rule: []string{
			"-m", "comment", "--comment", "QINGCLOUD Pod to Pod",
			"-i", "nic+",
			"-m", "set", "--match-set", ipsetName, "dst",
			"-j", "MARK", "--set-mark", fmt.Sprintf("%#x/%#x", n.vnicMark, n.vnicMark),
		},
	})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "connmark for primary NIC",
		ShouldExist: n.nodePortSupportEnabled,
		Table:       "mangle",
		Chain:       mangleChain,
		Rule: []string{
			"-m", "comment", "--comment", "QINGCLOUD, primary NIC",
			"-i", primaryIntf,
			"-m", "addrtype", "--dst-type", "LOCAL", "--limit-iface-in",
			"-j", "CONNMARK", "--set-mark", fmt.Sprintf("%#x/%#x", n.mainNICMark, n.mainNICMark),
		},
	})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "connmark restore for primary NIC",
		ShouldExist: n.nodePortSupportEnabled,
		Table:       "mangle",
		Chain:       mangleChain,
		Rule: []string{
			"-m", "comment", "--comment", "QINGCLOUD, primary NIC",
			"-i", "nic+",
			"-m", "set", "!", "--match-set", ipsetName, "dst",
			"-j", "CONNMARK", "--restore-mark", "--mask", fmt.Sprintf("%#x", n.mainNICMark),
		},
	})

	for _, rule := range iptableRules {
		klog.V(2).Infof("execute iptable rule : %s", rule.Name)

		exists, err := ipt.Exists(rule.Table, rule.Chain, rule.Rule...)
		if err != nil {
			klog.Errorf("host network setup: failed to check existence of %v, %v", rule, err)
			return errors.Wrapf(err, "host network setup: failed to check existence of %v", rule)
		}

		if !exists && rule.ShouldExist {
			err = ipt.Append(rule.Table, rule.Chain, rule.Rule...)
			if err != nil {
				klog.Errorf("host network setup: failed to add %v, %v", rule, err)
				return errors.Wrapf(err, "host network setup: failed to add %v", rule)
			}
		} else if exists && !rule.ShouldExist {
			err = ipt.Delete(rule.Table, rule.Chain, rule.Rule...)
			if err != nil {
				klog.Errorf("host network setup: failed to delete %v, %v", rule, err)
				return errors.Wrapf(err, "host network setup: failed to delete %v", rule)
			}
		}
	}

	return nil
}

// SetupNICNetwork adds default route to route table (nic-<nic_table>)
func (n *linuxNetwork) SetupNICNetwork(nicIP string, mac string, nicTable int, nicSubnetCIDR string) error {
	netLink := n.netLink
	netLink.Init()

	klog.Infof("Setting up network for an NIC with IP address %s, mac %s,  CIDR %s and route table %d",
		nicIP, mac, nicSubnetCIDR, nicTable)

	_, ipnet, err := net.ParseCIDR(nicSubnetCIDR)
	if err != nil {
		return errors.Wrapf(err, "setupNICNetwork: invalid IPv4 CIDR block %s", nicSubnetCIDR)
	}
	gw, err := incrementIPv4Addr(ipnet.IP)
	if err != nil {
		return errors.Wrapf(err, "setupNICNetwork: failed to define gateway address from %v", ipnet.IP)
	}

	link, _ := netLink.LinkByIndex(nicTable)

	intfRPFilter := "/proc/sys/net/ipv4/conf/" + link.Attrs().Name + "/rp_filter"

	err = n.setProcSys(intfRPFilter, "0")
	if err != nil {
		return errors.Wrapf(err, "failed to configure RPF check")
	}

	netLink.LinkSetMTU(link, ethernetMTU)
	netLink.LinkSetUp(link)

	addrs, err := netLink.AddrList(link, unix.AF_INET)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		netLink.AddrDel(link, &addr)
	}

	routes := []netlink.Route{
		// Add a direct link route for the host's NIC IP only
		{
			LinkIndex: link.Attrs().Index,
			Dst:       &net.IPNet{IP: gw, Mask: net.CIDRMask(32, 32)},
			Scope:     netlink.SCOPE_LINK,
			Table:     nicTable,
		},
		//TODO: find out which kernel bug will cause the problem below.
		//In centos 7.5 (kernel 3.10.0-862),  the following route will fail to add.
		// Route all other traffic via the host's NIC IP
		{
			LinkIndex: link.Attrs().Index,
			Dst:       &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
			Scope:     netlink.SCOPE_UNIVERSE,
			Gw:        gw,
			Table:     nicTable,
		},
	}

	for _, r := range routes {
		netLink.RouteAdd(&r)
	}

	return netLink.Error()
}

// NewFakeNetworkAPI is used by unit test
func NewFakeNetworkAPI(netlink netlinkwrapper.NetLink, iptableIface iptables.IptablesIface, setProcSys func(string, string) error) NetworkAPIs {
	return &linuxNetwork{
		useExternalSNAT:        useExternalSNAT(),
		typeOfSNAT:             typeOfSNAT(),
		nodePortSupportEnabled: nodePortSupportEnabled(),
		mainNICMark:            getConnmark(),
		vnicMark:               defaultVnicConnmark,
		netLink:                netlink,
		ns:                     &nswrapper.FakeNsWrapper{},
		newIptables: func() (iptables.IptablesIface, error) {
			return iptableIface, nil
		},
		setProcSys: setProcSys,
	}
}
