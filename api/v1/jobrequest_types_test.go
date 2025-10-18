package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("JobRequest types", func() {

	It("should perform deep copy correctly", func() {
		// Create an original JobRequest object
		original := &JobRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "test-ns",
			},
			Spec: JobRequestSpec{
				Image:   "original-image",
				Command: []string{"echo", "original"},
			},
			Status: JobRequestStatus{
				Phase: "Pending",
			},
		}

		// Perform a deep copy
		copied := original.DeepCopy()

		// Assert that the copied object is not the same instance
		Expect(copied).NotTo(BeIdenticalTo(original))

		// Assert that the contents are equal
		Expect(copied).To(Equal(original))

		// Modify the copy and assert the original is unchanged
		copied.Spec.Image = "modified-image"
		Expect(original.Spec.Image).To(Equal("original-image"))

		// Test DeepCopyObject
		obj := original.DeepCopyObject()
		Expect(obj).NotTo(BeNil())
		_, ok := obj.(*JobRequest)
		Expect(ok).To(BeTrue())
	})

})
