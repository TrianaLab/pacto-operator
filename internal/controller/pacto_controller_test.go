/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
)

const validContract = `
pactoVersion: "1.0"
service:
  name: test-svc
  version: 1.0.0
  owner: team-a
interfaces:
  - name: http-api
    type: http
    port: 8080
`

const (
	timeout  = 10 * time.Second
	interval = 250 * time.Millisecond
)

var _ = Describe("Pacto Controller", func() {

	Context("When no contract source is specified", func() {
		const name = "test-no-contract"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{},
					Target:      pactov1alpha1.TargetRef{ServiceName: "nonexistent"},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			pacto := &pactov1alpha1.Pacto{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pacto)).To(Succeed())
		})

		It("should set ContractValid=False and contractStatus=NonCompliant", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
				cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(pactov1alpha1.ReasonContractInvalid))
				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusNonCompliant))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When target service does not exist", func() {
		const name = "test-no-target"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target:      pactov1alpha1.TargetRef{ServiceName: "nonexistent-svc"},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			pacto := &pactov1alpha1.Pacto{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pacto)).To(Succeed())
		})

		It("should set ServiceExists=False and contractStatus=NonCompliant", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				contractCond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
				g.Expect(contractCond).NotTo(BeNil())
				g.Expect(contractCond.Status).To(Equal(metav1.ConditionTrue))

				svcCond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionServiceExists)
				g.Expect(svcCond).NotTo(BeNil())
				g.Expect(svcCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(svcCond.Reason).To(Equal(pactov1alpha1.ReasonNotFound))

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusNonCompliant))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When service exists with matching ports", func() {
		const name = "test-compliant"
		const svcName = "compliant-svc"

		BeforeEach(func() {
			createService(svcName, "default", 8080)
			createDeployment(svcName, "default")

			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target:      pactov1alpha1.TargetRef{ServiceName: svcName},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
			deleteService(svcName, "default")
			deleteDeployment(svcName, "default")
		})

		It("should set contractStatus=Compliant with all checks passed", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusCompliant))
				g.Expect(pacto.Status.Summary).NotTo(BeNil())
				g.Expect(pacto.Status.Summary.Failed).To(Equal(int32(0)))
				g.Expect(pacto.Status.LastReconciledAt).NotTo(BeNil())

				// Check resources are populated
				g.Expect(pacto.Status.Resources).NotTo(BeNil())
				g.Expect(pacto.Status.Resources.Service).NotTo(BeNil())
				g.Expect(pacto.Status.Resources.Service.Exists).To(BeTrue())
				g.Expect(pacto.Status.Resources.Workload).NotTo(BeNil())
				g.Expect(pacto.Status.Resources.Workload.Exists).To(BeTrue())
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When service exists with wrong ports", func() {
		const name = "test-port-mismatch"
		const svcName = "wrong-port-svc"

		BeforeEach(func() {
			createService(svcName, "default", 9090)
			createDeployment(svcName, "default")

			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target:      pactov1alpha1.TargetRef{ServiceName: svcName},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
			deleteService(svcName, "default")
			deleteDeployment(svcName, "default")
		})

		It("should set contractStatus=Warning with missing ports", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusWarning))

				portsCond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionPortsValid)
				g.Expect(portsCond).NotTo(BeNil())
				g.Expect(portsCond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(portsCond.Reason).To(Equal(pactov1alpha1.ReasonMissingPorts))

				// Check port status
				g.Expect(pacto.Status.Ports).NotTo(BeNil())
				g.Expect(pacto.Status.Ports.Missing).To(ContainElement(int32(8080)))
				g.Expect(pacto.Status.Ports.Unexpected).To(ContainElement(int32(9090)))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("Reference-only contract (no target)", func() {
		const name = "test-reference"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					// No Target — reference-only
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
		})

		It("should set contractStatus=Reference with no runtime conditions", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusReference))
				g.Expect(pacto.Status.Summary).NotTo(BeNil())
				g.Expect(pacto.Status.Summary.Total).To(Equal(int32(1)))
				g.Expect(pacto.Status.Summary.Passed).To(Equal(int32(1)))
				g.Expect(pacto.Status.Summary.Failed).To(Equal(int32(0)))

				// No runtime status should be set
				g.Expect(pacto.Status.Resources).To(BeNil())
				g.Expect(pacto.Status.Ports).To(BeNil())
				g.Expect(pacto.Status.Endpoints).To(BeNil())

				// Contract info should be populated
				g.Expect(pacto.Status.Contract).NotTo(BeNil())
				g.Expect(pacto.Status.Contract.ServiceName).To(Equal("test-svc"))

				// ContractValid should be set with ReferenceOnly reason
				cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(pactov1alpha1.ReasonReferenceOnly))

				// No ServiceExists or WorkloadExists conditions should exist
				g.Expect(meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionServiceExists)).To(BeNil())
				g.Expect(meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionWorkloadExists)).To(BeNil())
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("Summary counts are accurate", func() {
		const name = "test-summary"
		const svcName = "summary-svc"

		BeforeEach(func() {
			createService(svcName, "default", 8080)
			createDeployment(svcName, "default")

			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target:      pactov1alpha1.TargetRef{ServiceName: svcName},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
			deleteService(svcName, "default")
			deleteDeployment(svcName, "default")
		})

		It("should have total = passed + failed", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.Summary).NotTo(BeNil())
				g.Expect(pacto.Status.Summary.Total).To(Equal(pacto.Status.Summary.Passed + pacto.Status.Summary.Failed))
				g.Expect(pacto.Status.Summary.Total).To(BeNumerically(">", 0))

				// Verify conditions count matches summary
				condCount := int32(len(pacto.Status.Conditions))
				g.Expect(pacto.Status.Summary.Total).To(Equal(condCount))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("Contract validation failure clears stale status", func() {
		const name = "test-stale-clear"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: "invalid yaml: [[["},
					Target:      pactov1alpha1.TargetRef{ServiceName: "some-svc"},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
		})

		It("should have no stale runtime fields", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusNonCompliant))

				// No runtime fields should exist
				g.Expect(pacto.Status.Resources).To(BeNil())
				g.Expect(pacto.Status.Ports).To(BeNil())
				g.Expect(pacto.Status.Endpoints).To(BeNil())
				g.Expect(pacto.Status.Contract).To(BeNil())
				g.Expect(pacto.Status.Interfaces).To(BeNil())

				// Only ContractValid condition should exist
				g.Expect(pacto.Status.Conditions).To(HaveLen(1))
				cond := pacto.Status.Conditions[0]
				g.Expect(cond.Type).To(Equal(pactov1alpha1.ConditionContractValid))
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("ObservedGeneration is set correctly", func() {
		const name = "test-observed-gen"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
		})

		It("should match metadata.generation", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
				g.Expect(pacto.Status.ObservedGeneration).To(Equal(pacto.Generation))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})
})

// --- Test helpers ---

func createService(name, namespace string, port int32) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{{
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}},
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, svc)).To(Succeed())
}

func createDeployment(name, namespace string) {
	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "app",
						Image: fmt.Sprintf("%s:latest", name),
					}},
				},
			},
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, deploy)).To(Succeed())
}

func deleteResource(name, namespace string) { //nolint:unparam // test helper always uses "default"
	pacto := &pactov1alpha1.Pacto{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, pacto); err == nil {
		_ = k8sClient.Delete(ctx, pacto)
	}
}

func deleteService(name, namespace string) {
	svc := &corev1.Service{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, svc); err == nil {
		_ = k8sClient.Delete(ctx, svc)
	}
}

func deleteDeployment(name, namespace string) {
	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deploy); err == nil {
		_ = k8sClient.Delete(ctx, deploy)
	}
}
