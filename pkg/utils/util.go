package utils

import "github.com/yunify/hostnic-cni/pkg/rpc"

func HostNicToIDs(nics []*rpc.HostNic) []string {
	var result []string

	for _, nic := range nics {
		result = append(result, nic.ID)
	}

	return result
}

func HasString(src string, dst []string) bool {
	for _, i := range dst {
		if src == i {
			return true
		}
	}

	return false
}

func RemoveNic(nics []*rpc.HostNic, id string) {
	var result []*rpc.HostNic

	for _, nic := range nics {
		if nic.ID != id {
			result = append(result, nic)
		}
	}

	nics = result
}

func SetHostNicStatus(nics map[string]*rpc.HostNic, status rpc.Status) {
	for _, nic := range nics {
		if nic.Status == rpc.Status_USING || nic.Status == rpc.Status_DELETED {
			continue
		}
		nic.Status = status
	}
}

func HostNicToMap(nics []*rpc.HostNic) map[string]*rpc.HostNic {
	result := make(map[string]*rpc.HostNic)
	for _, nic := range nics {
		result[nic.ID] = nic
	}

	return result
}

func HostNicMapToIDs(nics map[string]*rpc.HostNic) []string {
	var result []string

	for _, nic := range nics {
		result = append(result, nic.ID)
	}

	return result
}

func PodKey(info *rpc.PodInfo) string {
	return info.Containter
}
