package datastore

import (
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	k8sapi "github.com/yunify/hostnic-cni/pkg/k8sclient"
	"k8s.io/klog"
)

const (
	minLifeTime = 1 * time.Minute
	// addressNICCoolingPeriod is used to ensure NIC will NOT get freed back to EC2 control plane if one of
	// its secondary IP addresses is used for a Pod within last addressNICCoolingPeriod
	addressNICCoolingPeriod = 1 * time.Minute

	// addressCoolingPeriod is used to ensure an IP not get assigned to a Pod if this IP is used by a different Pod
	// in addressCoolingPeriod
	addressCoolingPeriod = 30 * time.Second

	// DuplicatedNICError is an error when caller tries to add an duplicate NIC to data store
	DuplicatedNICError = "data store: duplicate NIC"

	// DuplicateIPError is an error when caller tries to add an duplicate IP address to data store
	DuplicateIPError = "datastore: duplicated IP"

	// UnknownIPError is an error when caller tries to delete an IP which is unknown to data store
	UnknownIPError = "datastore: unknown IP"

	// IPInUseError is an error when caller tries to delete an IP where IP is still assigned to a Pod
	IPInUseError = "datastore: IP is used and can not be deleted"

	// NICInUseError is an error when caller tries to delete an NIC where there are IP still assigned to a pod
	NICInUseError = "datastore: NIC is used and can not be deleted"

	// UnknownNICError is an error when caller tries to access an NIC which is unknown to datastore
	UnknownNICError = "datastore: unknown NIC"
)

// ErrUnknownPod is an error when there is no pod in data store matching pod name, namespace, container id
var ErrUnknownPod = errors.New("datastore: unknown pod")

// ErrUnknownPodIP is an error where pod's IP address is not found in data store
var ErrUnknownPodIP = errors.New("datastore: pod using unknown IP address")

var (
	nics = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "yunify_nic_nic_allocated",
			Help: "The number of NICs allocated",
		},
	)
	totalIPs = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "yunify_nic_total_ip_addresses",
			Help: "The total number of IP addresses",
		},
	)
	assignedIPs = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "yunify_nic_assigned_ip_addresses",
			Help: "The number of IP addresses assigned to pods",
		},
	)
	prometheusRegistered = false
)

// NICIPPool contains NIC/IP Pool information. Exported fields will be marshaled for introspection.
type NICIPPool struct {
	createTime         time.Time
	lastUnassignedTime time.Time
	// IsPrimary indicates whether NIC is a primary NIC
	IsPrimary bool
	ID        string
	// DeviceNumber is the device number of NIC
	DeviceNumber int
	// AssignedIPv4Addresses is the number of IP addresses already been assigned
	AssignedIPv4Addresses int
	// IPv4Addresses shows whether each address is assigned, the key is IP address, which must
	// be in dot-decimal notation with no leading zeros and no whitespace(eg: "10.1.0.253")
	IPv4Addresses map[string]*AddressInfo
}

// AddressInfo contains information about an IP, Exported fields will be marshaled for introspection.
type AddressInfo struct {
	Address        string
	Assigned       bool // true if it is assigned to a pod
	UnassignedTime time.Time
}

// PodKey is used to locate pod IP
type PodKey struct {
	name      string
	namespace string
	container string
}

// PodIPInfo contains pod's IP and the device number of the NIC
type PodIPInfo struct {
	// IP is the IP address of pod
	IP string
	// DeviceNumber is the device number of  pod
	DeviceNumber int
}

// DataStore contains node level NIC/IP
type DataStore struct {
	total      int
	assigned   int
	nicIPPools map[string]*NICIPPool
	podsIP     map[PodKey]PodIPInfo
	lock       sync.RWMutex
}

// PodInfos contains pods IP information which uses key name_namespace_container
type PodInfos map[string]PodIPInfo

// NICInfos contains NIC IP information
type NICInfos struct {
	// TotalIPs is the total number of IP addresses
	TotalIPs int
	// assigned is the number of IP addresses that has been assigned
	AssignedIPs int
	// NICIPPools contains NIC IP pool information
	NICIPPools map[string]NICIPPool
}

func prometheusRegister() {
	if !prometheusRegistered {
		prometheus.MustRegister(nics)
		prometheus.MustRegister(totalIPs)
		prometheus.MustRegister(assignedIPs)
		prometheusRegistered = true
	}
}

// NewDataStore returns DataStore structure
func NewDataStore() *DataStore {
	prometheusRegister()
	return &DataStore{
		nicIPPools: make(map[string]*NICIPPool),
		podsIP:     make(map[PodKey]PodIPInfo),
	}
}

// AddNIC add NIC to data store
func (ds *DataStore) AddNIC(nicID string, deviceNumber int, isPrimary bool) error {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	klog.V(2).Infoln("DataStore Add an NIC ", nicID)

	_, ok := ds.nicIPPools[nicID]
	if ok {
		return errors.New(DuplicatedNICError)
	}
	ds.nicIPPools[nicID] = &NICIPPool{
		createTime:    time.Now(),
		IsPrimary:     isPrimary,
		ID:            nicID,
		DeviceNumber:  deviceNumber,
		IPv4Addresses: make(map[string]*AddressInfo)}
	nics.Set(float64(len(ds.nicIPPools)))
	return nil
}

// AddIPv4AddressFromStore add an IP of an NIC to data store
func (ds *DataStore) AddIPv4AddressFromStore(nicID string, ipv4 string) error {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	klog.V(2).Infof("Adding NIC(%s)'s IPv4 address %s to datastore", nicID, ipv4)
	klog.V(2).Infof("IP Address Pool stats: total: %d, assigned: %d", ds.total, ds.assigned)

	curNIC, ok := ds.nicIPPools[nicID]
	if !ok {
		return errors.New("add NIC's IP to datastore: unknown NIC")
	}

	_, ok = curNIC.IPv4Addresses[ipv4]
	if ok {
		return errors.New(DuplicateIPError)
	}

	ds.total++
	// Prometheus gauge
	totalIPs.Set(float64(ds.total))

	curNIC.IPv4Addresses[ipv4] = &AddressInfo{Address: ipv4, Assigned: false}
	klog.V(1).Infof("Added NIC(%s)'s IP %s to datastore", nicID, ipv4)
	return nil
}

// DelIPv4AddressFromStore delete an IP of NIC from datastore
func (ds *DataStore) DelIPv4AddressFromStore(nicID string, ipv4 string) error {
	ds.lock.Lock()
	defer ds.lock.Unlock()
	klog.V(2).Infof("Deleting NIC(%s)'s IPv4 address %s from datastore", nicID, ipv4)
	klog.V(2).Infof("IP Address Pool stats: total: %d, assigned: %d", ds.total, ds.assigned)

	curNIC, ok := ds.nicIPPools[nicID]
	if !ok {
		return errors.New(UnknownNICError)
	}

	ipAddr, ok := curNIC.IPv4Addresses[ipv4]
	if !ok {
		return errors.New(UnknownIPError)
	}

	if ipAddr.Assigned {
		return errors.New(IPInUseError)
	}

	ds.total--
	// Prometheus gauge
	totalIPs.Set(float64(ds.total))

	delete(curNIC.IPv4Addresses, ipv4)

	klog.V(1).Infof("Deleted NIC(%s)'s IP %s from datastore", nicID, ipv4)
	return nil
}

// AssignPodIPv4Address assigns an IPv4 address to pod
// It returns the assigned IPv4 address, device number, error
func (ds *DataStore) AssignPodIPv4Address(k8sPod *k8sapi.K8SPodInfo) (string, int, error) {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	klog.V(2).Infof("AssignIPv4Address: IP address pool stats: total: %d, assigned %d", ds.total, ds.assigned)
	podKey := PodKey{
		name:      k8sPod.Name,
		namespace: k8sPod.Namespace,
		container: k8sPod.Container,
	}
	ipAddr, ok := ds.podsIP[podKey]
	if ok {
		if ipAddr.IP == k8sPod.IP && k8sPod.IP != "" {
			// The caller invoke multiple times to assign(PodName/NameSpace --> same IPAddress). It is not a error, but not very efficient.
			klog.V(1).Infof("AssignPodIPv4Address: duplicate pod assign for IP %s, name %s, namespace %s, container %s",
				k8sPod.IP, k8sPod.Name, k8sPod.Namespace, k8sPod.Container)
			return ipAddr.IP, ipAddr.DeviceNumber, nil
		}
		// TODO Handle this bug assert? May need to add a counter here, if counter is too high, need to mark node as unhealthy...
		//      This is a bug that the caller invokes multiple times to assign(PodName/NameSpace -> a different IP address).
		klog.Errorf("AssignPodIPv4Address: current IP %s is changed to IP %s for pod(name %s, namespace %s, container %s)",
			ipAddr.IP, k8sPod.IP, k8sPod.Name, k8sPod.Namespace, k8sPod.Container)
		return "", 0, errors.New("AssignPodIPv4Address: invalid pod with multiple IP addresses")
	}
	return ds.assignPodIPv4AddressUnsafe(k8sPod)
}

// It returns the assigned IPv4 address, device number, error
func (ds *DataStore) assignPodIPv4AddressUnsafe(k8sPod *k8sapi.K8SPodInfo) (string, int, error) {
	podKey := PodKey{
		name:      k8sPod.Name,
		namespace: k8sPod.Namespace,
		container: k8sPod.Container,
	}
	curTime := time.Now()
	for _, nic := range ds.nicIPPools {
		if (k8sPod.IP == "") && (len(nic.IPv4Addresses) == nic.AssignedIPv4Addresses) {
			// skip this NIC, since it has no available IP addresses
			klog.V(2).Infof("AssignPodIPv4Address: Skip NIC %s that does not have available addresses", nic.ID)
			continue
		}
		for _, addr := range nic.IPv4Addresses {
			if k8sPod.IP == addr.Address {
				// After L-IPAM restart and built IP warm-pool, it needs to take the existing running pod IP out of the pool.
				if !addr.Assigned {
					incrementAssignedCount(ds, nic, addr)
				}
				klog.V(1).Infof("AssignPodIPv4Address: Reassign IP %v to pod (name %s, namespace %s)",
					addr.Address, k8sPod.Name, k8sPod.Namespace)
				ds.podsIP[podKey] = PodIPInfo{IP: addr.Address, DeviceNumber: nic.DeviceNumber}
				return addr.Address, nic.DeviceNumber, nil
			}
			if !addr.Assigned && k8sPod.IP == "" && curTime.Sub(addr.UnassignedTime) > addressCoolingPeriod {
				// This is triggered by a pod's Add Network command from CNI plugin
				incrementAssignedCount(ds, nic, addr)
				klog.V(1).Infof("AssignPodIPv4Address: Assign IP %v to pod (name %s, namespace %s container %s)",
					addr.Address, k8sPod.Name, k8sPod.Namespace, k8sPod.Container)
				ds.podsIP[podKey] = PodIPInfo{IP: addr.Address, DeviceNumber: nic.DeviceNumber}
				return addr.Address, nic.DeviceNumber, nil
			}
		}
	}
	klog.Errorf("DataStore has no available IP addresses")
	return "", 0, errors.New("assignPodIPv4AddressUnsafe: no available IP addresses")
}

func incrementAssignedCount(ds *DataStore, nic *NICIPPool, addr *AddressInfo) {
	ds.assigned++
	nic.AssignedIPv4Addresses++
	addr.Assigned = true
	// Prometheus gauge
	assignedIPs.Set(float64(ds.assigned))
}

// GetStats returns total number of IP addresses and number of assigned IP addresses
func (ds *DataStore) GetStats() (int, int) {
	return ds.total, ds.assigned
}

func (ds *DataStore) getDeletableNIC() *NICIPPool {
	for _, nic := range ds.nicIPPools {
		if nic.IsPrimary {
			continue
		}

		if time.Now().Sub(nic.createTime) < minLifeTime {
			continue
		}

		if time.Now().Sub(nic.lastUnassignedTime) < addressNICCoolingPeriod {
			continue
		}

		if nic.AssignedIPv4Addresses != 0 {
			continue
		}

		klog.V(2).Infof("getDeletableNIC: found a deletable NIC %s", nic.ID)
		return nic
	}
	return nil
}

// GetNICNeedsIP finds an NIC in the datastore that needs more IP addresses allocated
func (ds *DataStore) GetNICNeedsIP(maxIPperNIC int, skipPrimary bool) *NICIPPool {
	for _, nic := range ds.nicIPPools {
		if skipPrimary && nic.IsPrimary {
			klog.V(2).Infof("Skip the primary NIC for need IP check")
			continue
		}
		if len(nic.IPv4Addresses) < maxIPperNIC {
			klog.V(2).Infof("Found NIC %s that has less than the maximum number of IP addresses allocated: cur=%d, max=%d",
				nic.ID, len(nic.IPv4Addresses), maxIPperNIC)
			return nic
		}
	}
	return nil
}

// RemoveUnusedNICFromStore removes a deletable NIC from the data store.
// It returns the name of the NIC which has been removed from the data store and needs to be deleted,
// or empty string if no NIC could be removed.
func (ds *DataStore) RemoveUnusedNICFromStore() string {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	deletableNIC := ds.getDeletableNIC()
	if deletableNIC == nil {
		klog.V(2).Infof("No NIC can be deleted at this time")
		return ""
	}

	removableNIC := deletableNIC.ID
	nicIPCount := len(ds.nicIPPools[removableNIC].IPv4Addresses)
	ds.total -= nicIPCount
	klog.V(1).Infof("RemoveUnusedNICFromStore %s: IP address pool stats: free %d addresses, total: %d, assigned: %d",
		removableNIC, nicIPCount, ds.total, ds.assigned)
	delete(ds.nicIPPools, removableNIC)

	// Prometheus update
	nics.Set(float64(len(ds.nicIPPools)))
	totalIPs.Set(float64(ds.total))
	return removableNIC
}

// RemoveNICFromDataStore removes an NIC from the datastore.  It return nil on success or an error.
func (ds *DataStore) RemoveNICFromDataStore(nic string) error {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	nicIPPool, ok := ds.nicIPPools[nic]
	if !ok {
		return errors.New(UnknownNICError)
	}

	// Only unused NICs can be deleted
	if nicIPPool.AssignedIPv4Addresses != 0 {
		return errors.New(NICInUseError)
	}

	ds.total -= len(nicIPPool.IPv4Addresses)
	klog.V(1).Infof("RemoveNICFromDataStore %s: IP address pool stats: free %d addresses, total: %d, assigned: %d",
		nic, len(nicIPPool.IPv4Addresses), ds.total, ds.assigned)
	delete(ds.nicIPPools, nic)

	// Prometheus gauge
	nics.Set(float64(len(ds.nicIPPools)))
	return nil
}

// UnassignPodIPv4Address a) find out the IP address based on PodName and PodNameSpace
// b)  mark IP address as unassigned c) returns IP address, NIC's device number, error
func (ds *DataStore) UnassignPodIPv4Address(k8sPod *k8sapi.K8SPodInfo) (string, int, error) {
	ds.lock.Lock()
	defer ds.lock.Unlock()
	klog.V(2).Infof("UnassignPodIPv4Address: IP address pool stats: total:%d, assigned %d, pod(Name: %s, Namespace: %s, Container %s)",
		ds.total, ds.assigned, k8sPod.Name, k8sPod.Namespace, k8sPod.Container)

	podKey := PodKey{
		name:      k8sPod.Name,
		namespace: k8sPod.Namespace,
		container: k8sPod.Container,
	}
	ipAddr, ok := ds.podsIP[podKey]
	if !ok {
		klog.Warningf("UnassignPodIPv4Address: Failed to find pod %s namespace %s Container %s",
			k8sPod.Name, k8sPod.Namespace, k8sPod.Container)
		return "", 0, ErrUnknownPod
	}

	for _, nic := range ds.nicIPPools {
		ip, ok := nic.IPv4Addresses[ipAddr.IP]
		if ok && ip.Assigned {
			ip.Assigned = false
			ds.assigned--
			assignedIPs.Set(float64(ds.assigned))
			nic.AssignedIPv4Addresses--
			curTime := time.Now()
			ip.UnassignedTime = curTime
			nic.lastUnassignedTime = curTime
			klog.V(1).Infof("UnassignPodIPv4Address: pod (Name: %s, NameSpace %s Container %s)'s ipAddr %s, DeviceNumber%d",
				k8sPod.Name, k8sPod.Namespace, k8sPod.Container, ip.Address, nic.DeviceNumber)
			delete(ds.podsIP, podKey)
			return ip.Address, nic.DeviceNumber, nil
		}
	}

	klog.Warningf("UnassignPodIPv4Address: Failed to find pod %s namespace %s container %s using IP %s",
		k8sPod.Name, k8sPod.Namespace, k8sPod.Container, ipAddr.IP)
	return "", 0, ErrUnknownPodIP
}

// GetPodInfos provides pod IP information to introspection endpoint
func (ds *DataStore) GetPodInfos() *map[string]PodIPInfo {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	var podInfos = make(map[string]PodIPInfo, len(ds.podsIP))

	for podKey, podInfo := range ds.podsIP {
		key := podKey.name + "_" + podKey.namespace + "_" + podKey.container
		podInfos[key] = podInfo
		klog.V(2).Infof("GetPodInfos: key %s", key)
	}

	klog.V(2).Infof("GetPodInfos: len %d", len(ds.podsIP))
	return &podInfos
}

// GetNICInfos provides NIC IP information to introspection endpoint
func (ds *DataStore) GetNICInfos() *NICInfos {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	var nicInfos = NICInfos{
		TotalIPs:    ds.total,
		AssignedIPs: ds.assigned,
		NICIPPools:  make(map[string]NICIPPool, len(ds.nicIPPools)),
	}

	for nic, nicInfo := range ds.nicIPPools {
		nicInfos.NICIPPools[nic] = *nicInfo
	}
	return &nicInfos
}

// GetNICs provides the number of NIC in the datastore
func (ds *DataStore) GetNICs() int {
	ds.lock.Lock()
	defer ds.lock.Unlock()
	return len(ds.nicIPPools)
}

// GetNICIPPools returns nic's IP address list
func (ds *DataStore) GetNICIPPools(nic string) (map[string]*AddressInfo, error) {
	ds.lock.Lock()
	defer ds.lock.Unlock()

	nicIPPool, ok := ds.nicIPPools[nic]
	if !ok {
		return nil, errors.New(UnknownNICError)
	}

	var ipPool = make(map[string]*AddressInfo, len(nicIPPool.IPv4Addresses))
	for ip, ipAddr := range nicIPPool.IPv4Addresses {
		ipPool[ip] = ipAddr
	}
	return ipPool, nil
}
