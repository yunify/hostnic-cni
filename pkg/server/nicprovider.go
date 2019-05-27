package server

import "github.com/yunify/hostnic-cni/pkg/types"

//NicProvider nic resource provider
type NicProvider interface {
	GenerateNic() (*types.HostNic, error)
	ValidateNic(nicid string) bool
	ReclaimNic([]string) error
	GetNicsInfo([]string) ([]*types.HostNic, error)
	DisableNic(nicid string) error
}
