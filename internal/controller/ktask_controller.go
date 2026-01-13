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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	customv1 "github.com/kndclark/kubetasker/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	reconcileDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "kubetasker",
			Subsystem: "controller",
			Name:      "reconcile_duration_seconds",
			Help:      "Time spent reconciling Ktask resources",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"result"},
	)

	reconcileErrors = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kubetasker",
			Subsystem: "controller",
			Name:      "reconcile_errors_total",
			Help:      "Total number of reconciliation errors",
		},
	)

	activeReconciles = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kubetasker",
			Subsystem: "controller",
			Name:      "active_reconciles",
			Help:      "Number of active reconciliations",
		},
	)
)

// KtaskReconciler reconciles a Ktask object
type KtaskReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	DefaultResources corev1.ResourceRequirements
}

// +kubebuilder:rbac:groups=task.ktasker.com,resources=ktasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=task.ktasker.com,resources=ktasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=task.ktasker.com,resources=ktasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *KtaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	start := time.Now()
	activeReconciles.Inc()
	defer func() {
		activeReconciles.Dec()
		duration := time.Since(start).Seconds()

		result := "success"
		if err != nil {
			result = "error"
			reconcileErrors.Inc()
		}
		reconcileDuration.WithLabelValues(result).Observe(duration)
	}()

	log := logf.FromContext(ctx)

	// Create a contextual logger with the Ktask's name and namespace.
	// This will be used for all subsequent logs in this reconciliation loop.
	log = log.WithValues("ktask", req.NamespacedName)
	log.Info("Reconciling Ktask")

	// 1. Fetch the Ktask instance
	var ktask customv1.Ktask
	if err := r.Get(ctx, req.NamespacedName, &ktask); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Ktask resource deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Ktask")
		return ctrl.Result{}, err
	}

	// 2. Check if a Job for this Ktask already exists
	var childJob batchv1.Job
	jobName := fmt.Sprintf("%s-job", ktask.Name)
	if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: req.Namespace}, &childJob); err == nil {
		return r.reconcileExistingJob(ctx, &ktask, &childJob)
	} else if !errors.IsNotFound(err) {
		// Some other error occurred when trying to get the job.
		log.Error(err, "Failed to get child Job", "Job", jobName)
		return ctrl.Result{}, err
	}

	// The backoff limit is defaulted by the webhook, so we can use it directly.
	// This nil check is a safeguard in case webhooks are disabled.
	if ktask.Spec.BackoffLimit == nil {
		ktask.Spec.BackoffLimit = new(int32)
		*ktask.Spec.BackoffLimit = 4
	}

	// 3. If the Job does not exist, and the Ktask is not in a terminal state, create it.
	// Define the new Job from the Ktask's spec
	jobResources := ktask.Spec.Resources
	if len(jobResources.Limits) == 0 && len(jobResources.Requests) == 0 {
		jobResources = r.DefaultResources
	}

	newJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: ktask.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					ServiceAccountName: ktask.Spec.ServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:    "job-container",
							Image:   ktask.Spec.Image,
							Command: ktask.Spec.Command,
							Env:     ktask.Spec.Env,
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:                &[]int64{1001}[0],
								RunAsGroup:               &[]int64{1001}[0],
								AllowPrivilegeEscalation: &[]bool{false}[0],
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{
										"ALL",
									},
								},
							},
							Resources: jobResources,
						},
					},
					RestartPolicy: corev1.RestartPolicy(ktask.Spec.RestartPolicy),
				},
			},
			BackoffLimit: ktask.Spec.BackoffLimit,
		},
	}

	// Set the Ktask as the owner of this Job. When the Ktask is deleted,
	// Kubernetes will automatically delete the Job it owns.
	if err := ctrl.SetControllerReference(&ktask, newJob, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference on Job")
		return ctrl.Result{}, err
	}

	// Check if the Ktask is already in a terminal phase before creating a new Job.
	if ktask.Status.Phase == "" || ktask.Status.Phase == customv1.PhasePending {
		log.Info("Creating a new Job for Ktask")
		if err := r.Create(ctx, newJob); err != nil {
			log.Error(err, "Failed to create new Job", "Job", client.ObjectKeyFromObject(newJob))
			return ctrl.Result{}, err
		}

		// Job created successfully, update the status of the Ktask to Processing.
		log.Info("Ktask processing")
		ktask.Status.Phase = customv1.PhaseProcessing
		if err := r.Status().Update(ctx, &ktask); err != nil {
			log.Error(err, "Failed to update Ktask status to Processing")
			return ctrl.Result{}, err
		}

		// Requeue the request to check the job status in the next reconciliation.
		return ctrl.Result{RequeueAfter: time.Second * 1}, nil
	} else {
		log.Info("Ktask is in a terminal phase, skipping Job creation", "phase", ktask.Status.Phase)
	}

	return ctrl.Result{}, nil
}

// reconcileExistingJob handles the logic for a Ktask that already has an associated Job.
func (r *KtaskReconciler) reconcileExistingJob(ctx context.Context, ktask *customv1.Ktask, childJob *batchv1.Job) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	jobName := childJob.Name

	// 1. Check for Job completion first. This is a terminal state.
	if childJob.Status.Succeeded > 0 {
		log.Info("Child Job has succeeded")
		if ktask.Status.Phase != customv1.PhaseSucceeded {
			ktask.Status.Phase = customv1.PhaseSucceeded
			meta.SetStatusCondition(&ktask.Status.Conditions, metav1.Condition{
				Type:    customv1.JobReady,
				Status:  metav1.ConditionTrue,
				Reason:  "JobSucceeded",
				Message: "The job completed successfully.",
			})
			if err := r.Status().Update(ctx, ktask); err != nil {
				return ctrl.Result{}, err // Requeue on error
			}
			return ctrl.Result{RequeueAfter: time.Second * 1}, nil // Requeue to confirm status
		}
		return ctrl.Result{}, nil
	}

	// 2. Check for "fail-fast" conditions by inspecting Pods.
	if updated, err := r.checkPodFailures(ctx, ktask, jobName); err != nil || updated {
		if err != nil {
			log.Error(err, "Failed to check pod failures")
		}
		return ctrl.Result{}, err
	}

	// 3. If no fail-fast condition was met, check if the Job itself has officially failed.
	if childJob.Status.Failed > 0 {
		log.Info("Child Job has failed according to its own status")
		if ktask.Status.Phase != customv1.PhaseFailed {
			ktask.Status.Phase = customv1.PhaseFailed

			reason := customv1.ReasonTransientFailure // Default to transient
			message := "Job failed after reaching its backoff limit."
			for _, cond := range childJob.Status.Conditions {
				if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
					if cond.Reason == "ImagePullBackOff" || cond.Reason == "ErrImagePull" {
						reason = customv1.ReasonPermanentFailure
						message = "Job failed due to an image pull error."
					}
					break
				}
			}

			meta.SetStatusCondition(&ktask.Status.Conditions, metav1.Condition{Type: customv1.JobReady, Status: metav1.ConditionFalse, Reason: reason, Message: message})
			if err := r.Status().Update(ctx, ktask); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil // Stop reconciliation
	}

	// 4. If none of the above, the job is still processing.
	log.Info("Child Job is still processing")
	return ctrl.Result{RequeueAfter: time.Second * 2}, nil
}

// checkPodFailures inspects the pods of a job for fail-fast conditions.
// It returns true if the Ktask status was updated, and an error if one occurred.
func (r *KtaskReconciler) checkPodFailures(ctx context.Context, ktask *customv1.Ktask, jobName string) (bool, error) {
	log := logf.FromContext(ctx)
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(ktask.Namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return false, fmt.Errorf("failed to list pods for Job: %w", err)
	}

	for _, pod := range podList.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil {
				reason := containerStatus.State.Waiting.Reason
				if reason == "ImagePullBackOff" || reason == "ErrImagePull" || reason == "CreateContainerConfigError" {
					var failureType, message string
					if reason == "CreateContainerConfigError" {
						failureType = customv1.ReasonConflictError
						message = fmt.Sprintf("Job failed due to a configuration error: %s", containerStatus.State.Waiting.Message)
					} else {
						failureType = customv1.ReasonPermanentFailure
						message = fmt.Sprintf("Job failed due to an image pull error: %s", containerStatus.State.Waiting.Message)
					}

					log.Info("Detected permanent pod failure", "reason", reason)
					ktask.Status.Phase = customv1.PhaseFailed
					meta.SetStatusCondition(&ktask.Status.Conditions, metav1.Condition{Type: customv1.JobReady, Status: metav1.ConditionFalse, Reason: failureType, Message: message})
					if err := r.Status().Update(ctx, ktask); err != nil {
						return false, err
					}
					return true, nil // Status updated, stop further reconciliation
				}
			}
			// Check for OOMKilled in Terminated state or LastTerminationState
			// This ensures we catch the failure even if the pod has already restarted (CrashLoopBackOff)
			if (containerStatus.State.Terminated != nil && containerStatus.State.Terminated.Reason == "OOMKilled") ||
				(containerStatus.LastTerminationState.Terminated != nil && containerStatus.LastTerminationState.Terminated.Reason == "OOMKilled") {
				log.Info("Detected OOMKilled pod failure")
				ktask.Status.Phase = customv1.PhaseFailed
				meta.SetStatusCondition(&ktask.Status.Conditions, metav1.Condition{
					Type:    customv1.JobReady,
					Status:  metav1.ConditionFalse,
					Reason:  customv1.ReasonPermanentFailure,
					Message: "Job failed due to OOMKilled (Out of Memory).",
				})
				if err := r.Status().Update(ctx, ktask); err != nil {
					return false, err
				}
				return true, nil // Status updated, stop further reconciliation
			}
		}
	}
	return false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KtaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&customv1.Ktask{}).
		Named("ktask").
		Owns(&batchv1.Job{}).
		Complete(r)
}
