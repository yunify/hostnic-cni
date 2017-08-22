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
	"fmt"
	"net"

	"context"
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
	_ "github.com/yunify/hostnic-cni/pkg/provider/qingcloud"
	"google.golang.org/grpc"
)

func cmdAdd(args *skel.CmdArgs) error {
	conf, err := pkg.LoadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	conn, err := grpc.Dial(conf.BindAddr,grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("Failed to open socket %v",err)
	}
	defer conn.Close()
	client := messages.NewNicservicesClient(conn)

	autoAssignGateway := false
	if conf.IPAM != nil {
		for _, route := range conf.IPAM.Routes {
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
		return fmt.Errorf("Failed to allocate nic :%v",err)
	}
	iface, err := pkg.LinkByMacAddr(nic.Nicid)
	if err != nil {
		logger.Error("LinkByMacAddr err %s, delete Nic %s", err.Error(), nic.Nicid)
		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: nic.Nicid})
		return fmt.Errorf("failed to get link by MacAddr %q: %v", nic.Nicid, err)
	}

	if err = netlink.LinkSetNsFd(iface, int(netns.Fd())); err != nil {
		logger.Error("LinkSetNsFd err %s, delete Nic %s", err.Error(), nic.Nicid)
		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: nic.Nicid})
		return fmt.Errorf("failed to set namespace on link %q: %v", nic.Nicid, err)
	}

	srcName := iface.Attrs().Name
	_, ipNet, err := net.ParseCIDR(nic.Niccidr)
	if err != nil {
		logger.Error("ParseCIDR err %s, delete Nic %s", err.Error(), nic.Nicid)
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
	if conf.IPAM != nil {
		routeTable = append(routeTable, conf.IPAM.Routes...)
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
	conf, err := pkg.LoadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	if args.Netns == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}

	conn, err := grpc.Dial(conf.BindAddr,grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("Failed to open socket %v",err)
	}
	defer conn.Close()
	client := messages.NewNicservicesClient(conn)

	err = ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {

		ifName := args.IfName
		iface, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q: %v", ifName, err)
		}

		client.FreeNic(context.Background(), &messages.FreeNicRequest{Nicid: iface.Attrs().HardwareAddr.String()})
		if err := netlink.LinkSetDown(iface); err != nil {
			return err
		}
		return nil
	})
	return err
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.All)
}
