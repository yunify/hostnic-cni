package server

import "github.com/yunify/hostnic-cni/pkg"

type NicProvider interface {
	GenerateNic() (*pkg.HostNic, error)
	ValidateNic(nicid string) bool
	ReclaimNic([]*string) error
	GetNicsInfo([]*string) ([]*pkg.HostNic, error)
}
