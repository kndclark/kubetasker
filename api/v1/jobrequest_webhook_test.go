package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("JobRequest Webhook", func() {

	Context("When creating a JobRequest with defaultable fields", func() {
		It("should default the status phase to Pending", func() {
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

			// The Phase should be empty initially
			Expect(jr.Status.Phase).To(BeEmpty())

			// Call the defaulting webhook logic
			jr.Default()

			// Assert that the Phase is now "Pending"
			Expect(jr.Status.Phase).To(Equal("Pending"))
		})
	})

	Context("When validating a JobRequest", func() {
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
			// The old object is required for the method signature but not used in this specific validation
			var old runtime.Object = &JobRequest{}

			_, err := jr.ValidateUpdate(old)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should pass validation on delete", func() {
			jr := &JobRequest{}

			// ValidateDelete is a no-op, so it should always pass
			_, err := jr.ValidateDelete()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
