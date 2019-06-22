package ipwrapper

import (
	"net"

	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/vishvananda/netlink"
)

type IP interface {
	AddDefaultRoute(gw net.IP, dev netlink.Link) error
}

type ipRoute struct {
}

func NewIP() IP {
	return &ipRoute{}
}

func (*ipRoute) AddDefaultRoute(gw net.IP, dev netlink.Link) error {
	return ip.AddDefaultRoute(gw, dev)
}
