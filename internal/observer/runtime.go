/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package observer reads Kubernetes resources and produces a RuntimeSnapshot.
// It does NOT validate anything — that is the validator's job.
package observer

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RuntimeSnapshot is the deterministic observation of cluster state.
// It contains only facts — no interpretation, no inference.
type RuntimeSnapshot struct {
	ServiceExists  bool
	WorkloadExists bool
	WorkloadKind   string
	ServicePorts   []int32

	// Extended runtime observations for contract reconciliation.
	DeploymentStrategy      string // "RollingUpdate", "Recreate", or "" if not a Deployment
	PodManagementPolicy     string // "OrderedReady", "Parallel", or "" if not a StatefulSet
	TerminationGracePeriod  *int64
	ContainerImages         []string // images from the first container in the pod spec
	HasPVC                  bool     // workload references PersistentVolumeClaims
	HasEmptyDir             bool     // workload uses emptyDir volumes
	HealthProbeInitialDelay *int32   // from readiness or liveness probe on first container
}

// Observer inspects Kubernetes resources to produce a RuntimeSnapshot.
type Observer struct {
	client client.Client
}

// New creates a new Observer.
func New(c client.Client) *Observer {
	return &Observer{client: c}
}

// Observe reads the target Service and workload, returning a snapshot of what exists.
func (o *Observer) Observe(ctx context.Context, namespace, serviceName, workloadName, workloadKind string) (*RuntimeSnapshot, error) {
	snapshot := &RuntimeSnapshot{
		WorkloadKind: workloadKind,
	}

	// Observe Service
	if serviceName != "" {
		svc := &corev1.Service{}
		err := o.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: serviceName}, svc)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return nil, fmt.Errorf("failed to get service %s: %w", serviceName, err)
			}
		} else {
			snapshot.ServiceExists = true
			for _, port := range svc.Spec.Ports {
				snapshot.ServicePorts = append(snapshot.ServicePorts, port.Port)
			}
		}
	}

	// Observe Workload
	if workloadName != "" {
		if err := o.observeWorkload(ctx, namespace, workloadName, workloadKind, snapshot); err != nil {
			return nil, err
		}
	}

	return snapshot, nil
}

// observeWorkload reads the workload resource and populates extended snapshot fields.
func (o *Observer) observeWorkload(ctx context.Context, namespace, name, kind string, snap *RuntimeSnapshot) error {
	key := types.NamespacedName{Namespace: namespace, Name: name}

	switch kind {
	case "Deployment":
		return o.observeDeployment(ctx, key, snap)
	case "StatefulSet":
		return o.observeStatefulSet(ctx, key, snap)
	case "ReplicaSet":
		return o.observeReplicaSet(ctx, key, snap)
	case "Job":
		return o.observeJob(ctx, key, snap)
	case "CronJob":
		return o.observeCronJob(ctx, key, snap)
	default:
		return o.observeDeployment(ctx, key, snap)
	}
}

func (o *Observer) observeDeployment(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	dep := &appsv1.Deployment{}
	if err := o.client.Get(ctx, key, dep); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get Deployment %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	if dep.Spec.Strategy.Type != "" {
		snap.DeploymentStrategy = string(dep.Spec.Strategy.Type)
	}
	o.extractPodTemplateInfo(&dep.Spec.Template.Spec, snap)
	return nil
}

func (o *Observer) observeStatefulSet(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	sts := &appsv1.StatefulSet{}
	if err := o.client.Get(ctx, key, sts); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get StatefulSet %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	snap.PodManagementPolicy = string(sts.Spec.PodManagementPolicy)
	// StatefulSets with volumeClaimTemplates use PVCs
	if len(sts.Spec.VolumeClaimTemplates) > 0 {
		snap.HasPVC = true
	}
	o.extractPodTemplateInfo(&sts.Spec.Template.Spec, snap)
	return nil
}

func (o *Observer) observeReplicaSet(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	rs := &appsv1.ReplicaSet{}
	if err := o.client.Get(ctx, key, rs); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get ReplicaSet %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	o.extractPodTemplateInfo(&rs.Spec.Template.Spec, snap)
	return nil
}

func (o *Observer) observeJob(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	job := &batchv1.Job{}
	if err := o.client.Get(ctx, key, job); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get Job %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	o.extractPodTemplateInfo(&job.Spec.Template.Spec, snap)
	return nil
}

func (o *Observer) observeCronJob(ctx context.Context, key types.NamespacedName, snap *RuntimeSnapshot) error {
	cj := &batchv1.CronJob{}
	if err := o.client.Get(ctx, key, cj); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get CronJob %s: %w", key.Name, err)
		}
		return nil
	}
	snap.WorkloadExists = true
	o.extractPodTemplateInfo(&cj.Spec.JobTemplate.Spec.Template.Spec, snap)
	return nil
}

// extractPodTemplateInfo reads common fields from a PodSpec into the snapshot.
func (o *Observer) extractPodTemplateInfo(podSpec *corev1.PodSpec, snap *RuntimeSnapshot) {
	// Termination grace period
	snap.TerminationGracePeriod = podSpec.TerminationGracePeriodSeconds

	// Container images
	for _, c := range podSpec.Containers {
		snap.ContainerImages = append(snap.ContainerImages, c.Image)
	}

	// Health probe initial delay (from first container's readiness or liveness probe)
	if len(podSpec.Containers) > 0 {
		c := podSpec.Containers[0]
		if c.ReadinessProbe != nil {
			delay := c.ReadinessProbe.InitialDelaySeconds
			snap.HealthProbeInitialDelay = &delay
		} else if c.LivenessProbe != nil {
			delay := c.LivenessProbe.InitialDelaySeconds
			snap.HealthProbeInitialDelay = &delay
		}
	}

	// Volume analysis
	for _, vol := range podSpec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			snap.HasPVC = true
		}
		if vol.EmptyDir != nil {
			snap.HasEmptyDir = true
		}
	}
}
