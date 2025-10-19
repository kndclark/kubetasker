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
	"errors"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var jobrequestlog = logf.Log.WithName("jobrequest-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *JobRequest) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-custom-custom-io-v1-jobrequest,mutating=true,failurePolicy=fail,sideEffects=None,groups=custom.custom.io,resources=jobrequests,verbs=create;update,versions=v1,name=mjobrequest.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &JobRequest{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *JobRequest) Default() {
	jobrequestlog.Info("default", "name", r.Name)
	// Default the restart policy if it's not set.
	if r.Spec.RestartPolicy == "" {
		r.Spec.RestartPolicy = "OnFailure"
	}

	// Default the backoff limit.
	if r.Spec.BackoffLimit == nil {
		if r.Spec.RestartPolicy == "Never" {
			r.Spec.BackoffLimit = new(int32) // Defaults to 0
		} else {
			r.Spec.BackoffLimit = new(int32)
			*r.Spec.BackoffLimit = 4
		}
	}
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern: /validate-{group}-{version}-{kind}
// +kubebuilder:webhook:path=/validate-custom-custom-io-v1-jobrequest,mutating=false,failurePolicy=fail,sideEffects=None,groups=custom.custom.io,resources=jobrequests,verbs=create;update,versions=v1,name=vjobrequest.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &JobRequest{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *JobRequest) ValidateCreate() (admission.Warnings, error) {
	jobrequestlog.Info("validate create", "name", r.Name)
	return nil, r.validateJobRequest().ToAggregate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *JobRequest) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	jobrequestlog.Info("validate update", "name", r.Name)
	oldJobRequest, ok := old.(*JobRequest)
	if !ok {
		return nil, field.InternalError(nil, errors.New("expected old object to be a JobRequest"))
	}

	var allErrs field.ErrorList
	allErrs = append(allErrs, r.validateJobRequest()...)

	// Check for immutable fields
	allErrs = append(allErrs, validateImmutableFields(r, oldJobRequest)...)

	return nil, allErrs.ToAggregate()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *JobRequest) ValidateDelete() (admission.Warnings, error) {
	jobrequestlog.Info("validate delete", "name", r.Name)
	// No validation needed on deletion.
	return nil, nil
}

// validateJobRequest contains the actual validation logic.
func (r *JobRequest) validateJobRequest() field.ErrorList {
	var allErrs field.ErrorList //

	// The image field is required.
	if r.Spec.Image == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec").Child("image"),
			"image field is required",
		))
	}

	// Ensure the command is not empty.
	if len(r.Spec.Command) == 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec").Child("command"),
			r.Spec.Command,
			"command field cannot be empty",
		))
	}

	if len(allErrs) == 0 {
		return nil //
	}

	return allErrs
}

// validateImmutableFields checks that immutable fields have not been changed.
func validateImmutableFields(new, old *JobRequest) field.ErrorList {
	var allErrs field.ErrorList

	if new.Spec.Image != old.Spec.Image {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec").Child("image"),
			new.Spec.Image, "field is immutable"))
	}

	return allErrs
}
