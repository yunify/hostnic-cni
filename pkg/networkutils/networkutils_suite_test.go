package networkutils

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestNetworkutils(t *testing.T) {
	// klog.InitFlags(nil)
	// flag.Set("logtostderr", "true")
	// flag.Set("v", "4")
	// flag.Parse()
	// klog.SetOutput(GinkgoWriter)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Networkutils Suite")
}
