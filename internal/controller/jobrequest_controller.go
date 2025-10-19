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

// +kubebuilder:rbac:groups=custom.custom.io,resources=jobrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=custom.custom.io,resources=jobrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=custom.custom.io,resources=jobrequests/finalizers,verbs=update
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
		// Job already exists. Let's check its status and update our JobRequest status.
		log.Info("Found child Job, checking status", "Job", client.ObjectKeyFromObject(&childJob))

		// Determine the new phase based on the Job's status
		currentPhase := jobRequest.Status.Phase
		var newPhase string

		// Check if the Job has failed and determine the reason.
		var jobFailed bool
		var failureReason, failureMessage string
		for _, condition := range childJob.Status.Conditions {
			if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
				jobFailed = true
				failureReason, failureMessage = r.determineFailureReason(ctx, &jobRequest, jobName, &condition)

				break
			}
		}

		if jobFailed {
			log.Info("Child Job exceeded its backoff limit and failed", "Job", client.ObjectKeyFromObject(&childJob))
			newPhase = customv1.JobRequestPhaseFailed
			meta.SetStatusCondition(&jobRequest.Status.Conditions, metav1.Condition{
				Type:    customv1.JobReady,
				Status:  metav1.ConditionFalse,
				Reason:  failureReason,
				Message: failureMessage,
			})
		} else if childJob.Status.Succeeded > 0 {
			log.Info("Child Job has succeeded", "Job", client.ObjectKeyFromObject(&childJob))
			newPhase = customv1.JobRequestPhaseSucceeded
		} else {
			log.Info("Child Job is still processing", "Job", client.ObjectKeyFromObject(&childJob))
			newPhase = customv1.JobRequestPhaseProcessing
		}

		// Update the status only if the phase has changed
		if currentPhase != newPhase {
			log.Info("Updating JobRequest status", "from", currentPhase, "to", newPhase)
			jobRequest.Status.Phase = newPhase
			if err := r.Status().Update(ctx, &jobRequest); err != nil {
				log.Error(err, "Failed to update JobRequest status")
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil

	} else if !errors.IsNotFound(err) {
		// Some other error occurred when trying to get the job.
		log.Error(err, "Failed to get child Job", "Job", jobName)
		return ctrl.Result{}, err
	}

	// 3. If the Job does not exist, and the JobRequest is not in a terminal state, create it.
	// Check if the JobRequest is already in a terminal phase.
	if jobRequest.Status.Phase == customv1.JobRequestPhaseSucceeded || jobRequest.Status.Phase == customv1.JobRequestPhaseFailed {
		log.Info("JobRequest is in a terminal phase, skipping Job creation", "phase", jobRequest.Status.Phase)
		return ctrl.Result{}, nil
	}

	// If we are here, it means the job does not exist and the JobRequest is not in a terminal state.
	// So, we should create the job.
	log.Info("Creating a new Job for JobRequest")

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
					Containers: []corev1.Container{
						{
							Name:    "job-container",
							Image:   jobRequest.Spec.Image,
							Command: jobRequest.Spec.Command,
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
			BackoffLimit: new(int32), // A pointer to an int32
		},
	}
	*newJob.Spec.BackoffLimit = 4 // Set the backoff limit

	// Set the JobRequest as the owner of this Job. When the JobRequest is deleted,
	// Kubernetes will automatically delete the Job it owns.
	if err := ctrl.SetControllerReference(&jobRequest, newJob, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference on Job")
		return ctrl.Result{}, err
	}

	log.Info("Job created", "Job", client.ObjectKeyFromObject(newJob))
	if err := r.Create(ctx, newJob); err != nil {
		log.Error(err, "Failed to create new Job", "Job", client.ObjectKeyFromObject(newJob))
		return ctrl.Result{}, err
	}

	// Job created successfully, update the status of the JobRequest
	log.Info("JobRequest processing")
	jobRequest.Status.Phase = customv1.JobRequestPhaseProcessing
	if err := r.Status().Update(ctx, &jobRequest); err != nil {
		log.Error(err, "Failed to update JobRequest status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// determineFailureReason inspects the Job and its Pods to find the most specific failure reason.
func (r *JobRequestReconciler) determineFailureReason(ctx context.Context, jobRequest *customv1.JobRequest, jobName string, jobCondition *batchv1.JobCondition) (string, string) {
	log := logf.FromContext(ctx)

	// For more detailed errors, we inspect the pods of the failed job.
	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.InNamespace(jobRequest.Namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		log.Error(err, "Could not list pods for failed job, falling back to job condition")
		// If we can't list pods, fall back to the less specific Job condition.
		if jobCondition.Reason == "ImagePullBackOff" || jobCondition.Reason == "ErrImagePull" {
			return customv1.ReasonPermanentFailure, "The underlying Job failed due to an image pull error."
		}
		return customv1.ReasonTransientFailure, "The underlying Job has failed after multiple retries."
	}

	// Check for specific pod failure reasons in a hierarchical order.
	for _, pod := range podList.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil {
				// Configuration errors are highly specific and actionable.
				if containerStatus.State.Waiting.Reason == "CreateContainerConfigError" {
					return customv1.ReasonConflictError, fmt.Sprintf("Job failed due to a configuration error: %s", containerStatus.State.Waiting.Message)
				}
				// Image pull errors are also a permanent, user-correctable issue.
				if containerStatus.State.Waiting.Reason == "ImagePullBackOff" || containerStatus.State.Waiting.Reason == "ErrImagePull" {
					return customv1.ReasonPermanentFailure, fmt.Sprintf("Job failed due to an image pull error: %s", containerStatus.State.Waiting.Message)
				}
			}
			// Check for application-level errors.
			if containerStatus.State.Terminated != nil && containerStatus.State.Terminated.ExitCode == 1 {
				return customv1.ReasonRecoverableLogicError, fmt.Sprintf("Job failed with a recoverable logic error (exit code 1). Reason: %s", containerStatus.State.Terminated.Reason)
			}
		}
	}

	// If no specific pod-level error is found, check the job condition again.
	if jobCondition.Reason == "ImagePullBackOff" || jobCondition.Reason == "ErrImagePull" {
		return customv1.ReasonPermanentFailure, "The underlying Job failed due to an image pull error."
	}

	// If no specific pod-level error is found, default to a transient failure.
	return customv1.ReasonTransientFailure, "The underlying Job has failed after multiple retries."
}

// SetupWithManager sets up the controller with the Manager.
func (r *JobRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&customv1.JobRequest{}).
		Named("jobrequest").
		Owns(&batchv1.Job{}).
		Complete(r)
}
