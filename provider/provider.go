package provider

import (
	"errors"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/provider/qingcloud"
	"strings"
)

type NicProvider interface {
	CreateNic() (*pkg.HostNic, error)
	DeleteNic(nicID string) error
	//GetVxNet(vxNet string) (*pkg.VxNet, error)
}

func CreateNicProvider(name string, configFile string, vxNets []string) (NicProvider, error) {
	name = strings.ToLower(name)
	switch name {
	case "qingcloud":
		return qingcloud.NewQCNicProvider(configFile, vxNets)
	default:
		return nil, errors.New("Unsupported nic provider type")
	}
}
