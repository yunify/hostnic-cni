package udev

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	udev "github.com/pilebones/go-udev/netlink"
	"github.com/sirupsen/logrus"
)

type UdevNotify struct {
	Action string
	MAC    string
	index  int    //unused
	name   string //unused
}

func splitSubN(s string, n int) []string {
	sub := ""
	subs := []string{}

	runes := bytes.Runes([]byte(s))
	l := len(runes)
	for i, r := range runes {
		sub = sub + string(r)
		if (i+1)%n == 0 {
			subs = append(subs, sub)
			sub = ""
		} else if (i + 1) == l {
			subs = append(subs, sub)
		}
	}

	return subs
}

func formatMac(str string) string {
	str = strings.TrimPrefix(str, "enx")
	return strings.Join(splitSubN(str, 2), ":")
}

type Udev struct {
	Ch chan UdevNotify
}

func (u Udev) Start(stopCh <-chan struct{}) error {
	go u.run(stopCh)
	return nil
}

// Monitor run Monitor mode
func (u Udev) run(stopCh <-chan struct{}) {
	conn := new(udev.UEventConn)
	if err := conn.Connect(udev.UdevEvent); err != nil {
		logrus.WithError(err).Fatal("cannot init udev")
	}
	defer conn.Close()

	var matchers udev.RuleDefinitions
	actionsAdd := "add"
	matcher := udev.RuleDefinition{
		Action: &actionsAdd,
		Env: map[string]string{
			"SUBSYSTEM": "net",
		},
	}
	matchers.AddRule(matcher)

	actionsRemove := "remove"
	matcher = udev.RuleDefinition{
		Action: &actionsRemove,
		Env: map[string]string{
			"SUBSYSTEM": "net",
		},
	}
	matchers.AddRule(matcher)

	queue := make(chan udev.UEvent)
	errors := make(chan error)
	conn.Monitor(queue, errors, &matchers)

	// Handling message from queue
	for {
		select {
		case <-stopCh:
			logrus.Info("udev stoped")
			return
		case uevent := <-queue:
			if uevent.Action == ActionRemove || uevent.Action == ActionAdd {
				if uevent.Env["INTERFACE"] != "" {
					name := uevent.Env["INTERFACE"]

					if uevent.Action == ActionAdd {
						_, err := os.Stat(fmt.Sprintf("/sys/devices/virtual/net/%s", name))
						if !os.IsNotExist(err) {
							continue
						}
					}

					logrus.WithField("uevent", uevent).Info("receive physical nic uevent")

					index, _ := strconv.Atoi(uevent.Env["IFINDEX"])
					u.Ch <- UdevNotify{
						name:   name,
						MAC:    formatMac(uevent.Env["ID_NET_NAME_MAC"]),
						Action: string(uevent.Action),
						index:  index,
					}
				}
			}
		case err := <-errors:
			logrus.WithError(err).Fatal("udev recv error")
		}
	}
}

const (
	ActionAdd    = "add"
	ActionRemove = "remove"
)
