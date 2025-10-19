package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("JobRequest Webhook", func() {

	Context("When creating a JobRequest with defaultable fields", func() {
		It("should default the restart policy to OnFailure", func() {
			jr := &JobRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-jobrequest",
					Namespace: "default",
				},
				Spec: JobRequestSpec{
					Image:   "busybox",
					Command: []string{"echo", "hello"},
				},
			}

			// The RestartPolicy should be empty initially
			Expect(jr.Spec.RestartPolicy).To(BeEmpty())

			// Call the defaulting webhook logic
			jr.Default()

			// Assert that the RestartPolicy is now "OnFailure"
			Expect(jr.Spec.RestartPolicy).To(Equal("OnFailure"))
		})

		It("should default the backoff limit to 4 for OnFailure restart policy", func() {
			jr := &JobRequest{
				Spec: JobRequestSpec{
					RestartPolicy: "OnFailure",
				},
			}
			jr.Default()
			Expect(jr.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*jr.Spec.BackoffLimit).To(Equal(int32(4)))
		})

		It("should default the backoff limit to 0 for Never restart policy", func() {
			jr := &JobRequest{
				Spec: JobRequestSpec{
					RestartPolicy: "Never",
				},
			}
			jr.Default()
			Expect(jr.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*jr.Spec.BackoffLimit).To(Equal(int32(0)))
		})

		It("should not override an existing backoff limit", func() {
			limit := int32(5)
			jr := &JobRequest{
				Spec: JobRequestSpec{BackoffLimit: &limit},
			}
			jr.Default()
			Expect(jr.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*jr.Spec.BackoffLimit).To(Equal(int32(5)))
		})
	})

	Context("When validating a JobRequest", func() {
		It("should fail validation if the image is empty", func() {
			jr := &JobRequest{
				Spec: JobRequestSpec{
					Image:   "", // Empty image
					Command: []string{"echo", "hello"},
				},
			}

			// ValidateCreate should fail
			_, err := jr.ValidateCreate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.image: Required value"))
		})

		It("should fail validation if the command is empty", func() {
			jr := &JobRequest{
				Spec: JobRequestSpec{
					Image:   "busybox",
					Command: []string{}, // Empty command
				},
			}

			_, err := jr.ValidateCreate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("command field cannot be empty"))
		})

		It("should pass validation if the command is not empty", func() {
			jr := &JobRequest{
				Spec: JobRequestSpec{
					Image:   "busybox",
					Command: []string{"echo", "hello"},
				},
			}

			_, err := jr.ValidateCreate()
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation on update if an immutable field (image) is changed", func() {
			oldJr := &JobRequest{
				Spec: JobRequestSpec{
					Image:   "original-image",
					Command: []string{"echo", "hello"},
				},
			}

			newJr := oldJr.DeepCopy()
			newJr.Spec.Image = "new-image" // Change the image

			// ValidateUpdate should fail because the image is immutable
			_, err := newJr.ValidateUpdate(oldJr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("field is immutable"))
		})

		It("should fail validation on update if the command is empty", func() {
			jr := &JobRequest{
				Spec: JobRequestSpec{
					Image:   "busybox",
					Command: []string{}, // Empty command
				},
			}
			// The old object is required for the method signature but not used in this specific validation
			var old runtime.Object = &JobRequest{}

			_, err := jr.ValidateUpdate(old)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("command field cannot be empty"))
		})

		It("should pass validation on update if the command is not empty", func() {
			jr := &JobRequest{
				Spec: JobRequestSpec{
					Image:   "busybox",
					Command: []string{"echo", "hello"},
				},
			}
			// Create an old object that is identical to the new one to avoid triggering
			// the immutability check.
			var old runtime.Object = &JobRequest{
				Spec: jr.Spec,
			}

			_, err := jr.ValidateUpdate(old)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should pass validation on delete", func() {
			jr := &JobRequest{}

			// ValidateDelete is a no-op, so it should always pass
			_, err := jr.ValidateDelete()
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation on update if the old object is not a JobRequest", func() {
			jr := &JobRequest{}
			// Pass a different type to trigger the type assertion error
			var old runtime.Object = &corev1.Pod{}

			_, err := jr.ValidateUpdate(old)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expected old object to be a JobRequest"))
		})
	})
})
