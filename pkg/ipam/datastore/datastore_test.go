package datastore

import (
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/yunify/hostnic-cni/pkg/k8sclient"
)

var _ = Describe("Datastore", func() {
	var ds *DataStore

	BeforeEach(func() {
		ds = NewDataStore()
	})

	It("should no error when add nic", func() {
		Expect(ds.AddNIC("nic-1", 1, true)).ShouldNot(HaveOccurred())
		Expect(ds.AddNIC("nic-1", 1, true)).Should(HaveOccurred())
		Expect(ds.AddNIC("nic-2", 2, false)).ShouldNot(HaveOccurred())
		Expect(ds.nicIPPools).To(HaveLen(2))
		Expect(ds.GetNICInfos().NICIPPools).To(HaveLen(2))
	})

	It("Should be ok to delete nic", func() {
		Expect(ds.AddNIC("nic-1", 1, true)).ShouldNot(HaveOccurred())
		Expect(ds.AddNIC("nic-2", 2, false)).ShouldNot(HaveOccurred())
		Expect(ds.AddNIC("nic-3", 3, false)).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().NICIPPools).To(HaveLen(3))
		Expect(ds.RemoveNICFromDataStore("nic-2")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().NICIPPools).To(HaveLen(2))
		Expect(ds.RemoveNICFromDataStore("nic-unknown")).Should(HaveOccurred())
		Expect(ds.GetNICInfos().NICIPPools).To(HaveLen(2))
	})

	It("Should be ok to add ip", func() {
		Expect(ds.AddNIC("nic-1", 1, true)).ShouldNot(HaveOccurred())
		Expect(ds.AddNIC("nic-2", 2, false)).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().NICIPPools).To(HaveLen(2))
		Expect(ds.AddIPv4AddressFromStore("nic-1", "1.1.1.1")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(1))
		Expect(ds.AddIPv4AddressFromStore("nic-1", "1.1.1.1")).Should(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(1))
		Expect(ds.AddIPv4AddressFromStore("nic-1", "1.1.1.2")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(2))
		Expect(ds.AddIPv4AddressFromStore("nic-unknown", "1.1.1.3")).Should(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(2))
		Expect(ds.AddIPv4AddressFromStore("nic-2", "1.1.2.2")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(3))
		Expect(ds.nicIPPools["nic-1"].IPv4Addresses).To(HaveLen(2))
		Expect(ds.nicIPPools["nic-2"].IPv4Addresses).To(HaveLen(1))
		_, err := ds.GetNICIPPools("nic-unknown")
		Expect(err).Should(HaveOccurred())
	})

	It("Should be ok to delete ips", func() {
		Expect(ds.AddNIC("nic-1", 1, true)).ShouldNot(HaveOccurred())
		Expect(ds.AddNIC("nic-2", 2, false)).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().NICIPPools).To(HaveLen(2))
		Expect(ds.AddIPv4AddressFromStore("nic-1", "1.1.1.1")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(1))
		Expect(ds.AddIPv4AddressFromStore("nic-1", "1.1.1.2")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(2))
		Expect(ds.AddIPv4AddressFromStore("nic-2", "1.1.2.2")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(3))
		Expect(ds.nicIPPools["nic-1"].IPv4Addresses).To(HaveLen(2))
		Expect(ds.nicIPPools["nic-2"].IPv4Addresses).To(HaveLen(1))

		Expect(ds.DelIPv4AddressFromStore("nic-1", "1.1.1.1")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(2))

		Expect(ds.DelIPv4AddressFromStore("nic-1", "1.1.1.1")).Should(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(2))
		Expect(ds.nicIPPools["nic-1"].IPv4Addresses).To(HaveLen(1))
		Expect(ds.nicIPPools["nic-2"].IPv4Addresses).To(HaveLen(1))
	})

	It("Should be ok to work with pod", func() {
		Expect(ds.AddNIC("nic-1", 1, true)).ShouldNot(HaveOccurred())
		Expect(ds.AddNIC("nic-2", 2, false)).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().NICIPPools).To(HaveLen(2))
		Expect(ds.AddIPv4AddressFromStore("nic-1", "1.1.1.1")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(1))
		Expect(ds.AddIPv4AddressFromStore("nic-1", "1.1.1.2")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(2))
		Expect(ds.AddIPv4AddressFromStore("nic-2", "1.1.2.2")).ShouldNot(HaveOccurred())
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(3))
		Expect(ds.nicIPPools["nic-1"].IPv4Addresses).To(HaveLen(2))
		Expect(ds.nicIPPools["nic-2"].IPv4Addresses).To(HaveLen(1))

		podInfo := k8sclient.K8SPodInfo{
			Name:      "pod-1",
			Namespace: "ns-1",
			IP:        "1.1.1.1",
		}

		ip, _, err := ds.AssignPodIPv4Address(&podInfo)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(ip).To(Equal("1.1.1.1"))
		Expect(ds.nicIPPools["nic-1"].AssignedIPv4Addresses).To(Equal(1))
		Expect(ds.nicIPPools["nic-1"].IPv4Addresses).To(HaveLen(2))

		_, _, err = ds.AssignPodIPv4Address(&podInfo)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(ds.nicIPPools["nic-1"].AssignedIPv4Addresses).To(Equal(1))
		Expect(ds.nicIPPools["nic-1"].IPv4Addresses).To(HaveLen(2))

		podInfo.IP = "1.1.10.1"
		_, _, err = ds.AssignPodIPv4Address(&podInfo)
		Expect(err).Should(HaveOccurred())

		podInfo = k8sclient.K8SPodInfo{
			Name:      "pod-1",
			Namespace: "ns-2",
			IP:        "1.1.1.2",
		}

		ip, _, err = ds.AssignPodIPv4Address(&podInfo)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(ip).To(Equal("1.1.1.2"))
		Expect(ds.nicIPPools["nic-1"].AssignedIPv4Addresses).To(Equal(2))
		Expect(ds.nicIPPools["nic-1"].IPv4Addresses).To(HaveLen(2))

		podInfo = k8sclient.K8SPodInfo{
			Name:      "pod-1",
			Namespace: "ns-3",
			Container: "container-1",
		}
		ip, pod1Ns3Device, err := ds.AssignPodIPv4Address(&podInfo)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(ip).To(Equal("1.1.2.2"))
		Expect(ds.assigned).To(Equal(3))

		// no more IP addresses
		podInfo = k8sclient.K8SPodInfo{
			Name:      "pod-2",
			Namespace: "ns-3",
		}
		_, _, err = ds.AssignPodIPv4Address(&podInfo)
		Expect(err).Should(HaveOccurred())
		_, _, err = ds.UnassignPodIPv4Address(&podInfo)
		Expect(err).Should(HaveOccurred())

		// Unassign pod which have same name/namespace, but different container
		podInfo = k8sclient.K8SPodInfo{
			Name:      "pod-1",
			Namespace: "ns-3",
			Container: "container-2",
		}
		_, _, err = ds.UnassignPodIPv4Address(&podInfo)
		Expect(err).Should(HaveOccurred())

		podInfo = k8sclient.K8SPodInfo{
			Name:      "pod-1",
			Namespace: "ns-3",
			Container: "container-1",
		}

		_, deviceNum, err := ds.UnassignPodIPv4Address(&podInfo)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(ds.assigned).To(Equal(2))
		Expect(deviceNum).To(Equal(pod1Ns3Device))
		Expect(ds.nicIPPools["nic-2"].AssignedIPv4Addresses).To(Equal(0))
		Expect(ds.nicIPPools["nic-2"].IPv4Addresses).To(HaveLen(1))

		Expect(ds.RemoveUnusedNICFromStore()).Should(BeEmpty())
		ds.nicIPPools["nic-2"].createTime = time.Time{}
		ds.nicIPPools["nic-2"].lastUnassignedTime = time.Time{}
		Expect(ds.RemoveUnusedNICFromStore()).Should(Equal("nic-2"))
		Expect(ds.GetNICInfos().TotalIPs).To(Equal(2))
	})
})
