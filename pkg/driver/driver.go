package driver

import (
	"net"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/netlinkwrapper"
	"github.com/yunify/hostnic-cni/pkg/nswrapper"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
)

const (
	// ip rules priority and leave 512 gap for future
	toContainerRulePriority = 512
	// 1024 is reserved for (ip rule not to <vpc's subnet> table main)
	fromContainerRulePriority = 1536

	// main routing table number
	mainRouteTable = unix.RT_TABLE_MAIN
	// MTU of veth - ENI MTU defined in pkg/networkutils/network.go
	ethernetMTU = 9001
)

// NetworkAPIs defines network API calls
type NetworkAPIs interface {
	SetupNS(netnsPath string, addr *net.IPNet, table int, useExternalSnat bool) error
	TeardownNS(addr *net.IPNet, table int) error
	run(hostNS ns.NetNS) error
	Setup(hostVethName string, contVethName string, addr *net.IPNet)
}

type linuxNetwork struct {
	netLink      netlinkwrapper.NetLink
	ns           nswrapper.NS
	contVethName string
	hostVethName string
	addr         *net.IPNet
}

func newDriverNetworkAPI(netLink netlinkwrapper.NetLink, ns nswrapper.NS) NetworkAPIs {
	return &linuxNetwork{
		netLink: netLink,
		ns:      ns,
	}
}

// New creates linuxNetwork object
func New() NetworkAPIs {
	return newDriverNetworkAPI(netlinkwrapper.NewNetLink(), nswrapper.NewNS())
}

func (os *linuxNetwork) Setup(hostVethName string, contVethName string, addr *net.IPNet) {
	os.contVethName = contVethName
	os.hostVethName = hostVethName
	os.addr = addr
}

// run defines the closure to execute within the container's namespace to
// create the veth pair
func (os *linuxNetwork) run(hostNS ns.NetNS) error {
	netLink := os.netLink
	netLink.Init()

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name:   os.contVethName,
			Flags:  net.FlagUp,
			MTU:    ethernetMTU,
			TxQLen: -1,
		},
		PeerName: os.hostVethName,
	}

	netLink.LinkAdd(veth)

	hostVeth, err := netLink.LinkByName(os.hostVethName)
	if err!= nil {
		return err
	}

	// Explicitly set the veth to UP state, because netlink doesn't always do that on all the platforms with net.FlagUp.
	// veth won't get a link local address unless it's set to UP state.
	netLink.LinkSetUp(hostVeth)

	contVeth, err := netLink.LinkByName(os.contVethName)
	if err != nil {
		return err
	}

	// Explicitly set the veth to UP state, because netlink doesn't always do that on all the platforms with net.FlagUp.
	// veth won't get a link local address unless it's set to UP state.
	netLink.LinkSetUp(contVeth)

	netLink.AddrAdd(contVeth, &netlink.Addr{IPNet: os.addr})

	// Add a connected route to a dummy next hop (169.254.1.1)
	// # ip route show
	// default via 169.254.1.1 dev eth0
	// 169.254.1.1 dev eth0
	gw := net.IPv4(169, 254, 1, 1)
	gwNet := &net.IPNet{IP: gw, Mask: net.CIDRMask(32, 32)}

	// add static ARP entry for default gateway
	// we are using routed mode on the host and container need this static ARP entry to resolve its default gateway.
	neigh := &netlink.Neigh{
		LinkIndex:    contVeth.Attrs().Index,
		State:        netlink.NUD_PERMANENT,
		IP:           gwNet.IP,
		HardwareAddr: hostVeth.Attrs().HardwareAddr,
	}

	netLink.NeighAdd(neigh)

	//TODO: 判断是否存在路由
	netLink.RouteAdd(&netlink.Route{
		LinkIndex: contVeth.Attrs().Index,
		Scope:     netlink.SCOPE_LINK,
		Dst:       gwNet})

	// Add a default route via dummy next hop(169.254.1.1). Then all outgoing traffic will be routed by this
	// default route via dummy next hop (169.254.1.1).
	netLink.RouteAdd(&netlink.Route{
		LinkIndex: contVeth.Attrs().Index,
		Dst:       &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
		Scope:     netlink.SCOPE_UNIVERSE,
		Gw:        gw})

	// Now that the everything has been successfully set up in the container, move the "host" end of the
	// veth into the host namespace.
	netLink.LinkSetNsFd(hostVeth, int(hostNS.Fd()))

	return netLink.Error()
}

// SetupNS wires up linux networking for a pod's network
func (os *linuxNetwork) SetupNS(netnsPath string, addr *net.IPNet, table int, useExternalSnat bool) error {
	netLink := os.netLink
	netLink.Init()

	if err := os.ns.WithNetNSPath(netnsPath, os.run); err != nil {
		klog.Errorf("Failed to setup NS network %v", err)
		return errors.Wrap(err, "setupNS network: failed to setup NS network")
	}

	hostVeth, err := netLink.LinkByName(os.hostVethName)
	if err != nil {
		return err
	}

	// Explicitly set the veth to UP state, because netlink doesn't always do that on all the platforms with net.FlagUp.
	// veth won't get a link local address unless it's set to UP state.
	netLink.LinkSetUp(hostVeth)

	addrHostAddr := &net.IPNet{
		IP:   addr.IP,
		Mask: net.CIDRMask(32, 32)}

	netLink.RouteAdd(&netlink.Route{
		LinkIndex: hostVeth.Attrs().Index,
		Scope:     netlink.SCOPE_LINK,
		Dst:       addrHostAddr})

	rule := netLink.NewRule()
	rule.Table = table
	rule.Src = addrHostAddr
	if !useExternalSnat {
		//TODO: 0x81 config
		rule.Mark = int(0x81)
		rule.Mask = int(0x81)
	}
	netLink.RuleAdd(rule)

	return netLink.Error()
}

// TeardownPodNetwork cleanup ip rules
func (os *linuxNetwork) TeardownNS(addr *net.IPNet, table int) error {
	klog.V(2).Infof("TeardownNS: addr %s, table %d", addr.String(), table)

	netLink := os.netLink
	netLink.Init()

	netLink.DeleteRuleBySrc(*addr)

	addrHostAddr := &net.IPNet{
		IP:   addr.IP,
		Mask: net.CIDRMask(32, 32)}

	// cleanup host route:
	netLink.RouteDel(&netlink.Route{
		Scope: netlink.SCOPE_LINK,
		Dst:   addrHostAddr})

	return netLink.Error()
}
