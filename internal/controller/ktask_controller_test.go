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

var _ = Describe("Ktask Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		ktask := &customv1.Ktask{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Ktask")
			err := k8sClient.Get(ctx, typeNamespacedName, ktask)
			if err != nil && errors.IsNotFound(err) {
				resource := &customv1.Ktask{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: customv1.KtaskSpec{
						Image:              "test-image:latest",
						Command:            []string{"echo", "hello"},
						RestartPolicy:      "OnFailure", // Set default for tests
						ServiceAccountName: "test-sa",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the Ktask resource")
			ktask := &customv1.Ktask{}
			// First, try to get the resource. If it exists, delete it.
			if err := k8sClient.Get(ctx, typeNamespacedName, ktask); err == nil {
				Expect(k8sClient.Delete(ctx, ktask, client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
				// Wait for the deletion to complete.
				Eventually(func() bool {
					return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, ktask))
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

		// Helper function to get a specific condition
		getCondition := func(ktask *customv1.Ktask, condType string) *metav1.Condition {
			for _, cond := range ktask.Status.Conditions {
				if cond.Type == condType {
					return &cond
				}
			}
			return nil
		}

		It("should successfully reconcile the resource and create a Job", func() {
			By("Reconciling the created resource")
			controllerReconciler := &KtaskReconciler{
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
			Expect(createdJob.Spec.Template.Spec.ServiceAccountName).To(Equal("test-sa"))
		})

		It("should update the Ktask status to Succeeded when the Job completes", func() {
			By("Reconciling the created resource to create the Job")
			controllerReconciler := &KtaskReconciler{
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

			By("Checking if the Ktask status is updated to Succeeded")
			updatedKtask := &customv1.Ktask{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedKtask)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedKtask.Status.Phase).To(Equal(customv1.PhaseSucceeded))

				// Verify the condition is set correctly
				succeededCondition := getCondition(updatedKtask, customv1.JobReady)
				g.Expect(succeededCondition).NotTo(BeNil())
				g.Expect(succeededCondition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(succeededCondition.Reason).To(Equal("JobSucceeded"))
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		}) //

		It("should update status to Failed with TransientFailure for BackoffLimitExceeded", func() {
			By("Reconciling the created resource to create the Job")
			controllerReconciler := &KtaskReconciler{
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
					Type:   batchv1.JobFailureTarget,
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

			By("Checking if the Ktask status is updated to Failed")
			updatedKtask := &customv1.Ktask{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedKtask)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedKtask.Status.Phase).To(Equal(customv1.PhaseFailed))

				failedCondition := getCondition(updatedKtask, customv1.JobReady)
				g.Expect(failedCondition).NotTo(BeNil())
				g.Expect(failedCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(failedCondition.Reason).To(Equal(customv1.ReasonTransientFailure))
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})

		It("should update status to Failed with PermanentFailure for ImagePullBackOff", func() {
			By("Reconciling to create the Job")
			controllerReconciler := &KtaskReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Simulating Job failure due to ImagePullBackOff")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			Eventually(func() error { return k8sClient.Get(ctx, jobNamespacedName, createdJob) }, time.Second*10, time.Millisecond*250).Should(Succeed())

			now := metav1.Now()
			createdJob.Status.StartTime = &now
			createdJob.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "ImagePullBackOff"},
				{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
					Reason: "ImagePullBackOff",
				},
			}
			createdJob.Status.Failed = 1
			Expect(k8sClient.Status().Update(ctx, createdJob)).To(Succeed())

			By("Re-reconciling to observe the failure")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Checking for PermanentFailure reason")
			updatedKtask := &customv1.Ktask{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedKtask)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedKtask.Status.Phase).To(Equal(customv1.PhaseFailed))

				failedCondition := getCondition(updatedKtask, customv1.JobReady)
				g.Expect(failedCondition).NotTo(BeNil())
				g.Expect(failedCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(failedCondition.Reason).To(Equal(customv1.ReasonPermanentFailure))
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})

		It("should update status to Failed with PermanentFailure for ImagePullBackOff on Pod", func() {
			By("Reconciling to create the Job")
			controllerReconciler := &KtaskReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			createdJob := &batchv1.Job{}
			Eventually(func() error { return k8sClient.Get(ctx, jobNamespacedName, createdJob) }, time.Second*10, time.Millisecond*250).Should(Succeed())

			By("Creating a mock Pod with ImagePullBackOff")
			mockPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-pod-image-pull-fail",
					Namespace: "default",
					Labels:    map[string]string{"job-name": jobNamespacedName.Name},
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "bad-image"}}},
			}
			Expect(k8sClient.Create(ctx, mockPod)).To(Succeed())
			defer func() {
				Expect(k8sClient.Delete(ctx, mockPod)).To(Succeed())
			}()

			mockPod.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					Name:  "main",
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
				},
			}
			Expect(k8sClient.Status().Update(ctx, mockPod)).To(Succeed())

			// Simulate the parent Job failing
			createdJob.Status.Failed = 1
			now := metav1.Now()
			createdJob.Status.StartTime = &now
			createdJob.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
				{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
					Reason: "BackoffLimitExceeded",
				},
			}
			Expect(k8sClient.Status().Update(ctx, createdJob)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			updatedKtask := &customv1.Ktask{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, updatedKtask)).To(Succeed())
				g.Expect(updatedKtask.Status.Phase).To(Equal(customv1.PhaseFailed))

				failedCondition := getCondition(updatedKtask, customv1.JobReady)
				g.Expect(failedCondition).NotTo(BeNil())
				g.Expect(failedCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(failedCondition.Reason).To(Equal(customv1.ReasonPermanentFailure))
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})

		It("should update status to Failed with ConflictError for CreateContainerConfigError", func() {
			By("Reconciling to create the Job")
			controllerReconciler := &KtaskReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			createdJob := &batchv1.Job{}
			Eventually(func() error { return k8sClient.Get(ctx, jobNamespacedName, createdJob) }, time.Second*10, time.Millisecond*250).Should(Succeed())

			By("Creating a mock Pod with CreateContainerConfigError")
			mockPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-pod-conflict",
					Namespace: "default",
					Labels:    map[string]string{"job-name": jobNamespacedName.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "main", Image: "busybox"}},
				},
			}
			Expect(k8sClient.Create(ctx, mockPod)).To(Succeed())

			mockPod.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					Name: "main",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CreateContainerConfigError",
							Message: "missing secret 'my-secret'",
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, mockPod)).To(Succeed())

			By("Simulating Job failure")
			now := metav1.Now()
			createdJob.Status.StartTime = &now
			createdJob.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
				{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
					Reason: "BackoffLimitExceeded",
				},
			}
			createdJob.Status.Failed = 1
			Expect(k8sClient.Status().Update(ctx, createdJob)).To(Succeed())

			By("Re-reconciling to observe the failure")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Checking for ConflictError reason")
			updatedKtask := &customv1.Ktask{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedKtask)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedKtask.Status.Phase).To(Equal(customv1.PhaseFailed))

				failedCondition := getCondition(updatedKtask, customv1.JobReady)
				g.Expect(failedCondition).NotTo(BeNil())
				g.Expect(failedCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(failedCondition.Reason).To(Equal(customv1.ReasonConflictError))
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			// Clean up the mock pod
			By("Cleaning up the mock conflict pod")
			Expect(k8sClient.Delete(ctx, mockPod)).To(Succeed())
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(mockPod), &corev1.Pod{}))
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})

		It("should update status to Failed with RecoverableLogicError for exit code 1", func() {
			By("Reconciling to create the Job")
			controllerReconciler := &KtaskReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			createdJob := &batchv1.Job{}
			Eventually(func() error { return k8sClient.Get(ctx, jobNamespacedName, createdJob) }, time.Second*10, time.Millisecond*250).Should(Succeed())

			By("Creating a mock Pod with a failed exit code")
			mockPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName + "-pod-logic-error",
					Namespace: "default",
					Labels:    map[string]string{"job-name": jobNamespacedName.Name},
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "busybox"}}},
			}
			Expect(k8sClient.Create(ctx, mockPod)).To(Succeed())

			mockPod.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					Name: "main",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, mockPod)).To(Succeed())

			By("Simulating Job failure")
			now := metav1.Now()
			createdJob.Status.StartTime = &now
			createdJob.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
				{
					Type:   batchv1.JobFailureTarget,
					Status: corev1.ConditionTrue,
					Reason: "BackoffLimitExceeded",
				},
			}
			createdJob.Status.Failed = 1
			Expect(k8sClient.Status().Update(ctx, createdJob)).To(Succeed())

			By("Re-reconciling to observe the failure")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Checking for RecoverableLogicError reason")
			updatedKtask := &customv1.Ktask{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedKtask)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updatedKtask.Status.Phase).To(Equal(customv1.PhaseFailed))

				failedCondition := getCondition(updatedKtask, customv1.JobReady)
				g.Expect(failedCondition).NotTo(BeNil())
				g.Expect(failedCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(failedCondition.Reason).To(Equal(customv1.ReasonRecoverableLogicError))
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			// Clean up the mock pod
			By("Cleaning up the mock logic error pod")
			Expect(k8sClient.Delete(ctx, mockPod)).To(Succeed())
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(mockPod), &corev1.Pod{}))
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})

		It("should not create a new Job if the Ktask is already Succeeded", func() {
			By("Manually setting the Ktask status to Succeeded")
			succeededKtask := &customv1.Ktask{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, succeededKtask)).To(Succeed())
			succeededKtask.Status.Phase = customv1.PhaseSucceeded
			Expect(k8sClient.Status().Update(ctx, succeededKtask)).To(Succeed())

			By("Reconciling the resource")
			controllerReconciler := &KtaskReconciler{
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

		It("should set the Ktask as the owner of the created Job", func() {
			By("Reconciling the resource to create the Job")
			controllerReconciler := &KtaskReconciler{
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
			Expect(createdJob.OwnerReferences[0].Kind).To(Equal("Ktask"))
			Expect(createdJob.OwnerReferences[0].Name).To(Equal(resourceName))
		})

		It("should not create a new Job if the Ktask is already Failed", func() {
			By("Manually setting the Ktask status to Failed")
			failedKtask := &customv1.Ktask{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, failedKtask)).To(Succeed())
			failedKtask.Status.Phase = customv1.PhaseFailed
			Expect(k8sClient.Status().Update(ctx, failedKtask)).To(Succeed())

			By("Reconciling the resource")
			controllerReconciler := &KtaskReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Ensuring no Job was created")
			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			Expect(errors.IsNotFound(k8sClient.Get(ctx, jobNamespacedName, &batchv1.Job{}))).To(BeTrue())
		})

		It("should requeue if the job is still processing", func() {
			By("Reconciling to create the Job")
			controllerReconciler := &KtaskReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Finding the created Job and simulating it is active")
			createdJob := &batchv1.Job{}
			jobNamespacedName := types.NamespacedName{Name: resourceName + "-job", Namespace: "default"}
			Eventually(func() error {
				return k8sClient.Get(ctx, jobNamespacedName, createdJob)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			// Manually update the Job's status to Active
			createdJob.Status.Active = 1
			Expect(k8sClient.Status().Update(ctx, createdJob)).To(Succeed())

			By("Re-reconciling the resource to observe the active state")
			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(result.RequeueAfter).To(Equal(time.Second * 10))
		})
	})
})
