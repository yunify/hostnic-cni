package server

import (
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg"
)

//NicPool nic cached pool
type NicPool struct {
	sync.RWMutex
	nicDict map[string]*pkg.HostNic

	nicpool chan string

	nicGenerator          NICGenerator
	nicStopGeneratorChann chan struct{}

	gatewayMgr map[string]string
}

//NewNicPool new nic pool
func NewNicPool(size int, generator NICGenerator) *NicPool {
	return &NicPool{
		nicDict:               make(map[string]*pkg.HostNic),
		nicpool:               make(chan string, size),
		nicStopGeneratorChann: make(chan struct{}),
	}
}

//NICGenerator nic generator function
type NICGenerator func() (*pkg.HostNic, error)

//startNicGenerator add new nics to resource pool
func (pool *NicPool) startNicGenerator() {
	go func() {
	eventloop:
		for {
			select {
			case <-pool.nicStopGeneratorChann:
				break eventloop
			default:
				nic, err := pool.nicGenerator()
				if err != nil {
					log.Errorf("Failed to get nic from generator", err)
				}
				pool.Lock()
				defer pool.Unlock()
				pool.nicDict[nic.ID] = nic
				pool.nicpool <- nic.ID
			}
		}
	}()
}

func (pool *NicPool) DeleteNic(nicids ...string) error {
	pool.Lock()
	defer pool.Unlock()
	for _, nicid := range nicids {
		delete(pool.nicDict, nicid)
	}
	return nil
}

func (pool *NicPool) ReturnNic(nicid ...string) error {
	pool.RLock()
	defer pool.RUnlock()
	//todo: implement return logic
	return nil
}

func (pool *NicPool) BorrowNic(autoAssignGateway bool) (*pkg.HostNic, error) {
	pool.RLock()
	defer pool.RUnlock()
	//todo: implement borrow logic
	return nil, nil
}
