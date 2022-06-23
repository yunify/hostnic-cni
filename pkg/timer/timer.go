package timer

import (
	"time"

	log "k8s.io/klog/v2"
)

type Timer struct {
	name     string
	period   int
	callback func()
}

func NewTimer(name string, period int, callback func()) *Timer {
	return &Timer{
		name:     name,
		period:   period,
		callback: callback,
	}
}

func (t *Timer) Run(stop <-chan struct{}) {
	log.Info("%s start", t.name)
	ticker := time.NewTicker(time.Duration(t.period) * time.Second)
	defer func() {
		ticker.Stop()
	}()

Loop:
	for {
		select {
		case <-stop:
			break Loop
		case <-ticker.C:
			func() {
				defer func() {
					if err := recover(); err != nil {
						log.Infof("handle failed: %v", err)
					}
				}()
				t.callback()
			}()
		}
	}

	log.Infof("%s stop", t.name)
}
