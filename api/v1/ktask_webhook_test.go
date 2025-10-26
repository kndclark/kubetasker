package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("Ktask Webhook", func() {

	Context("When creating a Ktask with defaultable fields", func() {
		It("should default the restart policy to OnFailure", func() {
			ktask := &Ktask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ktask",
					Namespace: "default",
				},
				Spec: KtaskSpec{
					Image:   "busybox",
					Command: []string{"echo", "hello"},
				},
			}

			// The RestartPolicy should be empty initially
			Expect(ktask.Spec.RestartPolicy).To(BeEmpty())

			// Call the defaulting webhook logic
			ktask.Default()

			// Assert that the RestartPolicy is now "OnFailure"
			Expect(ktask.Spec.RestartPolicy).To(Equal("OnFailure"))
		})

		It("should default the backoff limit to 4 for OnFailure restart policy", func() {
			ktask := &Ktask{
				Spec: KtaskSpec{
					RestartPolicy: "OnFailure",
				},
			}
			ktask.Default()
			Expect(ktask.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*ktask.Spec.BackoffLimit).To(Equal(int32(4)))
		})

		It("should default the backoff limit to 0 for Never restart policy", func() {
			ktask := &Ktask{
				Spec: KtaskSpec{
					RestartPolicy: "Never",
				},
			}
			ktask.Default()
			Expect(ktask.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*ktask.Spec.BackoffLimit).To(Equal(int32(0)))
		})

		It("should not override an existing backoff limit", func() {
			limit := int32(5)
			ktask := &Ktask{
				Spec: KtaskSpec{BackoffLimit: &limit},
			}
			ktask.Default()
			Expect(ktask.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*ktask.Spec.BackoffLimit).To(Equal(int32(5)))
		})
	})

	Context("When validating a Ktask", func() {
		It("should fail validation if the image is empty", func() {
			ktask := &Ktask{
				Spec: KtaskSpec{
					Image:   "", // Empty image
					Command: []string{"echo", "hello"},
				},
			}

			// ValidateCreate should fail
			_, err := ktask.ValidateCreate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.image: Required value"))
		})

		It("should fail validation if the command is empty", func() {
			ktask := &Ktask{
				Spec: KtaskSpec{
					Image:   "busybox",
					Command: []string{}, // Empty command
				},
			}

			_, err := ktask.ValidateCreate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("command field cannot be empty"))
		})

		It("should pass validation if the command is not empty", func() {
			ktask := &Ktask{
				Spec: KtaskSpec{
					Image:   "busybox",
					Command: []string{"echo", "hello"},
				},
			}

			_, err := ktask.ValidateCreate()
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation on update if an immutable field (image) is changed", func() {
			oldKtask := &Ktask{
				Spec: KtaskSpec{
					Image:   "original-image",
					Command: []string{"echo", "hello"},
				},
			}

			newKtask := oldKtask.DeepCopy()
			newKtask.Spec.Image = "new-image" // Change the image

			// ValidateUpdate should fail because the image is immutable
			_, err := newKtask.ValidateUpdate(oldKtask)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("field is immutable"))
		})

		It("should pass validation on update if a mutable field is changed", func() {
			oldKtask := &Ktask{
				Spec: KtaskSpec{
					Image:   "original-image",
					Command: []string{"echo", "hello"},
				},
			}

			newKtask := oldKtask.DeepCopy()
			newKtask.Spec.Command = []string{"echo", "world"} // Change a mutable field

			_, err := newKtask.ValidateUpdate(oldKtask)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation on update if the command is empty", func() {
			ktask := &Ktask{
				Spec: KtaskSpec{
					Image:   "busybox",
					Command: []string{}, // Empty command
				},
			}
			// The old object is required for the method signature but not used in this specific validation
			var old runtime.Object = &Ktask{}

			_, err := ktask.ValidateUpdate(old)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("command field cannot be empty"))
		})

		It("should pass validation on update if the command is not empty", func() {
			ktask := &Ktask{
				Spec: KtaskSpec{
					Image:   "busybox",
					Command: []string{"echo", "hello"},
				},
			}
			// Create an old object that is identical to the new one to avoid triggering
			// the immutability check.
			var old runtime.Object = &Ktask{
				Spec: ktask.Spec,
			}

			_, err := ktask.ValidateUpdate(old)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should pass validation on delete", func() {
			ktask := &Ktask{}

			// ValidateDelete is a no-op, so it should always pass
			_, err := ktask.ValidateDelete()
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail validation on update if the old object is not a Ktask", func() {
			ktask := &Ktask{}
			// Pass a different type to trigger the type assertion error
			var old runtime.Object = &corev1.Pod{}

			_, err := ktask.ValidateUpdate(old)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expected old object to be a Ktask"))
		})
	})
})
