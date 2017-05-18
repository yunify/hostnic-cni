//
// =========================================================================
// Copyright (C) 2017 by Yunify, Inc...
// -------------------------------------------------------------------------
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this work except in compliance with the License.
// You may obtain a copy of the License in the LICENSE file, or at:
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// =========================================================================
//

package qingcloud

import (
	"errors"

	"github.com/mitchellh/mapstructure"
)

//Config QingCloud nic provider configuration
type Config struct {
	ProviderConfigFile string   `json:"providerConfigFile"`
	VxNets             []string `json:"vxNets"`
}

//DecodeConfiguration decode configuration from map
func DecodeConfiguration(config map[string]interface{}) (*Config, error) {
	var qingconfig Config
	err := mapstructure.Decode(config, &qingconfig)
	if err != nil {
		return nil, err
	}
	if len(qingconfig.VxNets) == 0 {
		return nil, errors.New("vxNets list is emtpy")
	}
	if qingconfig.ProviderConfigFile == "" {
		return nil, errors.New("qingcloud sdk config file path is emtpy")
	}
	return &qingconfig, nil
}
