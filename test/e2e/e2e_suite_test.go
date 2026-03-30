//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trianalab/pacto-operator/test/utils"
)

var (
	// managerImage is the manager image to be built and loaded for testing.
	managerImage = "example.com/pacto-operator:v0.0.1"
)

// TestE2E runs the e2e test suite to validate operator behavior in an isolated Kind cluster.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting pacto-operator e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	By("building the manager image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager image")

	By("loading the manager image on Kind")
	err = utils.LoadImageToKindClusterWithName(managerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")

	// No CertManager needed — this operator does not use webhooks.
})

var _ = AfterSuite(func() {
	// Cleanup is handled per-test in AfterAll blocks.
	// The Kind cluster is destroyed by `make cleanup-test-e2e`.

	// Ensure any leftover test namespace is cleaned up.
	if os.Getenv("E2E_SKIP_CLEANUP") != "true" {
		cmd := exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)
	}
})
