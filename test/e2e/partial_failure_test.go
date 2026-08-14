// partial_failure_test.go - Partial failure / fail-closed E2E tests
//
// Behaviors under test (implemented in pkg/controller/externalsecret/externalsecret_controller.go):
//  1. Partial failure without template: successful keys are written with fresh
//     values while failed keys retain their previous Secret values; failure
//     details are reported in status.dataSyncResults (ExternalSecretKey is the
//     data source Key, Status=Failed, Reason carries the error).
//  2. Total failure: the Secret is retained in full (CleanUpSecretOnFailure
//     defaults to false), never cleared.
//  3. Template fail-closed: when spec.target.template is set and any data
//     source fails, the whole Secret write is skipped and the previously
//     rendered content is preserved.
//  4. cleanupSecretOnFailure deletion contract: with --cleanup-secret-on-failure=true
//     a TOTAL failure deletes the target Secret; the suite default is false,
//     so this Context patches the manager Deployment args (and restores them).
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// pfUpdateES applies mutate to the latest ExternalSecret spec and updates it,
// retrying on resource-version conflicts.
func pfUpdateES(ctx context.Context, namespace, name string, mutate func(es *api.ExternalSecret)) {
	Eventually(func() error {
		latest := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, latest); err != nil {
			return err
		}
		mutate(latest)
		return k8sClient.Update(ctx, latest)
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
		"should update ExternalSecret %s/%s spec", namespace, name)
}

// pfGetSecretData returns a deep copy of the given Secret's data map.
func pfGetSecretData(ctx context.Context, namespace, name string) map[string][]byte {
	secret := &corev1.Secret{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret)).To(Succeed())
	data := make(map[string][]byte, len(secret.Data))
	for k, v := range secret.Data {
		cp := make([]byte, len(v))
		copy(cp, v)
		data[k] = cp
	}
	return data
}

// pfTryGetSecretData is the non-panicking variant of pfGetSecretData, safe for
// use inside Eventually/Consistently closures before the Secret is guaranteed
// to exist.
func pfTryGetSecretData(ctx context.Context, namespace, name string) (map[string][]byte, bool) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return nil, false
	}
	data := make(map[string][]byte, len(secret.Data))
	for k, v := range secret.Data {
		cp := make([]byte, len(v))
		copy(cp, v)
		data[k] = cp
	}
	return data, true
}

// pfStatusHasFailedKeys reports whether status.dataSyncResults contains a
// Failed entry (with non-empty Reason) for every one of failedKeys.
// Note: the controller reports only failure entries per round; a fully
// successful round carries a single Succeeded entry without per-key details.
func pfStatusHasFailedKeys(ctx context.Context, namespace, name string, failedKeys ...string) bool {
	es := &api.ExternalSecret{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, es); err != nil {
		return false
	}
	failed := make(map[string]bool)
	for _, r := range es.Status.DataSyncResults {
		if r.Status == "Failed" && r.Reason != "" {
			failed[r.ExternalSecretKey] = true
		}
	}
	for _, k := range failedKeys {
		if !failed[k] {
			return false
		}
	}
	return true
}

// pfStatusHasKey reports whether status.dataSyncResults contains any entry
// whose ExternalSecretKey equals key (e.g. "template_processing_errors").
func pfStatusHasKey(ctx context.Context, namespace, name string, key string) bool {
	es := &api.ExternalSecret{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, es); err != nil {
		return false
	}
	for _, r := range es.Status.DataSyncResults {
		if r.ExternalSecretKey == key {
			return true
		}
	}
	return false
}

// pfSecretAbsent reports whether the named Secret does not exist (NotFound).
func pfSecretAbsent(ctx context.Context, namespace, name string) bool {
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &corev1.Secret{})
	return k8serrors.IsNotFound(err)
}

// patchDeploymentCleanupOnFailureArg patches the ack-secret-manager Deployment
// to set --cleanup-secret-on-failure=<enabled> and waits for the rollout.
// Returns the original args so callers can restore them via
// restoreDeploymentArgs. Mirrors patchDeploymentCrossNamespaceArgs.
func patchDeploymentCleanupOnFailureArg(ctx context.Context, enabled bool) (originalArgs []string) {
	return patchDeploymentArgs(ctx,
		[]string{"--cleanup-secret-on-failure"},
		[]string{fmt.Sprintf("--cleanup-secret-on-failure=%v", enabled)})
}

// pfStatusSucceeded reports whether status.dataSyncResults is non-empty and
// every entry is Succeeded.
func pfStatusSucceeded(ctx context.Context, namespace, name string) bool {
	es := &api.ExternalSecret{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, es); err != nil {
		return false
	}
	if len(es.Status.DataSyncResults) == 0 {
		return false
	}
	for _, r := range es.Status.DataSyncResults {
		if r.Status != "Succeeded" {
			return false
		}
	}
	return true
}

var _ = Describe("Partial Failure E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-partialfail-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	Context("Partial failure without template", func() {
		It("Should write fresh values for successful keys and retain old values for failed keys", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "pf-partial-store-"+getRandString())

			// Baseline: both data sources are valid.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pf-partial-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "stable-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:       JsonKMSSecretName,
							Name:      "victim-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			baseline := pfGetSecretData(ctx, testNamespace.Name, externalSecret.Name)
			baselineStable := baseline["stable-key"]
			baselineVictim := baseline["victim-key"]
			Expect(baselineStable).NotTo(BeEmpty(), "baseline stable-key should exist")
			Expect(baselineVictim).NotTo(BeEmpty(), "baseline victim-key should exist")

			// Introduce a partial failure:
			//  - stable-key now points to a DIFFERENT valid KMS secret, so its
			//    value must be refreshed (proving successful keys are still written);
			//  - victim-key points to a non-existent KMS secret, so its previous
			//    value must be retained in the Secret.
			nonExistentKey := TestResourcePrefix + "nonexistent-" + getRandString()
			pfUpdateES(ctx, externalSecret.Namespace, externalSecret.Name, func(es *api.ExternalSecret) {
				es.Spec.Data[0].Key = YamlKMSSecretName
				es.Spec.Data[1].Key = nonExistentKey
			})

			// Fetch retries add up to ~6s worst case, so leave generous margins.
			Eventually(func() bool {
				// 1) status must carry a Failed record for the broken key.
				if !pfStatusHasFailedKeys(ctx, externalSecret.Namespace, externalSecret.Name, nonExistentKey) {
					return false
				}
				// 2) Secret must merge: failed key keeps its old value,
				//    successful key carries the fresh value of the new source.
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, externalSecret.Name)
				if !ok {
					return false
				}
				victim, okVictim := data["victim-key"]
				stable, okStable := data["stable-key"]
				if !okVictim || !bytes.Equal(victim, baselineVictim) {
					return false
				}
				if !okStable || bytes.Equal(stable, baselineStable) {
					return false
				}
				return true
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				"partial failure should report Failed in status, refresh successful keys and retain failed key values")

			// The failed key must never be cleared by subsequent reconciles.
			Consistently(func() bool {
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, externalSecret.Name)
				if !ok {
					return false
				}
				victim, ok := data["victim-key"]
				return ok && bytes.Equal(victim, baselineVictim)
			}).WithTimeout(time.Second*15).WithPolling(time.Second*3).Should(BeTrue(),
				"failed key should consistently retain its old value and never be cleared")
		})
	})

	Context("Total failure without template", func() {
		It("Should retain the full Secret when all data sources fail", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "pf-total-store-"+getRandString())

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pf-total-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "first-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:       JsonKMSSecretName,
							Name:      "second-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			baseline := pfGetSecretData(ctx, testNamespace.Name, externalSecret.Name)
			Expect(baseline).To(HaveKey("first-key"))
			Expect(baseline).To(HaveKey("second-key"))

			// Break every data source. CleanUpSecretOnFailure defaults to false,
			// so the Secret must be preserved in full.
			nonExistentKey1 := TestResourcePrefix + "nonexistent-a-" + getRandString()
			nonExistentKey2 := TestResourcePrefix + "nonexistent-b-" + getRandString()
			pfUpdateES(ctx, externalSecret.Namespace, externalSecret.Name, func(es *api.ExternalSecret) {
				es.Spec.Data[0].Key = nonExistentKey1
				es.Spec.Data[1].Key = nonExistentKey2
			})

			Eventually(func() bool {
				return pfStatusHasFailedKeys(ctx, externalSecret.Namespace, externalSecret.Name,
					nonExistentKey1, nonExistentKey2)
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				"status should report Failed for every broken data source")

			Consistently(func() bool {
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, externalSecret.Name)
				if !ok {
					return false
				}
				return reflect.DeepEqual(data, baseline)
			}).WithTimeout(time.Second*15).WithPolling(time.Second*3).Should(BeTrue(),
				"Secret should consistently remain fully intact (not cleared) on total failure")
		})
	})

	Context("Template fail-closed", func() {
		It("Should keep the rendered Secret unchanged when a templated ExternalSecret partially fails", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "pf-template-store-"+getRandString())

			targetSecretName := "pf-template-target-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pf-template-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "user",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:       JsonKMSSecretName,
							Name:      "env",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: targetSecretName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"greeting": `User: {{ .user }}`,
								"envinfo":  `Env: {{ .env }}`,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
				// The synced Secret is named after target.name, not the ExternalSecret.
				_ = k8sClient.Delete(ctx, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: targetSecretName, Namespace: testNamespace.Name},
				})
			})

			// Wait for the initial successful templated sync.
			Eventually(func() bool {
				if !pfStatusSucceeded(ctx, externalSecret.Namespace, externalSecret.Name) {
					return false
				}
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, targetSecretName)
				if !ok {
					return false
				}
				_, okGreeting := data["greeting"]
				_, okEnv := data["envinfo"]
				return okGreeting && okEnv
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"templated ExternalSecret should sync successfully and render the target Secret")

			baseline := pfGetSecretData(ctx, testNamespace.Name, targetSecretName)

			// Break one data source: templates may reference the failed key, so
			// the controller must fail closed and skip the write entirely.
			nonExistentKey := TestResourcePrefix + "nonexistent-tpl-" + getRandString()
			pfUpdateES(ctx, externalSecret.Namespace, externalSecret.Name, func(es *api.ExternalSecret) {
				es.Spec.Data[1].Key = nonExistentKey
			})

			Eventually(func() bool {
				return pfStatusHasFailedKeys(ctx, externalSecret.Namespace, externalSecret.Name, nonExistentKey)
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				fmt.Sprintf("status should report Failed for %s", nonExistentKey))

			Consistently(func() bool {
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, targetSecretName)
				if !ok {
					return false
				}
				return reflect.DeepEqual(data, baseline)
			}).WithTimeout(time.Second*15).WithPolling(time.Second*3).Should(BeTrue(),
				"templated Secret should consistently keep its previously rendered content on partial failure")
		})
	})

	// Contract precedence: data-source failures outrank template fatal errors.
	// A same-round template parse error is downgraded to
	// template_processing_errors (never template_processing_fatal), the round
	// reports the data-source failure, and the fail-closed skip is kept.
	Context("Template error contract precedence", func() {
		It("Should defer template fatal errors to the data-source failure contract on partial failure", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "pf-precedence-store-"+getRandString())

			targetSecretName := "pf-precedence-target-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pf-precedence-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "user",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:       JsonKMSSecretName,
							Name:      "env",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: targetSecretName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"greeting": `User: {{ .user }}`,
								// {{ .env }} references the key that will fail below.
								// The initial round renders fine; a later update injects a
								// template parse error to force a fatal template error in
								// the same round as the data-source failure.
								"envinfo": `Env: {{ .env }}`,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
				// The synced Secret is named after target.name, not the ExternalSecret.
				_ = k8sClient.Delete(ctx, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: targetSecretName, Namespace: testNamespace.Name},
				})
			})

			// Wait for the initial successful templated sync.
			Eventually(func() bool {
				if !pfStatusSucceeded(ctx, externalSecret.Namespace, externalSecret.Name) {
					return false
				}
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, targetSecretName)
				if !ok {
					return false
				}
				_, okGreeting := data["greeting"]
				_, okEnv := data["envinfo"]
				return okGreeting && okEnv
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"templated ExternalSecret should sync successfully before the failure injection")

			baseline := pfGetSecretData(ctx, testNamespace.Name, targetSecretName)

			// Break one data source AND inject a template parse error (unclosed
			// action) so this round has BOTH a data-source failure and a fatal
			// template error. Parse errors are always fatal, so they reliably
			// exercise the contract-precedence rule.
			nonExistentKey := TestResourcePrefix + "nonexistent-precedence-" + getRandString()
			pfUpdateES(ctx, externalSecret.Namespace, externalSecret.Name, func(es *api.ExternalSecret) {
				es.Spec.Data[1].Key = nonExistentKey
				es.Spec.Target.Template.Data["envinfo"] = `Env: {{ .env`
			})

			// The data-source failure contract keeps precedence: status reports
			// the failed data key...
			Eventually(func() bool {
				return pfStatusHasFailedKeys(ctx, externalSecret.Namespace, externalSecret.Name, nonExistentKey)
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				fmt.Sprintf("status should report Failed for %s", nonExistentKey))

			// ...and the template fatal error is downgraded to a non-fatal
			// template_processing_errors entry instead of template_processing_fatal.
			Eventually(func() bool {
				return pfStatusHasKey(ctx, externalSecret.Namespace, externalSecret.Name, "template_processing_errors")
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				"template errors should be reported as template_processing_errors when data sources also fail")
			Expect(pfStatusHasKey(ctx, externalSecret.Namespace, externalSecret.Name, "template_processing_fatal")).To(BeFalse(),
				"template errors must not be fatal while data sources also fail")

			// Fail-closed skip: the previously rendered Secret stays untouched.
			Consistently(func() bool {
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, targetSecretName)
				if !ok {
					return false
				}
				return reflect.DeepEqual(data, baseline)
			}).WithTimeout(time.Second*15).WithPolling(time.Second*3).Should(BeTrue(),
				"templated Secret should keep its previously rendered content when template errors defer to the data-source contract")
		})
	})

	// cleanupSecretOnFailure deletion contract: the suite default is false, so
	// this Ordered Context patches the Deployment once for all enclosed specs
	// and restores the original args afterwards (even on spec failure, via
	// DeferCleanup). Synced Secrets carry no owner reference, so cleanup relies
	// on CleanupExternalSecretAndSyncedSecret plus namespace deletion.
	Context("cleanupSecretOnFailure deletion contract", Ordered, func() {
		var originalDepArgs []string

		BeforeAll(func() {
			By("patching Deployment to enable --cleanup-secret-on-failure=true")
			originalDepArgs = patchDeploymentCleanupOnFailureArg(ctx, true)
			DeferCleanup(func() {
				By("restoring original Deployment args (cleanup-secret-on-failure baseline)")
				if len(originalDepArgs) > 0 {
					restoreDeploymentArgs(ctx, originalDepArgs)
				}
			})
		})

		It("Should delete the Secret when all data sources fail (no template)", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "pf-cleanup-store-"+getRandString())

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pf-cleanup-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "first-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:       JsonKMSSecretName,
							Name:      "second-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			// Baseline: the Secret exists before the failure is introduced,
			// so the deletion below proves the real delete path (not just the
			// NotFound-tolerant branch of handleProviderDeletion).
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			// Break every data source: total failure + cleanup enabled.
			nonExistentKey1 := TestResourcePrefix + "nonexistent-cleanup-a-" + getRandString()
			nonExistentKey2 := TestResourcePrefix + "nonexistent-cleanup-b-" + getRandString()
			pfUpdateES(ctx, externalSecret.Namespace, externalSecret.Name, func(es *api.ExternalSecret) {
				es.Spec.Data[0].Key = nonExistentKey1
				es.Spec.Data[1].Key = nonExistentKey2
			})

			Eventually(func() bool {
				return pfStatusHasFailedKeys(ctx, externalSecret.Namespace, externalSecret.Name,
					nonExistentKey1, nonExistentKey2)
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				"status should report Failed for every broken data source")

			// Deletion contract: the target Secret must eventually be gone.
			// The assertion is NotFound-based, so it also holds when the
			// Secret was never created (handleProviderDeletion tolerates
			// NotFound).
			Eventually(func() bool {
				return pfSecretAbsent(ctx, testNamespace.Name, externalSecret.Name)
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				"Secret should be deleted by the cleanupSecretOnFailure deletion contract on total failure")

			// Subsequent reconciles must not recreate the Secret.
			Consistently(func() bool {
				return pfSecretAbsent(ctx, testNamespace.Name, externalSecret.Name)
			}).WithTimeout(time.Second*20).WithPolling(time.Second*5).Should(BeTrue(),
				"Secret should consistently stay deleted while all data sources keep failing")
		})

		It("Should delete the templated Secret on total failure and never write template-rendered static content", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "pf-cleanup-tpl-store-"+getRandString())

			targetSecretName := "pf-cleanup-target-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pf-cleanup-tpl-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "user",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:       JsonKMSSecretName,
							Name:      "env",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: targetSecretName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// References the data-source keys, so rendering
								// breaks once they fail.
								"greeting": `User: {{ .user }}`,
								"envinfo":  `Env: {{ .env }}`,
								// Purely static: still renders a non-empty map on
								// total failure (secretMapLen > 0 truth-table row).
								// The deletion contract must win over this output.
								"static": "always-rendered-literal",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
				// The synced Secret is named after target.name, not the ExternalSecret.
				_ = k8sClient.Delete(ctx, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: targetSecretName, Namespace: testNamespace.Name},
				})
			})

			// Baseline: the templated Secret exists before the failure.
			Eventually(func() bool {
				if !pfStatusSucceeded(ctx, externalSecret.Namespace, externalSecret.Name) {
					return false
				}
				data, ok := pfTryGetSecretData(ctx, testNamespace.Name, targetSecretName)
				if !ok {
					return false
				}
				_, okGreeting := data["greeting"]
				_, okStatic := data["static"]
				return okGreeting && okStatic
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"templated ExternalSecret should sync successfully before the failure is introduced")

			// Break every data source: total failure + cleanup enabled.
			nonExistentKey1 := TestResourcePrefix + "nonexistent-cleanup-tpl-a-" + getRandString()
			nonExistentKey2 := TestResourcePrefix + "nonexistent-cleanup-tpl-b-" + getRandString()
			pfUpdateES(ctx, externalSecret.Namespace, externalSecret.Name, func(es *api.ExternalSecret) {
				es.Spec.Data[0].Key = nonExistentKey1
				es.Spec.Data[1].Key = nonExistentKey2
			})

			Eventually(func() bool {
				return pfStatusHasFailedKeys(ctx, externalSecret.Namespace, externalSecret.Name,
					nonExistentKey1, nonExistentKey2)
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				"status should report Failed for every broken data source")

			// The Secret must be deleted -- in particular it must NOT be
			// rewritten with the template's static content.
			Eventually(func() bool {
				return pfSecretAbsent(ctx, testNamespace.Name, targetSecretName)
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				"templated Secret should be deleted on total failure instead of retaining or rewriting static template content")

			Consistently(func() bool {
				return pfSecretAbsent(ctx, testNamespace.Name, targetSecretName)
			}).WithTimeout(time.Second*20).WithPolling(time.Second*5).Should(BeTrue(),
				"templated Secret should consistently stay deleted (static template content never re-written)")
		})
	})
})
