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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	logger "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/pkg/messages"
	"google.golang.org/grpc"
)

//IPAMConfig routing rules configuratioins
type IPAMConfig struct {
	Routes []*types.Route `json:"routes"`
}

//NetConf nic plugin configuration
type NetConf struct {
	types.NetConf
	BindAddr   string      `json:"bindaddr"`
	IPAMConfig *IPAMConfig `json:"ipamConfig"`
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	netconf := &NetConf{}
	if err := json.Unmarshal(bytes, netconf); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}

	if netconf.BindAddr == "" {
		netconf.BindAddr = "127.0.0.1:31080"
	}
	return netconf, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	conf, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	conn, err := grpc.Dial(conf.BindAddr, grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("Failed to open socket %v", err)
	}
	defer conn.Close()
	client := messages.NewNicservicesClient(conn)

	autoAssignGateway := false
	if conf.IPAMConfig != nil {
		for _, route := range conf.IPAMConfig.Routes {
			if route.GW != nil && route.GW.Equal(net.IPv4(0, 0, 0, 0)) {
				autoAssignGateway = true
			}
		}
	}
	nic, err := client.AllocateNic(context.Background(), &messages.AllocateNicRequest{AutoAssignGateway: autoAssignGateway})
	if err != nil {
		if nic != nil {
			client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: nic.Nicid})
		}
		return fmt.Errorf("Failed to allocate nic :%v", err)
	}
	iface, err := pkg.LinkByMacAddr(nic.Nicid)
	if err != nil {
		logger.Errorf("LinkByMacAddr err %s, delete Nic %s", err.Error(), nic.Nicid)
		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: nic.Nicid})
		return fmt.Errorf("failed to get link by MacAddr %q: %v", nic.Nicid, err)
	}

	if err = netlink.LinkSetNsFd(iface, int(netns.Fd())); err != nil {
		logger.Errorf("LinkSetNsFd err %s, delete Nic %s", err.Error(), nic.Nicid)
		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: nic.Nicid})
		return fmt.Errorf("failed to set namespace on link %q: %v", nic.Nicid, err)
	}

	srcName := iface.Attrs().Name
	_, ipNet, err := net.ParseCIDR(nic.Niccidr)
	if err != nil {
		logger.Errorf("ParseCIDR err %s, delete Nic %s", err.Error(), nic.Nicid)
		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: nic.Nicid})
		return fmt.Errorf("failed to parse network %q: %v", nic.Niccidr, err)
	}
	gateWay := net.ParseIP(nic.Nicgateway)
	netIF := &current.Interface{Name: args.IfName, Mac: nic.Nicid, Sandbox: args.ContainerID}
	numOfiface := 0
	ipConfig := &current.IPConfig{Address: net.IPNet{IP: net.ParseIP(nic.Nicip), Mask: ipNet.Mask}, Interface: &numOfiface, Version: "4", Gateway: gateWay}
	//TODO support ipv6
	route := &types.Route{Dst: net.IPNet{IP: net.IPv4zero, Mask: net.IPMask(net.IPv4zero)}, GW: gateWay}

	var routeTable = []*types.Route{route}
	if conf.IPAMConfig != nil {
		for _, route := range conf.IPAMConfig.Routes {
			if route.GW != nil && route.GW.Equal(net.IPv4(0, 0, 0, 0)) {
				route.GW = net.ParseIP(nic.Servicegateway)
			}
			routeTable = append(routeTable, route)
		}
	}
	result := &current.Result{Interfaces: []*current.Interface{netIF}, IPs: []*current.IPConfig{ipConfig}, Routes: routeTable}
	err = netns.Do(func(_ ns.NetNS) error {
		nsIface, err := netlink.LinkByName(srcName)
		if err != nil {
			return fmt.Errorf("failed to get link by name %q: %v", srcName, err)
		}
		if err := netlink.LinkSetName(nsIface, args.IfName); err != nil {
			return fmt.Errorf("set link %s to name %s err: %v", nsIface.Attrs().HardwareAddr.String(), srcName, args.IfName)
		}
		return ipam.ConfigureIface(args.IfName, result)
	})
	if err != nil {
		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: nic.Nicid})
		return err
	}

	return types.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	if args.Netns == "" {
		return nil
	}
	defaultNs, err := ns.GetCurrentNS()
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer defaultNs.Close()

	conn, err := grpc.Dial(conf.BindAddr, grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("Failed to open socket %v", err)
	}
	defer conn.Close()
	client := messages.NewNicservicesClient(conn)

	err = ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {
		ifName := args.IfName
		iface, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q: %v", ifName, err)
		}
		if err = netlink.LinkSetDown(iface); err != nil {
			return err
		}
		attrs := iface.Attrs()
		if err := netlink.LinkSetName(iface, "eth"+strconv.Itoa(attrs.Index)); err != nil {
			return fmt.Errorf("Failed to set name for nic:%v", err)
		}

		if err = netlink.LinkSetNsFd(iface, int(defaultNs.Fd())); err != nil {
			return fmt.Errorf("Failed to set ns for nic:%v", err)
		}
		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: iface.Attrs().HardwareAddr.String()})
		return nil
	})
	return err
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.All)
}
