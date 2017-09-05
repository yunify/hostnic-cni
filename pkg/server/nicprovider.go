package server

import "github.com/yunify/hostnic-cni/pkg"

//NicProvider nic resource provider
type NicProvider interface {
	GenerateNic() (*pkg.HostNic, error)
	ValidateNic(nicid string) bool
	ReclaimNic([]*string) error
	GetNicsInfo([]*string) ([]*pkg.HostNic, error)
}
