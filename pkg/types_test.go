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

package pkg

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTypesJson(t *testing.T) {
	hostnic := &HostNic{ID: "test", Address: "192.168.1.10", HardwareAddr: "52:54:72:46:81:51",
		VxNet: &VxNet{ID: "testvxnet", GateWay: "192.168.1.1",
			Network: "192.168.1.0/24"}}
	bytes, err := json.Marshal(hostnic)
	if err != nil {
		t.Error(err)
	}
	hostnic2 := &HostNic{}
	err = json.Unmarshal(bytes, hostnic2)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(hostnic, hostnic2) {
		t.Errorf(" %++v != %++v", hostnic, hostnic2)
	}
}
