package networkutils

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hostnic", func() {
	It("Should get proper vpn net", func() {
		Expect(GetVPNNet("192.168.0.2")).To(Equal("192.168.255.254/32"))
		Expect(GetVPNNet("172.16.1.2")).To(Equal("172.16.255.254/32"))
	})
})
