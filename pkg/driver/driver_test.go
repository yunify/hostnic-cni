package driver

import (
	"fmt"
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/netlinkwrapper/fake"
	"github.com/yunify/hostnic-cni/pkg/nswrapper"
)

var _ = Describe("Driver", func() {
	It("Should works well to set up NS", func() {
		fakeNetlink := fake.NewFakeNetlink()
		fakeNs := &nswrapper.FakeNsWrapper{}
		api := newDriverNetworkAPI(fakeNetlink, fakeNs)
		hostVethName := "nic1234"
		contVethName := "nic5678"
		nsPath := "/proc/1234/netns"
		_, testIP, _ := net.ParseCIDR("10.10.10.10/32")

		api.Setup(hostVethName, contVethName, testIP)

		//add links
		veth := &netlink.Veth{
			LinkAttrs: netlink.NewLinkAttrs(),
			PeerName:  hostVethName,
		}
		veth.Name = contVethName
		fakeNetlink.LinkAdd(veth)

		Expect(api.SetupNS(nsPath, testIP, 2, false)).NotTo(HaveOccurred(), fmt.Sprintf("%+v", fakeNetlink.Links))
		expectVeth := &netlink.Veth{
			LinkAttrs: netlink.NewLinkAttrs(),
			PeerName:  hostVethName,
		}
		dummyip := "169.254.1.1/32"
		expectVeth.Name = contVethName
		expectVeth.Flags = net.FlagUp
		expectVeth.MTU = 9001
		Expect(expectVeth).To(Equal(fakeNetlink.Links[contVethName]))
		Expect(fakeNetlink.Routes).To(MatchAllKeys(Keys{
			"<nil>+" + dummyip:         Not(BeNil()),
			"<nil>+" + "0.0.0.0/0":            Not(BeNil()),
			"<nil>+" + testIP.String(): Not(BeNil()),
		}))

		Expect(fakeNetlink.LinkAddr[contVethName]).To(HaveKey(testIP.String()))
		Expect(fakeNetlink.Rules).To(MatchAllKeys(Keys{
			"10.10.10.10/32+no-dst": Not(BeNil()),
		}))

		//teardown ns
		Expect(api.TeardownNS(testIP, 2)).ShouldNot(HaveOccurred())
		Expect(fakeNetlink.Rules).To(HaveLen(0))
		Expect(fakeNetlink.Routes).To(MatchAllKeys(Keys{
			"<nil>+" + dummyip: Not(BeNil()),
			"<nil>+" + "0.0.0.0/0":    Not(BeNil()),
		})) //there is 2 remain routes in container which will not delete in test
	})
})
