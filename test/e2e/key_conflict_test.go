// key_conflict_test.go - Key conflict E2E tests
//
// Behaviors under test (implemented in pkg/controller/externalsecret/externalsecret_controller.go):
//  1. Duplicate data.name within a single ExternalSecret: getExternalSecret
//     writes entries into a map in spec order, so the later-defined entry
//     overwrites the earlier one (last write wins).
//  2. Multiple ExternalSecrets sharing the same target.name: every successful
//     sync performs a FULL replacement of the target Secret. As both
//     ExternalSecrets reconcile on their rotationInterval, the Secret content
//     alternates between the two full datasets and is never merged. The
//     observable contract: the Secret always equals exactly one of the
//     ExternalSecrets' complete datasets; the last reconciler wins each round.
package e2e

import (
	"bytes"
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Key Conflict E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-keyconflict-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	Context("Duplicate data.name within a single ExternalSecret", func() {
		It("Should let the later-defined entry overwrite the earlier one", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "kc-dup-store-"+getRandString())

			storeRef := &api.SecretStoreRef{
				Name:      store.Name,
				Namespace: store.Namespace,
				Kind:      ResourceSecretStore,
			}

			// Reference ExternalSecret fetching only the "later" source, used to
			// capture the expected value without hard-coding KMS secret contents.
			refES := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kc-ref-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:            SimpleTemplateSecretName,
							Name:           "dup-key",
							VersionId:      "v1",
							SecretStoreRef: storeRef,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, refES)).To(Succeed())
			validateExternalSecretSucceededAndSecretCreated(ctx, refES.Namespace, refES.Name, time.Second*60)

			expectedValue := pfGetSecretData(ctx, testNamespace.Name, refES.Name)["dup-key"]
			Expect(expectedValue).NotTo(BeEmpty(), "reference value should be captured")
			CleanupExternalSecretAndSyncedSecret(ctx, refES)

			// Conflict ExternalSecret: two data entries share the same target
			// name "dup-key" but point to different valid KMS secrets. The
			// second (later-defined) entry must win.
			conflictES := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kc-conflict-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:            CommonKMSSecretName,
							Name:           "dup-key",
							VersionId:      "v1",
							SecretStoreRef: storeRef,
						},
						{
							Key:            SimpleTemplateSecretName,
							Name:           "dup-key",
							VersionId:      "v1",
							SecretStoreRef: storeRef,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, conflictES)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, conflictES)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, conflictES.Namespace, conflictES.Name, time.Second*60)

			Eventually(func() bool {
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, conflictES.Name)
				if !ok {
					return false
				}
				// Exactly one entry: map semantics forbid duplicate keys, and
				// no extra keys should be produced by the conflict.
				if len(data) != 1 {
					return false
				}
				v, ok := data["dup-key"]
				// Last write wins: value must equal the later entry's source.
				return ok && bytes.Equal(v, expectedValue)
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"Secret should contain a single dup-key entry whose value comes from the later-defined data entry")

			Consistently(func() bool {
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, conflictES.Name)
				if !ok {
					return false
				}
				if len(data) != 1 {
					return false
				}
				v, ok := data["dup-key"]
				return ok && bytes.Equal(v, expectedValue)
			}).WithTimeout(time.Second*15).WithPolling(time.Second*3).Should(BeTrue(),
				"last-write-wins behavior should be stable across reconciles")
		})
	})

	Context("Multiple ExternalSecrets sharing the same target.name", func() {
		It("Should fully overwrite the shared Secret with the full dataset of the last reconciler", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "kc-shared-store-"+getRandString())

			storeRef := &api.SecretStoreRef{
				Name:      store.Name,
				Namespace: store.Namespace,
				Kind:      ResourceSecretStore,
			}

			// Behavior contract: both ExternalSecrets manage the SAME target
			// Secret and reconcile independently every rotationInterval. Each
			// successful sync is a FULL replacement, so the Secret alternates
			// between writer A's and writer B's complete datasets over time and
			// is never merged. The final content of any given round is exactly
			// the dataset of whichever ExternalSecret reconciled last.
			sharedTargetName := "kc-shared-target-" + getRandString()

			esA := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kc-writer-a",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:            CommonKMSSecretName,
							Name:           "only-key-a",
							VersionId:      "v1",
							SecretStoreRef: storeRef,
						},
					},
					Target: &api.ExternalSecretTarget{Name: sharedTargetName},
				},
			}
			esB := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kc-writer-b",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:            SimpleTemplateSecretName,
							Name:           "only-key-b",
							VersionId:      "v1",
							SecretStoreRef: storeRef,
						},
					},
					Target: &api.ExternalSecretTarget{Name: sharedTargetName},
				},
			}

			Expect(k8sClient.Create(ctx, esA)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, esA)
				CleanupExternalSecretAndSyncedSecret(ctx, esB)
				// The shared Secret is named after target.name, not the ExternalSecrets.
				_ = k8sClient.Delete(ctx, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: sharedTargetName, Namespace: testNamespace.Name},
				})
			})

			// Baseline: writer A alone produces its full dataset.
			Eventually(func() bool {
				if !pfStatusSucceeded(ctx, esA.Namespace, esA.Name) {
					return false
				}
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, sharedTargetName)
				if !ok || len(data) != 1 {
					return false
				}
				_, okA := data["only-key-a"]
				return okA
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"writer A should sync its full dataset into the shared target Secret")

			Expect(k8sClient.Create(ctx, esB)).To(Succeed())

			// Writer B's first reconcile must fully overwrite the Secret with
			// its own dataset (writer A's keys disappear, no merging happens).
			Eventually(func() bool {
				if !pfStatusSucceeded(ctx, esB.Namespace, esB.Name) {
					return false
				}
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, sharedTargetName)
				if !ok || len(data) != 1 {
					return false
				}
				_, okB := data["only-key-b"]
				return okB
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				"writer B should fully overwrite the shared target Secret with its own dataset")

			// Both writers keep reconciling: the Secret must always hold exactly
			// one complete dataset (A's or B's), never a mixture of both.
			Consistently(func() bool {
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, sharedTargetName)
				if !ok || len(data) != 1 {
					return false
				}
				_, okA := data["only-key-a"]
				_, okB := data["only-key-b"]
				return okA != okB // exactly one of the two datasets present
			}).WithTimeout(time.Second*20).WithPolling(time.Second*3).Should(BeTrue(),
				"shared Secret should consistently hold exactly one writer's full dataset, never merged")
		})
	})
})
