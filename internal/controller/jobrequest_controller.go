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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *JobRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Reconciliation loop started")

	// 1. Fetch the JobRequest instance
	var jobRequest customv1.JobRequest
	if err := r.Get(ctx, req.NamespacedName, &jobRequest); err != nil {
		if errors.IsNotFound(err) {
			// The JobRequest resource was not found. It might have been deleted.
			log.Info("JobRequest resource not found. Ignoring since object must be deleted.")
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
		log.Info("Child Job already exists, checking its status.", "Job.Name", childJob.Name)

		// Determine the new phase based on the Job's status
		currentPhase := jobRequest.Status.Phase
		newPhase := currentPhase

		if childJob.Status.Succeeded > 0 {
			log.Info("Child Job has succeeded.")
			newPhase = "Succeeded"
		} else if childJob.Status.Failed > 0 {
			log.Info("Child Job has failed.")
			newPhase = "Failed"
		} else {
			log.Info("Child Job is still processing.")
			newPhase = "Processing"
		}

		// Update the status only if the phase has changed
		if currentPhase != newPhase {
			jobRequest.Status.Phase = newPhase
			if err := r.Status().Update(ctx, &jobRequest); err != nil {
				log.Error(err, "Failed to update JobRequest status")
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil

	} else if !errors.IsNotFound(err) {
		// Some other error occurred when trying to get the job.
		log.Error(err, "Failed to get child Job")
		return ctrl.Result{}, err
	}

	// 3. If the Job does not exist, and the JobRequest is not in a terminal state, create it.
	// Check if the JobRequest is already in a terminal phase.
	if jobRequest.Status.Phase == "Succeeded" || jobRequest.Status.Phase == "Failed" {
		log.Info("JobRequest is already in a terminal phase, not creating a new Job.", "Phase", jobRequest.Status.Phase)
		return ctrl.Result{}, nil
	}

	// If we are here, it means the job does not exist and the JobRequest is not in a terminal state.
	// So, we should create the job.
	log.Info("Child Job not found, creating a new one.")

	// Define the new Job from the JobRequest's spec
	newJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: jobRequest.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "job-container",
							Image:   jobRequest.Spec.Image,
							Command: jobRequest.Spec.Command,
						},
					},
					RestartPolicy: corev1.RestartPolicyOnFailure,
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

	log.Info("Creating a new Job", "Job.Namespace", newJob.Namespace, "Job.Name", newJob.Name)
	if err := r.Create(ctx, newJob); err != nil {
		log.Error(err, "Failed to create new Job", "Job.Namespace", newJob.Namespace, "Job.Name", newJob.Name)
		return ctrl.Result{}, err
	}

	// Job created successfully, update the status of the JobRequest
	jobRequest.Status.Phase = "Processing"
	if err := r.Status().Update(ctx, &jobRequest); err != nil {
		log.Error(err, "Failed to update JobRequest status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *JobRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&customv1.JobRequest{}).
		Named("jobrequest").
		Complete(r)
}
