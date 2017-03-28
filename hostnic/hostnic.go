package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containernetworking/cni/pkg/ns"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/provider"
	"net"
	"github.com/yunify/hostnic-cni/pkg"
)

const defaultDataDir = "/var/lib/cni/hostnic"

type NetConf struct {
	types.NetConf
	DataDir            string   `json:"dataDir"`
	Provider           string   `json:"provider"`
	ProviderConfigFile string   `json:"providerConfigFile"`
	VxNets             []string `json:"vxNets"`
	InstanceID         string   `json:"instanceID"`
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	netconf := &NetConf{DataDir: defaultDataDir}
	if err := json.Unmarshal(bytes, netconf); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	if netconf.InstanceID == "" {
		instanceID, err := loadInstanceID()
		if err != nil {
			return nil, err
		}
		netconf.InstanceID = instanceID
	}
	return netconf, nil
}

func loadInstanceID() (string, error) {
	content, err := ioutil.ReadFile("/etc/qingcloud/instance-id")
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func saveScratchNetConf(containerID, dataDir string, nic *pkg.HostNic) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create the multus data directory(%q): %v", dataDir, err)
	}

	path := filepath.Join(dataDir, containerID)
	data, err := json.Marshal(nic)
	if err != nil {
		return fmt.Errorf("failed to marshal nic %++v : %v", *nic, err )
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
		return nil, fmt.Errorf("failed to unmarshal nic data in the path(%q): %v", path, err )
	}
	return hostNic, err
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	nicProvider, err := provider.CreateNicProvider(n.Provider, n.ProviderConfigFile, n.VxNets)
	if err != nil {
		return err
	}
	nic, err := nicProvider.CreateNic(n.InstanceID)
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

	if err := netlink.LinkSetNsFd(iface, int(netns.Fd())); err != nil {
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

	nicProvider, err := provider.CreateNicProvider(n.Provider, n.ProviderConfigFile, n.VxNets)
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
	return deleteNic(nic.ID, nicProvider)
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.All)
}
