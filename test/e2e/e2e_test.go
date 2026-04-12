//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trianalab/pacto-operator/test/utils"
)

// namespace where the project is deployed in.
const namespace = "pacto-operator-system"

// serviceAccountName created for the project.
const serviceAccountName = "pacto-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project.
const metricsServiceName = "pacto-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data.
const metricsRoleBindingName = "pacto-operator-metrics-binding"

// testNamespace is a dedicated namespace for test Pacto CRs (isolated from operator system ns).
const testNamespace = "pacto-e2e-test"

// Inline contracts used across tests. Each is minimal but valid for its purpose.
const (
	// contractSimple is a minimal valid contract with one HTTP interface.
	contractSimple = `
pactoVersion: "1.0"
service:
  name: simple-svc
  version: 1.0.0
  owner: team-test
interfaces:
  - name: http-api
    type: http
    port: 8080
`
	// contractWithRuntime includes runtime section for runtime validation tests.
	contractWithRuntime = `
pactoVersion: "1.0"
service:
  name: runtime-svc
  version: 2.0.0
  owner: team-runtime
  image:
    ref: docker.io/library/nginx:latest
interfaces:
  - name: http-api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 30
`
	// contractStateful includes stateful runtime expectations.
	contractStateful = `
pactoVersion: "1.0"
service:
  name: stateful-svc
  version: 1.0.0
  owner: team-data
interfaces:
  - name: http-api
    type: http
    port: 5432
runtime:
  workload: service
  state:
    type: stateful
    persistence:
      scope: shared
      durability: persistent
    dataCriticality: high
  lifecycle:
    upgradeStrategy: ordered
    gracefulShutdownSeconds: 60
`
	// contractInvalid is intentionally malformed to trigger contract validation failure.
	contractInvalid = `not valid yaml: [[[`

	// contractMissingFields is valid YAML but missing required contract fields.
	contractMissingFields = `
pactoVersion: "1.0"
service:
  name: ""
  version: ""
`

	// contractWithConfig includes a single named configuration.
	contractWithConfig = `
pactoVersion: "1.0"
service:
  name: config-svc
  version: 1.0.0
  owner: team-config
interfaces:
  - name: http-api
    type: http
    port: 8080
configurations:
  - name: default
    schema: config-schema.json
    values:
      db_host: localhost
      api_key: "secret://vault/key"
`

	// contractWithMultiConfig includes multiple named configuration scopes.
	contractWithMultiConfig = `
pactoVersion: "1.0"
service:
  name: multi-config-svc
  version: 1.0.0
  owner: team-platform
interfaces:
  - name: http-api
    type: http
    port: 8080
configurations:
  - name: app
    schema: app-config.json
    values:
      port: 8080
  - name: monitoring
    ref: "oci://ghcr.io/acme/monitoring-config"
`

	// contractWithPolicies includes multiple policies.
	contractWithPolicies = `
pactoVersion: "1.0"
service:
  name: policy-svc
  version: 1.0.0
  owner: team-security
interfaces:
  - name: http-api
    type: http
    port: 8080
policies:
  - name: security
    schema: security-policy.json
  - name: baseline
    ref: "oci://org-policies/baseline"
`

	// contractWithSinglePolicy includes a single local-schema policy.
	contractWithSinglePolicy = `
pactoVersion: "1.0"
service:
  name: single-policy-svc
  version: 1.0.0
  owner: team-ops
interfaces:
  - name: http-api
    type: http
    port: 8080
policies:
  - name: ops
    schema: ops-policy.json
`
)

var _ = Describe("Operator", Ordered, func() {
	var controllerPodName string

	// Shared setup: deploy the operator once for all tests.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("creating test namespace for Pacto CRs")
		cmd = exec.Command("kubectl", "create", "ns", testNamespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")
	})

	AfterAll(func() {
		By("deleting test namespace")
		cmd := exec.Command("kubectl", "delete", "ns", testNamespace, "--ignore-not-found", "--wait=false")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	// Collect debug info on failure.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			if controllerPodName != "" {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace, "--tail=100")
				controllerLogs, err := utils.Run(cmd)
				if err == nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n%s", controllerLogs)
				}
			}

			By("Fetching Kubernetes events in test namespace")
			cmd := exec.Command("kubectl", "get", "events", "-n", testNamespace,
				"--sort-by=.lastTimestamp", "--no-headers")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Events:\n%s", eventsOutput)
			}

			By("Fetching Pacto resources in test namespace")
			cmd = exec.Command("kubectl", "get", "pactos,pactorevisions",
				"-n", testNamespace, "-o", "wide")
			resources, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Pacto resources:\n%s", resources)
			}
		}
	})

	// ── A. Operator Lifecycle ─────────────────────────────────────────────

	Context("Operator Lifecycle", func() {
		It("should run the controller-manager pod successfully", func() {
			verifyControllerUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods",
					"-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)
				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to list controller-manager pods")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected exactly 1 controller pod")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				cmd = exec.Command("kubectl", "get", "pods", controllerPodName,
					"-o", "jsonpath={.status.phase}", "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "controller pod not in Running phase")
			}
			Eventually(verifyControllerUp, 90*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ── B. Contract Status Computation ────────────────────────────────────

	Context("Contract Status Computation", Ordered, func() {
		// These tests run in parallel-safe fashion: each creates a uniquely-named
		// Pacto CR in the shared test namespace.

		It("should set contractStatus=Reference for a contract with no target", func() {
			name := "e2e-reference"
			applyPacto(name, testNamespace, contractSimple, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Reference"),
					"expected Reference status for contract without target")
				g.Expect(status.Summary.Total).To(BeNumerically(">=", 1))
				g.Expect(status.Summary.Failed).To(Equal(float64(0)))
				g.Expect(status.LastReconciledAt).NotTo(BeEmpty())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should set contractStatus=NonCompliant when workload does not exist", func() {
			name := "e2e-noncompliant-missing"
			applyPacto(name, testNamespace, contractSimple, "nonexistent-svc", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("NonCompliant"),
					"expected NonCompliant when target resources are missing")
				g.Expect(status.Summary.Failed).To(BeNumerically(">", 0))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should set contractStatus=Compliant when service and deployment match", func() {
			name := "e2e-compliant"
			svcName := "e2e-compliant-svc"
			createKubeService(svcName, testNamespace, 8080)
			createKubeDeployment(svcName, testNamespace, "nginx:latest")
			DeferCleanup(deleteKubeService, svcName, testNamespace)
			DeferCleanup(deleteKubeDeployment, svcName, testNamespace)

			applyPacto(name, testNamespace, contractSimple, svcName, nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Compliant"),
					"expected Compliant when service+deployment match contract")
				g.Expect(status.Summary.Failed).To(Equal(float64(0)))
				g.Expect(status.Resources.Service.Exists).To(BeTrue())
				g.Expect(status.Resources.Workload.Exists).To(BeTrue())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should set contractStatus=Warning when ports mismatch", func() {
			name := "e2e-warning-ports"
			svcName := "e2e-warning-svc"
			createKubeService(svcName, testNamespace, 9090) // contract expects 8080
			createKubeDeployment(svcName, testNamespace, "nginx:latest")
			DeferCleanup(deleteKubeService, svcName, testNamespace)
			DeferCleanup(deleteKubeDeployment, svcName, testNamespace)

			applyPacto(name, testNamespace, contractSimple, svcName, nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Warning"),
					"expected Warning when ports don't match")
				g.Expect(status.Ports.Missing).To(ContainElement(float64(8080)))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ── C. PactoRevision Lifecycle ────────────────────────────────────────

	Context("PactoRevision Lifecycle", Ordered, func() {
		It("should create a PactoRevision linked to the parent Pacto", func() {
			name := "e2e-revision"
			applyPacto(name, testNamespace, contractSimple, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.CurrentRevision).NotTo(BeEmpty(),
					"expected currentRevision to be populated after reconciliation")

				// Verify the revision exists and is linked
				revName := status.CurrentRevision
				rev := getRevisionJSON(g, revName, testNamespace)
				g.Expect(rev.Spec.PactoRef).To(Equal(name),
					"revision.spec.pactoRef should reference the parent Pacto")
				g.Expect(rev.Spec.Version).To(Equal("1.0.0"))
				g.Expect(rev.Spec.ServiceName).To(Equal("simple-svc"))
				g.Expect(rev.Status.Resolved).To(BeTrue())
				g.Expect(rev.Status.ContractHash).NotTo(BeEmpty())

				// Verify owner reference exists for GC
				g.Expect(rev.Metadata.OwnerReferences).NotTo(BeEmpty(),
					"revision should have an owner reference to the parent Pacto")
				g.Expect(rev.Metadata.OwnerReferences[0].Name).To(Equal(name))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should populate revision labels correctly", func() {
			name := "e2e-revision-labels"
			applyPacto(name, testNamespace, contractSimple, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.CurrentRevision).NotTo(BeEmpty())

				// List revisions by label
				cmd := exec.Command("kubectl", "get", "pactorevisions",
					"-n", testNamespace,
					"-l", fmt.Sprintf("pacto.trianalab.io/pacto=%s", name),
					"-o", "jsonpath={.items[0].metadata.name}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal(status.CurrentRevision),
					"revision found by label should match currentRevision")
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ── D. Runtime Validation ─────────────────────────────────────────────

	Context("Runtime Validation", Ordered, func() {
		It("should validate runtime fields when workload matches contract", func() {
			name := "e2e-runtime-match"
			svcName := "e2e-runtime-match-svc"

			// Create service and deployment matching the contract's runtime expectations
			createKubeService(svcName, testNamespace, 8080)
			createKubeDeploymentWithOptions(svcName, testNamespace, deploymentOpts{
				image:                  "nginx:latest",
				strategy:               "RollingUpdate",
				terminationGracePeriod: 30,
			})
			DeferCleanup(deleteKubeService, svcName, testNamespace)
			DeferCleanup(deleteKubeDeployment, svcName, testNamespace)

			applyPacto(name, testNamespace, contractWithRuntime, svcName, nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Compliant"),
					"expected Compliant when runtime matches contract")

				// Verify observed runtime is populated
				g.Expect(status.ObservedRuntime).NotTo(BeNil())
				g.Expect(status.ObservedRuntime.WorkloadKind).To(Equal("Deployment"))

				// Verify runtime contract info is populated
				g.Expect(status.Runtime).NotTo(BeNil())
				g.Expect(status.Runtime.Workload).To(Equal("service"))
				g.Expect(status.Runtime.UpgradeStrategy).To(Equal("rolling"))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should produce Warning when runtime fields mismatch", func() {
			name := "e2e-runtime-mismatch"
			svcName := "e2e-runtime-mismatch-svc"

			// Deployment uses Recreate strategy but contract expects rolling
			createKubeService(svcName, testNamespace, 8080)
			createKubeDeploymentWithOptions(svcName, testNamespace, deploymentOpts{
				image:                  "nginx:latest",
				strategy:               "Recreate",
				terminationGracePeriod: 10, // contract expects 30
			})
			DeferCleanup(deleteKubeService, svcName, testNamespace)
			DeferCleanup(deleteKubeDeployment, svcName, testNamespace)

			applyPacto(name, testNamespace, contractWithRuntime, svcName, nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				// Runtime mismatches produce Warning (not NonCompliant — that's only for missing resources)
				g.Expect(status.ContractStatus).To(Equal("Warning"),
					"expected Warning for runtime field mismatches")
				g.Expect(status.Summary.Failed).To(BeNumerically(">", 0))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ── E. Error and Edge Cases ───────────────────────────────────────────

	Context("Error and Edge Cases", Ordered, func() {
		It("should set NonCompliant for an invalid inline contract", func() {
			name := "e2e-invalid-contract"
			applyPacto(name, testNamespace, contractInvalid, "some-svc", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("NonCompliant"),
					"expected NonCompliant for invalid YAML contract")
				g.Expect(status.Validation).NotTo(BeNil())
				g.Expect(status.Validation.Valid).To(BeFalse())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should clear stale status fields on invalid contract", func() {
			name := "e2e-stale-clear"
			applyPacto(name, testNamespace, contractInvalid, "some-svc", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("NonCompliant"))
				// No runtime fields should be populated on contract failure
				g.Expect(status.Resources).To(BeNil())
				g.Expect(status.Ports).To(BeNil())
				g.Expect(status.Contract).To(BeNil())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should set ObservedGeneration on every reconciliation", func() {
			name := "e2e-observed-gen"
			applyPacto(name, testNamespace, contractSimple, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				// Get both generation and observedGeneration
				cmd := exec.Command("kubectl", "get", "pacto", name,
					"-n", testNamespace,
					"-o", "jsonpath={.metadata.generation} {.status.observedGeneration}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				parts := strings.Fields(output)
				g.Expect(parts).To(HaveLen(2))
				g.Expect(parts[0]).To(Equal(parts[1]),
					"observedGeneration should match metadata.generation")
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ── F. Configuration and Policy Status ────────────────────────────────

	Context("Configuration and Policy Status", Ordered, func() {
		It("should surface named config in status.configurations", func() {
			name := "e2e-named-config"
			applyPacto(name, testNamespace, contractWithConfig, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Reference"))
				g.Expect(status.Configurations).To(HaveLen(1),
					"single named config should produce one entry")
				g.Expect(status.Configurations[0].Name).To(Equal("default"))
				g.Expect(status.Configurations[0].HasSchema).To(BeTrue())
				g.Expect(status.Configurations[0].ValueKeys).To(ContainElement("db_host"))
				g.Expect(status.Configurations[0].SecretKeys).To(ContainElement("api_key"))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should surface multi-config in status.configurations", func() {
			name := "e2e-multi-config"
			applyPacto(name, testNamespace, contractWithMultiConfig, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Reference"))
				g.Expect(status.Configurations).To(HaveLen(2),
					"multi-config should produce two entries")
				g.Expect(status.Configurations[0].Name).To(Equal("app"))
				g.Expect(status.Configurations[0].HasSchema).To(BeTrue())
				g.Expect(status.Configurations[1].Name).To(Equal("monitoring"))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should surface multiple policies in status.policies", func() {
			name := "e2e-multi-policy"
			applyPacto(name, testNamespace, contractWithPolicies, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				// Contract may be NonCompliant if policy schemas aren't found in bundle,
				// but policies metadata should still be surfaced in status.
				g.Expect(status.Policies).To(HaveLen(2),
					"should have two policy entries")
				g.Expect(status.Policies[0].Name).To(Equal("security"))
				g.Expect(status.Policies[0].HasSchema).To(BeTrue())
				g.Expect(status.Policies[1].Name).To(Equal("baseline"))
				g.Expect(status.Policies[1].Ref).To(Equal("oci://org-policies/baseline"))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should surface single local-schema policy", func() {
			name := "e2e-single-policy"
			applyPacto(name, testNamespace, contractWithSinglePolicy, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.Policies).To(HaveLen(1))
				g.Expect(status.Policies[0].HasSchema).To(BeTrue())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should have empty configurations and policies when not declared", func() {
			name := "e2e-no-config-policy"
			applyPacto(name, testNamespace, contractSimple, "", nil)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Reference"))
				g.Expect(status.Configurations).To(BeEmpty())
				g.Expect(status.Policies).To(BeEmpty())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ── F2. Configuration Overrides ──────────────────────────────────────

	Context("Configuration Overrides", Ordered, func() {
		It("should merge override values into resolved contract", func() {
			name := "e2e-config-override"
			overridesYAML := `
  overrides:
    configurations:
      - name: default
        values:
          db_host: staging-db.example.com`
			applyPactoRaw(name, testNamespace, contractWithConfig, "", nil, overridesYAML)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("Reference"))
				g.Expect(status.Configurations).To(HaveLen(1))
				g.Expect(status.Configurations[0].Name).To(Equal("default"))
				g.Expect(status.Configurations[0].OverriddenKeys).To(ContainElement("db_host"))
				// Original keys should still be present.
				g.Expect(status.Configurations[0].ValueKeys).To(ContainElement("db_host"))
				g.Expect(status.Configurations[0].ValueKeys).To(ContainElement("api_key"))
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should fail reconciliation for unknown configuration name in overrides", func() {
			name := "e2e-config-override-unknown"
			overridesYAML := `
  overrides:
    configurations:
      - name: nonexistent
        values:
          key: value`
			applyPactoRaw(name, testNamespace, contractWithConfig, "", nil, overridesYAML)
			DeferCleanup(deletePacto, name, testNamespace)

			Eventually(func(g Gomega) {
				status := getPactoStatus(g, name, testNamespace)
				g.Expect(status.ContractStatus).To(Equal("NonCompliant"))
				g.Expect(status.Validation).NotTo(BeNil())
				g.Expect(status.Validation.Valid).To(BeFalse())
			}, 60*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ── G. Metrics Verification ───────────────────────────────────────────

	Context("Metrics", Ordered, func() {
		It("should expose pacto-specific metrics on the metrics endpoint", func() {
			By("creating a ClusterRoleBinding for metrics access")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=pacto-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")
			DeferCleanup(func() {
				cmd := exec.Command("kubectl", "delete", "clusterrolebinding",
					metricsRoleBindingName, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			})

			// Create a Pacto CR so the operator emits metrics for it.
			metricsTestName := "e2e-metrics-probe"
			applyPacto(metricsTestName, testNamespace, contractSimple, "", nil)
			DeferCleanup(deletePacto, metricsTestName, testNamespace)

			// Wait for reconciliation to complete
			Eventually(func(g Gomega) {
				status := getPactoStatus(g, metricsTestName, testNamespace)
				g.Expect(status.ContractStatus).NotTo(BeEmpty())
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("fetching metrics via port-forward")
			metricsOutput := fetchMetricsViaPortForward(token)

			By("verifying pacto-specific metrics are present")
			Expect(metricsOutput).To(ContainSubstring("pacto_contract_compliance_status"),
				"expected pacto_contract_compliance_status metric")
			Expect(metricsOutput).To(ContainSubstring("pacto_contract_status"),
				"expected pacto_contract_status metric")
			Expect(metricsOutput).To(ContainSubstring("pacto_contract_validation_result"),
				"expected pacto_contract_validation_result metric")
			Expect(metricsOutput).To(ContainSubstring("pacto_contract_validation_errors"),
				"expected pacto_contract_validation_errors metric")
			Expect(metricsOutput).To(ContainSubstring("pacto_contract_validation_warnings"),
				"expected pacto_contract_validation_warnings metric")
			// Verify the reconciliation happened for our specific CR
			Expect(metricsOutput).To(ContainSubstring("simple-svc"),
				"expected metrics to reference our test service")
		})
	})
})

// ── Types for JSON parsing ────────────────────────────────────────────────

// pactoStatus is a lightweight JSON representation of PactoStatus for assertions.
// Uses interface{} for numeric fields since kubectl JSON output may use float64.
type pactoStatus struct {
	ContractStatus     string              `json:"contractStatus"`
	ContractVersion    string              `json:"contractVersion"`
	CurrentRevision    string              `json:"currentRevision"`
	LastReconciledAt   string              `json:"lastReconciledAt"`
	ObservedGeneration float64             `json:"observedGeneration"`
	Summary            *checkSummary       `json:"summary"`
	Contract           *contractInfo       `json:"contract"`
	Validation         *validationResult   `json:"validation"`
	Resources          *resourcesStatus    `json:"resources"`
	Ports              *portStatus         `json:"ports"`
	Runtime            *runtimeInfo        `json:"runtime"`
	ObservedRuntime    *observedRuntime    `json:"observedRuntime"`
	Configurations     []configurationInfo `json:"configurations"`
	Policies           []policyInfo        `json:"policies"`
	Conditions         []condition         `json:"conditions"`
}

type configurationInfo struct {
	Name           string   `json:"name"`
	HasSchema      bool     `json:"hasSchema"`
	Ref            string   `json:"ref"`
	ValueKeys      []string `json:"valueKeys"`
	SecretKeys     []string `json:"secretKeys"`
	OverriddenKeys []string `json:"overriddenKeys"`
}

type policyInfo struct {
	Name      string `json:"name"`
	HasSchema bool   `json:"hasSchema"`
	Schema    string `json:"schema"`
	Ref       string `json:"ref"`
}

type checkSummary struct {
	Total  float64 `json:"total"`
	Passed float64 `json:"passed"`
	Failed float64 `json:"failed"`
}

type contractInfo struct {
	ServiceName string `json:"serviceName"`
	Version     string `json:"version"`
}

type validationResult struct {
	Valid bool `json:"valid"`
}

type resourcesStatus struct {
	Service  *resourceStatus `json:"service"`
	Workload *resourceStatus `json:"workload"`
}

type resourceStatus struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Exists bool   `json:"exists"`
}

type portStatus struct {
	Expected   []float64 `json:"expected"`
	Observed   []float64 `json:"observed"`
	Missing    []float64 `json:"missing"`
	Unexpected []float64 `json:"unexpected"`
}

type runtimeInfo struct {
	Workload        string `json:"workload"`
	UpgradeStrategy string `json:"upgradeStrategy"`
}

type observedRuntime struct {
	WorkloadKind string `json:"workloadKind"`
}

type condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type revisionJSON struct {
	Metadata struct {
		Name            string `json:"name"`
		OwnerReferences []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"ownerReferences"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Version     string `json:"version"`
		PactoRef    string `json:"pactoRef"`
		ServiceName string `json:"serviceName"`
		Source      struct {
			Inline bool   `json:"inline"`
			OCI    string `json:"oci"`
		} `json:"source"`
	} `json:"spec"`
	Status struct {
		Resolved     bool   `json:"resolved"`
		ContractHash string `json:"contractHash"`
	} `json:"status"`
}

// ── Helpers ───────────────────────────────────────────────────────────────

// applyPacto creates a Pacto CR using kubectl apply.
func applyPacto(name, ns, inlineContract, serviceName string, workloadRef *struct{ name, kind string }) {
	spec := fmt.Sprintf(`  contractRef:
    inline: |
%s`, indentYAML(inlineContract, 6))

	if serviceName != "" {
		spec += fmt.Sprintf(`
  target:
    serviceName: %s`, serviceName)
	}

	if workloadRef != nil {
		spec += fmt.Sprintf(`
    workloadRef:
      name: %s
      kind: %s`, workloadRef.name, workloadRef.kind)
	}

	// Use a short check interval for faster test feedback
	spec += "\n  checkIntervalSeconds: 30"

	manifest := fmt.Sprintf(`apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: %s
  namespace: %s
spec:
%s`, name, ns, spec)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to apply Pacto CR %s", name)
}

// applyPactoRaw creates a Pacto CR with optional raw spec additions (e.g. overrides).
func applyPactoRaw(name, ns, inlineContract, serviceName string, workloadRef *struct{ name, kind string }, extraSpec string) {
	spec := fmt.Sprintf(`  contractRef:
    inline: |
%s`, indentYAML(inlineContract, 6))

	if serviceName != "" {
		spec += fmt.Sprintf(`
  target:
    serviceName: %s`, serviceName)
	}

	if workloadRef != nil {
		spec += fmt.Sprintf(`
    workloadRef:
      name: %s
      kind: %s`, workloadRef.name, workloadRef.kind)
	}

	spec += "\n  checkIntervalSeconds: 30"

	if extraSpec != "" {
		spec += extraSpec
	}

	manifest := fmt.Sprintf(`apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: %s
  namespace: %s
spec:
%s`, name, ns, spec)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to apply Pacto CR %s", name)
}

func deletePacto(name, ns string) {
	cmd := exec.Command("kubectl", "delete", "pacto", name, "-n", ns, "--ignore-not-found")
	_, _ = utils.Run(cmd)
}

func getPactoStatus(g Gomega, name, ns string) pactoStatus {
	cmd := exec.Command("kubectl", "get", "pacto", name, "-n", ns, "-o", "json")
	output, err := utils.Run(cmd)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to get Pacto %s", name)

	var raw struct {
		Status pactoStatus `json:"status"`
	}
	g.Expect(json.Unmarshal([]byte(output), &raw)).To(Succeed(), "Failed to parse Pacto JSON")
	return raw.Status
}

func getRevisionJSON(g Gomega, name, ns string) revisionJSON {
	cmd := exec.Command("kubectl", "get", "pactorevision", name, "-n", ns, "-o", "json")
	output, err := utils.Run(cmd)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to get PactoRevision %s", name)

	var rev revisionJSON
	g.Expect(json.Unmarshal([]byte(output), &rev)).To(Succeed(), "Failed to parse PactoRevision JSON")
	return rev
}

type deploymentOpts struct {
	image                  string
	strategy               string // "RollingUpdate" or "Recreate"
	terminationGracePeriod int64
}

func createKubeService(name, ns string, port int32) {
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    app: %s
  ports:
    - port: %d
      targetPort: %d`, name, ns, name, port, port)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to create Service %s", name)
}

func deleteKubeService(name, ns string) {
	cmd := exec.Command("kubectl", "delete", "service", name, "-n", ns, "--ignore-not-found")
	_, _ = utils.Run(cmd)
}

func createKubeDeployment(name, ns, image string) {
	createKubeDeploymentWithOptions(name, ns, deploymentOpts{
		image:                  image,
		strategy:               "RollingUpdate",
		terminationGracePeriod: 30,
	})
}

func createKubeDeploymentWithOptions(name, ns string, opts deploymentOpts) {
	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  strategy:
    type: %s
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      terminationGracePeriodSeconds: %d
      containers:
        - name: app
          image: %s`, name, ns, opts.strategy, name, name, opts.terminationGracePeriod, opts.image)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to create Deployment %s", name)
}

func deleteKubeDeployment(name, ns string) {
	cmd := exec.Command("kubectl", "delete", "deployment", name, "-n", ns, "--ignore-not-found")
	_, _ = utils.Run(cmd)
}

func indentYAML(yaml string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	var lines []string
	for _, line := range strings.Split(yaml, "\n") {
		if line == "" {
			lines = append(lines, "")
		} else {
			lines = append(lines, prefix+line)
		}
	}
	return strings.Join(lines, "\n")
}

// serviceAccountToken returns a token for the specified service account.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace, serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation, 30*time.Second, 2*time.Second).Should(Succeed())

	return out, err
}

// fetchMetricsViaPortForward uses kubectl port-forward to access the metrics endpoint
// directly from the test runner, avoiding the slow curl-pod-in-cluster pattern.
// This saves ~60-90s compared to the pod-based approach by eliminating pod scheduling,
// image pull, and container startup time.
func fetchMetricsViaPortForward(token string) string {
	// Find a free local port
	localPort := "18443"

	// Start port-forward in the background
	pfCmd := exec.Command("kubectl", "port-forward",
		fmt.Sprintf("svc/%s", metricsServiceName),
		fmt.Sprintf("%s:8443", localPort),
		"-n", namespace)
	pfCmd.Dir, _ = utils.GetProjectDir()
	err := pfCmd.Start()
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to start port-forward")
	defer func() {
		if pfCmd.Process != nil {
			_ = pfCmd.Process.Kill()
			_ = pfCmd.Wait()
		}
	}()

	// Wait for port-forward to be ready, then fetch metrics
	var metricsOutput string
	Eventually(func(g Gomega) {
		curlCmd := exec.Command("curl", "-s", "-k",
			"-H", fmt.Sprintf("Authorization: Bearer %s", token),
			fmt.Sprintf("https://localhost:%s/metrics", localPort))
		output, err := curlCmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred(), "curl failed: %s", string(output))
		g.Expect(string(output)).NotTo(BeEmpty())
		g.Expect(string(output)).NotTo(ContainSubstring("refused"))
		metricsOutput = string(output)
	}, 30*time.Second, 2*time.Second).Should(Succeed())

	return metricsOutput
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
