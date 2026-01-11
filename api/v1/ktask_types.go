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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// KtaskSpec defines the desired state of Ktask
type KtaskSpec struct {
	// Image is the container image to run for the job.
	// This field is required.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Command is the command to run inside the container.
	// +optional
	Command []string `json:"command,omitempty"`

	// RestartPolicy defines the restart policy for the job's pods.
	// Can be "OnFailure" or "Never". Defaults to "OnFailure".
	// +kubebuilder:validation:Enum=OnFailure;Never
	// +optional
	RestartPolicy string `json:"restartPolicy,omitempty"`

	// Env defines environment variables to set in the container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// BackoffLimit specifies the number of retries before marking this job as failed.
	// Defaults to 4. Set to 0 to fail on the first error when RestartPolicy is Never.
	// +optional
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// ServiceAccountName is the name of the ServiceAccount to use to run this job.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Resources describes the compute resource requirements.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// Define the valid phases for a Ktask
const (
	PhasePending    = "Pending"
	PhaseProcessing = "Processing"
	PhaseSucceeded  = "Succeeded"
	PhaseFailed     = "Failed"
)

// Condition types for a Ktask.
const (
	// JobReady indicates whether the underlying Job is ready and the Ktask is progressing.
	// This is a positive-polarity condition, meaning its status is True when the job is ready.
	JobReady string = "JobReady"

	// Reasons for conditions
	// ReasonJobFailed is a generic reason for a failed job.
	ReasonJobFailed string = "JobFailed"
	// ReasonPermanentFailure indicates a failure that is unlikely to be resolved by retrying,
	// such as an invalid image name.
	ReasonPermanentFailure string = "PermanentFailure"
	// ReasonTransientFailure indicates a temporary failure that might be resolved by retrying.
	ReasonTransientFailure string = "TransientFailure"
	// ReasonConflictError indicates a conflict with the state of the system, such as a missing
	// ConfigMap or Secret, that prevents the job from running.
	ReasonConflictError string = "ConflictError"
)

// KtaskStatus defines the observed state of Ktask.
// +kubebuilder:pruning:PreserveUnknownFields
// This marker is required to preserve the `conditions` field,
// which is not always known by the CRD schema.
type KtaskStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file.

	// Phase represents the current phase of the Ktask.
	// E.g., Pending, Running, Succeeded, Failed.
	// +optional
	Phase string `json:"phase,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the Ktask resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Ktask is the Schema for the ktasks API
type Ktask struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Ktask
	// +required
	Spec KtaskSpec `json:"spec"`

	// status defines the observed state of Ktask
	// +optional
	Status KtaskStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// KtaskList contains a list of Ktask
type KtaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Ktask `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Ktask{}, &KtaskList{})
}
