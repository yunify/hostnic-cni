package networkutils

import (
	"fmt"
	"net"
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/vishvananda/netlink"
	fakenetlink "github.com/yunify/hostnic-cni/pkg/netlinkwrapper/fake"
	"github.com/yunify/hostnic-cni/pkg/networkutils/iptables"
)

const (
	testMAC          = "01:23:45:67:89:ab"
	testMAC1         = "01:23:45:67:89:a0"
	testMAC2         = "01:23:45:67:89:a1"
	testIP           = "10.0.10.10"
	testContVethName = "eth0"
	testHostVethName = "yunify-eth0"
	testFD           = 10
	testnetnsPath    = "/proc/1234/netns"
	testTable        = 10
	testnicIP        = "10.10.10.20"
	testeniMAC       = "01:23:45:67:89:ab"
	testnicSubnet    = "10.10.0.0/16"
	// Default MTU of NIC and veth
	testMTU = 9001
)

var (
	_, testVPC, _ = net.ParseCIDR(testnicSubnet)
	testNICIP     = net.ParseIP(testnicIP)
)

var _ = Describe("Networkutils", func() {
	It("Should get proper vpn net", func() {
		Expect(GetVPNNet("192.168.0.2")).To(Equal("192.168.255.254/32"))
		Expect(GetVPNNet("172.16.1.2")).To(Equal("172.16.255.254/32"))
	})
	var setProcSys = func(string, string) error {
		return nil
	}
	It("Should set up the hostnetwork properly when supporting nodePort", func() {
		iptablesData := iptables.NewFakeIPTables()
		netlinkData := fakenetlink.NewFakeNetlink()

		eth0 := &netlink.Device{
			LinkAttrs: netlink.NewLinkAttrs(),
		}
		eth0.Name = "eth0"
		eth0.HardwareAddr = net.HardwareAddr(testMAC)
		netlinkData.LinkAdd(eth0)
		os.Setenv(envNodePortSupport, "true")
		api := NewFakeNetworkAPI(netlinkData, iptablesData, netlinkData.FindPrimaryInterfaceName, setProcSys)
		//prepare setup network parameter
		testSubnet1 := "10.10.1.0/24"
		testSubnet2 := "10.10.2.0/24"
		err := api.SetupHostNetwork(testVPC, []*string{&testSubnet1, &testSubnet2}, testMAC, &testNICIP)
		Expect(err).ShouldNot(HaveOccurred())

		//rule check
		mainNICRule := netlink.NewRule()
		mainNICRule.Mark = defaultConnmark
		mainNICRule.Mask = defaultConnmark
		mainNICRule.Table = mainRoutingTable
		mainNICRule.Priority = hostRulePriority
		rules, _ := netlinkData.RuleList(0)
		Expect(rules[0]).To(Equal(*mainNICRule))
		//nat chains check
		Expect(iptablesData.Data["nat"]).To(MatchAllKeys(
			Keys{
				"POSTROUTING":            HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-0": HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-1": HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-2": HaveLen(1),
			},
		))
		Expect(iptablesData.Data["nat"]["POSTROUTING"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD SNAT CHAIN", "-j", "QINGCLOUD-SNAT-CHAIN-0"}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-0"][0].Rule).To(Equal([]string{
			"!", "-d", "10.10.1.0/24", "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-1",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-1"][0].Rule).To(Equal([]string{
			"!", "-d", "10.10.2.0/24", "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-2",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-2"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD, SNAT",
			"-m", "addrtype", "!", "--dst-type", "LOCAL",
			"-j", "SNAT", "--to-source", testnicIP, "--random"}))

		// filter chain check
		Expect(iptablesData.Data["filter"]).To(MatchAllKeys(
			Keys{
				"FORWARD": HaveLen(2),
			},
		))
		Expect(iptablesData.Data["filter"]["FORWARD"][0].Rule).To(Equal([]string{"-i", "nic+", "-j", "ACCEPT"}))
		Expect(iptablesData.Data["filter"]["FORWARD"][1].Rule).To(Equal([]string{"-o", "nic+", "-j", "ACCEPT"}))

		// mangle chain check
		Expect(iptablesData.Data["mangle"]).To(MatchAllKeys(
			Keys{
				"PREROUTING": HaveLen(2),
			},
		))
		Expect(iptablesData.Data["mangle"]["PREROUTING"][0].Rule).To(Equal([]string{
			"-m", "comment", "--comment", "QINGCLOUD, primary NIC",
			"-i", "eth0",
			"-m", "addrtype", "--dst-type", "LOCAL", "--limit-iface-in",
			"-j", "CONNMARK", "--set-mark", fmt.Sprintf("%#x/%#x", defaultConnmark, defaultConnmark),
		}))

		Expect(iptablesData.Data["mangle"]["PREROUTING"][1].Rule).To(Equal([]string{
			"-m", "comment", "--comment", "QINGCLOUD, primary NIC",
			"-i", "nic+", "-j", "CONNMARK", "--restore-mark", "--mask", fmt.Sprintf("%#x", defaultConnmark),
		}))
	})

	It("Should set up the hostnetwork properly without supporting nodePort", func() {
		iptablesData := iptables.NewFakeIPTables()
		netlinkData := fakenetlink.NewFakeNetlink()

		eth0 := &netlink.Device{
			LinkAttrs: netlink.NewLinkAttrs(),
		}
		eth0.Name = "eth0"
		eth0.HardwareAddr = net.HardwareAddr(testMAC)
		netlinkData.LinkAdd(eth0)
		os.Setenv(envNodePortSupport, "false")
		api := NewFakeNetworkAPI(netlinkData, iptablesData, netlinkData.FindPrimaryInterfaceName, setProcSys)
		//prepare setup network parameter
		testSubnet1 := "10.10.1.0/24"
		testSubnet2 := "10.10.2.0/24"
		err := api.SetupHostNetwork(testVPC, []*string{&testSubnet1, &testSubnet2}, testMAC, &testNICIP)
		Expect(err).ShouldNot(HaveOccurred())

		//nat chains check
		Expect(iptablesData.Data["nat"]).To(MatchAllKeys(
			Keys{
				"POSTROUTING":            HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-0": HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-1": HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-2": HaveLen(1),
			},
		))
		Expect(iptablesData.Data["nat"]["POSTROUTING"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD SNAT CHAIN", "-j", "QINGCLOUD-SNAT-CHAIN-0"}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-0"][0].Rule).To(Equal([]string{
			"!", "-d", "10.10.1.0/24", "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-1",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-1"][0].Rule).To(Equal([]string{
			"!", "-d", "10.10.2.0/24", "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-2",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-2"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD, SNAT",
			"-m", "addrtype", "!", "--dst-type", "LOCAL",
			"-j", "SNAT", "--to-source", testnicIP, "--random"}))

		// filter chain check
		Expect(iptablesData.Data["filter"]).To(MatchAllKeys(
			Keys{
				"FORWARD": HaveLen(2),
			},
		))
		Expect(iptablesData.Data["filter"]["FORWARD"][0].Rule).To(Equal([]string{"-i", "nic+", "-j", "ACCEPT"}))
		Expect(iptablesData.Data["filter"]["FORWARD"][1].Rule).To(Equal([]string{"-o", "nic+", "-j", "ACCEPT"}))

		// mangle chain check
		Expect(iptablesData.Data["mangle"]).To(HaveLen(0))
	})

	It("Should setup nic network properly", func() {
		iptablesData := iptables.NewFakeIPTables()
		netlinkData := fakenetlink.NewFakeNetlink()

		eth0 := &netlink.Device{
			LinkAttrs: netlink.NewLinkAttrs(),
		}
		eth0.Name = "eth0"
		eth0.HardwareAddr, _ = net.ParseMAC(testMAC)
		netlinkData.LinkAdd(eth0)

		eth1 := &netlink.Device{
			LinkAttrs: netlink.NewLinkAttrs(),
		}
		eth1.Name = "eth1"
		eth1.HardwareAddr, _ = net.ParseMAC(testMAC1)
		netlinkData.LinkAdd(eth1)
		api := NewFakeNetworkAPI(netlinkData, iptablesData, netlinkData.FindPrimaryInterfaceName, setProcSys)
		Expect(api.SetupNICNetwork(testIP, testMAC1, 2, "10.0.10.0/24")).ShouldNot(HaveOccurred())
		Expect(eth1.MTU).To(Equal(ethernetMTU))
		Expect(eth1.Flags | net.FlagUp).To(Equal(eth1.Flags))

		Expect(netlinkData.Routes).To(HaveLen(2))
		Expect(netlinkData.Routes["<nil>+0.0.0.0/0"].String()).To(Equal("{Ifindex: 0 Dst: 0.0.0.0/0 Src: <nil> Gw: 10.0.10.1 Flags: [] Table: 2}"))
		Expect(netlinkData.Routes["<nil>+10.0.10.1/32"].String()).To(Equal("{Ifindex: 0 Dst: 10.0.10.1/32 Src: <nil> Gw: <nil> Flags: [] Table: 2}"))
	})

	It("Can get rule list by source", func() {
		iptablesData := iptables.NewFakeIPTables()
		netlinkData := fakenetlink.NewFakeNetlink()

		eth0 := &netlink.Device{
			LinkAttrs: netlink.NewLinkAttrs(),
		}
		eth0.Name = "eth0"
		eth0.HardwareAddr, _ = net.ParseMAC(testMAC)
		netlinkData.LinkAdd(eth0)
		api := NewFakeNetworkAPI(netlinkData, iptablesData, netlinkData.FindPrimaryInterfaceName, setProcSys)

		_, sourceIP, _ := net.ParseCIDR("10.10.10.0/24")
		for index := 1; index < 10; index++ {
			rule := netlink.NewRule()
			if index%2 == 0 {
				_, rule.Src, _ = net.ParseCIDR(fmt.Sprintf("10.10.%d0.0/24", index))
			} else {
				rule.Src = sourceIP
			}
			_, rule.Dst, _ = net.ParseCIDR(fmt.Sprintf("10.11.10.%d/32", index))
			netlinkData.RuleAdd(rule)
		}

		ruleList, _ := api.GetRuleList()
		Expect(ruleList).To(HaveLen(9))
		rules, err := api.GetRuleListBySrc(ruleList, *sourceIP)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(rules).To(HaveLen(5))
	})
})

//TODO: add externalSNAT test
