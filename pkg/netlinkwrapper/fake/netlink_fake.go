package fake

import (
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/vishvananda/netlink"
)

func init() {
	rand.Seed(time.Now().Unix())
}

type FakeNetlink struct {
	Links    map[string]netlink.Link
	LinkAddr map[string]map[string]*netlink.Addr
	Routes   map[string]netlink.Route
	Rules    map[string]netlink.Rule
}

// LinkByName gets a link object given the device name
func (f *FakeNetlink) LinkByName(name string) (netlink.Link, error) {
	if l, ok := f.Links[name]; ok {
		return l, nil
	}
	return nil, fmt.Errorf("Netlink %s not found", name)
}

// LinkSetNsFd is equivalent to `ip link set $link netns $ns`
func (f *FakeNetlink) LinkSetNsFd(link netlink.Link, fd int) error {
	f.Links[link.Attrs().Name].Attrs().NetNsID = fd
	return nil
}

// ParseAddr parses an address string
func (f *FakeNetlink) ParseAddr(s string) (*netlink.Addr, error) {
	return netlink.ParseAddr(s)
}

// AddrAdd is equivalent to `ip addr add $addr dev $link`
func (f *FakeNetlink) AddrAdd(link netlink.Link, addr *netlink.Addr) error {
	if addrs, ok := f.LinkAddr[link.Attrs().Name]; ok {
		addrs[addr.String()] = addr
	} else {
		f.LinkAddr[link.Attrs().Name] = make(map[string]*netlink.Addr)
		f.LinkAddr[link.Attrs().Name][addr.String()] = addr
	}
	return nil
}

// AddrDel is equivalent to `ip addr del $addr dev $link`
func (f *FakeNetlink) AddrDel(link netlink.Link, addr *netlink.Addr) error {
	if addrs, ok := f.LinkAddr[link.Attrs().Name]; ok {
		delete(addrs, addr.String())
	}
	return nil
}

// AddrList is equivalent to `ip addr show `
func (f *FakeNetlink) AddrList(link netlink.Link, family int) ([]netlink.Addr, error) {
	result := make([]netlink.Addr, 0)
	for _, addr := range f.LinkAddr[link.Attrs().Name] {
		result = append(result, *addr)
	}
	return result, nil
}

// LinkAdd is equivalent to `ip link add`
func (f *FakeNetlink) LinkAdd(link netlink.Link) error {
	f.Links[link.Attrs().Name] = link
	if veth, ok := link.(*netlink.Veth); ok {
		peer := &netlink.Veth{
			LinkAttrs: netlink.NewLinkAttrs(),
			PeerName:  veth.Name,
		}
		peer.Name = veth.PeerName
		f.Links[veth.PeerName] = peer
	}
	return nil
}

// LinkSetUp is equivalent to `ip link set $link up`
func (f *FakeNetlink) LinkSetUp(link netlink.Link) error {
	f.Links[link.Attrs().Name].Attrs().Flags = f.Links[link.Attrs().Name].Attrs().Flags | net.FlagUp
	return nil
}

// LinkList is equivalent to: `ip link show`
func (f *FakeNetlink) LinkList() ([]netlink.Link, error) {
	result := make([]netlink.Link, 0)
	for _, v := range f.Links {
		result = append(result, v)
	}
	return result, nil
}

// LinkSetDown is equivalent to: `ip link set $link down`
func (f *FakeNetlink) LinkSetDown(link netlink.Link) error {
	f.Links[link.Attrs().Name].Attrs().Flags = f.Links[link.Attrs().Name].Attrs().Flags & (^net.FlagUp)
	return nil
}

// RouteList gets a list of routes in the system.
func (f *FakeNetlink) RouteList(link netlink.Link, family int) ([]netlink.Route, error) {
	result := make([]netlink.Route, 0)
	for _, v := range f.Routes {
		result = append(result, v)
	}
	return result, nil
}

// RouteAdd will add a route to the route table
func (f *FakeNetlink) RouteAdd(route *netlink.Route) error {
	f.Routes[keyForRoute(route)] = *route
	return nil
}

func (f *FakeNetlink) AddDefaultRoute(gw net.IP, dev netlink.Link) error {
	route := &netlink.Route{
		Gw:        gw,
		LinkIndex: dev.Attrs().Index,
	}
	return f.RouteAdd(route)
}

func keyForRoute(route *netlink.Route) string {
	if route.Dst != nil {
		return route.Src.String() + "+" + route.Dst.String()
	}
	return route.Src.String() + "+default"
}

// RouteReplace will replace the route in the route table
func (f *FakeNetlink) RouteReplace(route *netlink.Route) error {
	f.Routes[keyForRoute(route)] = *route
	return nil
}

// RouteDel is equivalent to `ip route del`
func (f *FakeNetlink) RouteDel(route *netlink.Route) error {
	delete(f.Routes, keyForRoute(route))
	return nil
}

// NeighAdd equivalent to: `ip neigh add ....`
func (f *FakeNetlink) NeighAdd(neigh *netlink.Neigh) error {
	return nil
}

// LinkDel equivalent to: `ip link del $link`
func (f *FakeNetlink) LinkDel(link netlink.Link) error {
	delete(f.Links, link.Attrs().Name)
	return nil
}

// NewRule creates a new empty rule
func (f *FakeNetlink) NewRule() *netlink.Rule {
	return netlink.NewRule()
}

// RuleAdd is equivalent to: ip rule add
func (f *FakeNetlink) RuleAdd(rule *netlink.Rule) error {
	f.Rules[KeyForRule(rule)] = *rule
	return nil
}

// RuleDel is equivalent to: ip rule del
func (f *FakeNetlink) RuleDel(rule *netlink.Rule) error {
	key := KeyForRule(rule)
	if _, ok := f.Rules[key]; ok {
		delete(f.Rules, key)
	}
	return nil
}

// RuleList is equivalent to: ip rule list
func (f *FakeNetlink) RuleList(family int) ([]netlink.Rule, error) {
	result := make([]netlink.Rule, 0)
	for _, v := range f.Rules {
		result = append(result, v)
	}
	return result, nil
}

// LinkSetMTU is equivalent to `ip link set dev $link mtu $mtu`
func (f *FakeNetlink) LinkSetMTU(link netlink.Link, mtu int) error {
	f.Links[link.Attrs().Name].Attrs().MTU = mtu
	return nil
}

func (f *FakeNetlink) FindPrimaryInterfaceName(mac string) (string, error) {
	if rand.Int()%2 == 0 {
		return "", fmt.Errorf("Random Error")
	}
	for _, link := range f.Links {
		if string(link.Attrs().HardwareAddr) == mac {
			return link.Attrs().Name, nil
		}
	}
	return "", fmt.Errorf("PrimaryInterfaceName not found")
}

func NewFakeNetlink() *FakeNetlink {
	f := &FakeNetlink{
		Routes:   make(map[string]netlink.Route),
		Rules:    make(map[string]netlink.Rule),
		Links:    make(map[string]netlink.Link),
		LinkAddr: make(map[string]map[string]*netlink.Addr),
	}

	lo := &netlink.Device{
		LinkAttrs: netlink.NewLinkAttrs(),
	}
	lo.Name = "lo"
	f.LinkAdd(lo)
	return f
}

func KeyForRule(rule *netlink.Rule) string {
	var left, right string
	if rule.Src != nil {
		left = rule.Src.String()
	} else {
		left = "no-src"
	}
	if rule.Dst != nil {
		right = rule.Dst.String()
	} else {
		right = "no-dst"
	}
	return left + "+" + right
}
