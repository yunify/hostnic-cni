package driver

import (
	"fmt"
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/netlinkwrapper/fake"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/nswrapper"
)

var _ = Describe("Driver", func() {
	It("Should works well to set up NS", func() {
		fakeNetlink := fake.NewFakeNetlink()
		fakeNs := &nswrapper.FakeNsWrapper{}
		networkAPI := networkutils.NewFakeNetworkAPI(fakeNetlink, nil, nil, nil)
		api := newDriverNetworkAPI(fakeNetlink, fakeNetlink, networkAPI, fakeNs, fakeNetlink)
		hostVethName := "nic1234"
		contVethName := "nic5678"
		nsPath := "/proc/1234/netns"
		_, testIP, _ := net.ParseCIDR("10.10.10.10/32")
		cidrs := []string{"10.10.11.0/24", "10.10.12.0/24", "10.10.13.0/24", "10.10.14.0/24"}

		//add links
		veth := &netlink.Veth{
			LinkAttrs: netlink.NewLinkAttrs(),
			PeerName:  hostVethName,
		}
		veth.Name = contVethName
		fakeNetlink.LinkAdd(veth)

		Expect(api.SetupNS(hostVethName, contVethName, nsPath, testIP, 2, cidrs, "", false)).NotTo(HaveOccurred(), fmt.Sprintf("%+v", fakeNetlink.Links))
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
			"<nil>+default":            Not(BeNil()),
			"<nil>+" + testIP.String(): Not(BeNil()),
		}))

		Expect(fakeNetlink.LinkAddr[contVethName]).To(HaveKey(testIP.String()))
		Expect(fakeNetlink.Rules).To(MatchAllKeys(Keys{
			"no-src+10.10.10.10/32":        Not(BeNil()),
			"10.10.10.10/32+10.10.11.0/24": Not(BeNil()),
			"10.10.10.10/32+10.10.12.0/24": Not(BeNil()),
			"10.10.10.10/32+10.10.13.0/24": Not(BeNil()),
			"10.10.10.10/32+10.10.14.0/24": Not(BeNil()),
		}))

		//teardown ns
		Expect(api.TeardownNS(testIP, 2)).ShouldNot(HaveOccurred())
		Expect(fakeNetlink.Rules).To(HaveLen(0))
		Expect(fakeNetlink.Routes).To(MatchAllKeys(Keys{
			"<nil>+" + dummyip: Not(BeNil()),
			"<nil>+default":    Not(BeNil()),
		})) //there is 2 remain routes in container which will not delete in test
	})
})
