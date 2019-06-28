package networkutils

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestNetworkutils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Networkutils Suite")
}
