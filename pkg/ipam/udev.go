package ipam

import (
	"github.com/yunify/hostnic-cni/pkg/go-udev"
	"k8s.io/klog"
	"strconv"
)

type udevNotify struct {
	action	string
	index 	int
	name  string
	mac   string
}

// monitor run monitor mode
func (s *IpamD)monitor() {
	conn := new(go_udev.UEventConn)
	if err := conn.Connect(go_udev.UdevEvent); err != nil {
		klog.Error("Unable to connect to Netlink Kobject UEvent socket")
		return
	}
	defer conn.Close()

	var matchers go_udev.RuleDefinitions
	actions_add := "add"
	matcher := go_udev.RuleDefinition{
		Action: &actions_add,
		Env: make(map[string]string),
	}
	matcher.Env["SUBSYSTEM"] = "net"
	matchers.AddRule(matcher)

	actions_remove := "remove"
	matcher = go_udev.RuleDefinition{
		Action: &actions_remove,
		Env: make(map[string]string),
	}
	matchers.AddRule(matcher)

	queue := make(chan go_udev.UEvent)
	errors := make(chan error)
	conn.Monitor(queue, errors, &matchers)

	// Handling message from queue
	for {
		select {
		case uevent := <-queue:
			if uevent.Action == "remove" || uevent.Action == "add" {
				if uevent.Env["INTERFACE"] != "" {
					index, _ := strconv.Atoi(uevent.Env["IFINDEX"])
					s.trigCh <- udevNotify{
						name: uevent.Env["INTERFACE"],
						mac: uevent.Env["ID_NET_NAME_MAC"],
						action: string(uevent.Action),
						index: index,
					}
				}
			}
		case err := <-errors:
			klog.Errorf("ERROR: %v", err)
		}
	}
}