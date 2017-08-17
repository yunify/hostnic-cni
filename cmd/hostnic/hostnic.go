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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"

	"net"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/provider"
	_ "github.com/yunify/hostnic-cni/provider/qingcloud"
	logger "github.com/sirupsen/logrus"
)

const processLockFile = pkg.DefaultDataDir + "/lock"

func saveScratchNetConf(containerID, dataDir string, nic *pkg.HostNic) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create the multus data directory(%q): %v", dataDir, err)
	}

	path := filepath.Join(dataDir, containerID)
	data, err := json.Marshal(nic)
	if err != nil {
		return fmt.Errorf("failed to marshal nic %++v : %v", *nic, err)
	}
	err = ioutil.WriteFile(path, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write container data in the path(%q): %v", path, err)
	}

	return err
}

func consumeScratchNetConf(containerID, dataDir string) (*pkg.HostNic, error) {
	path := filepath.Join(dataDir, containerID)
	defer os.Remove(path)

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read container data in the path(%q): %v", path, err)
	}
	hostNic := &pkg.HostNic{}
	err = json.Unmarshal(data, hostNic)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal nic data in the path(%q): %v", path, err)
	}
	return hostNic, err
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := pkg.LoadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	nicProvider, err := provider.New(n.Provider, n.Args)
	if err != nil {
		return err
	}
	nic, err := nicProvider.CreateNic()
	if err != nil {
		return err
	}

	if n.IPAM != nil {
		for _, route := range n.IPAM.Routes {
			if route.GW != nil && route.GW.Equal(net.IPv4(0, 0, 0, 0)) {
				gateway, err := getOrAllocateNicAsGateway(nicProvider, nic)
				if err != nil {
					logger.Error("getOrAllocateNicAsGateway err %s, delete Nic %s", err.Error(), nic.ID)
					deleteNic(nic.ID, nicProvider)
					return err
				}
				route.GW = net.ParseIP(gateway.Address)
			}
		}
	}

	iface, err := pkg.LinkByMacAddr(nic.HardwareAddr)
	if err != nil {
		logger.Error("LinkByMacAddr err %s, delete Nic %s", err.Error(), nic.ID)
		deleteNic(nic.ID, nicProvider)
		return fmt.Errorf("failed to get link by MacAddr %q: %v", nic.HardwareAddr, err)
	}

	if err = netlink.LinkSetNsFd(iface, int(netns.Fd())); err != nil {
		logger.Error("LinkSetNsFd err %s, delete Nic %s", err.Error(), nic.ID)
		deleteNic(nic.ID, nicProvider)
		return fmt.Errorf("failed to set namespace on link %q: %v", nic.HardwareAddr, err)
	}

	srcName := iface.Attrs().Name
	_, ipNet, err := net.ParseCIDR(nic.VxNet.Network)
	if err != nil {
		logger.Error("ParseCIDR err %s, delete Nic %s", err.Error(), nic.ID)
		deleteNic(nic.ID, nicProvider)
		return fmt.Errorf("failed to parse vxnet %q network %q: %v", nic.VxNet.ID, nic.VxNet.Network, err)
	}
	gateWay := net.ParseIP(nic.VxNet.GateWay)
	netIF := &current.Interface{Name: args.IfName, Mac: nic.HardwareAddr, Sandbox: args.ContainerID}
	numOfiface :=0
	ipConfig := &current.IPConfig{Address: net.IPNet{IP: net.ParseIP(nic.Address), Mask: ipNet.Mask}, Interface: &numOfiface, Version: "4", Gateway: gateWay}
	//TODO support ipv6
	route := &types.Route{Dst: net.IPNet{IP: net.IPv4zero, Mask: net.IPMask(net.IPv4zero)}, GW: gateWay}
	var routeTable = []*types.Route{route}
	if n.IPAM != nil {
		routeTable = append(routeTable, n.IPAM.Routes...)
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
		deleteNic(nic.ID, nicProvider)
		return err
	}

	err = saveScratchNetConf(args.ContainerID, n.DataDir, nic)
	if err != nil {
		logger.Error("saveScratchNetConf err %s, delete Nic %s", err.Error(), nic.ID)
		deleteNic(nic.ID, nicProvider)
		return err
	}

	return types.PrintResult(result, n.CNIVersion)
}

func deleteNic(nicID string, nicProvider provider.NicProvider) error {
	if err := nicProvider.DeleteNic(nicID); err != nil {
		return fmt.Errorf("failed to delete nic %q : %v", nicID, err)
	}
	return nil
}

//getOrAllocateNicAsGateway
func getOrAllocateNicAsGateway(nicProvider provider.NicProvider, containernic *pkg.HostNic) (nic *pkg.HostNic, err error) {
	// get process lock first
	err = os.MkdirAll(pkg.DefaultDataDir, os.ModeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create the multus data directory(%q): %v", pkg.DefaultDataDir, err)
	}
	processLock, err := os.Create(processLockFile)
	if err != nil {
		return nil, err
	}
	defer processLock.Close()
	if err = syscall.Flock(int(processLock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(processLock.Fd()), syscall.LOCK_UN)

	// get a list of nics in current vxnet
	niclist, err := nicProvider.GetNicsUnderCurNamesp(&containernic.VxNet.ID)
	if err != nil {
		return nil, err
	}
	if niclist != nil {
		for _, nic := range niclist {
			if nic.HardwareAddr != containernic.HardwareAddr {
				return nic, nil
			}
		}
	}
	gateway, err := nicProvider.CreateNicInVxnet(containernic.VxNet.ID)
	if err != nil {
		logger.Error("CreateNicInVxnet err %s, delete Nic %s", err.Error(), gateway.ID)
		deleteNic(gateway.ID, nicProvider)
		return nil, err
	}
	//TODO refactor to  use pkg.ConfigureIface
	iface, err := pkg.LinkByMacAddr(gateway.HardwareAddr)
	if err != nil {
		logger.Error("LinkByMacAddr err %s, delete Nic %s", err.Error(), gateway.ID)
		deleteNic(gateway.ID, nicProvider)
		return nil, err
	}
	_, ipNet, err := net.ParseCIDR(gateway.VxNet.Network)
	if err != nil {
		logger.Error("ParseCIDR err %s, delete Nic %s", err.Error(), gateway.ID)
		deleteNic(gateway.ID, nicProvider)
		return nil, err
	}
	if err := netlink.LinkSetDown(iface); err != nil {
		logger.Error("LinkSetDown err %s, delete Nic %s", err.Error(), gateway.ID)
		deleteNic(gateway.ID, nicProvider)
		return nil, err
	}
	//start to configure ip
	pkg.ClearLinkAddr(iface)
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: net.ParseIP(gateway.Address), Mask: ipNet.Mask}, Label: ""}
	if err := netlink.AddrAdd(iface, addr); err != nil {
		logger.Error("AddrAdd err %s, delete Nic %s", err.Error(), gateway.ID)
		deleteNic(gateway.ID, nicProvider)
		return nil, err
	}
	//bring up interface
	if err := netlink.LinkSetUp(iface); err != nil {
		logger.Error("LinkSetUp err %s, delete Nic %s", err.Error(), gateway.ID)
		deleteNic(gateway.ID, nicProvider)
		return nil, err
	}

	return gateway, nil
}

func cmdDel(args *skel.CmdArgs) error {
	n, err := pkg.LoadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	if args.Netns == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}

	nicProvider, err := provider.New(n.Provider, n.Args)
	if err != nil {
		return err
	}
	nic, err := consumeScratchNetConf(args.ContainerID, n.DataDir)
	if err != nil {
		return err
	}
	err = ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {
		ifName := args.IfName
		iface, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q: %v", ifName, err)
		}
		if err := netlink.LinkSetDown(iface); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return deleteNic(nic.ID, nicProvider)
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.All)
}
