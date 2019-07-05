package retry_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/yunify/hostnic-cni/pkg/retry"
)

var _ = Describe("Retry", func() {
	It("Should fail", func() {
		var alwaysError = func() error {
			return fmt.Errorf("Error")
		}
		err := retry.Do(5, time.Second, alwaysError)
		Expect(retry.IsMaxRetries(err)).To(BeTrue())
	})

	It("Should fail", func() {
		i := 0
		var willOK = func() error {
			if i == 4 {
				return nil
			}
			i++
			return fmt.Errorf("Error")
		}
		Expect(retry.Do(5, time.Second, willOK)).ShouldNot(HaveOccurred())
	})
})
