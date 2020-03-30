package ipam

import (
	"github.com/yunify/hostnic-cni/pkg/types"
	"time"

	"k8s.io/klog"
)

const (
	nodeIPPoolReconcileInterval = 60 * time.Second
	decreaseIPPoolInterval      = 30 * time.Second
	defaultSleepDuration        = 10 * time.Second
	maxNic						= 60
)


// StartReconcileIPPool will start reconciling ip pool
func (s *IpamD) StartReconcileIPPool(stopCh <-chan struct{}, sleepDuration ...time.Duration) {
	klog.Infoln("Starting ip pool reconciling")
	for {
		select {
		case  <- time.After(defaultSleepDuration):
			for macKey, macValue := range s.deletingNic {
				if macValue == "" {
					klog.Infof("Period delete nic %s", macKey)
					if err := s.qcClient.DeleteNic(macKey); err == nil {
						delete(s.deletingNic, macKey)
					}
				}
			}
			s.updateIPPoolIfRequired()
		case nic:= <-s.trigCh:
			if nic.action == "" {
				klog.Infoln("Begin to update nic pool")
				s.updateIPPoolIfRequired()
			} else {
				klog.Infof("Uevent: %s, %s, %s", nic.action, nic.name, nic.mac)
				if nic.action == "add" {
					if hostNic := s.pendingNic[nic.mac]; hostNic != nil {
						delete(s.pendingNic, nic.mac)
						hostNic.DeviceNumber = nic.index
						err := s.setupNic(hostNic)
						if err != nil {
							klog.Errorf("Failed to setup nic in host, err: %s", err.Error())
							s.qcClient.DeattachNic(hostNic.ID)
							continue
						}
					}
				} else if nic.action == "remove" {
					if mac := s.deletingNic[nic.mac]; mac != "" {
						if err := s.qcClient.DeleteNic(mac); err != nil {
							s.deletingNic[mac] = ""
						} else {
							delete(s.deletingNic, nic.mac)
						}
					}
				}
			}
		case <-stopCh:
			klog.V(1).Infoln("Receive stop signal, stop pool manager")
			return
		}
	}
}

func (s *IpamD) updateIPPoolIfRequired() {
	if s.nodeIPPoolTooLow() {
		s.increaseIPPool()
	} else if s.nodeIPPoolTooHigh() {
		s.decreaseIPPool()
	}
}

func (s *IpamD) nodeIPPoolReconcile() {

}

func (s *IpamD) nodeIPPoolTooLow() bool {
	total, used := s.dataStore.GetStats()
	klog.V(4).Infof("IP pool stats: total = %d, used = %d", total, used)
	if (total + len(s.pendingNic) - used) < s.poolSize && total < maxNic {
		return true
	}
	return false
}

func (s *IpamD) nodeIPPoolTooHigh() bool {
	total, used := s.dataStore.GetStats()
	klog.V(4).Infof("IP pool stats: total = %d, used = %d", total, used)
	if (total - used) > s.maxPoolSize {
		return true
	}
	return false
}

func (s *IpamD) increaseIPPool() {
	klog.V(2).Infoln("try to increase ip pool")
	s.tryAllocateNIC()
}

func (s *IpamD) tryAllocateNIC() {
	klog.V(2).Infoln("Try to allocate nics to pool")
	nics, err := s.qcClient.CreateNicsAndAttach(*s.vxnet, defaultNICStep)
	if err != nil {
		klog.Errorf("Failed to create a nic in %s, err: %s", s.vxnet.ID, err.Error())
		return
	}
	//tag nic
	for _, nic := range nics {
		err = s.setupNic(nic)
		if err != nil {
			klog.Errorf("Failed to setup nic in host, err: %s", err.Error())
			s.qcClient.DeleteNic(nic.ID)
			continue
		}
	}
	klog.V(2).Infoln("Allocate nic successfully")
}

func (s *IpamD) decreaseIPPool() {
	nicid := s.dataStore.RemoveUnusedNICFromStore()
	if nicid != "" {
		klog.V(2).Infof("Delete nic %s from datastore", nicid)
		s.deletingNic[types.FormatMacAddr(nicid)] = nicid
		s.qcClient.DeattachNic(nicid)
	}
}
