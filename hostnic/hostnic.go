package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containernetworking/cni/pkg/ip"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/provider"
	"net"
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

func saveScratchNetConf(containerID, dataDir string, netconf []byte) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create the multus data directory(%q): %v", dataDir, err)
	}

	path := filepath.Join(dataDir, containerID)

	err := ioutil.WriteFile(path, netconf, 0600)
	if err != nil {
		return fmt.Errorf("failed to write container data in the path(%q): %v", path, err)
	}

	return err
}

func consumeScratchNetConf(containerID, dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, containerID)
	defer os.Remove(path)

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read container data in the path(%q): %v", path, err)
	}

	return data, err
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
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	iface, err := linkByMacAddr(nic.HardwareAddr)
	if err != nil {
		return fmt.Errorf("failed to get link by MacAddr %q: %v", nic.HardwareAddr, err)
	}

	if err := netlink.LinkSetNsFd(iface, int(netns.Fd())); err != nil {
		return fmt.Errorf("failed to set namespace on link %q: %v", nic.HardwareAddr, err)
	}

	srcName := iface.Attrs().Name

	netIF := &current.Interface{Name: args.IfName, Mac: nic.HardwareAddr, Sandbox: args.ContainerID}
	ipConfig := &current.IPConfig{Address: net.IPNet{IP: net.ParseIP(nic.Address), Mask: nic.VxNet.Network.Mask}, Interface: 0, Version: "4", Gateway: nic.VxNet.GateWay}
	//TODO support ipv6
	route := &types.Route{Dst: net.IPNet{IP: net.IPv4zero, Mask: net.IPMask(net.IPv4zero)}, GW: nic.VxNet.GateWay}
	result := &current.Result{Interfaces: []*current.Interface{netIF}, IPs: []*current.IPConfig{ipConfig}, Routes: []*types.Route{route}}
	err = netns.Do(func(_ ns.NetNS) error {
		nsIface, err := netlink.LinkByName(srcName)
		if err != nil {
			return fmt.Errorf("failed to get link by name %q: %v", srcName, err)
		}
		if err := netlink.LinkSetName(nsIface, args.IfName); err != nil {
			return fmt.Errorf("set link %s to name %s err: %v", nsIface.Attrs().HardwareAddr.String(), srcName, args.IfName)
		}
		//return ipam.ConfigureIface(args.IfName, result)
		return configureIface(args.IfName, result)
	})
	if err != nil {
		return err
	}

	result.DNS = n.DNS
	return types.PrintResult(result, n.CNIVersion)
}

func configureIface(ifName string, res *current.Result) error {
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

func linkByMacAddr(macAddr string) (netlink.Link, error) {
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

func cmdDel(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	if args.Netns == "" {
		return nil
	}
	//netns, err := ns.GetNS(args.Netns)
	//hostNS, err := ns.GetCurrentNS()
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}

	nicProvider, err := provider.CreateNicProvider(n.Provider, n.ProviderConfigFile, n.VxNets)
	if err != nil {
		return err
	}

	return ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {
		ifName := args.IfName
		iface, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q: %v", ifName, err)
		}
		if err := netlink.LinkSetDown(iface); err != nil {
			return err
		}
		//TODO set link name to origin name.

		// move link to default ns
		//if err := netlink.LinkSetNsFd(iface, int(hostNS.Fd())); err != nil {
		//	return fmt.Errorf("failed to set namespace on link %q: %v", iface.Attrs().HardwareAddr.String(), err)
		//}

		nicID := iface.Attrs().HardwareAddr.String()
		if err = nicProvider.DeleteNic(nicID); err != nil {
			return fmt.Errorf("failed to delete %q, nic: %s: %v", ifName, nicID, err)
		}
		return nil
	})
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.All)
}
