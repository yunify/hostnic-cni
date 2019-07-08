package ipam

import (
	"time"

	"k8s.io/klog"
)

const (
	nodeIPPoolReconcileInterval = 60 * time.Second
	decreaseIPPoolInterval      = 30 * time.Second
	defaultSleepDuration        = 10 * time.Second
)

// StartReconcileIPPool will start reconciling ip pool
func (s *IpamD) StartReconcileIPPool(stopCh <-chan struct{}, sleepDuration ...time.Duration) {
	klog.V(1).Infoln("Starting ip pool reconciling")
	for {
		select {
		case <-stopCh:
			klog.V(1).Infoln("Receive stop signal, stop pool manager")
			return
		default:
			klog.V(3).Infoln("Begin to reconcile nic pool")
			sleep := defaultSleepDuration
			if len(sleepDuration) == 1 {
				sleep = sleepDuration[0]
			}
			time.Sleep(sleep)
			s.updateIPPoolIfRequired()
			time.Sleep(sleep)
			s.nodeIPPoolReconcile()
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
	if (total - used) < s.poolSize {
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
	klog.V(2).Infoln("Try to allocate a new nic to pool")
	nic, err := s.qcClient.CreateNic(s.vxnet.ID)
	if err != nil {
		klog.Errorf("Failed to create a nic in %s, err: %s", s.vxnet.ID, err.Error())
		return
	}
	err = s.setupNic(nic)
	if err != nil {
		klog.Errorf("Failed to setup nic in host, err: %s", err.Error())
	}
	klog.V(2).Infoln("Allocate nic successfully")
}

func (s *IpamD) decreaseIPPool() {
	klog.V(2).Infoln("try to decrease ip pool")
	nicid := s.dataStore.RemoveUnusedNICFromStore()
	if nicid != "" {
		klog.V(2).Infof("delete nic %s", nicid)
		err := s.qcClient.DeleteNic(nicid)
		if err != nil {
			klog.Errorf("Failed to delete nic %s in cloud, err: %s", nicid, err.Error())
		}
	}
	klog.V(2).Infoln("decrease pool successfully")
}
