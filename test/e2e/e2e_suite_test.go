package e2e

import (
	"fmt"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "Starting dbaas-operator suite\n")
	ginkgo.RunSpecs(t, "e2e suite")
}
