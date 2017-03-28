package pkg

import (
	"testing"
	"encoding/json"
	"reflect"
)

func TestTypesJson(t *testing.T){
	hostnic := &HostNic{ID:"test",Address:"192.168.1.10", HardwareAddr:"52:54:72:46:81:51",
		VxNet:&VxNet{ID:"testvxnet", GateWay:"192.168.1.1",
			Network:"192.168.1.0/24"}}
	bytes, err := json.Marshal(hostnic)
	if err != nil {
		t.Error(err)
	}
	hostnic2 := &HostNic{}
	err = json.Unmarshal(bytes, hostnic2)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(hostnic, hostnic2){
		t.Errorf(" %++v != %++v", hostnic,hostnic2)
	}
}