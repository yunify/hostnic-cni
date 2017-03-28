package pkg

import "net"

type HostNic struct {
	ID string
	VxNet *VxNet
	HardwareAddr string
	Address      string
}

type VxNet struct {
	ID	string
	GateWay net.IP
	Network net.IPNet
}