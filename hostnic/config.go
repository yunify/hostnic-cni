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

package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/containernetworking/cni/pkg/types"
)

//NetConf nic plugin configuration
type NetConf struct {
	types.NetConf
	DataDir  string                 `json:"dataDir"`
	Provider string                 `json:"provider"`
	Args     map[string]interface{} `json:"args"`
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	netconf := &NetConf{DataDir: defaultDataDir}
	if err := json.Unmarshal(bytes, netconf); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	if netconf.DataDir == "" {
		return nil, errors.New("Data dir is empty")
	}
	if netconf.Provider == "" {
		return nil, errors.New("Provider name is empty")
	}
	return netconf, nil
}
