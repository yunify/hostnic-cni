package networkutils

import (
	"github.com/yunify/hostnic-cni/pkg/networkutils/ipset"
	"k8s.io/klog"
	"k8s.io/utils/exec"
)

type ipsetHandler struct {
	Handler ipset.Interface
	Ipset ipset.IPSet
}

const (
	ipsetName string = "hostnic-ippools"
)

func InitIpset() {
	klog.Infof("Init ipset %s", ipsetName)
	IpsetHandler.Handler = ipset.New(exec.New())
	IpsetHandler.Ipset = ipset.IPSet{
		Name:       ipsetName,
		SetType:    ipset.HashNet,
		Comment:    ipsetName,
	}
	if err := IpsetHandler.Handler.CreateSet(&IpsetHandler.Ipset, true); err != nil {
		klog.Infof("Failed to create ipset %s: %s", ipsetName, err)
	}
}

var IpsetHandler ipsetHandler