package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/yunify/hostnic-cni/provider"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/containernetworking/cni/pkg/ipam"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/vishvananda/netlink"
)

const defaultDataDir = "/var/lib/cni/hostnic"

type NetConf struct {
	types.NetConf
	DataDir    string                 `json:"dataDir"`
	Provider  string 	`json:"provider"`
	ProviderConfigFile string	`json:"providerConfigFile"`
	VxNets     []string              `json:"VxNets"`
	InstanceID string `json:"instanceID"`
}


func loadNetConf(bytes []byte) (*NetConf, error) {
	netconf := &NetConf{DataDir:defaultDataDir}
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
	netIF := &current.Interface{Name:args.IfName, Mac: nic.HardwareAddr, Sandbox: args.ContainerID}
	ipConfig := &current.IPConfig{Address: nic.VxNet.Network, Interface:0, Version: "4", Gateway: nic.VxNet.GateWay}
	result := &current.Result{Interfaces:[]*current.Interface{netIF}, IPs:[]*current.IPConfig{ipConfig}}
	err = netns.Do(func(_ ns.NetNS) error {
		return ipam.ConfigureIface(args.IfName, result)
	})
	if err != nil {
		return err
	}

	result.DNS = n.DNS
	return types.PrintResult(result, n.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	if args.Netns == "" {
		return nil
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
		if err = netlink.LinkDel(iface); err != nil {
			return fmt.Errorf("failed to delete %q: %v", ifName, err)
		}
		nicID := iface.Attrs().HardwareAddr.String()
		if err = nicProvider.DeleteNic(nicID); err != nil{
			return fmt.Errorf("failed to delete %q, nic: %s: %v", ifName, nicID, err)
		}
		return nil
	})
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.All)
}
