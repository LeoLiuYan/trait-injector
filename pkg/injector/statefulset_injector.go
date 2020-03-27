package injector

import (
	"encoding/json"
	"fmt"
	"path"

	"github.com/go-logr/logr"
	corev1alpha1 "github.com/oam-dev/trait-injector/api/v1alpha1"
	"github.com/oam-dev/trait-injector/pkg/plugin"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var _ plugin.TargetInjector = &StatefulsetTargetInjector{}

type StatefulsetTargetInjector struct {
	Log logr.Logger
}

func newStatefulsetTargetInjector() *StatefulsetTargetInjector {
	return &StatefulsetTargetInjector{
		Log: ctrl.Log.WithName("targetInjectors").WithName("Statefulset"),
	}
}

func (ti *StatefulsetTargetInjector) Name() string {
	return "StatefulsetTargetInjector"
}

func (ti *StatefulsetTargetInjector) Match(req *admissionv1beta1.AdmissionRequest, w *corev1alpha1.WorkloadReference) bool {
	k := req.Kind
	if k.Group == "apps" && k.Version == "v1" && k.Kind == "StatefulSet" && req.Name == w.Name {
		return true
	}
	return false
}

func (ti *StatefulsetTargetInjector) Inject(ctx plugin.TargetContext, raw runtime.RawExtension) ([]webhook.JSONPatchOp, error) {
	var statefulSet *appsv1.StatefulSet
	err := json.Unmarshal(raw.Raw, &statefulSet)
	if err != nil {
		return nil, err
	}

	var patches []webhook.JSONPatchOp

	b := ctx.Binding
	secretName, pvcName := getValues(ctx)
	volumemountName := makeVolumeMountName(secretName, pvcName)
	// Inject secret to env in deployment
	if b.To.Env {
		for i, c := range statefulSet.Spec.Template.Spec.Containers {
			if len(c.EnvFrom) == 0 {
				patch := webhook.JSONPatchOp{
					Operation: "add",
					Path:      fmt.Sprintf("/spec/template/spec/containers/%d/envFrom", i),
					Value:     []corev1.EnvFromSource{},
				}
				patches = append(patches, patch)
			}

			patch := webhook.JSONPatchOp{
				Operation: "add",
				Path:      fmt.Sprintf("/spec/template/spec/containers/%d/envFrom/-", i),
				Value: corev1.EnvFromSource{
					SecretRef: &corev1.SecretEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secretName,
						},
					},
				},
			}
			patches = append(patches, patch)
		}
		ti.Log.Info("injected secret to env", "statefulSet", path.Join(statefulSet.Namespace, statefulSet.Name))
	}

	// inject secret as file in Pod
	if len(b.To.FilePath) != 0 {
		if len(statefulSet.Spec.Template.Spec.Volumes) == 0 {
			patch := webhook.JSONPatchOp{
				Operation: "add",
				Path:      "/spec/template/spec/volumes",
				Value:     []corev1.Volume{},
			}
			patches = append(patches, patch)
		}

		patch := webhook.JSONPatchOp{
			Operation: "add",
			Path:      "/spec/template/spec/volumes/-",
			Value: corev1.Volume{
				Name:         volumemountName,
				VolumeSource: makeVolumeSource(secretName, pvcName),
			},
		}
		patches = append(patches, patch)

		for i, c := range statefulSet.Spec.Template.Spec.Containers {
			if len(c.VolumeMounts) == 0 {
				patch := webhook.JSONPatchOp{
					Operation: "add",
					Path:      fmt.Sprintf("/spec/template/spec/containers/%d/volumeMounts", i),
					Value:     []corev1.VolumeMount{},
				}
				patches = append(patches, patch)
			}

			patch := webhook.JSONPatchOp{
				Operation: "add",
				Path:      fmt.Sprintf("/spec/template/spec/containers/%d/volumeMounts/-", i),
				Value: corev1.VolumeMount{
					Name:      volumemountName,
					MountPath: b.To.FilePath,
				},
			}
			patches = append(patches, patch)
		}

		ti.Log.Info("injected volume to file", "statefulSet", path.Join(statefulSet.Namespace, statefulSet.Name))
	}

	return patches, nil
}
