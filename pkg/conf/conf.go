package conf

import (
	"fmt"
	"github.com/spf13/viper"
	"github.com/yunify/hostnic-cni/pkg/constants"
)

type IpamConf struct {
	Pool   PoolConf   `json:"pool,omitempty" yaml:"pool,omitempty"`
	Server ServerConf `json:"server,omitempty" yaml:"server,omitempty"`
}

type PoolConf struct {
	//per vxnet
	PoolHigh int `json:"poolHigh,omitempty" yaml:"poolHigh,omitempty"`
	PoolLow  int `json:"poolLow,omitempty" yaml:"poolLow,omitempty"`

	//global
	MaxNic         int      `json:"maxNic,omitempty" yaml:"maxNic,omitempty"`
	Sync           int      `json:"sync,omitempty" yaml:"sync,omitempty"`
	NodeSync       int      `json:"nodeSync,omitempty" yaml:"nodeSync,omitempty"`
	RouteTableBase int      `json:"routeTableBase,omitempty" yaml:"routeTableBase,omitempty"`
	Tag            string   `json:"tag,omitempty" yaml:"tag,omitempty"`
	VxNets         []string `json:"vxNets,omitempty" yaml:"vxNets,omitempty"`
}

type ServerConf struct {
	ServerPath string `json:"serverPath,omitempty" yaml:"serverPath,omitempty"`
}

// TryLoadFromDisk loads configuration from default location after server startup
// return nil error if configuration file not exists
func TryLoadFromDisk(name, path string) (*IpamConf, error) {
	viper.SetConfigName(name)
	viper.AddConfigPath(".")
	viper.AddConfigPath(path)

	conf := &IpamConf{
		Pool: PoolConf{
			PoolHigh:       constants.DefaultHighPoolSize,
			PoolLow:        constants.DefaultLowPoolSize,
			MaxNic:         constants.NicNumLimit,
			Sync:           constants.DefaultJobSyn,
			RouteTableBase: constants.DefaultRouteTableBase,
			NodeSync:       constants.DefaultNodeSync,
		},
		Server: ServerConf{
			ServerPath: constants.DefaultSocketPath,
		},
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return conf, nil
		}

		return nil, fmt.Errorf("failed to parsing config file %s/%s : %v", path, name, err)
	}

	if err := viper.Unmarshal(conf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file %s/%s : %v", path, name, err)
	}

	if err := validateConf(conf); err != nil {
		return nil, fmt.Errorf("config content invalid: %v", err)
	}

	return conf, nil
}

func validateConf(conf *IpamConf) error {
	if conf.Pool.PoolLow > conf.Pool.PoolHigh {
		return fmt.Errorf("PoolLow should less than PoolHigh")
	}

	if conf.Pool.MaxNic > constants.NicNumLimit {
		return fmt.Errorf("MaxNic should less than 63")
	}

	return nil
}
