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

package constants

import (
	"errors"
	"fmt"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

const (
	DefaultSocketPath     = "/var/run/hostnic/hostnic.socket"
	DefaultUnixSocketPath = "unix://" + DefaultSocketPath
	DefaultConfigPath     = "/etc/hostnic"
	DefaultConfigName     = "hostnic"

	DefaultJobSyn   = 3
	DefaultNodeSync = 1 * 60

	DefaultLowPoolSize  = 3
	DefaultHighPoolSize = 5

	NicNumLimit           = 63
	VxnetNicNumLimit      = 252
	DefaultRouteTableBase = 260

	NicPrefix = "hostnic_"

	HostNicPassThrough = "passthrough"
	HostNicVeth        = "veth"

	HostNicPrefix = "vnic"

	DefaultNatMark        = "0x10000"
	DefaultPrimaryNic     = "eth0"
	MainTable             = 254
	ManglePreroutingChain = "HOSTNIC-PREROUTING"
	MangleOutputChain     = "HOSTNIC-OUTPUT"
	MangleForward         = "HOSTNIC-FORWARD"

	ToContainerRulePriority   = 1535
	FromContainerRulePriority = 1536
)

func GetHostNicName(routeTableNum int) string {
	return fmt.Sprintf("%s%d", NicPrefix, routeTableNum)
}

func PodInfoKey(info *rpc.PodInfo) string {
	return fmt.Sprintf("%s", info.Containter)
}

type ResourceType string

const (
	ResourceTypeInstance ResourceType = "instance"
	ResourceTypeVxnet    ResourceType = "vxnet"
	ResourceTypeNic      ResourceType = "nic"
)

type NetConf struct {
	CNIVersion   string          `json:"cniVersion,omitempty"`
	Name         string          `json:"name,omitempty"`
	Type         string          `json:"type,omitempty"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	IPAM         struct {
		Name string `json:"name,omitempty"`
		Type string `json:"type,omitempty"`
	} `json:"server,omitempty"`
	HostVethPrefix string `json:"vethPrefix,omitempty"`
	HostNicType    string `json:"hostNicType,omitempty"`
	MTU            int    `json:"mtu,omitempty"`
	Service        string `json:"serviceCIDR,omitempty"`
	// Route table to pod
	RT2Pod    int    `json:"rt2Pod,omitempty"`
	Interface string `json:"interface,omitempty"`
	Hairpin   bool   `json:"hairpin,omitempty"`
	// 0x8000 for kube-proxy filter
	// 0x4000 for kube-proxy nat
	// 0xff000000 for calico
	NatMark  string `json:"natMark,omitempty"`
	LogLevel int    `json:"logLevel,omitempty"`
	LogFile  string `json:"logFile,omitempty"`
}

// K8sArgs is the valid CNI_ARGS used for Kubernetes
type K8sArgs struct {
	types.CommonArgs
	// K8S_POD_NAME is pod's name
	K8S_POD_NAME types.UnmarshallableString
	// K8S_POD_NAMESPACE is pod's namespace
	K8S_POD_NAMESPACE types.UnmarshallableString
	// K8S_POD_INFRA_CONTAINER_ID is pod's container id
	K8S_POD_INFRA_CONTAINER_ID types.UnmarshallableString
}

var (
	ErrNoAvailableNIC = errors.New("no free nic")
	ErrNicNotFound    = errors.New("hostnic not found")
)
