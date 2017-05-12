package qingcloud

import (
	"errors"

	"github.com/mitchellh/mapstructure"
)

//Config QingCloud nic provider configuration
type Config struct {
	VxNets            []string `json:"vxNets"`
	IsGateway         bool     `json:"isGateway"`
	QyAccessKeyID     string   `json:"QyAccessKeyID"`
	QySecretAccessKey string   `json:"QySecretAccessKey"`
	Zone              string   `json:"zone"`
}

//DecodeConfiguration decode configuration from map
func DecodeConfiguration(config map[string]interface{}) (*Config, error) {
	var qingconfig Config
	error := mapstructure.Decode(config, &qingconfig)
	if error != nil {
		return nil, error
	}
	if len(qingconfig.VxNets) == 0 {
		return nil, errors.New("vxNets list is emtpy")
	}
	if qingconfig.QyAccessKeyID == "" || qingconfig.QySecretAccessKey == "" {
		return nil, errors.New("QingCloud Access key pair is missing")
	}
	if qingconfig.Zone == "" {
		return nil, errors.New("Zone is empty")
	}
	return &qingconfig, nil
}
