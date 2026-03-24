package observer

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func int64Ptr(v int64) *int64 { return &v }

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	return s
}

func TestObserve_DeploymentWithDetails(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: int64Ptr(30),
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "ghcr.io/org/app:v1.0.0",
							ReadinessProbe: &corev1.Probe{
								InitialDelaySeconds: 10,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name:         "data",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8080}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep, svc).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "my-app", "my-app", "Deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.ServiceExists {
		t.Error("expected ServiceExists=true")
	}
	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if snap.DeploymentStrategy != "RollingUpdate" {
		t.Errorf("expected RollingUpdate, got %s", snap.DeploymentStrategy)
	}
	if snap.TerminationGracePeriod == nil || *snap.TerminationGracePeriod != 30 {
		t.Errorf("expected grace period 30, got %v", snap.TerminationGracePeriod)
	}
	if len(snap.ContainerImages) != 1 || snap.ContainerImages[0] != "ghcr.io/org/app:v1.0.0" {
		t.Errorf("expected image ghcr.io/org/app:v1.0.0, got %v", snap.ContainerImages)
	}
	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 10 {
		t.Errorf("expected probe delay 10, got %v", snap.HealthProbeInitialDelay)
	}
	if !snap.HasEmptyDir {
		t.Error("expected HasEmptyDir=true")
	}
	if snap.HasPVC {
		t.Error("expected HasPVC=false")
	}
}

func TestObserve_StatefulSetWithPVC(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			PodManagementPolicy: appsv1.OrderedReadyPodManagement,
			Selector:            &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "db", Image: "postgres:15"},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(sts).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "", "db", "StatefulSet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if snap.PodManagementPolicy != "OrderedReady" {
		t.Errorf("expected OrderedReady, got %s", snap.PodManagementPolicy)
	}
	if !snap.HasPVC {
		t.Error("expected HasPVC=true from volumeClaimTemplates")
	}
	if len(snap.ContainerImages) != 1 || snap.ContainerImages[0] != "postgres:15" {
		t.Errorf("expected postgres:15, got %v", snap.ContainerImages)
	}
}

func TestObserve_Job(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "migration", Namespace: "default"},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "migrate", Image: "ghcr.io/org/migrate:v1"},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(job).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "", "migration", "Job")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if snap.WorkloadKind != "Job" {
		t.Errorf("expected Job, got %s", snap.WorkloadKind)
	}
	if len(snap.ContainerImages) != 1 || snap.ContainerImages[0] != "ghcr.io/org/migrate:v1" {
		t.Errorf("expected migrate image, got %v", snap.ContainerImages)
	}
}

func TestObserve_CronJob(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "cleanup", Namespace: "default"},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "cleanup", Image: "ghcr.io/org/cleanup:v1"},
							},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(cj).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "", "cleanup", "CronJob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
}

func TestObserve_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "nonexistent", "nonexistent", "Deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap.ServiceExists {
		t.Error("expected ServiceExists=false")
	}
	if snap.WorkloadExists {
		t.Error("expected WorkloadExists=false")
	}
}

func TestObserve_LivenessProbe(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "app:latest",
							LivenessProbe: &corev1.Probe{
								InitialDelaySeconds: 15,
							},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "app", "Deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 15 {
		t.Errorf("expected liveness probe delay 15, got %v", snap.HealthProbeInitialDelay)
	}
}

func TestObserve_PVCVolume(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "app:latest"},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "data-pvc",
								},
							},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "app", "Deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.HasPVC {
		t.Error("expected HasPVC=true from PVC volume")
	}
}

func TestObserve_ReplicaSet(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "app:v1"},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(rs).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "app", "ReplicaSet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if len(snap.ContainerImages) != 1 || snap.ContainerImages[0] != "app:v1" {
		t.Errorf("expected app:v1, got %v", snap.ContainerImages)
	}
}

func TestObserve_ReplicaSetWithVolumesAndProbes(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "app-rs", Namespace: "default"},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "app-rs"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "app-rs"}},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: int64Ptr(60),
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "ghcr.io/org/app:v2.0.0",
							ReadinessProbe: &corev1.Probe{
								InitialDelaySeconds: 5,
							},
						},
						{
							Name:  "sidecar",
							Image: "ghcr.io/org/sidecar:v1.0.0",
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "data-pvc",
								},
							},
						},
						{
							Name:         "cache",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(rs).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "app-rs", "ReplicaSet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if len(snap.ContainerImages) != 2 {
		t.Fatalf("expected 2 images, got %d", len(snap.ContainerImages))
	}
	if snap.ContainerImages[0] != "ghcr.io/org/app:v2.0.0" {
		t.Errorf("expected first image ghcr.io/org/app:v2.0.0, got %s", snap.ContainerImages[0])
	}
	if snap.ContainerImages[1] != "ghcr.io/org/sidecar:v1.0.0" {
		t.Errorf("expected second image ghcr.io/org/sidecar:v1.0.0, got %s", snap.ContainerImages[1])
	}
	if !snap.HasPVC {
		t.Error("expected HasPVC=true")
	}
	if !snap.HasEmptyDir {
		t.Error("expected HasEmptyDir=true")
	}
	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 5 {
		t.Errorf("expected probe delay 5, got %v", snap.HealthProbeInitialDelay)
	}
	if snap.TerminationGracePeriod == nil || *snap.TerminationGracePeriod != 60 {
		t.Errorf("expected grace period 60, got %v", snap.TerminationGracePeriod)
	}
}

func TestObserve_JobWithVolumesAndProbes(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "etl-job", Namespace: "default"},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: int64Ptr(120),
					Containers: []corev1.Container{
						{
							Name:  "etl",
							Image: "ghcr.io/org/etl:v3",
							LivenessProbe: &corev1.Probe{
								InitialDelaySeconds: 20,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "input",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "input-pvc",
								},
							},
						},
						{
							Name:         "tmp",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(job).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "etl-job", "Job")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if len(snap.ContainerImages) != 1 || snap.ContainerImages[0] != "ghcr.io/org/etl:v3" {
		t.Errorf("expected etl image, got %v", snap.ContainerImages)
	}
	if !snap.HasPVC {
		t.Error("expected HasPVC=true")
	}
	if !snap.HasEmptyDir {
		t.Error("expected HasEmptyDir=true")
	}
	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 20 {
		t.Errorf("expected probe delay 20, got %v", snap.HealthProbeInitialDelay)
	}
	if snap.TerminationGracePeriod == nil || *snap.TerminationGracePeriod != 120 {
		t.Errorf("expected grace period 120, got %v", snap.TerminationGracePeriod)
	}
}

func TestObserve_CronJobWithVolumesAndProbes(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "report", Namespace: "default"},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 2 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							TerminationGracePeriodSeconds: int64Ptr(45),
							Containers: []corev1.Container{
								{
									Name:  "report",
									Image: "ghcr.io/org/report:v2",
									ReadinessProbe: &corev1.Probe{
										InitialDelaySeconds: 3,
									},
								},
								{
									Name:  "exporter",
									Image: "ghcr.io/org/exporter:v1",
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "output",
									VolumeSource: corev1.VolumeSource{
										PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
											ClaimName: "output-pvc",
										},
									},
								},
								{
									Name:         "scratch",
									VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
								},
							},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(cj).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "report", "CronJob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if len(snap.ContainerImages) != 2 {
		t.Fatalf("expected 2 images, got %d", len(snap.ContainerImages))
	}
	if snap.ContainerImages[0] != "ghcr.io/org/report:v2" {
		t.Errorf("expected first image ghcr.io/org/report:v2, got %s", snap.ContainerImages[0])
	}
	if snap.ContainerImages[1] != "ghcr.io/org/exporter:v1" {
		t.Errorf("expected second image ghcr.io/org/exporter:v1, got %s", snap.ContainerImages[1])
	}
	if !snap.HasPVC {
		t.Error("expected HasPVC=true")
	}
	if !snap.HasEmptyDir {
		t.Error("expected HasEmptyDir=true")
	}
	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 3 {
		t.Errorf("expected probe delay 3, got %v", snap.HealthProbeInitialDelay)
	}
	if snap.TerminationGracePeriod == nil || *snap.TerminationGracePeriod != 45 {
		t.Errorf("expected grace period 45, got %v", snap.TerminationGracePeriod)
	}
}

func TestObserve_NotFound_AllKinds(t *testing.T) {
	kinds := []string{"StatefulSet", "ReplicaSet", "Job", "CronJob"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			fc := fake.NewClientBuilder().WithScheme(newScheme()).Build()
			obs := New(fc)

			snap, err := obs.Observe(context.Background(), "default", "", "nonexistent", kind)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", kind, err)
			}
			if snap.WorkloadExists {
				t.Errorf("expected WorkloadExists=false for missing %s", kind)
			}
		})
	}
}

func TestObserve_UnsupportedKindFallsBackToDeployment(t *testing.T) {
	// The default case in observeWorkload falls through to observeDeployment.
	// With no Deployment present, WorkloadExists should be false.
	fc := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "something", "DaemonSet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.WorkloadExists {
		t.Error("expected WorkloadExists=false for unsupported kind with no deployment")
	}
	if snap.WorkloadKind != "DaemonSet" {
		t.Errorf("expected WorkloadKind=DaemonSet, got %s", snap.WorkloadKind)
	}
}

func TestObserve_ServiceGetError(t *testing.T) {
	// A scheme missing corev1 will cause Get(Service) to fail with a non-NotFound error.
	brokenScheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "my-svc", "", "Deployment")
	if err == nil {
		t.Fatal("expected error when scheme cannot handle Service")
	}
}

func TestObserve_DeploymentGetError(t *testing.T) {
	// A scheme missing appsv1 will cause Get(Deployment) to fail with a non-NotFound error.
	brokenScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(brokenScheme)
	_ = batchv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "", "app", "Deployment")
	if err == nil {
		t.Fatal("expected error for Deployment with broken scheme")
	}
}

func TestObserve_StatefulSetGetError(t *testing.T) {
	brokenScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(brokenScheme)
	_ = batchv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "", "sts", "StatefulSet")
	if err == nil {
		t.Fatal("expected error for StatefulSet with broken scheme")
	}
}

func TestObserve_ReplicaSetGetError(t *testing.T) {
	brokenScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(brokenScheme)
	_ = batchv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "", "rs", "ReplicaSet")
	if err == nil {
		t.Fatal("expected error for ReplicaSet with broken scheme")
	}
}

func TestObserve_JobGetError(t *testing.T) {
	brokenScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(brokenScheme)
	_ = appsv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "", "job", "Job")
	if err == nil {
		t.Fatal("expected error for Job with broken scheme")
	}
}

func TestObserve_CronJobGetError(t *testing.T) {
	brokenScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(brokenScheme)
	_ = appsv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "", "cj", "CronJob")
	if err == nil {
		t.Fatal("expected error for CronJob with broken scheme")
	}
}

func TestObserve_UnsupportedKindWithDeploymentPresent(t *testing.T) {
	// When an unsupported kind is given but a Deployment with the same name exists,
	// the default branch finds it via observeDeployment.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ds", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-ds"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-ds"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "app:v1"},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	snap, err := obs.Observe(context.Background(), "default", "", "my-ds", "DaemonSet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true (default falls back to Deployment)")
	}
}
