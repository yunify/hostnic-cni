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
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/ip"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/vishvananda/netlink"
)

func StringPtr(str string) *string {
	return &str
}

func ConfigureIface(ifName string, res *current.Result) error {
	if len(res.Interfaces) == 0 {
		return fmt.Errorf("no interfaces to configure")
	}
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to lookup %q: %v", ifName, err)
	}

	// Down the interface before configuring
	if err := netlink.LinkSetDown(link); err != nil {
		return fmt.Errorf("failed to set link down: %v", err)
	}

	var v4gw, v6gw net.IP
	for _, ipc := range res.IPs {
		if int(ipc.Interface) >= len(res.Interfaces) || res.Interfaces[ipc.Interface].Name != ifName {
			// IP address is for a different interface
			return fmt.Errorf("failed to add IP addr %v to %q: invalid interface index", ipc, ifName)
		}

		addr := &netlink.Addr{IPNet: &ipc.Address, Label: ""}
		if err := netlink.AddrAdd(link, addr); err != nil {
			return fmt.Errorf("failed to add IP addr %v to %q: %v", ipc, ifName, err)
		}

		gwIsV4 := ipc.Gateway.To4() != nil
		if gwIsV4 && v4gw == nil {
			v4gw = ipc.Gateway
		} else if !gwIsV4 && v6gw == nil {
			v6gw = ipc.Gateway
		}
	}
	// setup before set routes
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("failed to set %q UP: %v", ifName, err)
	}

	for _, r := range res.Routes {
		routeIsV4 := r.Dst.IP.To4() != nil
		gw := r.GW
		if gw == nil {
			if routeIsV4 && v4gw != nil {
				gw = v4gw
			} else if !routeIsV4 && v6gw != nil {
				gw = v6gw
			}
		}
		if err := ip.AddRoute(&r.Dst, gw, link); err != nil {
			// we skip over duplicate routes as we assume the first one wins
			if !os.IsExist(err) {
				return fmt.Errorf("failed to add route '%v via %v dev %v': %v", r.Dst, gw, ifName, err)
			}
		}
	}

	return nil
}

func LinkByMacAddr(macAddr string) (netlink.Link, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		attr := link.Attrs()
		if attr.HardwareAddr.String() == macAddr {
			return link, nil
		}
	}
	return nil, fmt.Errorf("Can not find link by address: %s", macAddr)
}
