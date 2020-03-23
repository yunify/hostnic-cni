package nswrapper

import (
	"github.com/containernetworking/plugins/pkg/ns"
)

type NS interface {
	WithNetNSPath(nspath string, toRun func(ns.NetNS) error) error
}

type nsType struct {
}

func NewNS() NS {
	return &nsType{}
}

func (*nsType) WithNetNSPath(nspath string, toRun func(ns.NetNS) error) error {
	return ns.WithNetNSPath(nspath, toRun)
}
