package networkutils

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	coreosiptables "github.com/coreos/go-iptables/iptables"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
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
	defaultConnmark = 0x80

	// MTU of NIC - veth MTU defined in plugins/routed-nic/driver/driver.go
	ethernetMTU = 9001

	// number of retries to add a route
	maxRetryRouteAdd = 5

	retryRouteAddInterval = 5 * time.Second

	// number of attempts to find an NIC by MAC address after it is attached
	maxAttemptsLinkByMac = 5

	retryLinkByMacInterval = 5 * time.Second
)

// NetworkAPIs defines the host level and the nic level network related operations
type NetworkAPIs interface {
	// SetupNodeNetwork performs node level network configuration
	SetupHostNetwork(vpcCIDR *net.IPNet, vpcCIDRs []*string, primaryMAC string, primaryAddr *net.IP) error
	// SetupNICNetwork performs nic level network configuration
	SetupNICNetwork(nicIP string, mac string, table int, subnetCIDR string) error
	UseExternalSNAT() bool
	GetRuleList() ([]netlink.Rule, error)
	GetRuleListBySrc(ruleList []netlink.Rule, src net.IPNet) ([]netlink.Rule, error)
	UpdateRuleListBySrc(ruleList []netlink.Rule, src net.IPNet, toCIDRs []string, toFlag bool, table int) error
	DeleteRuleListBySrc(src net.IPNet) error
}

type linuxNetwork struct {
	useExternalSNAT        bool
	typeOfSNAT             snatType
	nodePortSupportEnabled bool
	connmark               uint32
	vpnSupportEnabled      bool

	netLink                  netlinkwrapper.NetLink
	ns                       nswrapper.NS
	newIptables              func() (iptables.IptablesIface, error)
	mainNICMark              uint32
	findPrimaryInterfaceName func(primaryMAC string) (string, error)
	setProcSys               func(string, string) error
}

type snatType uint32

const (
	sequentialSNAT snatType = iota
	randomHashSNAT
	randomPRNGSNAT
)

// New creates a linuxNetwork object
func New() NetworkAPIs {
	return &linuxNetwork{
		useExternalSNAT:        useExternalSNAT(),
		typeOfSNAT:             typeOfSNAT(),
		nodePortSupportEnabled: nodePortSupportEnabled(),
		mainNICMark:            getConnmark(),

		netLink: netlinkwrapper.NewNetLink(),
		ns:      nswrapper.NewNS(),
		newIptables: func() (iptables.IptablesIface, error) {
			ipt, err := coreosiptables.New()
			return ipt, err
		},
		findPrimaryInterfaceName: findPrimaryInterfaceName,
		setProcSys:               setProcSysByWritingFile,
	}
}

type stringWriteCloser interface {
	io.Closer
	WriteString(s string) (int, error)
}

// find out the primary interface name
func findPrimaryInterfaceName(primaryMAC string) (string, error) {
	klog.V(2).Infof("Trying to find primary interface that has mac : %s", primaryMAC)

	interfaces, err := net.Interfaces()
	if err != nil {
		klog.Errorf("Failed to read all interfaces: %v", err)
		return "", errors.Wrapf(err, "findPrimaryInterfaceName: failed to find interfaces")
	}

	for _, intf := range interfaces {
		klog.V(2).Infof("Discovered interface: %v, mac: %v", intf.Name, intf.HardwareAddr)

		if strings.Compare(primaryMAC, intf.HardwareAddr.String()) == 0 {
			klog.V(1).Infof("Discovered primary interface: %s", intf.Name)
			return intf.Name, nil
		}
	}

	klog.Errorf("No primary interface found")
	return "", errors.New("no primary interface found")
}

// SetupHostNetwork performs node level network configuration
func (n *linuxNetwork) SetupHostNetwork(vpcCIDR *net.IPNet, vpcCIDRs []*string, primaryMAC string, primaryAddr *net.IP) error {
	klog.V(1).Info("Setting up host network... ")

	primaryIntf := "eth0"
	var err error
	if n.nodePortSupportEnabled {
		primaryIntf, err = n.findPrimaryInterfaceName(primaryMAC)
		if err != nil {
			return errors.Wrapf(err, "failed to SetupHostNetwork")
		}
		// If node port support is enabled, configure the kernel's reverse path filter check on eth0 for "loose"
		// filtering.  This is required because
		// - NodePorts are exposed on eth0
		// - The kernel's RPF check happens after incoming packets to NodePorts are DNATted to the pod IP.
		// - For pods assigned to secondary NICs, the routing table includes source-based routing.  When the kernel does
		//   the RPF check, it looks up the route using the pod IP as the source.
		// - Thus, it finds the source-based route that leaves via the secondary NIC.
		// - In "strict" mode, the RPF check fails because the return path uses a different interface to the incoming
		//   packet.  In "loose" mode, the check passes because some route was found.
		primaryIntfRPFilter := "/proc/sys/net/ipv4/conf/" + primaryIntf + "/rp_filter"
		const rpFilterLoose = "2"

		klog.V(2).Infof("Setting RPF for primary interface: %s", primaryIntfRPFilter)
		err = n.setProcSys(primaryIntfRPFilter, rpFilterLoose)
		if err != nil {
			return errors.Wrapf(err, "failed to configure %s RPF check", primaryIntf)
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
	err = n.netLink.RuleDel(mainNICRule)
	if err != nil && !containsNoSuchRule(err) {
		klog.Errorf("Failed to cleanup old main NIC Rule: %v", err)
		return errors.Wrapf(err, "host network setup: failed to delete old main NIC rule")
	}

	if n.nodePortSupportEnabled {
		err = n.netLink.RuleAdd(mainNICRule)
		if err != nil {
			klog.Errorf("Failed to add host main NIC Rule: %v", err)
			return errors.Wrapf(err, "host network setup: failed to add main NIC rule")
		}
	}

	ipt, err := n.newIptables()
	if err != nil {
		return errors.Wrap(err, "host network setup: failed to create iptables")
	}

	// build IPTABLES chain for SNAT of non-VPC outbound traffic
	var chains []string
	for i := 0; i <= len(vpcCIDRs); i++ {
		chain := fmt.Sprintf("QINGCLOUD-SNAT-CHAIN-%d", i)
		klog.V(2).Infof("Setup Host Network: iptables -N %s -t nat", chain)
		if err = ipt.NewChain("nat", chain); err != nil {
			if containChainExistErr(err) {
				klog.V(1).Infof("Clear chain %s before insert rule", chain)
				err = ipt.ClearChain("nat", chain)
				if err != nil {
					klog.Errorf("Failed to clear chain %s", chain)
					return err
				}
			} else {
				klog.Errorf("ipt.NewChain error for chain [%s]: %v", chain, err)
				return errors.Wrapf(err, "host network setup: failed to add chain")
			}
		}
		chains = append(chains, chain)
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
			"-m", "comment", "--comment", "QINGCLOUD SNAT CHAIN", "-j", "QINGCLOUD-SNAT-CHAIN-0",
		}})

	for i, cidr := range vpcCIDRs {
		curChain := chains[i]
		nextChain := chains[i+1]
		curName := fmt.Sprintf("[%d] QINGCLOUD-SNAT-CHAIN", i)

		klog.V(2).Infof("Setup Host Network: iptables -A %s ! -d %s -t nat -j %s", curChain, *cidr, nextChain)

		iptableRules = append(iptableRules, iptables.IptablesRule{
			Name:        curName,
			ShouldExist: !n.useExternalSNAT,
			Table:       "nat",
			Chain:       curChain,
			Rule: []string{
				"!", "-d", *cidr, "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", nextChain,
			}})
	}

	lastChain := chains[len(chains)-1]
	// Prepare the Desired Rule for SNAT Rule
	snatRule := []string{"-m", "comment", "--comment", "QINGCLOUD, SNAT",

		"-m", "addrtype", "!", "--dst-type", "LOCAL",
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
	//turn on the forward chain on the nics
	// iptableRules = append(iptableRules, iptables.IptablesRule{
	// 	Name:        "accept traffic to/from nics in chain Forward in RELATED,ESTABLISHED",
	// 	ShouldExist: true,
	// 	Table:       "filter",
	// 	Chain:       "FORWARD",
	// 	Rule:        []string{"-i", "nic+", "-m", "comment", "--comment", "hostnic forwarding conntrack rule", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
	// })

	// iptableRules = append(iptableRules, iptables.IptablesRule{
	// 	Name:        "accept traffic to/from nics in chain Forward in RELATED,ESTABLISHED",
	// 	ShouldExist: true,
	// 	Table:       "filter",
	// 	Chain:       "FORWARD",
	// 	Rule:        []string{"-o", "nic+", "-m", "comment", "--comment", "hostnic forwarding conntrack rule", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
	// })

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "accept traffic to/from nics in chain Forward",
		ShouldExist: true,
		Table:       "filter",
		Chain:       "FORWARD",
		Rule:        []string{"-i", "nic+", "-j", "ACCEPT"},
	})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "accept traffic to/from nics in chain Forward",
		ShouldExist: true,
		Table:       "filter",
		Chain:       "FORWARD",
		Rule:        []string{"-o", "nic+", "-j", "ACCEPT"},
	})

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "last SNAT rule for non-VPC outbound traffic",
		ShouldExist: !n.useExternalSNAT,
		Table:       "nat",
		Chain:       lastChain,
		Rule:        snatRule,
	})

	for _, iptablerule := range iptableRules {
		klog.V(2).Infof("Preparing iptables rule: %s", iptablerule.String())
	}

	iptableRules = append(iptableRules, iptables.IptablesRule{
		Name:        "connmark for primary NIC",
		ShouldExist: n.nodePortSupportEnabled,
		Table:       "mangle",
		Chain:       "PREROUTING",
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
		Chain:       "PREROUTING",
		Rule: []string{
			"-m", "comment", "--comment", "QINGCLOUD, primary NIC",
			"-i", "nic+", "-j", "CONNMARK", "--restore-mark", "--mask", fmt.Sprintf("%#x", n.mainNICMark),
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

func containChainExistErr(err error) bool {
	return strings.Contains(err.Error(), "Chain already exists")
}

func setProcSysByWritingFile(key, value string) error {
	f, err := os.OpenFile(key, os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(value)
	if err != nil {
		// If the write failed, just close
		_ = f.Close()
		return err
	}
	return f.Close()
}

func containsNoSuchRule(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.ENOENT
	}
	return false
}

// GetConfigForDebug returns the active values of the configuration env vars (for debugging purposes).
func GetConfigForDebug() map[string]interface{} {
	return map[string]interface{}{
		envExternalSNAT:    useExternalSNAT(),
		envNodePortSupport: nodePortSupportEnabled(),
		envConnmark:        getConnmark(),
		envRandomizeSNAT:   typeOfSNAT(),
	}
}

// UseExternalSNAT returns whether SNAT of secondary NIC IPs should be handled with an external
// NAT gateway rather than on node. Failure to parse the setting will result in a log and the
// setting will be disabled.
func (n *linuxNetwork) UseExternalSNAT() bool {
	return useExternalSNAT()
}

func useExternalSNAT() bool {
	return getBoolEnvVar(envExternalSNAT, false)
}

func typeOfSNAT() snatType {
	defaultValue := randomHashSNAT
	defaultString := "hashrandom"
	strValue := os.Getenv(envRandomizeSNAT)
	switch strValue {
	case "":
		// empty means default
		return defaultValue
	case "prng":
		// prng means to use --random-fully
		// note: for old versions of iptables, this will fall back to --random
		return randomPRNGSNAT
	case "none":
		// none means to disable randomisation (no flag)
		return sequentialSNAT

	case defaultString:
		// hashrandom means to use --random
		return randomHashSNAT
	default:
		// if we get to this point, the environment variable has an invalid value
		klog.Errorf("Failed to parse %s; using default: %s. Provided string was %q", envRandomizeSNAT, defaultString,
			strValue)
		return defaultValue
	}
}

func nodePortSupportEnabled() bool {
	return getBoolEnvVar(envNodePortSupport, true)
}

func vpnSupportEnabled() bool {
	return getBoolEnvVar(envVPNSupport, true)
}

func getBoolEnvVar(name string, defaultValue bool) bool {
	if strValue := os.Getenv(name); strValue != "" {
		parsedValue, err := strconv.ParseBool(strValue)
		if err != nil {
			klog.Error("Failed to parse "+name+"; using default: "+fmt.Sprint(defaultValue), err.Error())
			return defaultValue
		}
		return parsedValue
	}
	return defaultValue
}

func getConnmark() uint32 {
	if connmark := os.Getenv(envConnmark); connmark != "" {
		mark, err := strconv.ParseInt(connmark, 0, 64)
		if err != nil {
			klog.Error("Failed to parse "+envConnmark+"; will use ", defaultConnmark, err.Error())
			return defaultConnmark
		}
		if mark > math.MaxUint32 || mark <= 0 {
			klog.Error(""+envConnmark+" out of range; will use ", defaultConnmark)
			return defaultConnmark
		}
		return uint32(mark)
	}
	return defaultConnmark
}

// LinkByMac returns linux netlink based on interface MAC
func LinkByMac(mac string, netLink netlinkwrapper.NetLink, retryInterval time.Duration) (netlink.Link, error) {
	// The adapter might not be immediately available, so we perform retries
	var lastErr error
	attempt := 0
	for {
		attempt++
		if attempt > maxAttemptsLinkByMac {
			return nil, lastErr
		} else if attempt > 1 {
			time.Sleep(retryInterval)
		}

		links, err := netLink.LinkList()

		if err != nil {
			lastErr = errors.Errorf("%s (attempt %d/%d)", err, attempt, maxAttemptsLinkByMac)
			klog.V(2).Infof(lastErr.Error())
			continue
		}

		for _, link := range links {
			if mac == link.Attrs().HardwareAddr.String() {
				klog.V(2).Infof("Found the Link that uses mac address %s and its index is %d (attempt %d/%d)",
					mac, link.Attrs().Index, attempt, maxAttemptsLinkByMac)
				return link, nil
			}
		}

		lastErr = errors.Errorf("no interface found which uses mac address %s (attempt %d/%d)", mac, attempt, maxAttemptsLinkByMac)
		klog.V(2).Infof(lastErr.Error())
	}

}

// SetupNICNetwork adds default route to route table (nic-<nic_table>)
func (n *linuxNetwork) SetupNICNetwork(nicIP string, nicMAC string, nicTable int, nicSubnetCIDR string) error {
	return setupNICNetwork(nicIP, nicMAC, nicTable, nicSubnetCIDR, n.netLink, retryLinkByMacInterval)
}

func killDhclient(nicName string) error {
	filename := fmt.Sprintf("/var/run/dhclient.%s.pid", nicName)
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("error reading %s: %s", filename, err)
	}

	pid, err := strconv.ParseInt(string(content[:len(content)-1]), 10, 64);
	if  err != nil {
		return fmt.Errorf("pid file %s has a invalid value: %s", filename, err)
	}

	filename = fmt.Sprintf("/proc/%d/cmdline", pid)
	content, err = ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("error reading %s: %s", filename, err)
	}
	if ! strings.Contains(string(content[:len(content)-1]), "dhclient") {
		return nil
	}

	p, _ := os.FindProcess(int(pid))
	return p.Kill()
}

func setupNICNetwork(nicIP string, nicMAC string, nicTable int, nicSubnetCIDR string, netLink netlinkwrapper.NetLink, retryLinkByMacInterval time.Duration) error {
	if nicTable == 0 {
		klog.V(2).Infof("Skipping set up NIC network for primary interface %s", nicIP)
		return nil
	}

	klog.V(1).Infof("Setting up network for an NIC with IP address %s, MAC address %s, CIDR %s and route table %d",
		nicIP, nicMAC, nicSubnetCIDR, nicTable)
	link, err := LinkByMac(nicMAC, netLink, retryLinkByMacInterval)
	if err != nil {
		return errors.Wrapf(err, "setupNICNetwork: failed to find the link which uses MAC address %s", nicMAC)
	}

	nicName := link.Attrs().Name;
	if err = killDhclient(nicName); err != nil {
		klog.Warningf("Please make sure to kill the dhclient process for %s: %s", nicName, err);
	}

	if err = netLink.LinkSetMTU(link, ethernetMTU); err != nil {
		return errors.Wrapf(err, "setupNICNetwork: failed to set MTU for %s", nicIP)
	}
	// TODO: due to the bug of iaas, we must set it down if it is up.
	if err = netLink.LinkSetUp(link); err != nil {
		return errors.Wrapf(err, "setupNICNetwork: failed to bring up NIC %s", nicIP)
	}

	deviceNumber := link.Attrs().Index

	_, ipnet, err := net.ParseCIDR(nicSubnetCIDR)
	if err != nil {
		return errors.Wrapf(err, "setupNICNetwork: invalid IPv4 CIDR block %s", nicSubnetCIDR)
	}

	gw, err := incrementIPv4Addr(ipnet.IP)
	if err != nil {
		return errors.Wrapf(err, "setupNICNetwork: failed to define gateway address from %v", ipnet.IP)
	}

	// Explicitly set the IP on the device if not already set.
	// Required for older kernels.
	// ip addr show
	// ip add del <nicIP> dev <link> (if necessary)
	// ip add add <nicIP> dev <link>
	klog.V(2).Infof("Setting up NIC's primary IP %s", nicIP)
	addrs, err := netLink.AddrList(link, unix.AF_INET)
	if err != nil {
		return errors.Wrap(err, "setupNICNetwork: failed to list IP address for NIC")
	}

	for _, addr := range addrs {
		klog.V(2).Infof("Deleting existing IP address %s", addr.String())
		if err = netLink.AddrDel(link, &addr); err != nil {
			return errors.Wrap(err, "setupNICNetwork: failed to delete IP addr from NIC")
		}
	}

	klog.V(2).Infof("Setting up NIC's default gateway %v", gw)
	routes := []netlink.Route{
		// Add a direct link route for the host's NIC IP only
		{
			LinkIndex: deviceNumber,
			Dst:       &net.IPNet{IP: gw, Mask: net.CIDRMask(32, 32)},
			Scope:     netlink.SCOPE_LINK,
			Table:     nicTable,
		},
		//TODO: find out which kernel bug will cause the problem below.
		//In centos 7.5 (kernel 3.10.0-862),  the following route will fail to add.
		// Route all other traffic via the host's NIC IP
		{
			LinkIndex: deviceNumber,
			Dst:       &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
			Scope:     netlink.SCOPE_UNIVERSE,
			Gw:        gw,
			Table:     nicTable,
		},
	}
	for _, r := range routes {
		err := netLink.RouteDel(&r)
		if err != nil && !netlinkwrapper.IsNotExistsError(err) {
			return errors.Wrap(err, "setupNICNetwork: failed to clean up old routes")
		}

		// In case of route dependency, retry few times
		retry := 0
		for {
			if err := netLink.RouteAdd(&r); err != nil {
				if netlinkwrapper.IsNetworkUnreachableError(err) {
					retry++
					if retry > maxRetryRouteAdd {
						klog.Errorf("Failed to add route %s/0 via %s table %d",
							r.Dst.IP.String(), gw.String(), nicTable)
						return errors.Wrapf(err, "setupNICNetwork: failed to add route %s/0 via %s table %d",
							r.Dst.IP.String(), gw.String(), nicTable)
					}
					klog.V(2).Infof("Not able to add route route %s/0 via %s table %d (attempt %d/%d)",
						r.Dst.IP.String(), gw.String(), nicTable, retry, maxRetryRouteAdd)
					time.Sleep(retryRouteAddInterval)
				} else if netlinkwrapper.IsRouteExistsError(err) {
					if err := netLink.RouteReplace(&r); err != nil {
						return errors.Wrapf(err, "setupNICNetwork: unable to replace route entry %s", r.Dst.IP.String())
					}
					klog.V(2).Infof("Successfully replaced route to be %s/0", r.Dst.IP.String())
					break
				} else {
					return errors.Wrapf(err, "setupNICNetwork: unable to add route %s/0 via %s table %d",
						r.Dst.IP.String(), gw.String(), nicTable)
				}
			} else {
				klog.V(2).Infof("Successfully added route route %s/0 via %s table %d", r.Dst.IP.String(), gw.String(), nicTable)
				break
			}
		}
	}

	// Remove the route that default out to NIC-x out of main route table
	_, cidr, err := net.ParseCIDR(nicSubnetCIDR)
	if err != nil {
		return errors.Wrapf(err, "setupNICNetwork: invalid IPv4 CIDR block %s", nicSubnetCIDR)
	}
	defaultRoute := netlink.Route{
		Dst:   cidr,
		Src:   net.ParseIP(nicIP),
		Table: mainRoutingTable,
		Scope: netlink.SCOPE_LINK,
	}

	if err := netLink.RouteDel(&defaultRoute); err != nil {
		if !netlinkwrapper.IsNotExistsError(err) {
			return errors.Wrapf(err, "setupNICNetwork: unable to delete default route %s for source IP %s", cidr.String(), nicIP)
		}
	}
	return nil
}

// incrementIPv4Addr returns incremented IPv4 address
func incrementIPv4Addr(ip net.IP) (net.IP, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("%q is not a valid IPv4 Address", ip)
	}
	intIP := binary.BigEndian.Uint32([]byte(ip4))
	if intIP == (1<<32 - 1) {
		return nil, fmt.Errorf("%q will be overflowed", ip)
	}
	intIP++
	bytes := make([]byte, 4)
	binary.BigEndian.PutUint32(bytes, intIP)
	return net.IP(bytes), nil
}

// GetRuleList returns IP rules
func (n *linuxNetwork) GetRuleList() ([]netlink.Rule, error) {
	return n.netLink.RuleList(unix.AF_INET)
}

// GetRuleListBySrc returns IP rules with matching source IP
func (n *linuxNetwork) GetRuleListBySrc(ruleList []netlink.Rule, src net.IPNet) ([]netlink.Rule, error) {
	var srcRuleList []netlink.Rule
	for _, rule := range ruleList {
		if rule.Src != nil && rule.Src.IP.Equal(src.IP) {
			srcRuleList = append(srcRuleList, rule)
		}
	}
	return srcRuleList, nil
}

// DeleteRuleListBySrc deletes IP rules that have a matching source IP
func (n *linuxNetwork) DeleteRuleListBySrc(src net.IPNet) error {
	klog.V(1).Infof("Delete Rule List By Src [%v]", src)

	ruleList, err := n.GetRuleList()
	if err != nil {
		klog.Errorf("DeleteRuleListBySrc: failed to get rule list %v", err)
		return err
	}

	srcRuleList, err := n.GetRuleListBySrc(ruleList, src)
	if err != nil {
		klog.Errorf("DeleteRuleListBySrc: failed to retrieve rule list %v", err)
		return err
	}

	klog.V(1).Infof("Remove current list [%v]", srcRuleList)
	for _, rule := range srcRuleList {
		if err := n.netLink.RuleDel(&rule); err != nil && !containsNoSuchRule(err) {
			klog.Errorf("Failed to cleanup old IP Rule: %v", err)
			return errors.Wrapf(err, "DeleteRuleListBySrc: failed to delete old rule")
		}

		var toDst string
		if rule.Dst != nil {
			toDst = rule.Dst.String()
		}
		klog.V(2).Infof("DeleteRuleListBySrc: Successfully removed current rule [%v] to %s", rule, toDst)
	}
	return nil
}

// UpdateRuleListBySrc modify IP rules that have a matching source IP
func (n *linuxNetwork) UpdateRuleListBySrc(ruleList []netlink.Rule, src net.IPNet, toCIDRs []string, toFlag bool, table int) error {
	klog.V(3).Infof("Update Rule List[%v] for source[%v] with toCIDRs[%v], toFlag[%v]", ruleList, src, toCIDRs, toFlag)

	srcRuleList, err := n.GetRuleListBySrc(ruleList, src)
	if err != nil {
		klog.Errorf("UpdateRuleListBySrc: failed to retrieve rule list %v", err)
		return err
	}

	klog.V(3).Infof("Remove current list [%v]", srcRuleList)
	var srcRuleTable int
	for _, rule := range srcRuleList {
		srcRuleTable = rule.Table
		if err := n.netLink.RuleDel(&rule); err != nil && !containsNoSuchRule(err) {
			klog.Errorf("Failed to cleanup old IP Rule: %v", err)
			return errors.Wrapf(err, "UpdateRuleListBySrc: failed to delete old rule")
		}
		var toDst string
		if rule.Dst != nil {
			toDst = rule.Dst.String()
		}
		klog.V(2).Infof("UpdateRuleListBySrc: Successfully removed current rule [%v] to %s", rule, toDst)
	}

	if len(srcRuleList) == 0 {
		klog.Warningln("UpdateRuleListBySrc: empty list, the rule is broken, try to rebuild")
		for _, cidr := range toCIDRs {
			podRule := n.netLink.NewRule()
			_, podRule.Dst, _ = net.ParseCIDR(cidr)
			podRule.Src = &src
			podRule.Table = table
			podRule.Priority = fromPodRulePriority

			err = n.netLink.RuleAdd(podRule)
			if IsRuleExistsError(err) {
				klog.Warningf("Rule already exists [%v]", podRule)
			} else {
				if err != nil {
					klog.Errorf("Failed to add pod IP rule [%v]: %v", podRule, err)
					return errors.Wrapf(err, "setupNS: failed to add pod rule [%v]", podRule)
				}
			}
		}
		klog.V(1).Infof("Rules for %s rebuilding done", src.String())
		return nil
	}
	if toFlag {
		for _, cidr := range toCIDRs {
			podRule := n.netLink.NewRule()
			_, podRule.Dst, _ = net.ParseCIDR(cidr)
			podRule.Src = &src
			podRule.Table = srcRuleTable
			podRule.Priority = fromPodRulePriority

			err = n.netLink.RuleAdd(podRule)
			if err != nil {
				klog.Errorf("Failed to add pod IP rule for external SNAT: %v", err)
				return errors.Wrapf(err, "UpdateRuleListBySrc: failed to add pod rule for CIDR %s", cidr)
			}
			var toDst string

			if podRule.Dst != nil {
				toDst = podRule.Dst.String()
			}
			klog.V(1).Infof("UpdateRuleListBySrc: Successfully added pod rule[%v] to %s", podRule, toDst)
		}
	} else {
		podRule := n.netLink.NewRule()

		podRule.Src = &src
		podRule.Table = srcRuleTable
		podRule.Priority = fromPodRulePriority

		err = n.netLink.RuleAdd(podRule)
		if err != nil {
			klog.Errorf("Failed to add pod IP Rule: %v", err)
			return errors.Wrapf(err, "UpdateRuleListBySrc: failed to add pod rule")
		}
		klog.V(1).Infof("UpdateRuleListBySrc: Successfully added pod rule[%v]", podRule)
	}
	return nil
}

// GetVPNNet return the ip from the vpn tunnel, which in most time is the x.x.255.254
func GetVPNNet(ip string) string {
	i := net.ParseIP(ip).To4()
	i[2] = 255
	i[3] = 254
	addr := &net.IPNet{
		IP:   i,
		Mask: net.IPv4Mask(255, 255, 255, 255),
	}
	return addr.String()
}

// NewFakeNetworkAPI is used by unit test
func NewFakeNetworkAPI(netlink netlinkwrapper.NetLink, iptableIface iptables.IptablesIface, findPrimaryName func(string) (string, error), setProcSys func(string, string) error) NetworkAPIs {
	return &linuxNetwork{
		useExternalSNAT:        useExternalSNAT(),
		typeOfSNAT:             typeOfSNAT(),
		nodePortSupportEnabled: nodePortSupportEnabled(),
		mainNICMark:            getConnmark(),
		netLink:                netlink,
		ns:                     &nswrapper.FakeNsWrapper{},
		newIptables: func() (iptables.IptablesIface, error) {
			return iptableIface, nil
		},
		findPrimaryInterfaceName: findPrimaryName,
		setProcSys:               setProcSys,
	}
}

// ContainsNoSuchRule report whether the rule is not exist
func ContainsNoSuchRule(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.ENOENT
	}
	return false
}

// IsRuleExistsError report whether the rule is exist
func IsRuleExistsError(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.EEXIST
	}
	return false
}
