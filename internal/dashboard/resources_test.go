/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

var testConfig = Config{
	Enabled:   true,
	Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
	Namespace: "pacto-system",
}

func TestBuildServiceAccount(t *testing.T) {
	sa := BuildServiceAccount(testConfig)

	if sa.Name != Name {
		t.Errorf("expected name %q, got %q", Name, sa.Name)
	}
	if sa.Namespace != testConfig.Namespace {
		t.Errorf("expected namespace %q, got %q", testConfig.Namespace, sa.Namespace)
	}
	assertLabels(t, sa.Labels)
}

func TestBuildClusterRole(t *testing.T) {
	cr := BuildClusterRole()

	if cr.Name != Name {
		t.Errorf("expected name %q, got %q", Name, cr.Name)
	}
	assertLabels(t, cr.Labels)

	if len(cr.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cr.Rules))
	}

	// Pacto CRs rule
	pactoRule := cr.Rules[0]
	if pactoRule.APIGroups[0] != "pacto.trianalab.io" {
		t.Errorf("expected API group pacto.trianalab.io, got %q", pactoRule.APIGroups[0])
	}
	assertContains(t, pactoRule.Resources, "pactos")
	assertContains(t, pactoRule.Resources, "pactorevisions")
	assertContains(t, pactoRule.Verbs, "get")
	assertContains(t, pactoRule.Verbs, "list")
	assertContains(t, pactoRule.Verbs, "watch")
	// Ensure no write verbs
	assertNotContains(t, pactoRule.Verbs, "create")
	assertNotContains(t, pactoRule.Verbs, "update")
	assertNotContains(t, pactoRule.Verbs, "delete")

	// Services rule
	svcRule := cr.Rules[1]
	assertContains(t, svcRule.Resources, "services")
	assertContains(t, svcRule.Verbs, "get")
	assertNotContains(t, svcRule.Verbs, "create")
}

func TestBuildClusterRoleBinding(t *testing.T) {
	crb := BuildClusterRoleBinding(testConfig)

	if crb.Name != Name {
		t.Errorf("expected name %q, got %q", Name, crb.Name)
	}
	assertLabels(t, crb.Labels)

	if len(crb.Subjects) != 1 {
		t.Fatalf("expected 1 subject, got %d", len(crb.Subjects))
	}
	if crb.Subjects[0].Name != Name {
		t.Errorf("expected subject name %q, got %q", Name, crb.Subjects[0].Name)
	}
	if crb.Subjects[0].Namespace != testConfig.Namespace {
		t.Errorf("expected subject namespace %q, got %q", testConfig.Namespace, crb.Subjects[0].Namespace)
	}
	if crb.RoleRef.Name != Name {
		t.Errorf("expected role ref name %q, got %q", Name, crb.RoleRef.Name)
	}
}

func TestBuildDeployment(t *testing.T) {
	deploy := BuildDeployment(testConfig)

	if deploy.Name != Name {
		t.Errorf("expected name %q, got %q", Name, deploy.Name)
	}
	if deploy.Namespace != testConfig.Namespace {
		t.Errorf("expected namespace %q, got %q", testConfig.Namespace, deploy.Namespace)
	}
	assertLabels(t, deploy.Labels)

	// Check replicas
	if *deploy.Spec.Replicas != 1 {
		t.Errorf("expected 1 replica, got %d", *deploy.Spec.Replicas)
	}

	// Check container
	containers := deploy.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	container := containers[0]

	if container.Image != testConfig.Image {
		t.Errorf("expected image %q, got %q", testConfig.Image, container.Image)
	}

	// Check port
	if len(container.Ports) != 1 || container.Ports[0].ContainerPort != DashboardPort {
		t.Errorf("expected port %d, got %v", DashboardPort, container.Ports)
	}

	// Check liveness probe
	if container.LivenessProbe == nil {
		t.Fatal("expected liveness probe")
	}
	if container.LivenessProbe.HTTPGet.Path != HealthPath {
		t.Errorf("expected liveness path %q, got %q", HealthPath, container.LivenessProbe.HTTPGet.Path)
	}

	// Check readiness probe
	if container.ReadinessProbe == nil {
		t.Fatal("expected readiness probe")
	}
	if container.ReadinessProbe.HTTPGet.Path != HealthPath {
		t.Errorf("expected readiness path %q, got %q", HealthPath, container.ReadinessProbe.HTTPGet.Path)
	}

	// Check security context
	if container.SecurityContext == nil {
		t.Fatal("expected security context")
	}
	if !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("expected read-only root filesystem")
	}
	if *container.SecurityContext.AllowPrivilegeEscalation {
		t.Error("expected no privilege escalation")
	}

	// Check pod security context
	podSec := deploy.Spec.Template.Spec.SecurityContext
	if podSec == nil {
		t.Fatal("expected pod security context")
	}
	if !*podSec.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true")
	}

	// Check service account
	if deploy.Spec.Template.Spec.ServiceAccountName != Name {
		t.Errorf("expected service account %q, got %q", Name, deploy.Spec.Template.Spec.ServiceAccountName)
	}

	// Check env vars
	envMap := make(map[string]string)
	for _, e := range container.Env {
		envMap[e.Name] = e.Value
	}
	if envMap["PACTO_DASHBOARD_PORT"] != "3000" {
		t.Error("expected PACTO_DASHBOARD_PORT=3000")
	}
	if envMap["PACTO_NO_UPDATE_CHECK"] != "1" {
		t.Error("expected PACTO_NO_UPDATE_CHECK=1")
	}
}

func TestBuildDeploymentWithWatchNamespace(t *testing.T) {
	cfg := Config{
		Enabled:        true,
		Image:          "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace:      "pacto-system",
		WatchNamespace: "production",
	}
	deploy := BuildDeployment(cfg)
	container := deploy.Spec.Template.Spec.Containers[0]

	envMap := make(map[string]string)
	for _, e := range container.Env {
		if e.Value != "" {
			envMap[e.Name] = e.Value
		}
	}
	if envMap["PACTO_WATCH_NAMESPACE"] != "production" {
		t.Errorf("expected PACTO_WATCH_NAMESPACE=production, got %q", envMap["PACTO_WATCH_NAMESPACE"])
	}
}

func TestBuildDeploymentWithoutWatchNamespace(t *testing.T) {
	deploy := BuildDeployment(testConfig) // No WatchNamespace
	container := deploy.Spec.Template.Spec.Containers[0]

	for _, e := range container.Env {
		if e.Name == "PACTO_WATCH_NAMESPACE" {
			t.Error("unexpected PACTO_WATCH_NAMESPACE when no watch namespace configured")
		}
	}
}

func TestBuildDeploymentWithOCISecret(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "pacto-system",
		OCISecret: "registry-creds",
	}
	deploy := BuildDeployment(cfg)
	container := deploy.Spec.Template.Spec.Containers[0]

	// Find secret-sourced env vars
	secretEnvs := make(map[string]*corev1.SecretKeySelector)
	for _, e := range container.Env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			secretEnvs[e.Name] = e.ValueFrom.SecretKeyRef
		}
	}

	expectedKeys := map[string]string{
		"PACTO_REGISTRY_USERNAME": "username",
		"PACTO_REGISTRY_PASSWORD": "password",
		"PACTO_REGISTRY_TOKEN":    "token",
	}
	for envName, secretKey := range expectedKeys {
		sel, ok := secretEnvs[envName]
		if !ok {
			t.Errorf("expected env var %q from secret", envName)
			continue
		}
		if sel.Name != cfg.OCISecret {
			t.Errorf("expected secret name %q for %q, got %q", cfg.OCISecret, envName, sel.Name)
		}
		if sel.Key != secretKey {
			t.Errorf("expected secret key %q for %q, got %q", secretKey, envName, sel.Key)
		}
		if !*sel.Optional {
			t.Errorf("expected optional=true for %q", envName)
		}
	}
}

func TestBuildDeploymentWithoutOCISecret(t *testing.T) {
	deploy := BuildDeployment(testConfig) // No OCISecret
	container := deploy.Spec.Template.Spec.Containers[0]

	for _, e := range container.Env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			t.Errorf("unexpected secret env var %q when no OCI secret configured", e.Name)
		}
	}
}

func TestBuildService(t *testing.T) {
	svc := BuildService(testConfig)

	if svc.Name != Name {
		t.Errorf("expected name %q, got %q", Name, svc.Name)
	}
	if svc.Namespace != testConfig.Namespace {
		t.Errorf("expected namespace %q, got %q", testConfig.Namespace, svc.Namespace)
	}
	assertLabels(t, svc.Labels)

	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(svc.Spec.Ports))
	}
	if svc.Spec.Ports[0].Port != DashboardPort {
		t.Errorf("expected port %d, got %d", DashboardPort, svc.Spec.Ports[0].Port)
	}

	// Selector should match pod selector labels
	selectorLabels := SelectorLabels()
	for k, v := range selectorLabels {
		if svc.Spec.Selector[k] != v {
			t.Errorf("expected selector %q=%q, got %q", k, v, svc.Spec.Selector[k])
		}
	}
}

func TestLabelsConsistency(t *testing.T) {
	labels := Labels()
	selectorLabels := SelectorLabels()

	// All selector labels must be present in the full labels
	for k, v := range selectorLabels {
		if labels[k] != v {
			t.Errorf("selector label %q=%q not found in full labels", k, v)
		}
	}
}

// --- helpers ---

func assertLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	if labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("expected label %q=%q, got %q", LabelManagedBy, ManagedByValue, labels[LabelManagedBy])
	}
	if labels[LabelComponent] != ComponentValue {
		t.Errorf("expected label %q=%q, got %q", LabelComponent, ComponentValue, labels[LabelComponent])
	}
	if labels[LabelName] != Name {
		t.Errorf("expected label %q=%q, got %q", LabelName, Name, labels[LabelName])
	}
}

func assertContains(t *testing.T, slice []string, val string) {
	t.Helper()
	if !slices.Contains(slice, val) {
		t.Errorf("expected slice to contain %q, got %v", val, slice)
	}
}

func assertNotContains(t *testing.T, slice []string, val string) {
	t.Helper()
	if slices.Contains(slice, val) {
		t.Errorf("expected slice to not contain %q", val)
	}
}
