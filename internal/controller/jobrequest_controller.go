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
)

// JobRequestReconciler reconciles a JobRequest object
type JobRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=task.ktasker.com,resources=jobrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=task.ktasker.com,resources=jobrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=task.ktasker.com,resources=jobrequests/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *JobRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Create a contextual logger with the JobRequest's name and namespace.
	// This will be used for all subsequent logs in this reconciliation loop.
	log = log.WithValues("jobrequest", req.NamespacedName)
	log.Info("Reconciling JobRequest")

	// 1. Fetch the JobRequest instance
	var jobRequest customv1.JobRequest
	if err := r.Get(ctx, req.NamespacedName, &jobRequest); err != nil {
		if errors.IsNotFound(err) {
			log.Info("JobRequest resource deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get JobRequest")
		return ctrl.Result{}, err
	}

	// 2. Check if a Job for this JobRequest already exists
	var childJob batchv1.Job
	jobName := fmt.Sprintf("%s-job", jobRequest.Name)
	if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: req.Namespace}, &childJob); err == nil {
		return r.reconcileExistingJob(ctx, &jobRequest, &childJob)
	} else if !errors.IsNotFound(err) {
		// Some other error occurred when trying to get the job.
		log.Error(err, "Failed to get child Job", "Job", jobName)
		return ctrl.Result{}, err
	}

	// The backoff limit is defaulted by the webhook, so we can use it directly.
	// This nil check is a safeguard in case webhooks are disabled.
	if jobRequest.Spec.BackoffLimit == nil {
		jobRequest.Spec.BackoffLimit = new(int32)
		*jobRequest.Spec.BackoffLimit = 4
	}

	// 3. If the Job does not exist, and the JobRequest is not in a terminal state, create it.
	// Define the new Job from the JobRequest's spec
	newJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: jobRequest.Namespace,
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
					ServiceAccountName: jobRequest.Spec.ServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:    "job-container",
							Image:   jobRequest.Spec.Image,
							Command: jobRequest.Spec.Command,
							Env:     jobRequest.Spec.Env,
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
						},
					},
					RestartPolicy: corev1.RestartPolicy(jobRequest.Spec.RestartPolicy),
				},
			},
			BackoffLimit: jobRequest.Spec.BackoffLimit,
		},
	}

	// Set the JobRequest as the owner of this Job. When the JobRequest is deleted,
	// Kubernetes will automatically delete the Job it owns.
	if err := ctrl.SetControllerReference(&jobRequest, newJob, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference on Job")
		return ctrl.Result{}, err
	}

	// Check if the JobRequest is already in a terminal phase before creating a new Job.
	if jobRequest.Status.Phase == "" || jobRequest.Status.Phase == customv1.JobRequestPhasePending {
		log.Info("Creating a new Job for JobRequest")
		if err := r.Create(ctx, newJob); err != nil {
			log.Error(err, "Failed to create new Job", "Job", client.ObjectKeyFromObject(newJob))
			return ctrl.Result{}, err
		}

		// Job created successfully, update the status of the JobRequest to Processing.
		log.Info("JobRequest processing")
		jobRequest.Status.Phase = customv1.JobRequestPhaseProcessing
		if err := r.Status().Update(ctx, &jobRequest); err != nil {
			log.Error(err, "Failed to update JobRequest status to Processing")
			return ctrl.Result{}, err
		}

		// Requeue the request to check the job status in the next reconciliation.
		return ctrl.Result{RequeueAfter: time.Second * 1}, nil
	} else {
		log.Info("JobRequest is in a terminal phase, skipping Job creation", "phase", jobRequest.Status.Phase)
	}

	return ctrl.Result{}, nil
}

// reconcileExistingJob handles the logic for a JobRequest that already has an associated Job.
func (r *JobRequestReconciler) reconcileExistingJob(ctx context.Context, jobRequest *customv1.JobRequest, childJob *batchv1.Job) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	jobName := childJob.Name

	// 1. Check for Job completion first. This is a terminal state.
	if childJob.Status.Succeeded > 0 {
		log.Info("Child Job has succeeded")
		if jobRequest.Status.Phase != customv1.JobRequestPhaseSucceeded {
			jobRequest.Status.Phase = customv1.JobRequestPhaseSucceeded
			meta.SetStatusCondition(&jobRequest.Status.Conditions, metav1.Condition{
				Type:    customv1.JobReady,
				Status:  metav1.ConditionTrue,
				Reason:  "JobSucceeded",
				Message: "The job completed successfully.",
			})
			if err := r.Status().Update(ctx, jobRequest); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// 2. Check for "fail-fast" conditions by inspecting Pods.
	if updated, err := r.checkPodFailures(ctx, jobRequest, jobName); err != nil || updated {
		if err != nil {
			log.Error(err, "Failed to check pod failures")
		}
		return ctrl.Result{}, err
	}

	// 3. If no fail-fast condition was met, check if the Job itself has officially failed.
	if childJob.Status.Failed > 0 {
		log.Info("Child Job has failed according to its own status")
		if jobRequest.Status.Phase != customv1.JobRequestPhaseFailed {
			jobRequest.Status.Phase = customv1.JobRequestPhaseFailed

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

			meta.SetStatusCondition(&jobRequest.Status.Conditions, metav1.Condition{Type: customv1.JobReady, Status: metav1.ConditionFalse, Reason: reason, Message: message})
			if err := r.Status().Update(ctx, jobRequest); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil // Stop reconciliation
	}

	// 4. If none of the above, the job is still processing.
	log.Info("Child Job is still processing")
	return ctrl.Result{RequeueAfter: time.Second * 10}, nil
}

// checkPodFailures inspects the pods of a job for fail-fast conditions.
// It returns true if the JobRequest status was updated, and an error if one occurred.
func (r *JobRequestReconciler) checkPodFailures(ctx context.Context, jobRequest *customv1.JobRequest, jobName string) (bool, error) {
	log := logf.FromContext(ctx)
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(jobRequest.Namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
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
					jobRequest.Status.Phase = customv1.JobRequestPhaseFailed
					meta.SetStatusCondition(&jobRequest.Status.Conditions, metav1.Condition{Type: customv1.JobReady, Status: metav1.ConditionFalse, Reason: failureType, Message: message})
					if err := r.Status().Update(ctx, jobRequest); err != nil {
						return false, err
					}
					return true, nil // Status updated, stop further reconciliation
				}
			}
			if containerStatus.State.Terminated != nil && containerStatus.State.Terminated.ExitCode != 0 {
				log.Info("Detected recoverable pod failure", "exitCode", containerStatus.State.Terminated.ExitCode)
				jobRequest.Status.Phase = customv1.JobRequestPhaseFailed
				meta.SetStatusCondition(&jobRequest.Status.Conditions, metav1.Condition{Type: customv1.JobReady, Status: metav1.ConditionFalse, Reason: customv1.ReasonRecoverableLogicError, Message: fmt.Sprintf("Job failed with a non-zero exit code (%d).", containerStatus.State.Terminated.ExitCode)})
				if err := r.Status().Update(ctx, jobRequest); err != nil {
					return false, err
				}
				return true, nil // Status updated, stop further reconciliation
			}
		}
	}
	return false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *JobRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&customv1.JobRequest{}).
		Named("jobrequest").
		Owns(&batchv1.Job{}).
		Complete(r)
}
