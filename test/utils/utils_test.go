package utils

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Utils", func() {

	Context("When calling GetNonEmptyLines", func() {
		It("should return only non-empty lines", func() {
			input := "line1\nline2\n\nline3\n"
			expected := []string{"line1", "line2", "line3"}
			Expect(GetNonEmptyLines(input)).To(Equal(expected))
		})

		It("should return an empty slice for empty input", func() {
			input := ""
			Expect(GetNonEmptyLines(input)).To(BeEmpty())
		})

		It("should return an empty slice for input with only newlines", func() {
			input := "\n\n\n"
			Expect(GetNonEmptyLines(input)).To(BeEmpty())
		})

		It("should handle input with no trailing newline", func() {
			input := "line1\nline2"
			expected := []string{"line1", "line2"}
			Expect(GetNonEmptyLines(input)).To(Equal(expected))
		})
	})

	// NOTE: Testing functions that interact with a live Kubernetes cluster
	// like LoadImageToKindClusterWithName, IsCertManagerCRDsInstalled, etc.,
	// is typically done in E2E tests rather than unit tests, as they require
	// a running cluster and external tools (kind, kubectl).
	//
	// The coverage for these functions will be inherently low in a unit test context.
	// The goal here is to cover the pure logic functions like GetNonEmptyLines.

})
