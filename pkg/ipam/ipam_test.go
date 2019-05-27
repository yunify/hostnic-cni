package ipam

import (
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/yunify/hostnic-cni/pkg/types"
)

var _ = Describe("Ipam", func() {
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
})
