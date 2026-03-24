/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// Resource naming
	Name = "pacto-dashboard"

	// Labels
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelName      = "app.kubernetes.io/name"

	// Values
	ManagedByValue = "pacto-operator"
	ComponentValue = "dashboard"

	// Dashboard defaults
	DashboardPort = 3000
	HealthPath    = "/health"
)

// Labels returns the standard labels applied to all dashboard resources.
func Labels() map[string]string {
	return map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelComponent: ComponentValue,
		LabelName:      Name,
	}
}

// SelectorLabels returns the labels used for pod selection.
func SelectorLabels() map[string]string {
	return map[string]string{
		LabelComponent: ComponentValue,
		LabelName:      Name,
	}
}

// BuildServiceAccount creates the ServiceAccount for the dashboard.
func BuildServiceAccount(cfg Config) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: cfg.Namespace,
			Labels:    Labels(),
		},
	}
}

// BuildClusterRole creates the ClusterRole granting the dashboard read access
// to Pacto CRs and Services.
func BuildClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   Name,
			Labels: Labels(),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"pacto.trianalab.io"},
				Resources: []string{"pactos", "pactorevisions"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"services"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

// BuildClusterRoleBinding binds the dashboard ClusterRole to its ServiceAccount.
func BuildClusterRoleBinding(cfg Config) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   Name,
			Labels: Labels(),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      Name,
				Namespace: cfg.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     Name,
		},
	}
}

// BuildDeployment creates the dashboard Deployment.
func BuildDeployment(cfg Config) *appsv1.Deployment {
	replicas := int32(1)

	env := []corev1.EnvVar{
		{Name: "PACTO_DASHBOARD_PORT", Value: "3000"},
		{Name: "PACTO_NO_UPDATE_CHECK", Value: "1"},
	}

	if cfg.WatchNamespace != "" {
		env = append(env, corev1.EnvVar{
			Name:  "PACTO_WATCH_NAMESPACE",
			Value: cfg.WatchNamespace,
		})
	}

	if cfg.OCISecret != "" {
		env = append(env,
			corev1.EnvVar{
				Name: "PACTO_REGISTRY_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: cfg.OCISecret},
						Key:                  "username",
						Optional:             boolPtr(true),
					},
				},
			},
			corev1.EnvVar{
				Name: "PACTO_REGISTRY_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: cfg.OCISecret},
						Key:                  "password",
						Optional:             boolPtr(true),
					},
				},
			},
			corev1.EnvVar{
				Name: "PACTO_REGISTRY_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: cfg.OCISecret},
						Key:                  "token",
						Optional:             boolPtr(true),
					},
				},
			},
		)
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: cfg.Namespace,
			Labels:    Labels(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: SelectorLabels(),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: Labels(),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: Name,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: boolPtr(true),
						RunAsUser:    int64Ptr(65532),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "dashboard",
							Image: cfg.Image,
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: DashboardPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: env,
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: HealthPath,
										Port: intstr.FromInt32(DashboardPort),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       30,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: HealthPath,
										Port: intstr.FromInt32(DashboardPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem:   boolPtr(true),
								AllowPrivilegeEscalation: boolPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
					TerminationGracePeriodSeconds: int64Ptr(10),
				},
			},
		},
	}
}

// BuildService creates the Service for the dashboard.
func BuildService(cfg Config) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: cfg.Namespace,
			Labels:    Labels(),
		},
		Spec: corev1.ServiceSpec{
			Selector: SelectorLabels(),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       DashboardPort,
					TargetPort: intstr.FromInt32(DashboardPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

func boolPtr(b bool) *bool    { return &b }
func int64Ptr(i int64) *int64 { return &i }
