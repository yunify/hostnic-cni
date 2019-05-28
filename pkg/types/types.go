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

package types

import (
	"net"
)

type HostNic struct {
	ID           string `json:"id"`
	VxNet        *VxNet `json:"vxNet"`
	HardwareAddr string `json:"hardwareAddr"`
	Address      string `json:"address"`
	DeviceNumber int32  `json:"deviceNumber"`
	IsPrimary    bool   `json:"IsPrimary"`
}

type VxNet struct {
	ID string `json:"id"`
	//GateWay eg: 192.168.1.1
	GateWay string `json:"gateWay"`
	//Network eg: 192.168.1.0/24
	Network *net.IPNet `json:"network"`
	//RouterId
	RouterID string `json:"router_id"`
	Name     string `json:"name"`
}

type HostInstance struct {
	ID        string `json:"id"`
	RouterID  string `json:"router_id"`
	ClusterID string `json:"cluster_id"`
}

type VPC struct {
	Network *net.IPNet
	ID      string
	VxNets  []string
}
