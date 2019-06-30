package nswrapper

import (
	"github.com/containernetworking/plugins/pkg/ns"
)

type FakeNsWrapper struct {
}

func (FakeNsWrapper) WithNetNSPath(nspath string, toRun func(ns.NetNS) error) error {
	ns, err := ns.GetCurrentNS()
	if err != nil {
		return err
	}
	return toRun(ns)
}
