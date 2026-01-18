package utils

import (
	"os/exec"

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

		It("should handle lines with spaces", func() {
			input := "  line1  \n\n line2 \n"
			expected := []string{"  line1  ", " line2 "}
			Expect(GetNonEmptyLines(input)).To(Equal(expected))
		})

		It("should handle mixed Windows and Unix newlines", func() {
			input := "line1\r\nline2\nline3"
			expected := []string{"line1", "line2", "line3"}
			Expect(GetNonEmptyLines(input)).To(Equal(expected))
		})
	})

	Context("When calling Run", func() {
		It("should return output on success", func() {
			cmd := exec.Command("echo", "hello")
			output, err := Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("hello"))
		})

		It("should return error on failure", func() {
			// 'false' command returns exit code 1
			cmd := exec.Command("false")
			_, err := Run(cmd)
			Expect(err).To(HaveOccurred())
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
