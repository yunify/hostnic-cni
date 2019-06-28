package k8sclient

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestK8sclient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "K8sclient Suite")
}
