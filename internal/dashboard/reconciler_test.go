/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = rbacv1.AddToScheme(s)
	return s
}

func newReconciler(cfg Config, objs ...client.Object) *Reconciler {
	scheme := newScheme()
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return &Reconciler{
		Client: builder.Build(),
		Scheme: scheme,
		Config: cfg,
	}
}

func TestReconcile_Disabled_NoResources(t *testing.T) {
	r := newReconciler(Config{Enabled: false, Namespace: "test-ns"})
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}

	// Verify no resources were created
	assertResourceNotFound(t, r.Client, ctx, "test-ns", &appsv1.Deployment{})
	assertResourceNotFound(t, r.Client, ctx, "test-ns", &corev1.Service{})
	assertResourceNotFound(t, r.Client, ctx, "test-ns", &corev1.ServiceAccount{})
}

func TestReconcile_Enabled_CreatesResources(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}

	// Pre-create the namespace (fake client doesn't require it, but let's be clean)
	r := newReconciler(cfg)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue when enabled")
	}

	// Verify all resources exist
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, &corev1.ServiceAccount{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Name: Name}, &rbacv1.ClusterRole{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Name: Name}, &rbacv1.ClusterRoleBinding{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, &appsv1.Deployment{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, &corev1.Service{})

	// Verify deployment has correct image
	deploy := &appsv1.Deployment{}
	_ = r.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, deploy)
	if deploy.Spec.Template.Spec.Containers[0].Image != cfg.Image {
		t.Errorf("expected image %q, got %q", cfg.Image, deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestReconcile_Enabled_UpdatesExistingResources(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.25.0",
		Namespace: "test-ns",
	}

	// Pre-create a deployment with an old image
	oldDeploy := BuildDeployment(Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.0",
		Namespace: "test-ns",
	})

	r := newReconciler(cfg,
		BuildServiceAccount(cfg),
		BuildClusterRole(),
		BuildClusterRoleBinding(cfg),
		oldDeploy,
		BuildService(cfg),
	)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify deployment was updated with new image
	deploy := &appsv1.Deployment{}
	_ = r.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, deploy)
	if deploy.Spec.Template.Spec.Containers[0].Image != cfg.Image {
		t.Errorf("expected updated image %q, got %q", cfg.Image, deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestReconcile_DisabledAfterEnabled_CleansUp(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}

	// Create resources as if dashboard was enabled
	r := newReconciler(cfg,
		BuildServiceAccount(cfg),
		BuildClusterRole(),
		BuildClusterRoleBinding(cfg),
		BuildDeployment(cfg),
		BuildService(cfg),
	)
	ctx := context.Background()

	// Now disable the dashboard
	r.Config.Enabled = false

	result, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue when disabled, got %v", result.RequeueAfter)
	}

	// Verify all resources were cleaned up
	assertResourceNotFound(t, r.Client, ctx, "test-ns", &appsv1.Deployment{})
	assertResourceNotFound(t, r.Client, ctx, "test-ns", &corev1.Service{})
	assertResourceNotFound(t, r.Client, ctx, "test-ns", &corev1.ServiceAccount{})
	assertClusterResourceNotFound(t, r.Client, ctx, &rbacv1.ClusterRole{})
	assertClusterResourceNotFound(t, r.Client, ctx, &rbacv1.ClusterRoleBinding{})
}

func TestReconcile_Cleanup_SkipsUnmanagedResources(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}

	// Create a service with the same name but WITHOUT our labels
	unmanagedSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: "test-ns",
			Labels:    map[string]string{"app": "something-else"},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80}},
		},
	}

	r := newReconciler(cfg, unmanagedSvc)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The unmanaged service should still exist
	svc := &corev1.Service{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, svc)
	if err != nil {
		t.Errorf("unmanaged service should not have been deleted: %v", err)
	}
}

func TestReconcile_Cleanup_NoErrorWhenNoResources(t *testing.T) {
	r := newReconciler(Config{Enabled: false, Namespace: "test-ns"})
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("cleanup with no existing resources should not error: %v", err)
	}
}

func TestReconcile_Idempotent(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}

	r := newReconciler(cfg)
	ctx := context.Background()

	// First reconcile
	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	// Second reconcile (should be idempotent)
	_, err = r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Resources should still exist and be correct
	deploy := &appsv1.Deployment{}
	_ = r.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, deploy)
	if deploy.Spec.Template.Spec.Containers[0].Image != cfg.Image {
		t.Errorf("expected image %q after idempotent reconcile, got %q", cfg.Image, deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

// --- helpers ---

func assertResourceExists(t *testing.T, c client.Client, ctx context.Context, key client.ObjectKey, obj client.Object) {
	t.Helper()
	if err := c.Get(ctx, key, obj); err != nil {
		t.Errorf("expected resource %T %v to exist: %v", obj, key, err)
	}
}

func assertResourceNotFound(t *testing.T, c client.Client, ctx context.Context, namespace string, obj client.Object) {
	t.Helper()
	key := client.ObjectKey{Namespace: namespace, Name: Name}
	err := c.Get(ctx, key, obj)
	if err == nil {
		t.Errorf("expected resource %T %v to not exist", obj, key)
	} else if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound error for %T %v, got: %v", obj, key, err)
	}
}

func assertClusterResourceNotFound(t *testing.T, c client.Client, ctx context.Context, obj client.Object) {
	t.Helper()
	key := client.ObjectKey{Name: Name}
	err := c.Get(ctx, key, obj)
	if err == nil {
		t.Errorf("expected cluster resource %T %v to not exist", obj, key)
	} else if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound error for %T %v, got: %v", obj, key, err)
	}
}
