/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"context"
	"fmt"
	"maps"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/trianalab/pacto-operator/internal/credentials"
)

// Reconciler manages the lifecycle of dashboard Kubernetes resources.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config Config

	// tickInterval overrides the periodic reconciliation interval (default 5m).
	// Exposed for testing only.
	tickInterval time.Duration
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=create;update;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;delete

// Reconcile ensures dashboard resources match the desired state.
// When the feature is enabled, it creates/updates all dashboard resources.
// When disabled, it cleans up any resources it previously created.
func (r *Reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("dashboard")

	if !r.Config.Enabled {
		log.V(1).Info("Dashboard feature disabled, cleaning up resources")
		if err := r.cleanup(ctx); err != nil {
			log.Error(err, "Failed to clean up dashboard resources")
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling dashboard resources", "image", r.Config.Image, "namespace", r.Config.Namespace)

	if err := r.ensureNamespace(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("namespace: %w", err)
	}
	if err := r.reconcileServiceAccount(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("service account: %w", err)
	}
	if err := r.reconcileClusterRole(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("cluster role: %w", err)
	}
	if err := r.reconcileClusterRoleBinding(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("cluster role binding: %w", err)
	}
	if err := r.reconcileOCICredentials(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("oci credentials: %w", err)
	}
	if err := r.reconcileDeployment(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("deployment: %w", err)
	}
	if err := r.reconcileService(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("service: %w", err)
	}

	log.Info("Dashboard resources reconciled successfully")
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// Start runs the initial reconciliation when the manager starts.
// This implements the manager.Runnable interface.
func (r *Reconciler) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("dashboard")
	log.Info("Starting dashboard reconciler",
		"enabled", r.Config.Enabled,
		"image", r.Config.Image,
		"namespace", r.Config.Namespace,
	)

	// Run initial reconciliation
	if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
		return fmt.Errorf("initial dashboard reconciliation failed: %w", err)
	}

	// If enabled, run periodic reconciliation
	if r.Config.Enabled {
		interval := r.tickInterval
		if interval == 0 {
			interval = 5 * time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
					log.Error(err, "Periodic dashboard reconciliation failed")
				}
			}
		}
	}

	return nil
}

func (r *Reconciler) ensureNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{}
	err := r.Get(ctx, client.ObjectKey{Name: r.Config.Namespace}, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   r.Config.Namespace,
			Labels: Labels(),
		},
	}
	return r.Create(ctx, ns)
}

func (r *Reconciler) reconcileServiceAccount(ctx context.Context) error {
	desired := BuildServiceAccount(r.Config)
	return r.applyResource(ctx, desired, &corev1.ServiceAccount{})
}

func (r *Reconciler) reconcileClusterRole(ctx context.Context) error {
	desired := BuildClusterRole()
	return r.applyResource(ctx, desired, &rbacv1.ClusterRole{})
}

func (r *Reconciler) reconcileClusterRoleBinding(ctx context.Context) error {
	desired := BuildClusterRoleBinding(r.Config)
	return r.applyResource(ctx, desired, &rbacv1.ClusterRoleBinding{})
}

// reconcileOCICredentials reads the configured OCI secrets, merges their credentials,
// and creates/updates a managed dockerconfigjson secret for the dashboard pod.
// If no OCI secrets are configured, it cleans up any previously-created managed secret.
func (r *Reconciler) reconcileOCICredentials(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("dashboard")
	secretNames := r.Config.EffectiveOCISecrets()

	if len(secretNames) == 0 {
		// Clean up managed secret if it exists
		existing := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: r.Config.Namespace, Name: ManagedSecretName}, existing)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return r.Delete(ctx, existing)
	}

	// Read all source secrets
	var sources []*corev1.Secret
	for _, name := range secretNames {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.Config.Namespace, Name: name}, secret); err != nil {
			log.Error(err, "Failed to read OCI secret", "secret", name)
			return fmt.Errorf("reading OCI secret %q: %w", name, err)
		}
		sources = append(sources, secret)
	}

	// Merge credentials into a single dockerconfigjson
	merged, err := credentials.MergeToDockerConfigJSON(sources)
	if err != nil {
		return fmt.Errorf("merging OCI credentials: %w", err)
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ManagedSecretName,
			Namespace: r.Config.Namespace,
			Labels:    Labels(),
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: merged,
		},
	}
	return r.applyResource(ctx, desired, &corev1.Secret{})
}

func (r *Reconciler) reconcileDeployment(ctx context.Context) error {
	desired := BuildDeployment(r.Config)
	return r.applyResource(ctx, desired, &appsv1.Deployment{})
}

func (r *Reconciler) reconcileService(ctx context.Context) error {
	desired := BuildService(r.Config)
	return r.applyResource(ctx, desired, &corev1.Service{})
}

// applyResource creates or updates a resource, preserving the resource version for updates.
// It merges labels and annotations so that external controllers (e.g. ArgoCD) can add
// their own metadata without being wiped on every reconciliation.
func (r *Reconciler) applyResource(ctx context.Context, desired client.Object, existing client.Object) error {
	key := client.ObjectKeyFromObject(desired)
	err := r.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Preserve resource version for update
	desired.SetResourceVersion(existing.GetResourceVersion())
	// Preserve UID to avoid issues
	desired.SetUID(existing.GetUID())
	// Merge labels and annotations so external metadata (e.g. ArgoCD tracking) is preserved
	desired.SetLabels(mergeMap(existing.GetLabels(), desired.GetLabels()))
	desired.SetAnnotations(mergeMap(existing.GetAnnotations(), desired.GetAnnotations()))
	return r.Update(ctx, desired)
}

// mergeMap returns a new map with all entries from base, overridden by entries from overlay.
func mergeMap(base, overlay map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(overlay))
	maps.Copy(merged, base)
	maps.Copy(merged, overlay)
	return merged
}

// cleanup deletes all dashboard resources owned by the operator.
func (r *Reconciler) cleanup(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("dashboard")

	// Delete in reverse order of creation
	resources := []struct {
		name string
		obj  client.Object
		key  client.ObjectKey
	}{
		{"Service", &corev1.Service{}, client.ObjectKey{Namespace: r.Config.Namespace, Name: Name}},
		{"Deployment", &appsv1.Deployment{}, client.ObjectKey{Namespace: r.Config.Namespace, Name: Name}},
		{"Secret", &corev1.Secret{}, client.ObjectKey{Namespace: r.Config.Namespace, Name: ManagedSecretName}},
		{"ClusterRoleBinding", &rbacv1.ClusterRoleBinding{}, client.ObjectKey{Name: Name}},
		{"ClusterRole", &rbacv1.ClusterRole{}, client.ObjectKey{Name: Name}},
		{"ServiceAccount", &corev1.ServiceAccount{}, client.ObjectKey{Namespace: r.Config.Namespace, Name: Name}},
	}

	for _, res := range resources {
		if err := r.Get(ctx, res.key, res.obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to get %s: %w", res.name, err)
		}

		// Only delete resources that have our management labels
		labels := res.obj.GetLabels()
		if labels[LabelManagedBy] != ManagedByValue || labels[LabelComponent] != ComponentValue {
			log.V(1).Info("Skipping resource not managed by us", "kind", res.name, "name", res.key.Name)
			continue
		}

		if err := r.Delete(ctx, res.obj, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete %s: %w", res.name, err)
		}
		log.Info("Deleted dashboard resource", "kind", res.name, "name", res.key.Name)
	}

	return nil
}
