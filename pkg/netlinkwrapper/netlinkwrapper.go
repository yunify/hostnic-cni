package netlinkwrapper

import (
	"github.com/vishvananda/netlink"
	neterror "github.com/yunify/hostnic-cni/pkg/errors"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
	"net"
	"syscall"
)

// NetLink wraps methods used from the vishvananda/netlink package
type NetLink interface {
	Init()
	Error() error
	// LinkByName gets a link object given the device name
	LinkByName(name string) (netlink.Link, error)
	//LinkByIndex gets a link object given the index
	LinkByIndex(index int) (netlink.Link, error)
	// LinkSetNsFd is equivalent to `ip link set $link netns $ns`
	LinkSetNsFd(link netlink.Link, fd int) error
	// ParseAddr parses an address string
	ParseAddr(s string) (*netlink.Addr, error)
	// AddrAdd is equivalent to `ip addr add $addr dev $link`
	AddrAdd(link netlink.Link, addr *netlink.Addr) error
	// AddrDel is equivalent to `ip addr del $addr dev $link`
	AddrDel(link netlink.Link, addr *netlink.Addr) error
	// AddrList is equivalent to `ip addr show `
	AddrList(link netlink.Link, family int) ([]netlink.Addr, error)
	// LinkAdd is equivalent to `ip link add`
	LinkAdd(link netlink.Link) error
	// LinkSetUp is equivalent to `ip link set $link up`
	LinkSetUp(link netlink.Link) error
	// LinkList is equivalent to: `ip link show`
	LinkList() ([]netlink.Link, error)
	// LinkSetDown is equivalent to: `ip link set $link down`
	LinkSetDown(link netlink.Link) error
	// RouteList gets a list of routes in the system.
	RouteList(link netlink.Link, family int) ([]netlink.Route, error)
	// RouteAdd will add a route to the route table
	RouteAdd(route *netlink.Route) error
	// RouteReplace will replace the route in the route table
	RouteReplace(route *netlink.Route) error
	// RouteDel is equivalent to `ip route del`
	RouteDel(route *netlink.Route) error
	// NeighAdd equivalent to: `ip neigh add ....`
	NeighAdd(neigh *netlink.Neigh) error
	// LinkDel equivalent to: `ip link del $link`
	LinkDel(link netlink.Link) error
	// NewRule creates a new empty rule
	NewRule() *netlink.Rule
	// RuleAdd is equivalent to: ip rule add
	RuleAdd(rule *netlink.Rule) error
	// RuleDel is equivalent to: ip rule del
	RuleDel(rule *netlink.Rule) error
	// RuleList is equivalent to: ip rule list
	RuleList(family int) ([]netlink.Rule, error)
	// LinkSetMTU is equivalent to `ip link set dev $link mtu $mtu`
	LinkSetMTU(link netlink.Link, mtu int) error
	LinkByMac(mac string) (netlink.Link, error)
	LinkDelByName(name string) error
	RuleListBySrc(src net.IPNet) ([]netlink.Rule, error)
	DeleteRuleBySrc(src net.IPNet) error
}

type netLink struct {
	//TODO: add ip version
	err 	error
}

// NewNetLink creates a new NetLink object
func NewNetLink() NetLink {
	return &netLink{}
}

func (n *netLink) Init() {
	n.err = nil
}

func (n *netLink) Error() error{
	return n.err
}

// GetRuleListBySrc returns IP rules with matching source IP
func (n *netLink) RuleListBySrc(src net.IPNet) ([]netlink.Rule, error) {
	ruleList, _ := n.RuleList(unix.AF_INET)

	if n.err != nil {
		return nil, nil
	}

	var srcRuleList []netlink.Rule
	for _, rule := range ruleList {
		if rule.Src != nil && rule.Src.IP.Equal(src.IP) {
			srcRuleList = append(srcRuleList, rule)
		}
	}
	return srcRuleList, nil
}

func (n *netLink) DeleteRuleBySrc(src net.IPNet) error {
	rules, _ := n.RuleListBySrc(src)

	if n.err != nil {
		return nil
	}

	for _, rule := range rules {
		n.RuleDel(&rule)
	}

	return n.err
}

func (n *netLink) LinkDelByName(name string) error {
	old, _ := n.LinkByName(name)

	if n.err != nil {
		return n.err
	}

	n.LinkDel(old)

	return n.err

}

// LinkByMac returns linux netlink based on interface MAC
func (n *netLink) LinkByMac(mac string) (netlink.Link, error) {
	links, _ := n.LinkList()

	if n.err != nil {
		return nil, n.err
	}

	for _, link := range links {
		if mac == link.Attrs().HardwareAddr.String() {
			return link, nil
		}
	}

	return nil, nil
}

func (n *netLink) LinkAdd(link netlink.Link) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.LinkAdd(link)
	return n.err
}

func (n *netLink) LinkByName(name string) (netlink.Link, error) {
	if n.err != nil {
		return nil, nil
	}
	link, err  := netlink.LinkByName(name)
	if err != nil {
		klog.Errorf("Cannot index link by name %s, err: %s", name, err)
	}
	n.err = err
	return link, err
}

func (n *netLink) LinkByIndex(index int) (netlink.Link, error) {
	if n.err != nil {
		return nil, nil
	}
	link, err  := netlink.LinkByIndex(index)
	n.err = err
	return link, err
}

func (n *netLink) LinkSetNsFd(link netlink.Link, fd int) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.LinkSetNsFd(link, fd)
	return n.err
}

func (n *netLink) ParseAddr(s string) (*netlink.Addr, error) {
	if n.err != nil {
		return nil, nil
	}
	addr, err := netlink.ParseAddr(s)
	n.err = err
	return addr, err
}

func (n *netLink) AddrAdd(link netlink.Link, addr *netlink.Addr) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.AddrAdd(link, addr)
	return n.err
}

func (n *netLink) AddrDel(link netlink.Link, addr *netlink.Addr) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.AddrDel(link, addr)
	return n.err
}

func (n *netLink) LinkSetUp(link netlink.Link) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.LinkSetUp(link)
	return n.err
}

func (n *netLink) LinkList() ([]netlink.Link, error) {
	if n.err != nil {
		return nil, nil
	}
	link, err  := netlink.LinkList()
	n.err = err
	return link, err
}

func (n *netLink) LinkSetDown(link netlink.Link) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.LinkSetDown(link)
	return n.err
}

func (n *netLink) RouteList(link netlink.Link, family int) ([]netlink.Route, error) {
	if n.err != nil {
		return nil, nil
	}
	route, err  := netlink.RouteList(link, family)
	n.err = err
	return route, err
}

func (n *netLink) RouteAdd(route *netlink.Route) error {
	if n.err != nil {
		return nil
	}
	err := netlink.RouteAdd(route)
	if IsRouteExistsError(err) {
		n.err = nil
	}
	return n.err
}

func (n *netLink) RouteReplace(route *netlink.Route) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.RouteReplace(route)
	return n.err
}

func (n *netLink) RouteDel(route *netlink.Route) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.RouteDel(route)
	return n.err
}

func (n *netLink) AddrList(link netlink.Link, family int) ([]netlink.Addr, error) {
	if n.err != nil {
		return nil, nil
	}
	addr, err  := netlink.AddrList(link, family)
	n.err =err
	return addr, err
}

func (n *netLink) NeighAdd(neigh *netlink.Neigh) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.NeighAdd(neigh)
	return n.err
}

func (n *netLink) LinkDel(link netlink.Link) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.LinkDel(link)
	return n.err
}

func (*netLink) NewRule() *netlink.Rule {
	return netlink.NewRule()
}

func (n *netLink) RuleAdd(rule *netlink.Rule) error {
	if n.err != nil {
		return nil
	}
	if n.err = netlink.RuleAdd(rule); n.err != nil {
		klog.Errorf("Add rule [%+v]", rule)
	}

	return n.err
}

func (n *netLink) RuleDel(rule *netlink.Rule) error {
	if n.err != nil {
		return nil
	}
	err := netlink.RuleDel(rule)
	if err != nil && !neterror.ContainsNoSuchRule(err) {
		klog.Errorf("Failed delete rule: err: %s , [%v]", rule, err)
		n.err = err
	}
	return n.err
}

func (n *netLink) RuleList(family int) ([]netlink.Rule, error) {
	if n.err != nil {
		return nil, nil
	}
	rules, err  := netlink.RuleList(family)
	n.err = err
	return rules, err
}

func (n *netLink) LinkSetMTU(link netlink.Link, mtu int) error {
	if n.err != nil {
		return nil
	}
	n.err = netlink.LinkSetMTU(link, mtu)
	return n.err
}

// IsNotExistsError returns true if the error type is syscall.ESRCH
// This helps us determine if we should ignore this error as the route
// that we want to cleanup has been deleted already routing table
func IsNotExistsError(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.ESRCH
	}
	return false
}

// IsRouteExistsError returns true if the error type is syscall.EEXIST
// This helps us determine if we should ignore this error as the route
// we want to add has been added already in routing table
func IsRouteExistsError(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.EEXIST
	}
	return false
}

// IsNetworkUnreachableError returns true if the error type is syscall.ENETUNREACH
// This helps us determine if we should ignore this error as the route the call
// depends on is not plumbed ready yet
func IsNetworkUnreachableError(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.ENETUNREACH
	}
	return false
}
