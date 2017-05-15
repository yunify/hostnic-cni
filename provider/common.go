package provider

import (
	"github.com/yunify/hostnic-cni/pkg"
)

//NicProvider network interface provider which attach new nic for container
type NicProvider interface {
	CreateNic() (*pkg.HostNic, error)
	DeleteNic(nicID string) error
	//GetVxNet(vxNet string) (*pkg.VxNet, error)
}

//Initializer initialization function of provider
type Initializer func(config map[string]interface{}) (NicProvider, error)
