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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	customv1 "github.com/kndclark/kubetasker/api/v1"
)

// nolint:unused
// log is for logging in this package.
var jobrequestlog = logf.Log.WithName("jobrequest-resource")

// SetupJobRequestWebhookWithManager registers the webhook for JobRequest in the manager.
func SetupJobRequestWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&customv1.JobRequest{}).
		WithValidator(&JobRequestCustomValidator{}).
		WithDefaulter(&JobRequestCustomDefaulter{}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-custom-custom-io-v1-jobrequest,mutating=true,failurePolicy=fail,sideEffects=None,groups=custom.custom.io,resources=jobrequests,verbs=create;update,versions=v1,name=mjobrequest-v1.kb.io,admissionReviewVersions=v1

// JobRequestCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind JobRequest when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type JobRequestCustomDefaulter struct {
	// TODO(user): Add more fields as needed for defaulting
}

var _ webhook.CustomDefaulter = &JobRequestCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind JobRequest.
func (d *JobRequestCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	jobrequest, ok := obj.(*customv1.JobRequest)

	if !ok {
		return fmt.Errorf("expected an JobRequest object but got %T", obj)
	}
	jobrequestlog.Info("Defaulting for JobRequest", "name", jobrequest.GetName())

	// TODO(user): fill in your defaulting logic.

	return nil
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-custom-custom-io-v1-jobrequest,mutating=false,failurePolicy=fail,sideEffects=None,groups=custom.custom.io,resources=jobrequests,verbs=create;update,versions=v1,name=vjobrequest-v1.kb.io,admissionReviewVersions=v1

// JobRequestCustomValidator struct is responsible for validating the JobRequest resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type JobRequestCustomValidator struct {
	// TODO(user): Add more fields as needed for validation
}

var _ webhook.CustomValidator = &JobRequestCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type JobRequest.
func (v *JobRequestCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	jobrequest, ok := obj.(*customv1.JobRequest)
	if !ok {
		return nil, fmt.Errorf("expected a JobRequest object but got %T", obj)
	}
	jobrequestlog.Info("Validation for JobRequest upon creation", "name", jobrequest.GetName())

	// TODO(user): fill in your validation logic upon object creation.

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type JobRequest.
func (v *JobRequestCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	jobrequest, ok := newObj.(*customv1.JobRequest)
	if !ok {
		return nil, fmt.Errorf("expected a JobRequest object for the newObj but got %T", newObj)
	}
	jobrequestlog.Info("Validation for JobRequest upon update", "name", jobrequest.GetName())

	// TODO(user): fill in your validation logic upon object update.

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type JobRequest.
func (v *JobRequestCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	jobrequest, ok := obj.(*customv1.JobRequest)
	if !ok {
		return nil, fmt.Errorf("expected a JobRequest object but got %T", obj)
	}
	jobrequestlog.Info("Validation for JobRequest upon deletion", "name", jobrequest.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
