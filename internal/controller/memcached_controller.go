/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	cachev1alpha1 "memcached-operator/api/v1alpha1"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	typeAvailableMemcached = "Available"
)

// MemcachedReconciler reconciles a Memcached object
type MemcachedReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Memcached object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
func (r *MemcachedReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	memcached := &cachev1alpha1.Memcached{}

	err := r.Get(ctx, req.NamespacedName, memcached)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("memcached resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get memcached resource")
		return ctrl.Result{}, err
	}

	if memcached.Status.Conditions == nil || len(memcached.Status.Conditions) == 0 {
		meta.SetStatusCondition(&memcached.Status.Conditions, metav1.Condition{
			Type:    typeAvailableMemcached,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"})

		if err = r.Status().Update(ctx, memcached); err != nil {
			logger.Error(err, "Failed to update memcached status")
			return ctrl.Result{}, err
		}

		if err = r.Get(ctx, req.NamespacedName, memcached); err != nil {
			logger.Error(err, "Failed to get memcached resource")
			return ctrl.Result{}, err
		}

	}

	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Namespace: memcached.Namespace, Name: memcached.Name}, found)
	if err != nil && apierrors.IsNotFound(err) {
		dep, err2 := r.deploymentForMemcached(memcached)
		if err2 != nil {
			logger.Error(err2, "Failed to define new Deployment resource for Memcached")

			meta.SetStatusCondition(&memcached.Status.Conditions,
				metav1.Condition{Type: typeAvailableMemcached,
					Status:  metav1.ConditionFalse,
					Reason:  "Reconciling",
					Message: fmt.Sprintf("Failed to create Deployment for the custom resource(%s): %s", memcached.Name, err2)})
			if err = r.Status().Update(ctx, memcached); err != nil {
				logger.Error(err, "Failed to update memcached status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}

		logger.Info("Creating a new Memcached Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)

		if err = r.Create(ctx, dep); err != nil {
			logger.Error(err, "Failed to create new Memcached Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: time.Minute}, nil

	} else if err != nil {
		logger.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	size := memcached.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size

		if err = r.Update(ctx, found); err != nil {
			logger.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			if err = r.Get(ctx, req.NamespacedName, memcached); err != nil {
				logger.Error(err, "Failed to re-fetch memcached")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&memcached.Status.Conditions, metav1.Condition{
				Type:    typeAvailableMemcached,
				Status:  metav1.ConditionFalse,
				Reason:  "Resizing",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", memcached.Name, err),
			})
			if err = r.Status().Update(ctx, memcached); err != nil {
				logger.Error(err, "Failed to update memcached status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{Requeue: true}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MemcachedReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cachev1alpha1.Memcached{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

func (r *MemcachedReconciler) deploymentForMemcached(memcached *cachev1alpha1.Memcached) (*appsv1.Deployment, error) {
	replicas := memcached.Spec.Size
	image := "memcached:1.6.26-alpine3.19"

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: memcached.Name, Namespace: memcached.Namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": "project"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app.kubernetes.io/name": "project"},
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Image:           image,
						Name:            "memcached",
						ImagePullPolicy: corev1.PullIfNotPresent,
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             ptr.To(true),
							RunAsUser:                ptr.To(int64(1001)),
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
						Ports: []corev1.ContainerPort{{
							ContainerPort: 11211,
							Name:          "memcached",
						}},
						Command: []string{"memcached", "--memory-limit=64", "-o", "modern", "v"},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(memcached, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}
