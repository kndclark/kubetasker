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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	customv1 "github.com/kndclark/kubetasker/api/v1"
)

var _ = Describe("JobRequest Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		jobrequest := &customv1.JobRequest{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind JobRequest")
			err := k8sClient.Get(ctx, typeNamespacedName, jobrequest)
			if err != nil && errors.IsNotFound(err) {
				resource := &customv1.JobRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: customv1.JobRequestSpec{
						Image:   "test-image:latest",
						Command: []string{"echo", "hello"},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the JobRequest resource")
			jobRequest := &customv1.JobRequest{}
			// First, try to get the resource. If it exists, delete it.
			if err := k8sClient.Get(ctx, typeNamespacedName, jobRequest); err == nil {
				Expect(k8sClient.Delete(ctx, jobRequest, client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
				// Wait for the deletion to complete.
				Eventually(func() bool {
					return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, jobRequest))
				}, time.Second*10, time.Millisecond*250).Should(BeTrue())
			}

			By("Cleanup the Job resource")
			job := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			// Try to get the job. If it exists, delete it.
			if err := k8sClient.Get(ctx, jobNamespacedName, job); err == nil {
				Expect(k8sClient.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
				// Wait for the deletion to complete.
				Eventually(func() bool {
					return errors.IsNotFound(k8sClient.Get(ctx, jobNamespacedName, job))
				}, time.Second*10, time.Millisecond*250).Should(BeTrue())
			}
		})
		It("should successfully reconcile the resource and create a Job", func() {
			By("Reconciling the created resource")
			controllerReconciler := &JobRequestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking if the Job was created")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{
				Name:      resourceName + "-job",
				Namespace: "default",
			}

			// We use Eventually to poll because the creation of the Job is asynchronous.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, jobNamespacedName, createdJob)
				return err == nil
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())

			Expect(createdJob.Spec.Template.Spec.Containers[0].Image).To(Equal("test-image:latest"))
		})

		It("should update the JobRequest status to Succeeded when the Job completes", func() {
			By("Reconciling the created resource to create the Job")
			controllerReconciler := &JobRequestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Finding the created Job and simulating its completion")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			Eventually(func() error {
				return k8sClient.Get(ctx, jobNamespacedName, createdJob)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			// Manually update the Job's status to Succeeded
			createdJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, createdJob)).To(Succeed())

			By("Re-reconciling the resource to observe the Job's completion")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking if the JobRequest status is updated to Succeeded")
			updatedJobRequest := &customv1.JobRequest{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedJobRequest)
				if err != nil {
					return ""
				}
				return updatedJobRequest.Status.Phase
			}, time.Second*10, time.Millisecond*250).Should(Equal(customv1.JobRequestPhaseSucceeded))
		}) //

		It("should update the JobRequest status to Failed when the Job fails", func() {
			By("Reconciling the created resource to create the Job")
			controllerReconciler := &JobRequestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Finding the created Job and simulating its failure")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			Eventually(func() error {
				return k8sClient.Get(ctx, jobNamespacedName, createdJob)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			// Manually update the Job's status to Failed
			now := metav1.Now()
			createdJob.Status.StartTime = &now
			createdJob.Status.Conditions = []batchv1.JobCondition{
				{
					Type:   batchv1.JobFailed,
					Status: corev1.ConditionTrue,
					Reason: "BackoffLimitExceeded",
				},
				{
					Type:   "FailureTarget",
					Status: corev1.ConditionTrue,
					Reason: "BackoffLimitExceeded",
				},
			}
			createdJob.Status.Failed = 1

			Expect(k8sClient.Status().Update(ctx, createdJob)).To(Succeed())

			By("Re-reconciling the resource to observe the Job's failure")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking if the JobRequest status is updated to Failed")
			updatedJobRequest := &customv1.JobRequest{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedJobRequest)
				if err != nil {
					return false
				}
				if updatedJobRequest.Status.Phase != customv1.JobRequestPhaseFailed {
					return false
				}
				for _, cond := range updatedJobRequest.Status.Conditions {
					return cond.Type == customv1.JobReady && cond.Status == metav1.ConditionFalse && cond.Reason == customv1.ReasonJobFailed
				}
				return false
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		}) //

		It("should not create a new Job if the JobRequest is already Succeeded", func() {
			By("Manually setting the JobRequest status to Succeeded")
			succeededJobRequest := &customv1.JobRequest{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, succeededJobRequest)).To(Succeed())
			succeededJobRequest.Status.Phase = customv1.JobRequestPhaseSucceeded
			Expect(k8sClient.Status().Update(ctx, succeededJobRequest)).To(Succeed())

			By("Reconciling the resource")
			controllerReconciler := &JobRequestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Ensuring no Job was created")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{
				Name:      resourceName + "-job",
				Namespace: "default",
			}
			err = k8sClient.Get(ctx, jobNamespacedName, createdJob)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should not create a new Job if the JobRequest is already Failed", func() {
			By("Manually setting the JobRequest status to Failed")
			failedJobRequest := &customv1.JobRequest{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, failedJobRequest)).To(Succeed())
			failedJobRequest.Status.Phase = customv1.JobRequestPhaseFailed
			Expect(k8sClient.Status().Update(ctx, failedJobRequest)).To(Succeed())

			By("Reconciling the resource")
			controllerReconciler := &JobRequestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Ensuring no Job was created")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{
				Name:      resourceName + "-job",
				Namespace: "default",
			}
			err = k8sClient.Get(ctx, jobNamespacedName, createdJob)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should set the JobRequest as the owner of the created Job", func() {
			By("Reconciling the resource to create the Job")
			controllerReconciler := &JobRequestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for the Job to be created and checking its owner reference")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			Eventually(func() error {
				return k8sClient.Get(ctx, jobNamespacedName, createdJob)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			Expect(createdJob.OwnerReferences).To(HaveLen(1))
			Expect(createdJob.OwnerReferences[0].APIVersion).To(Equal(customv1.GroupVersion.String()))
			Expect(createdJob.OwnerReferences[0].Kind).To(Equal("JobRequest"))
			Expect(createdJob.OwnerReferences[0].Name).To(Equal(resourceName))
		})
	})
})
