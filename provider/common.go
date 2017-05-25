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

package provider

import (
	"github.com/yunify/hostnic-cni/pkg"
)

//NicProvider network interface provider which attach new nic for container
type NicProvider interface {
	CreateNic() (*pkg.HostNic, error)
	CreateNicInVxnet(vxnet string) (*pkg.HostNic, error)
	DeleteNic(nicID string) error
	GetNics(vxnet *string) ([]*pkg.HostNic, error)
	//GetVxNet(vxNet string) (*pkg.VxNet, error)
}

//Initializer initialization function of provider
type Initializer func(config map[string]interface{}) (NicProvider, error)
