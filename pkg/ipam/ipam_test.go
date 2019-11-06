package ipam

import (
	"net"
	"os"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/k8sclient"
	fakenetlink "github.com/yunify/hostnic-cni/pkg/netlinkwrapper/fake"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/networkutils/iptables"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	nodeName      = "fake-node"
	RouterID      = "fake-vpc"
	primaryIntMac = "ff:ff:ff:ff:ff:ff"
	primaryIPNet  = "192.168.1.0/24"
	primaryIP     = "192.168.1.2"
)

var (
	clientset         kubernetes.Interface
	iptablesData      *iptables.FakeIPTables
	netlinkData       *fakenetlink.FakeNetlink
	qcapi             *qcclient.FakeQingCloudAPI
	fakeNetworkClient networkutils.NetworkAPIs

	nodeVxNet = &types.VxNet{
		ID:       "vxnet-main",
		Name:     "main",
		RouterID: RouterID,
	}
	nodePrimaryNIC = &types.HostNic{
		ID:           primaryIntMac,
		VxNet:        nodeVxNet,
		DeviceNumber: 0,
		IsPrimary:    true,
		Address:      primaryIP,
	}
)

var _ = Describe("Ipam", func() {
	BeforeEach(func() {
		_, nodeVxNet.Network, _ = net.ParseCIDR(primaryIPNet)
		os.Setenv(k8sclient.NodeNameEnvKey, nodeName)
		_, n, _ := net.ParseCIDR("192.168.0.0/16")
		vpc := &types.VPC{
			Network: n,
			ID:      RouterID,
			VxNets:  make([]*types.VxNet, 0),
		}

		qcapi = qcclient.NewFakeQingCloudAPI(nodeName, vpc)
		qcapi.VxNets["vxnet-main"] = nodeVxNet
		qcapi.Nics[primaryIntMac] = nodePrimaryNIC
		iptablesData = iptables.NewFakeIPTables()
		netlinkData = fakenetlink.NewFakeNetlink()
		fakeNetworkClient = networkutils.NewFakeNetworkAPI(netlinkData, iptablesData, netlinkData.FindPrimaryInterfaceName, func(string, string) error { return nil })
	})

	It("will choose right ip net", func() {
		_, ipnet, _ := net.ParseCIDR("192.168.0.0/16")
		vxnets := make([]*types.VxNet, 4)
		_, ipnet1, _ := net.ParseCIDR("192.168.1.0/24")
		_, ipnet2, _ := net.ParseCIDR("192.168.2.0/24")
		_, ipnet3, _ := net.ParseCIDR("192.168.3.0/24")
		_, ipnet4, _ := net.ParseCIDR("192.168.5.0/24")
		vxnets[0] = &types.VxNet{
			Network: ipnet1,
		}
		vxnets[1] = &types.VxNet{
			Network: ipnet2,
		}
		vxnets[2] = &types.VxNet{
			Network: ipnet3,
		}
		vxnets[3] = &types.VxNet{
			Network: ipnet4,
		}
		_, result, _ := net.ParseCIDR("192.168.4.0/24")

		ip := chooseIPFromVxnet(*ipnet, vxnets)
		Expect(*ip).To(Equal(*result))
	})

	It("Should be ok to set up ipam in a pure environment", func() {
		//prepare k8s
		node := &corev1.Node{}
		node.Name = nodeName
		clientset = fake.NewSimpleClientset(node)
		prepareCloud := func(config *qcclient.LabelResourceConfig) (qcclient.QingCloudAPI, error) {
			return qcapi, nil
		}
		ipamd := NewFakeIPAM(fakeNetworkClient, clientset, prepareCloud)
		stopCh := make(chan struct{})
		Expect(ipamd.StartIPAMD(stopCh)).ShouldNot(HaveOccurred())
		defer func() {
			stopCh <- struct{}{}
		}()
		Expect(qcapi.VxNets).To(HaveLen(2))
		node, err := ipamd.K8sClient.GetCurrentNode()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(node.Annotations).To(HaveKey(NodeAnnotationVxNet))
		podVxnet, _ := qcapi.GetVxNet(node.Annotations[NodeAnnotationVxNet])
		Expect(podVxnet.Network).ShouldNot(Equal(nodeVxNet.Network))

		//test setup hostnetwork

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
			"!", "-d", nodeVxNet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-1",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-1"][0].Rule).To(Equal([]string{
			"!", "-d", podVxnet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-2",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-2"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD, SNAT",
			"-m", "addrtype", "!", "--dst-type", "LOCAL",
			"-j", "SNAT", "--to-source", primaryIP, "--random"}))
	})

	It("Should work when there are some pods existing", func() {

		node := &corev1.Node{}
		node.Name = nodeName
		node.Annotations = map[string]string{NodeAnnotationVxNet: "vxnet-pod"}
		//prepare pod
		pod1 := &corev1.Pod{}
		pod1.Name = "pod1"
		pod1.Namespace = "ns1"
		pod1.Spec.NodeName = nodeName
		pod1.Status.PodIP = "192.168.2.2"
		pod1.Status.ContainerStatuses = []corev1.ContainerStatus{
			corev1.ContainerStatus{
				ContainerID: "container1",
			},
		}
		pod2 := &corev1.Pod{}
		pod2.Name = "pod2"
		pod2.Namespace = "ns1"
		pod2.Spec.NodeName = nodeName
		pod2.Status.PodIP = "192.168.2.3"
		pod2.Status.ContainerStatuses = []corev1.ContainerStatus{
			corev1.ContainerStatus{
				ContainerID: "container2",
			},
		}
		clientset = fake.NewSimpleClientset(node, pod1, pod2)

		//prepare nics
		podVxNet := &types.VxNet{
			ID:       "vxnet-pod",
			Name:     "pod",
			RouterID: RouterID,
		}
		_, podVxNet.Network, _ = net.ParseCIDR("192.168.2.0/24")
		nic1Mac := "aa:aa:aa:aa:aa:aa"
		nic1 := &types.HostNic{
			ID:           nic1Mac,
			VxNet:        podVxNet,
			HardwareAddr: nic1Mac,
			Address:      "192.168.2.2",
			DeviceNumber: 2,
		}
		nic2Mac := "bb:bb:bb:bb:bb:bb"
		nic2 := &types.HostNic{
			ID:           nic2Mac,
			VxNet:        podVxNet,
			HardwareAddr: nic1Mac,
			Address:      "192.168.2.3",
			DeviceNumber: 3,
		}
		qcapi.Nics[nic1Mac] = nic1
		qcapi.Nics[nic2Mac] = nic2
		qcapi.VxNets[podVxNet.ID] = podVxNet

		//prepare local nics
		eth1 := &netlink.Device{
			LinkAttrs: netlink.NewLinkAttrs(),
		}
		eth1.Name = "eth1"
		eth1.HardwareAddr, _ = net.ParseMAC(nic1Mac)
		netlinkData.LinkAdd(eth1)
		//prepare local nics
		eth2 := &netlink.Device{
			LinkAttrs: netlink.NewLinkAttrs(),
		}
		eth2.Name = "eth2"
		eth2.HardwareAddr, _ = net.ParseMAC(nic2Mac)
		netlinkData.LinkAdd(eth2)

		prepareCloud := func(config *qcclient.LabelResourceConfig) (qcclient.QingCloudAPI, error) {
			return qcapi, nil
		}
		ipamd := NewFakeIPAM(fakeNetworkClient, clientset, prepareCloud)
		stopCh := make(chan struct{})
		Expect(ipamd.StartIPAMD(stopCh)).ShouldNot(HaveOccurred())
		defer func() {
			stopCh <- struct{}{}
		}()
		//test setup hostnetwork

		Expect(iptablesData.Data["nat"]).To(MatchAllKeys(
			Keys{
				"POSTROUTING":            HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-0": HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-1": HaveLen(1),
				"QINGCLOUD-SNAT-CHAIN-2": HaveLen(1),
			},
		))
		Expect(iptablesData.Data["nat"]["POSTROUTING"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD SNAT CHAIN", "-j", "QINGCLOUD-SNAT-CHAIN-0"}))
		orMatch := Or(Equal([]string{
			"!", "-d", nodeVxNet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-1",
		}), Equal([]string{
			"!", "-d", podVxNet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-2",
		}), Equal([]string{
			"!", "-d", nodeVxNet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-2",
		}), Equal([]string{
			"!", "-d", podVxNet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-1",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-0"][0].Rule).To(orMatch)
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-1"][0].Rule).To(orMatch)
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-0"][0].Rule).ShouldNot(Equal(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-1"][0].Rule))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-2"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD, SNAT",
			"-m", "addrtype", "!", "--dst-type", "LOCAL",
			"-j", "SNAT", "--to-source", primaryIP, "--random"}))

		Eventually(func() int { return ipamd.dataStore.GetNICInfos().TotalIPs }, time.Second*20, time.Second*4).Should(Equal(2))
		Expect(ipamd.dataStore.GetNICInfos().AssignedIPs).To(Equal(2))
	})

	It("Should add a nic when starting pooling", func() {
		node := &corev1.Node{}
		node.Name = nodeName
		clientset = fake.NewSimpleClientset(node)
		qcapi.AfterCreatingNIC = func(nic *types.HostNic) error {
			eth1 := &netlink.Device{
				LinkAttrs: netlink.NewLinkAttrs(),
			}
			eth1.Name = nic.ID
			eth1.HardwareAddr, _ = net.ParseMAC(nic.ID)
			eth1.Index = nic.DeviceNumber
			netlinkData.LinkAdd(eth1)
			return nil
		}

		prepareCloud := func(config *qcclient.LabelResourceConfig) (qcclient.QingCloudAPI, error) {
			return qcapi, nil
		}

		ipamd := NewFakeIPAM(fakeNetworkClient, clientset, prepareCloud)
		stopCh := make(chan struct{})
		Expect(ipamd.StartIPAMD(stopCh)).ShouldNot(HaveOccurred())
		defer func() {
			stopCh <- struct{}{}
		}()
		Expect(qcapi.VxNets).To(HaveLen(2))
		node, err := ipamd.K8sClient.GetCurrentNode()
		Expect(err).ShouldNot(HaveOccurred())
		Expect(node.Annotations).To(HaveKey(NodeAnnotationVxNet))
		podVxnet, _ := qcapi.GetVxNet(node.Annotations[NodeAnnotationVxNet])
		Expect(podVxnet.Network).ShouldNot(Equal(nodeVxNet.Network))

		//test setup hostnetwork

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
			"!", "-d", nodeVxNet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-1",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-1"][0].Rule).To(Equal([]string{
			"!", "-d", podVxnet.Network.String(), "-m", "comment", "--comment", "QINGCLOUD SNAT CHAN", "-j", "QINGCLOUD-SNAT-CHAIN-2",
		}))
		Expect(iptablesData.Data["nat"]["QINGCLOUD-SNAT-CHAIN-2"][0].Rule).To(Equal([]string{"-m", "comment", "--comment", "QINGCLOUD, SNAT",
			"-m", "addrtype", "!", "--dst-type", "LOCAL",
			"-j", "SNAT", "--to-source", primaryIP, "--random"}))
		go ipamd.StartReconcileIPPool(stopCh, time.Second)
		Eventually(func() int { return ipamd.dataStore.GetNICInfos().TotalIPs }, time.Second*20, time.Second*4).Should(Equal(defaultPoolSize))
		Eventually(func() int { return ipamd.dataStore.GetNICInfos().AssignedIPs }, time.Second*20, time.Second*4).Should(Equal(0))
	})
})
