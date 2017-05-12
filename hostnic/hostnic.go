package main

import (
	"fmt"
	"runtime"

	"net"

	"github.com/containernetworking/cni/pkg/ns"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/provider"
	_ "github.com/yunify/hostnic-cni/provider/qingcloud"
)

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	nicProvider, err := provider.New(n.Provider, n.Config)
	if err != nil {
		return err
	}
	nic, err := nicProvider.CreateNic()
	if err != nil {
		return err
	}
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		deleteNic(nic.ID, nicProvider)
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	iface, err := pkg.LinkByMacAddr(nic.HardwareAddr)
	if err != nil {
		deleteNic(nic.ID, nicProvider)
		return fmt.Errorf("failed to get link by MacAddr %q: %v", nic.HardwareAddr, err)
	}

	if err = netlink.LinkSetNsFd(iface, int(netns.Fd())); err != nil {
		deleteNic(nic.ID, nicProvider)
		return fmt.Errorf("failed to set namespace on link %q: %v", nic.HardwareAddr, err)
	}

	srcName := iface.Attrs().Name
	_, ipNet, err := net.ParseCIDR(nic.VxNet.Network)
	if err != nil {
		deleteNic(nic.ID, nicProvider)
		return fmt.Errorf("failed to parse vxnet %q network %q: %v", nic.VxNet.ID, nic.VxNet.Network, err)
	}
	gateWay := net.ParseIP(nic.VxNet.GateWay)
	netIF := &current.Interface{Name: args.IfName, Mac: nic.HardwareAddr, Sandbox: args.ContainerID}
	ipConfig := &current.IPConfig{Address: net.IPNet{IP: net.ParseIP(nic.Address), Mask: ipNet.Mask}, Interface: 0, Version: "4", Gateway: gateWay}
	//TODO support ipv6
	route := &types.Route{Dst: net.IPNet{IP: net.IPv4zero, Mask: net.IPMask(net.IPv4zero)}, GW: gateWay}
	result := &current.Result{Interfaces: []*current.Interface{netIF}, IPs: []*current.IPConfig{ipConfig}, Routes: []*types.Route{route}}
	err = netns.Do(func(_ ns.NetNS) error {
		nsIface, err := netlink.LinkByName(srcName)
		if err != nil {
			return fmt.Errorf("failed to get link by name %q: %v", srcName, err)
		}
		if err := netlink.LinkSetName(nsIface, args.IfName); err != nil {
			return fmt.Errorf("set link %s to name %s err: %v", nsIface.Attrs().HardwareAddr.String(), srcName, args.IfName)
		}
		return pkg.ConfigureIface(args.IfName, result)
	})
	if err != nil {
		deleteNic(nic.ID, nicProvider)
		return err
	}

	err = saveScratchNetConf(args.ContainerID, n.DataDir, nic)
	if err != nil {
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

func cmdDel(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	if args.Netns == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}

	nicProvider, err := provider.New(n.Provider, n.Config)
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
