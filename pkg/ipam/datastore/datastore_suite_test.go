package datastore

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestDatastore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Datastore Suite")
}
