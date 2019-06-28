package ipwrapper

import (
	"net"

	"github.com/vishvananda/netlink"
)

type FakeIPWrapper struct {
	data map[string]net.IP
}

func (f *FakeIPWrapper) AddDefaultRoute(gw net.IP, dev netlink.Link) error {
	if _, ok := f.data[dev.Attrs().HardwareAddr.String()]; !ok {
		f.data[dev.Attrs().HardwareAddr.String()] = gw
	}
	return nil
}

func NewFakeIPWrapper() *FakeIPWrapper {
	return &FakeIPWrapper{
		data: make(map[string]net.IP),
	}
}
