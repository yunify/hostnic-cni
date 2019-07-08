package ipwrapper

import (
	"net"

	"github.com/vishvananda/netlink"
)

// FakeIPWrapper is using for ut
type FakeIPWrapper struct {
	data map[string]net.IP
}

// AddDefaultRoute add a default route on host
func (f *FakeIPWrapper) AddDefaultRoute(gw net.IP, dev netlink.Link) error {
	if _, ok := f.data[dev.Attrs().HardwareAddr.String()]; !ok {
		f.data[dev.Attrs().HardwareAddr.String()] = gw
	}
	return nil
}

// NewFakeIPWrapper create a fake IPWrapper
func NewFakeIPWrapper() *FakeIPWrapper {
	return &FakeIPWrapper{
		data: make(map[string]net.IP),
	}
}
