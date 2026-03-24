/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// newReconcilerWithInterceptors creates a Reconciler with a fake client that uses interceptor functions.
func newReconcilerWithInterceptors(cfg Config, funcs interceptor.Funcs, objs ...client.Object) *Reconciler {
	scheme := newScheme()
	builder := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(funcs)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return &Reconciler{
		Client: builder.Build(),
		Scheme: scheme,
		Config: cfg,
	}
}

// --- ensureNamespace: non-NotFound Get error ---

func TestEnsureNamespace_GetNonNotFoundError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Namespace); ok {
				return fmt.Errorf("simulated namespace get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from ensureNamespace Get failure")
	}
	if got := err.Error(); got != "namespace: simulated namespace get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- reconcileServiceAccount error ---

func TestReconcile_ServiceAccountError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.ServiceAccount); ok {
				return fmt.Errorf("simulated sa get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileServiceAccount")
	}
	if got := err.Error(); got != "service account: simulated sa get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- reconcileClusterRole error ---

func TestReconcile_ClusterRoleError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*rbacv1.ClusterRole); ok {
				return fmt.Errorf("simulated cr get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileClusterRole")
	}
	if got := err.Error(); got != "cluster role: simulated cr get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- reconcileClusterRoleBinding error ---

func TestReconcile_ClusterRoleBindingError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*rbacv1.ClusterRoleBinding); ok {
				return fmt.Errorf("simulated crb get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileClusterRoleBinding")
	}
	if got := err.Error(); got != "cluster role binding: simulated crb get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- reconcileDeployment error ---

func TestReconcile_DeploymentError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*appsv1.Deployment); ok {
				return fmt.Errorf("simulated deploy get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileDeployment")
	}
	if got := err.Error(); got != "deployment: simulated deploy get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- reconcileService error ---

func TestReconcile_ServiceError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return fmt.Errorf("simulated svc get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileService")
	}
	if got := err.Error(); got != "service: simulated svc get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- applyResource: Get non-NotFound error (covered via the individual reconcile step error tests above) ---
// The tests above already trigger the `if err != nil { return err }` path in applyResource
// since the interceptor returns a non-NotFound error on Get.

// --- cleanup: Get non-NotFound error ---

func TestCleanup_GetNonNotFoundError(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	// Inject a Get error for Service (first resource in cleanup order)
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return fmt.Errorf("simulated cleanup get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from cleanup Get failure")
	}
	if got := err.Error(); got != "failed to get Service: simulated cleanup get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- cleanup: Delete error ---

func TestCleanup_DeleteError(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	// Pre-create a managed service so cleanup will try to delete it
	svc := BuildService(Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"})

	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("simulated delete error")
		},
	}, svc)

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from cleanup Delete failure")
	}
	if got := err.Error(); got != "failed to delete Service: simulated delete error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- Start: disabled (no ticker, returns nil) ---

func TestStart_Disabled(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	r := newReconciler(cfg)

	err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start with disabled dashboard should return nil: %v", err)
	}
}

// --- Start: enabled with context cancel ---

func TestStart_Enabled_ContextCancel(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconciler(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the ticker loop exits on first select
	cancel()

	err := r.Start(ctx)
	if err != nil {
		t.Fatalf("Start with cancelled context should return nil: %v", err)
	}
}

// --- Start: initial reconcile failure ---

func TestStart_InitialReconcileFailure(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	// Use a scheme missing Namespace type to cause ensureNamespace to fail
	brokenScheme := runtime.NewScheme()
	// Register enough types for the client to work but NOT Namespace
	_ = appsv1.AddToScheme(brokenScheme)
	_ = rbacv1.AddToScheme(brokenScheme)
	// Don't register corev1, so Namespace Get will fail

	// Instead, use interceptors to force an error on the initial reconcile
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Namespace); ok {
				return fmt.Errorf("simulated start failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	err := r.Start(context.Background())
	if err == nil {
		t.Fatal("Start should return error when initial reconcile fails")
	}
	expected := "initial dashboard reconciliation failed: namespace: simulated start failure"
	if got := err.Error(); got != expected {
		t.Errorf("unexpected error:\n  got:  %s\n  want: %s", got, expected)
	}
}

// --- Reconcile: cleanup error when disabled ---

func TestReconcile_Disabled_CleanupError(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	// Create a managed deployment so cleanup will attempt to get/delete it
	deploy := BuildDeployment(Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"})

	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("simulated cleanup failure")
		},
	}, deploy)

	result, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error when cleanup fails")
	}
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter to be set on cleanup failure")
	}
}
