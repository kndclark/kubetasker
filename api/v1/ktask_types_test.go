package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Ktask types", func() {

	It("should perform deep copy correctly", func() {
		// Create an original Ktask object
		original := &Ktask{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "test-ns",
			},
			Spec: KtaskSpec{
				Image:   "original-image",
				Command: []string{"echo", "original"},
			},
			Status: KtaskStatus{
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
		_, ok := obj.(*Ktask)
		Expect(ok).To(BeTrue())
	})

	It("should handle deep copy of a nil object", func() {
		var original *Ktask = nil
		Expect(original.DeepCopy()).To(BeNil())
	})

	It("should perform deep copy of a list correctly", func() {
		originalList := &KtaskList{
			Items: []Ktask{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-1"},
					Spec:       KtaskSpec{Image: "image-1"},
				},
			},
		}

		copiedList := originalList.DeepCopy()
		Expect(copiedList).NotTo(BeIdenticalTo(originalList))
		Expect(copiedList).To(Equal(originalList))

		// Test DeepCopyObject for the list
		obj := originalList.DeepCopyObject()
		Expect(obj).NotTo(BeNil())
		_, ok := obj.(*KtaskList)
		Expect(ok).To(BeTrue())
	})

})
