//
// =========================================================================
// Copyright (C) 2020 by Yunify, Inc...
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
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/coreos/go-iptables/iptables"
	"github.com/davecgh/go-spew/spew"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	klog "k8s.io/klog/v2"

	ipam2 "github.com/yunify/hostnic-cni/cmd/hostnic/ipam"
	constants "github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/log"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"golang.org/x/sys/unix"
)

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

const (
	defaultIfName = "veth1"
)

func setupContainerVeth(netns ns.NetNS, hostIfName, contIfName string, conf constants.NetConf, pr *current.Result) (*current.Interface, *current.Interface, error) {
	_, _, err := net.ParseCIDR(conf.Service)
	if conf.HostNicType == constants.HostNicPassThrough && err != nil {
		return nil, nil, fmt.Errorf("should config valid service: %v", err)
	}

	hostInterface := &current.Interface{}
	containerInterface := &current.Interface{}

	err = netns.Do(func(hostNS ns.NetNS) error {
		hostVeth, contVeth, err := ip.SetupVethWithName(contIfName, hostIfName, conf.MTU, hostNS)
		if err != nil {
			logrus.Errorf("SetupVethWithName(contIfName:%s,hostIfName:%s) error:%v", contIfName, hostIfName, err)
			return err
		}

		_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv4/conf/%s/rp_filter", hostVeth.Name), "0")
		_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv4/conf/%s/rp_filter", contVeth.Name), "0")

		hostInterface.Name = hostVeth.Name
		hostInterface.Mac = hostVeth.HardwareAddr.String()
		containerInterface.Name = contVeth.Name
		containerInterface.Mac = contVeth.HardwareAddr.String()
		containerInterface.Sandbox = netns.Path()
		logrus.Infof("containerInterface info:%s", spew.Sdump(containerInterface))

		if conf.HostNicType != constants.HostNicPassThrough {
			pr.Interfaces = []*current.Interface{containerInterface, hostInterface}
			if err = ipam.ConfigureIface(contIfName, pr); err != nil {
				logrus.Errorf("ConfigureIface %s error:%v", contIfName, err)
				return err
			}
		}

		neigh := &netlink.Neigh{
			LinkIndex:    contVeth.Index,
			IP:           net.ParseIP("169.254.1.1"),
			HardwareAddr: hostVeth.HardwareAddr,
			State:        netlink.NUD_PERMANENT,
			Family:       syscall.AF_INET,
		}
		err = netlink.NeighAdd(neigh)
		if err != nil && !os.IsExist(err) {
			logrus.Errorf("failed to add permanent arp for container veth [%s : %v]", spew.Sdump(neigh), err)
			return fmt.Errorf("failed to add permanent arp for container veth [%s : %v]", spew.Sdump(neigh), err)
		}

		var (
			routes []netlink.Route
		)
		routes = append(routes, netlink.Route{
			LinkIndex: contVeth.Index,
			Dst: &net.IPNet{
				IP:   net.ParseIP("169.254.1.1"),
				Mask: net.CIDRMask(32, 32),
			},
			Scope: netlink.SCOPE_LINK,
		})
		if conf.HostNicType == constants.HostNicPassThrough {
			_, serviceNet, _ := net.ParseCIDR(conf.Service)
			routes = append(routes, netlink.Route{
				LinkIndex: contVeth.Index,
				Dst:       serviceNet,
				Scope:     netlink.SCOPE_UNIVERSE,
				Gw:        net.ParseIP("169.254.1.1"),
				Src:       pr.IPs[0].Address.IP,
			})
		} else {
			routes = append(routes, netlink.Route{
				LinkIndex: contVeth.Index,
				Dst: &net.IPNet{
					IP:   net.IPv4zero,
					Mask: net.CIDRMask(0, 32),
				},
				Scope: netlink.SCOPE_UNIVERSE,
				Gw:    net.ParseIP("169.254.1.1"),
				Src:   pr.IPs[0].Address.IP,
			})
		}
		for _, r := range routes {
			if err := netlink.RouteAdd(&r); err != nil && !os.IsExist(err) {
				logrus.Errorf("failed to add route %s, err=%v", spew.Sdump(r), err)
				return fmt.Errorf("failed to add route %s, err=%v", spew.Sdump(r), err)
			}
		}

		return nil
	})

	return hostInterface, containerInterface, err
}

func setupHostVeth(conf constants.NetConf, vethName string, msg *rpc.IPAMMessage, result *current.Result) error {
	// hostVeth moved namespaces and may have a new ifindex
	hostVeth, err := netlink.LinkByName(vethName)
	if err != nil {
		logrus.Errorf("failed to lookup link by name %q: %v", vethName, err)
		return fmt.Errorf("failed to lookup link by name %q: %v", vethName, err)
	}

	_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv4/conf/%s/rp_filter", vethName), "0")

	podIP := &net.IPNet{
		IP:   result.IPs[0].Address.IP,
		Mask: net.CIDRMask(32, 32),
	}
	route := &netlink.Route{
		LinkIndex: hostVeth.Attrs().Index,
		Scope:     netlink.SCOPE_LINK,
		Dst:       podIP,
		Table:     constants.MainTable,
	}
	err = netlink.RouteAdd(route)
	if err != nil && !os.IsExist(err) {
		logrus.Errorf("failed to add route %s to pod, err=%+v", spew.Sdump(route), err)
		return fmt.Errorf("failed to add route %s to pod, err=%+v", spew.Sdump(route), err)
	}

	return networkutils.NetworkHelper.SetupPodNetwork(msg.Nic, msg.IP)
}

func cmdAddVeth(conf constants.NetConf, hostIfName, contIfName string, msg *rpc.IPAMMessage, result *current.Result, netns ns.NetNS) error {
	link, err := netlink.LinkByName(hostIfName)
	if link != nil {
		logrus.Infof("cmdAddVeth LinkByName found intf %s, link type=%s, link attr=%s,err=%v", hostIfName, link.Type(), spew.Sdump(link.Attrs()), err)
		return nil
	}

	hostInterface, _, err := setupContainerVeth(netns, hostIfName, contIfName, conf, result)
	if err != nil {
		logrus.Errorf("setupContainerVeth(hostIfName:%s,contIfName:%s) error:%v", hostIfName, contIfName, err)
		return err
	}
	logrus.Infof("setupContainerVeth(hostIfName:%s,contIfName:%s) success, hostInterface=%s", hostIfName, contIfName, spew.Sdump(hostInterface))

	if err = setupHostVeth(conf, hostInterface.Name, msg, result); err != nil {
		logrus.Errorf("setupHostVeth %s error:%v", hostInterface.Name, err)
		return err
	}
	logrus.Infof("setupHostVeth %s success!", hostInterface.Name)

	return err
}

func moveLinkIn(hostDev netlink.Link, containerNs ns.NetNS, ifName string, pr *current.Result) (netlink.Link, error) {
	containerInterface := &current.Interface{}

	if err := netlink.LinkSetNsFd(hostDev, int(containerNs.Fd())); err != nil {
		return nil, err
	}

	var contDev netlink.Link
	if err := containerNs.Do(func(_ ns.NetNS) error {
		var err error
		contDev, err = netlink.LinkByName(hostDev.Attrs().Name)
		if err != nil {
			return fmt.Errorf("failed to find %q: %v", hostDev.Attrs().Name, err)
		}

		// Save host device name into the container device's alias property
		if err := netlink.LinkSetAlias(contDev, contDev.Attrs().Name); err != nil {
			return fmt.Errorf("failed to set alias to %q: %v", contDev.Attrs().Name, err)
		}
		klog.Infof("set nic %s alias to %s", contDev.Attrs().Name, contDev.Attrs().Alias)

		// Rename container device to respect args.IfName
		if err := netlink.LinkSetName(contDev, ifName); err != nil {
			return fmt.Errorf("failed to rename device %q to %q: %v", hostDev.Attrs().Name, ifName, err)
		}
		// Retrieve link again to get up-to-date name and attributes
		contDev, err = netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to find %q: %v", ifName, err)
		}

		for _, ipc := range pr.IPs {
			ipc.Interface = current.Int(0)
		}
		containerInterface.Name = contDev.Attrs().Name
		containerInterface.Mac = contDev.Attrs().HardwareAddr.String()
		containerInterface.Sandbox = containerNs.Path()
		pr.Interfaces = []*current.Interface{containerInterface}
		if err = ipam.ConfigureIface(ifName, pr); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return contDev, nil
}

func cmdAddPassThrough(conf constants.NetConf, hostIfName, contIfName string, msg *rpc.IPAMMessage, result *current.Result, netns ns.NetNS) error {
	if len(result.Interfaces) == 0 {
		logrus.Errorf("IPAM plugin returned missing Interface config")
		return errors.New("IPAM plugin returned missing Interface config")
	}

	if conf.Service == "" {
		logrus.Errorf("Netconf should config service_cidr")
		return fmt.Errorf("Netconf should config service_cidr")
	}

	if result.Interfaces[0].Mac == "" {
		logrus.Infof("mac is empty, return nil")
		return nil
	}
	hostDev, err := networkutils.NetworkHelper.LinkByMacAddr(result.Interfaces[0].Mac)
	if err != nil {
		logrus.Errorf("failed to find host device: %v", err)
		return fmt.Errorf("failed to find host device: %v", err)
	}

	_, err = moveLinkIn(hostDev, netns, contIfName, result)
	if err != nil {
		logrus.Errorf("failed to move link %v", err)
		return fmt.Errorf("failed to move link %v", err)
	}

	hostInterface, _, err := setupContainerVeth(netns, hostIfName, defaultIfName, conf, result)
	if err != nil {
		logrus.Errorf("setupContainerVeth(hostIfName:%s,defaultIfName:%s) error:%v", hostIfName, defaultIfName, err)
		return err
	}
	logrus.Infof("setupContainerVeth(hostIfName:%s,defaultIfName:%s) success, hostInterface=%s", hostIfName, defaultIfName, spew.Sdump(hostInterface))

	if err = setupHostVeth(conf, hostInterface.Name, msg, result); err != nil {
		logrus.Errorf("setupHostVeth %s error:%v", hostInterface.Name, err)
		return err
	}
	logrus.Infof("setupHostVeth %ss success!", hostInterface.Name)

	return err
}

func checkConf(conf *constants.NetConf) error {
	if conf.LogLevel == 0 {
		conf.LogLevel = int(logrus.InfoLevel)
	}
	log.Setup(&log.LogOptions{
		Level: conf.LogLevel,
		File:  conf.LogFile,
	})

	if conf.HostVethPrefix == "" {
		conf.HostVethPrefix = constants.HostNicPrefix
	}

	if conf.MTU == 0 {
		conf.MTU = 1500
	}

	if conf.HostNicType != constants.HostNicPassThrough {
		conf.HostNicType = constants.HostNicVeth
	}

	if conf.RT2Pod == 0 {
		conf.RT2Pod = constants.MainTable
	}

	if conf.Interface == "" {
		conf.Interface = constants.DefaultPrimaryNic
	}

	if conf.NatMark == "" {
		conf.NatMark = constants.DefaultNatMark
	}

	err := checkIptables(conf)
	if err != nil {
		return fmt.Errorf("failed to checkIptables, err=%v", err)
	}

	return nil
}

func checkIptables(conf *constants.NetConf) error {
	link, err := netlink.LinkByName(conf.Interface)
	if err != nil {
		return fmt.Errorf("LinkByName %s error: %v", conf.Interface, err)
	}
	addrs, err := netlink.AddrList(link, unix.AF_INET)
	if err != nil {
		return err
	}
	if len(addrs) <= 0 {
		return fmt.Errorf("primary nic should have ip address")
	}
	nodeIP := addrs[0].IP.String()

	ipt, err := iptables.New()
	if err != nil {
		return err
	}

	//For iptables mode nodeport
	ipt.NewChain("mangle", constants.ManglePreroutingChain)
	rule := []string{"-j", constants.ManglePreroutingChain}
	exist, err := ipt.Exists("mangle", "PREROUTING", rule...)
	if err != nil {
		return fmt.Errorf("failed to check rule %v, err=%v", rule, err)
	}
	if !exist {
		err = ipt.Append("mangle", "PREROUTING", rule...)
		if err != nil {
			return fmt.Errorf("failed to add rule %v, err=%v", rule, err)
		}
	}
	//iptables -t mangle -A PREROUTING -j MARK --set-xmark 0x100000/0x100000 -m conntrack --ctorigdst 172.22.0.21
	rule = []string{"-j", "MARK", "--set-xmark", conf.NatMark + "/" + conf.NatMark, "-m", "conntrack", "--ctorigdst", nodeIP}
	exist, err = ipt.Exists("mangle", constants.ManglePreroutingChain, rule...)
	if err != nil {
		return fmt.Errorf("failed to check rule %v, err=%v", rule, err)
	}
	if !exist {
		err = ipt.Append("mangle", constants.ManglePreroutingChain, rule...)
		if err != nil {
			return fmt.Errorf("failed to add rule %v, err=%v", rule, err)
		}
	}

	ipt.NewChain("mangle", constants.MangleOutputChain)
	rule = []string{"-j", constants.MangleOutputChain}
	exist, err = ipt.Exists("mangle", "OUTPUT", rule...)
	if err != nil {
		return fmt.Errorf("failed to check rule %v, err=%v", rule, err)
	}
	if !exist {
		err = ipt.Append("mangle", "OUTPUT", rule...)
		if err != nil {
			return fmt.Errorf("failed to add rule %v, err=%v", rule, err)
		}
	}
	//iptables -t mangle -A OUTPUT -j MARK --set-xmark 0x100000/0x100000 -m conntrack --ctorigdst 10.233.0.0/16 --ctreplsrc 172.22.0.21
	rule = []string{"-j", "MARK", "--set-xmark", conf.NatMark + "/" + conf.NatMark, "-m", "conntrack", "--ctorigdst", conf.Service}
	exist, err = ipt.Exists("mangle", constants.MangleOutputChain, rule...)
	if err != nil {
		return fmt.Errorf("failed to check rule %v, err=%v", rule, err)
	}
	if !exist {
		err = ipt.Append("mangle", constants.MangleOutputChain, rule...)
		if err != nil {
			return fmt.Errorf("failed to add rule %v, err=%v", rule, err)
		}
	}

	return nil
}

func cmdAdd(args *skel.CmdArgs) error {
	var err error

	logrus.Infof("cmdAdd args %+v", args)
	defer func() {
		klog.Infof("cmdAdd for %s rst: %v", args.ContainerID, err)
	}()

	conf := constants.NetConf{}
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf %s: %v", spew.Sdump(args), err)
	}
	if err = checkConf(&conf); err != nil {
		return fmt.Errorf("failed to checkConf: %v", err)
	}
	logrus.Infof("cmdAdd for %s load and check netconf success, conf=%+v", args.ContainerID, conf)

	// run the IPAM plugin and get back the config to apply
	ipamMsg, result, err := ipam2.AddrAlloc(args)
	if err != nil {
		return fmt.Errorf("failed to alloc addr: %v", err)
	}
	logrus.Infof("cmdAdd for %s AddrAlloc success, ipamMsg=%s,result=%s", args.ContainerID, spew.Sdump(ipamMsg), spew.Sdump(result))

	podInfo := ipamMsg.Args
	// podInfo.NicType is from annotation
	conf.HostNicType = podInfo.NicType

	if err = ip.EnableForward(result.IPs); err != nil {
		return fmt.Errorf("could not enable IP forwarding: %v", err)
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	hostIfName := generateHostVethName(conf.HostVethPrefix, podInfo.Namespace, podInfo.Name)
	contIfName := args.IfName
	logrus.Infof("HostNicType=%s,hostIfName=%s,contIfName=%s", conf.HostNicType, hostIfName, contIfName)

	switch conf.HostNicType {
	case constants.HostNicPassThrough:
		logrus.Infof("go to excute cmdAddPassThrough")
		err = cmdAddPassThrough(conf, hostIfName, contIfName, ipamMsg, result, netns)
	default:
		logrus.Infof("go to excute cmdAddVeth")
		err = cmdAddVeth(conf, hostIfName, contIfName, ipamMsg, result, netns)
	}

	if err != nil {
		logrus.Errorf("add veth error:%v", err)
		return err
	} else {
		return types.PrintResult(result, conf.CNIVersion)
	}
}

func cmdDelVeth(contIfName string, netns ns.NetNS) error {
	// There is a netns so try to clean up. Delete can be called multiple times
	// so don't return an error if the device is already removed.
	// If the device isn't there then don't try to clean up PodIP masq either.
	err := netns.Do(func(_ ns.NetNS) error {
		var err error
		_, err = ip.DelLinkByNameAddr(contIfName)
		if err != nil {
			if err == ip.ErrLinkNotFound {
				return nil
			}
			return fmt.Errorf("failed to delete link %s: %v", contIfName, err)
		}
		return nil
	})

	return err
}

func moveLinkOut(containerNs ns.NetNS, ifName string) error {
	hostNs, err := ns.GetCurrentNS()
	if err != nil {
		return err
	}
	defer hostNs.Close()

	return containerNs.Do(func(_ ns.NetNS) error {
		dev, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to find %q: %v", ifName, err)
		}

		// Devices can be renamed only when down
		if err = netlink.LinkSetDown(dev); err != nil {
			return fmt.Errorf("failed to set %q down: %v", ifName, err)
		}

		// Rename device to it's original name
		if err = netlink.LinkSetName(dev, dev.Attrs().Alias); err != nil {
			return fmt.Errorf("failed to restore %q to original name %q: %v", ifName, dev.Attrs().Alias, err)
		}
		defer func() {
			if err != nil {
				// if moving device to host namespace fails, we should revert device name
				// to ifName to make sure that device can be found in retries
				_ = netlink.LinkSetName(dev, ifName)
			}
		}()

		if err = netlink.LinkSetNsFd(dev, int(hostNs.Fd())); err != nil {
			return fmt.Errorf("failed to move %q to host netns: %v", dev.Attrs().Alias, err)
		}
		return nil
	})
}

func cmdDelPassThrough(svcIfName, contIfName string, netns ns.NetNS) error {
	err := moveLinkOut(netns, contIfName)
	if err != nil {
		return err
	}

	err = ip.DelLinkByName(svcIfName)
	if err != nil && err == ip.ErrLinkNotFound {
		return nil
	}

	return err
}

func cmdDel(args *skel.CmdArgs) error {
	var err error

	klog.Infof("cmdDel args %v", args)
	defer func() {
		klog.Infof("cmdDel for %s rst: %v", args.ContainerID, err)
	}()

	conf := constants.NetConf{}
	if err = json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to load netconf: %v", err)
	}
	if err = checkConf(&conf); err != nil {
		return err
	}

	ipamMsg, err := ipam2.AddrUnalloc(args, true)
	if err != nil {
		if err == constants.ErrNicNotFound {
			return nil
		}
		return err
	}

	podInfo := ipamMsg.Args
	conf.HostNicType = podInfo.NicType
	contIfName := args.IfName
	svcIfName := generateHostVethName(conf.HostVethPrefix, podInfo.Namespace, podInfo.Name)

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			goto end
		}
		return err
	}
	defer netns.Close()

	switch conf.HostNicType {
	case constants.HostNicPassThrough:
		err = cmdDelPassThrough(svcIfName, contIfName, netns)
	default:
		err = cmdDelVeth(contIfName, netns)
	}

	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			goto end
		}
		return err
	}
end:
	_, err = ipam2.AddrUnalloc(args, false)
	if err == constants.ErrNicNotFound {
		return nil
	}
	return err
}

// generateHostVethName returns a name to be used on the host-side veth device.
func generateHostVethName(prefix, namespace, podname string) string {
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("%s.%s", namespace, podname)))
	return fmt.Sprintf("%s%s", prefix, hex.EncodeToString(h.Sum(nil))[:11])
}

func main() {
	networkutils.SetupNetworkHelper()
	skel.PluginMain(cmdAdd, nil, cmdDel, version.All, bv.BuildString("hostnic"))
}
