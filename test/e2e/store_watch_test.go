// store_watch_test.go - E2E tests for the ExternalSecret controller's reverse
// watch on SecretStore/ClusterSecretStore. Store spec edits, trigger
// annotations and store deletion/recreation must cascade to referencing
// ExternalSecrets immediately, without waiting for the rotation poll.
//
// Design principle: every spec that must distinguish "immediate watch
// trigger" from "periodic polling" pins the ExternalSecret rotationInterval
// to 30m (far beyond every assertion window), so a refresh observed inside
// the short window can only come from the reverse watch.
//
// Observability: status.dataSyncResults[].SynchronizationTime only advances
// when the controller actually writes status (fingerprint change or a real
// Secret write). Specs therefore stage a new remote KMS version BEFORE the
// trigger: the watch-triggered round then fetches the new value, writes the
// Secret and advances SynchronizationTime; a no-op round could not fake it.
package e2e

import (
	"context"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// Constants storeWatchTriggerAnnotation, storeWatchLongInterval,
// storeWatchTriggerWindow and storeWatchQuietWindow are defined in
// helpers_test.go.

// latestSyncTimeOf, recordExternalSecretSyncBaseline, and
// waitForExternalSecretSyncTimeAdvance are defined in helpers_test.go.

// getSyncedSecretStringValue reads one data key of the synced Secret (named
// after the ExternalSecret per the suite's lookup convention).
func getSyncedSecretStringValue(ctx context.Context, namespace, name, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, secret); err != nil {
		return "", err
	}
	return string(secret.Data[key]), nil
}

// storeWatchFixture bundles the per-spec state shared by the watch specs.
type storeWatchFixture struct {
	kmsSecretName string
	esName        string
	dataKey       string
	baseline      metav1.Time
}

// setupStoreWatchExternalSecret creates a dedicated remote KMS secret, a
// long-rotation ExternalSecret referencing the given store and waits for the
// baseline sync. Cleanup is registered via DeferCleanup (LIFO: the
// ExternalSecret is reclaimed before the remote KMS secret is deleted).
func setupStoreWatchExternalSecret(ctx context.Context, namespace, esName string, ref *api.SecretStoreRef, specMutators ...func(*api.ExternalSecretSpec)) *storeWatchFixture {
	Expect(GlobalResourceManager).NotTo(BeNil())
	kmsSecretName, err := GlobalResourceManager.CreateKMSSecretForCredentialUpdate(ctx)
	Expect(err).NotTo(HaveOccurred(), "failed to create dedicated KMS secret for store watch spec")
	DeferCleanup(func() {
		if err := GlobalResourceManager.DeleteKMSSecret(kmsSecretName); err != nil {
			GinkgoWriter.Printf("WARNING: failed to delete dedicated KMS secret %s: %v\n", kmsSecretName, err)
		}
	})

	spec := api.ExternalSecretSpec{
		Provider: "kms",
		// Long rotation: polling can never explain a refresh inside the
		// short assertion windows below.
		RotationInterval: &metav1.Duration{Duration: storeWatchLongInterval},
		Data: []api.DataSource{
			{
				// No VersionId: always fetch the latest version so staged
				// remote values become observable on the next sync.
				Key:            kmsSecretName,
				Name:           "watch-secret-key",
				SecretStoreRef: ref,
			},
		},
	}
	// Optional mutators let specs reshape the baseline spec (e.g. move the
	// data source into dataProcess[].extract) without duplicating the setup.
	for _, mutate := range specMutators {
		mutate(&spec)
	}
	externalSecret := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      esName,
			Namespace: namespace,
		},
		Spec: spec,
	}
	Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
	DeferCleanup(func() {
		CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
	})

	validateExternalSecretSucceededAndSecretCreated(ctx, namespace, esName, storeWatchTriggerWindow)
	baseline := recordExternalSecretSyncBaseline(ctx, namespace, esName)

	return &storeWatchFixture{
		kmsSecretName: kmsSecretName,
		esName:        esName,
		dataKey:       "watch-secret-key",
		baseline:      baseline,
	}
}

// stageRemoteVersion appends a new latest version to the fixture's dedicated
// KMS secret; only a fresh sync round can observe it.
func (f *storeWatchFixture) stageRemoteVersion(ctx context.Context, versionID, value string) {
	Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, f.kmsSecretName, versionID, value)).To(Succeed(),
		"failed to stage remote KMS version %s for store watch spec", versionID)
}

// waitForSecretValue waits until the synced Secret holds the expected value.
func (f *storeWatchFixture) waitForSecretValue(ctx context.Context, namespace, expected string, timeout time.Duration) {
	Eventually(func() bool {
		value, err := getSyncedSecretStringValue(ctx, namespace, f.esName, f.dataKey)
		if err != nil {
			return false
		}
		return value == expected
	}).WithTimeout(timeout).WithPolling(time.Second*2).Should(BeTrue(),
		"synced Secret %s/%s should hold the staged remote value %q", namespace, f.esName, expected)
}

// expectNoResync asserts across the quiet window that neither the synced
// Secret value nor the newest SynchronizationTime changes, i.e. the given
// (non-)event did not trigger a resync. Transient API errors are not
// evidence of a resync and keep the window running.
func expectNoResync(ctx context.Context, namespace string, f *storeWatchFixture, expectedValue string, baseline metav1.Time, eventDesc string) {
	Consistently(func() bool {
		value, err := getSyncedSecretStringValue(ctx, namespace, f.esName, f.dataKey)
		if err != nil {
			return true
		}
		if value != expectedValue {
			return false
		}
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Namespace: namespace, Name: f.esName,
		}, es); err != nil {
			return true
		}
		return !latestSyncTimeOf(es).Time.After(baseline.Time)
	}).WithTimeout(storeWatchQuietWindow).WithPolling(time.Second*5).Should(BeTrue(),
		"%s must not trigger a resync: the synced Secret and SynchronizationTime must stay unchanged", eventDesc)
}

// setSecretStoreTriggerAnnotation is defined in helpers_test.go.

// setClusterSecretStoreTriggerAnnotation is the cluster-scoped counterpart of
// setSecretStoreTriggerAnnotation.
func setClusterSecretStoreTriggerAnnotation(ctx context.Context, name, value string) {
	Eventually(func() error {
		store := &api.ClusterSecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, store); err != nil {
			return err
		}
		if store.Annotations == nil {
			store.Annotations = make(map[string]string)
		}
		store.Annotations[storeWatchTriggerAnnotation] = value
		return k8sClient.Update(ctx, store)
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
		"should set the trigger annotation on ClusterSecretStore %s", name)
}

// waitForSecretStoreTriggerAnnotationCleared waits until the store controller
// has processed and removed the trigger annotation.
func waitForSecretStoreTriggerAnnotationCleared(ctx context.Context, namespace, name string) {
	Eventually(func() bool {
		store := &api.SecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, store); err != nil {
			return false
		}
		_, present := store.Annotations[storeWatchTriggerAnnotation]
		return !present
	}).WithTimeout(storeWatchTriggerWindow).WithPolling(time.Second*2).Should(BeTrue(),
		"store controller should clear the trigger annotation on SecretStore %s/%s after processing", namespace, name)
}

// recordStoreClientGeneration, recordClusterSecretStoreClientGeneration,
// storeClientGenerationAdvanced, expectStoreClientGenerationAdvanced,
// expectClusterSecretStoreClientGenerationAdvanced, setSecretStoreSessionName,
// setClusterSecretStoreSessionName, createRRSAClusterSecretStore,
// createRRSAOOSSecretStore, setOOSSecretStoreSessionName,
// setSecretStoreTriggerAnnotation, and expectExternalSecretFailedWith
// are defined in helpers_test.go.

// setupOOSStoreWatchFixture creates a long-rotation OOS ExternalSecret
// referencing the given store and dedicated OOS parameter, waits for the
// baseline sync and returns a storeWatchFixture usable with
// waitForExternalSecretSyncTimeAdvance / expectNoResync. Cleanup is
// registered via DeferCleanup (LIFO: the ExternalSecret is reclaimed before
// the caller's store and OOS parameter cleanups run).
func setupOOSStoreWatchFixture(ctx context.Context, namespace, esName, storeName, oosParamName string) *storeWatchFixture {
	externalSecret := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      esName,
			Namespace: namespace,
		},
		Spec: api.ExternalSecretSpec{
			Provider: "oos",
			// Long rotation: polling can never explain a refresh inside the
			// short assertion windows below.
			RotationInterval: &metav1.Duration{Duration: storeWatchLongInterval},
			Data: []api.DataSource{
				{
					// No VersionId: always fetch the latest parameter version so
					// staged remote values become observable on the next sync.
					Key:  oosParamName,
					Name: "oos-watch-secret-key",
					SecretStoreRef: &api.SecretStoreRef{
						Name:      storeName,
						Namespace: namespace,
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

	// Wider than storeWatchTriggerWindow: the first OOS sync also pays the
	// STS assume-role cost of the freshly created store client.
	validateExternalSecretSucceededAndSecretCreated(ctx, namespace, esName, time.Second*90)
	baseline := recordExternalSecretSyncBaseline(ctx, namespace, esName)

	return &storeWatchFixture{
		esName:   esName,
		dataKey:  "oos-watch-secret-key",
		baseline: baseline,
	}
}

// expectExternalSecretFailedWith is defined in helpers_test.go.

var _ = Describe("Store Watch E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-store-watch-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	Context("ExternalSecret watches its referenced SecretStore", func() {
		It("Should resync immediately when the referenced SecretStore spec changes", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "watch-spec-store-"+getRandString())
			fixture := setupStoreWatchExternalSecret(ctx, testNamespace.Name, "watch-spec-es", &api.SecretStoreRef{
				Name:      store.Name,
				Namespace: testNamespace.Name,
				Kind:      ResourceSecretStore,
			})

			By("staging a new remote value that only a fresh sync can observe")
			fixture.stageRemoteVersion(ctx, "v2", "store-watch-spec-v2")

			By("changing a side-effect-free store spec field (RAM role session name)")
			setSecretStoreSessionName(ctx, testNamespace.Name, store.Name, "e2e-watch-spec-"+getRandString())

			By("observing SynchronizationTime advance within the short watch window")
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, fixture.esName, fixture.baseline,
				storeWatchTriggerWindow, "the SecretStore spec change")
			fixture.waitForSecretValue(ctx, testNamespace.Name, "store-watch-spec-v2", time.Second*10)
		})

		It("Should resync immediately when a SecretStore referenced via dataProcess changes", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "watch-dataproc-store-"+getRandString())

			// dataProcess.extract requires JSON/YAML map data (the extract
			// operation unmarshals the remote payload into a map); the
			// plain-text KMS secret used by the other specs would fail with
			// "cannot unmarshal !!str ... into map[string]interface{}".
			By("creating a JSON-map KMS secret for the dataProcess extract path")
			initialData := `{"watch-secret-key":"initial-store-watch-dataproc-v2"}`
			kmsSecretName, err := GlobalResourceManager.CreateKMSSecretWithData(ctx, initialData)
			Expect(err).NotTo(HaveOccurred(), "failed to create JSON-map KMS secret for dataProcess store watch spec")
			DeferCleanup(func() {
				if err := GlobalResourceManager.DeleteKMSSecret(kmsSecretName); err != nil {
					GinkgoWriter.Printf("WARNING: failed to delete dedicated KMS secret %s: %v\n", kmsSecretName, err)
				}
			})

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "watch-dataproc-es",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: storeWatchLongInterval},
					DataProcess: []api.DataProcess{
						{
							Extract: &api.DataSource{
								Key:  kmsSecretName,
								Name: "watch-secret-key",
								SecretStoreRef: &api.SecretStoreRef{
									Name:      store.Name,
									Namespace: testNamespace.Name,
									Kind:      ResourceSecretStore,
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, testNamespace.Name, externalSecret.Name, storeWatchTriggerWindow)
			baseline := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, externalSecret.Name)

			By("staging a new remote value that only a fresh sync can observe")
			Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, kmsSecretName, "v2",
				`{"watch-secret-key":"store-watch-dataproc-v2"}`)).To(Succeed())

			By("changing a side-effect-free store spec field (RAM role session name)")
			setSecretStoreSessionName(ctx, testNamespace.Name, store.Name, "e2e-watch-dataproc-"+getRandString())

			By("observing SynchronizationTime advance within the short watch window")
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, externalSecret.Name, baseline,
				storeWatchTriggerWindow, "the SecretStore spec change referenced via dataProcess")

			// After extract, the target Secret's watch-secret-key holds the
			// string value from the JSON map (not the full JSON document).
			Eventually(func() bool {
				value, err := getSyncedSecretStringValue(ctx, testNamespace.Name, externalSecret.Name, "watch-secret-key")
				if err != nil {
					return false
				}
				return value == "store-watch-dataproc-v2"
			}).WithTimeout(time.Second*10).WithPolling(time.Second*2).Should(BeTrue(),
				"synced Secret should hold the staged extract value after the dataProcess resync")
		})

		It("Should resync immediately when the trigger annotation is set and the annotation must be cleared after processing", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "watch-trigger-store-"+getRandString())
			fixture := setupStoreWatchExternalSecret(ctx, testNamespace.Name, "watch-trigger-es", &api.SecretStoreRef{
				Name:      store.Name,
				Namespace: testNamespace.Name,
				Kind:      ResourceSecretStore,
			})

			By("recording the store's Status.ClientGeneration baseline before triggering")
			genBefore := recordStoreClientGeneration(ctx, testNamespace.Name, store.Name)

			By("staging a new remote value that only a fresh sync can observe")
			fixture.stageRemoteVersion(ctx, "v2", "store-watch-trigger-v2")

			By("setting the trigger annotation to a fresh UnixNano value")
			setSecretStoreTriggerAnnotation(ctx, testNamespace.Name, store.Name, strconv.FormatInt(time.Now().UnixNano(), 10))

			By("observing the store controller clear the annotation after processing")
			waitForSecretStoreTriggerAnnotationCleared(ctx, testNamespace.Name, store.Name)

			// Ordering contract (synchronous, non-retrying read): the bump is
			// persisted before the annotation removal, so it must already be
			// visible the instant the cleared annotation is observed.
			expectStoreClientGenerationAlreadyAdvanced(ctx, testNamespace.Name, store.Name, genBefore,
				"the non-empty trigger annotation")

			By("observing the store controller bump Status.ClientGeneration for the rebuilt client")
			// Strictly-increasing comparison (never an exact value) keeps the
			// assertion race-free against concurrent rebuilds; the Ready
			// condition must also have observed the current generation, which
			// is the exact freshness signal the ExternalSecret guard consumes.
			expectStoreClientGenerationAdvanced(ctx, testNamespace.Name, store.Name, genBefore,
				"the non-empty trigger annotation")

			By("observing the ExternalSecret resync within the short watch window")
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, fixture.esName, fixture.baseline,
				storeWatchTriggerWindow, "the non-empty trigger annotation")
			fixture.waitForSecretValue(ctx, testNamespace.Name, "store-watch-trigger-v2", time.Second*10)

			// The store controller cleared the annotation after processing;
			// the clearing itself must not trigger any further resync. Stage
			// a new remote value so a spurious resync would be observable.
			baselineAfterClear := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, fixture.esName)
			fixture.stageRemoteVersion(ctx, "v3", "store-watch-trigger-v3")
			expectNoResync(ctx, testNamespace.Name, fixture, "store-watch-trigger-v2", baselineAfterClear,
				"the cleared trigger annotation")
		})

		It("Should not resync when the trigger annotation is set to an empty string", func() {
			store := createRRSASecretStore(ctx, testNamespace.Name, "watch-clear-store-"+getRandString())
			fixture := setupStoreWatchExternalSecret(ctx, testNamespace.Name, "watch-clear-es", &api.SecretStoreRef{
				Name:      store.Name,
				Namespace: testNamespace.Name,
				Kind:      ResourceSecretStore,
			})

			// Positive leg: a non-empty annotation triggers a resync. Staging
			// the remote value first makes the resync observable and
			// establishes a fresh baseline for the negative leg.
			fixture.stageRemoteVersion(ctx, "v2", "store-watch-clear-v2")
			setSecretStoreTriggerAnnotation(ctx, testNamespace.Name, store.Name, strconv.FormatInt(time.Now().UnixNano(), 10))
			waitForSecretStoreTriggerAnnotationCleared(ctx, testNamespace.Name, store.Name)
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, fixture.esName, fixture.baseline,
				storeWatchTriggerWindow, "the non-empty trigger annotation")
			fixture.waitForSecretValue(ctx, testNamespace.Name, "store-watch-clear-v2", time.Second*10)

			// Explicit clearing assertion: the annotation has been cleared by
			// the store controller; the clearing itself must not trigger any
			// further resync. Stage a new remote value so a spurious resync
			// would be observable.
			baselineAfterClear := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, fixture.esName)
			fixture.stageRemoteVersion(ctx, "v3", "store-watch-clear-v3")
			expectNoResync(ctx, testNamespace.Name, fixture, "store-watch-clear-v2", baselineAfterClear,
				"the cleared trigger annotation")

			// Negative leg: stage yet another remote value, then set the
			// annotation to an empty string. The unified predicate semantics
			// (a cleared/empty trigger annotation must not trigger) combined
			// with the 30m rotation must keep the ExternalSecret untouched.
			fixture.stageRemoteVersion(ctx, "v4", "store-watch-clear-v4")
			setSecretStoreTriggerAnnotation(ctx, testNamespace.Name, store.Name, "")

			// Shared helper keeps the quiet window and the frozen-state check
			// identical to every other negative assertion (no duplicated
			// Consistently magic numbers).
			expectNoResync(ctx, testNamespace.Name, fixture, "store-watch-clear-v2", baselineAfterClear,
				"the empty trigger annotation")
		})

		It("Should turn Failed immediately when the referenced SecretStore is deleted and recover when recreated", func() {
			storeName := "watch-delete-store-" + getRandString()
			store := createRRSASecretStore(ctx, testNamespace.Name, storeName)

			// Stable shared KMS secret is fine here: the spec observes the
			// status transition, not a remote value change.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "watch-delete-es",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: storeWatchLongInterval},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "watch-delete-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: testNamespace.Name,
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
			validateExternalSecretSucceededAndSecretCreated(ctx, testNamespace.Name, externalSecret.Name, storeWatchTriggerWindow)

			By("deleting the referenced SecretStore")
			Expect(k8sClient.Delete(ctx, store)).To(Succeed())

			By("observing the ExternalSecret fail fast with the controller's store-lookup error")
			expectExternalSecretFailedWith(ctx, testNamespace.Name, externalSecret.Name,
				"failed to get SecretStore", storeWatchTriggerWindow)

			By("recreating the SecretStore with the same name")
			recreatedStore := createRRSASecretStore(ctx, testNamespace.Name, storeName)
			DeferCleanup(func() {
				// Idempotent: mirrors the ClusterSecretStore side cleanup.
				if err := k8sClient.Delete(ctx, recreatedStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete recreated SecretStore %s: %v\n", storeName, err)
				}
			})

			By("observing the ExternalSecret recover without any polling help")
			validateExternalSecretSucceededAndSecretCreated(ctx, testNamespace.Name, externalSecret.Name, time.Second*90)
		})
	})

	Context("ExternalSecret watches its referenced ClusterSecretStore", func() {
		It("Should resync immediately when the referenced ClusterSecretStore spec changes", func() {
			cssName := "watch-spec-css-" + getRandString()
			css := createRRSAClusterSecretStore(ctx, cssName)
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, css); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", cssName, err)
				}
			})

			fixture := setupStoreWatchExternalSecret(ctx, testNamespace.Name, "watch-spec-css-es", &api.SecretStoreRef{
				Name: cssName,
				Kind: ResourceClusterSecretStore,
			})

			By("staging a new remote value that only a fresh sync can observe")
			fixture.stageRemoteVersion(ctx, "v2", "store-watch-css-spec-v2")

			By("changing a side-effect-free ClusterSecretStore spec field (RAM role session name)")
			setClusterSecretStoreSessionName(ctx, cssName, "e2e-watch-css-spec-"+getRandString())

			By("observing SynchronizationTime advance within the short watch window")
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, fixture.esName, fixture.baseline,
				storeWatchTriggerWindow, "the ClusterSecretStore spec change")
			fixture.waitForSecretValue(ctx, testNamespace.Name, "store-watch-css-spec-v2", time.Second*10)
		})

		It("Should not resync when the ClusterSecretStore trigger annotation is set to an empty string", func() {
			cssName := "watch-clear-css-" + getRandString()
			css := createRRSAClusterSecretStore(ctx, cssName)
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, css); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", cssName, err)
				}
			})

			fixture := setupStoreWatchExternalSecret(ctx, testNamespace.Name, "watch-clear-css-es", &api.SecretStoreRef{
				Name: cssName,
				Kind: ResourceClusterSecretStore,
			})

			// Positive leg: a non-empty annotation triggers a resync and
			// establishes a fresh baseline for the negative leg.
			genBefore := recordClusterSecretStoreClientGeneration(ctx, cssName)
			fixture.stageRemoteVersion(ctx, "v2", "store-watch-css-clear-v2")
			setClusterSecretStoreTriggerAnnotation(ctx, cssName, strconv.FormatInt(time.Now().UnixNano(), 10))
			Eventually(func() bool {
				store := &api.ClusterSecretStore{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: cssName}, store); err != nil {
					return false
				}
				_, present := store.Annotations[storeWatchTriggerAnnotation]
				return !present
			}).WithTimeout(storeWatchTriggerWindow).WithPolling(time.Second*2).Should(BeTrue(),
				"store controller should clear the trigger annotation on ClusterSecretStore %s after processing", cssName)

			// Ordering contract (synchronous, non-retrying read): the bump is
			// persisted before the annotation removal, so it must already be
			// visible the instant the cleared annotation is observed.
			expectClusterSecretStoreClientGenerationAlreadyAdvanced(ctx, cssName, genBefore,
				"the non-empty trigger annotation")

			By("observing the ClusterSecretStore controller bump Status.ClientGeneration for the rebuilt client")
			// Strictly-increasing comparison (never an exact value) keeps the
			// assertion race-free against concurrent rebuilds.
			expectClusterSecretStoreClientGenerationAdvanced(ctx, cssName, genBefore,
				"the non-empty trigger annotation")
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, fixture.esName, fixture.baseline,
				storeWatchTriggerWindow, "the non-empty trigger annotation")
			fixture.waitForSecretValue(ctx, testNamespace.Name, "store-watch-css-clear-v2", time.Second*10)
			baselineAfterTrigger := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, fixture.esName)

			// Negative leg: empty-string annotation must not trigger.
			fixture.stageRemoteVersion(ctx, "v3", "store-watch-css-clear-v3")
			setClusterSecretStoreTriggerAnnotation(ctx, cssName, "")

			// Shared helper keeps the quiet window and the frozen-state check
			// identical to every other negative assertion (no duplicated
			// Consistently magic numbers).
			expectNoResync(ctx, testNamespace.Name, fixture, "store-watch-css-clear-v2", baselineAfterTrigger,
				"the empty trigger annotation on the ClusterSecretStore")
		})

		It("Should turn Failed immediately when the referenced ClusterSecretStore is deleted and recover when recreated", func() {
			cssName := "watch-delete-css-" + getRandString()
			css := createRRSAClusterSecretStore(ctx, cssName)

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "watch-delete-css-es",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: storeWatchLongInterval},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "watch-delete-css-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name: cssName,
								Kind: ResourceClusterSecretStore,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})
			DeferCleanup(func() {
				// Idempotent: the store may already have been recreated/deleted
				// by the spec body.
				if err := k8sClient.Delete(ctx, css); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", cssName, err)
				}
			})
			validateExternalSecretSucceededAndSecretCreated(ctx, testNamespace.Name, externalSecret.Name, storeWatchTriggerWindow)

			By("deleting the referenced ClusterSecretStore")
			Expect(k8sClient.Delete(ctx, css)).To(Succeed())

			By("observing the ExternalSecret fail fast with the controller's store-lookup error")
			expectExternalSecretFailedWith(ctx, testNamespace.Name, externalSecret.Name,
				"failed to get ClusterSecretStore", storeWatchTriggerWindow)

			By("recreating the ClusterSecretStore with the same name")
			createRRSAClusterSecretStore(ctx, cssName)

			By("observing the ExternalSecret recover without any polling help")
			validateExternalSecretSucceededAndSecretCreated(ctx, testNamespace.Name, externalSecret.Name, time.Second*90)
		})
	})

	Context("OOS SecretStore lifecycle", func() {
		It("Should resync immediately when the OOS SecretStore spec changes", func() {
			if RAMRoleArnForRRSA == "" || OIDCProviderARN == "" {
				Skip("RRSA not configured (RAMRoleArnForRRSA/OIDCProviderARN empty); the OOS RRSA SecretStore lifecycle cannot be exercised")
			}

			By("creating a dedicated OOS parameter whose value can be staged")
			oosParamName, err := GlobalResourceManager.CreateOOSSecretParameterForUpdate(ctx, "oos-store-watch-v1")
			Expect(err).NotTo(HaveOccurred(), "failed to create dedicated OOS parameter for store watch spec")
			DeferCleanup(func() {
				if err := GlobalResourceManager.DeleteOOSSecretParameter(oosParamName); err != nil {
					GinkgoWriter.Printf("WARNING: failed to delete dedicated OOS parameter %s: %v\n", oosParamName, err)
				}
			})

			store := createRRSAOOSSecretStore(ctx, testNamespace.Name, "watch-oos-store-"+getRandString())
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, store); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete OOS SecretStore %s: %v\n", store.Name, err)
				}
			})

			fixture := setupOOSStoreWatchFixture(ctx, testNamespace.Name, "watch-oos-spec-es", store.Name, oosParamName)

			By("staging a new remote OOS parameter value that only a fresh sync can observe")
			Expect(GlobalResourceManager.PutOOSParameterValue(ctx, oosParamName, "oos-store-watch-v2")).To(Succeed(),
				"failed to stage the new OOS parameter value")

			By("changing a side-effect-free OOS store spec field (RAM role session name)")
			setOOSSecretStoreSessionName(ctx, testNamespace.Name, store.Name, "e2e-watch-oos-spec-"+getRandString())

			By("observing SynchronizationTime advance within the short watch window")
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, fixture.esName, fixture.baseline,
				storeWatchTriggerWindow, "the OOS SecretStore spec change")
			fixture.waitForSecretValue(ctx, testNamespace.Name, "oos-store-watch-v2", time.Second*30)
		})

		It("Should trigger an immediate resync via the trigger annotation on an OOS SecretStore and stay frozen after clearing", func() {
			if RAMRoleArnForRRSA == "" || OIDCProviderARN == "" {
				Skip("RRSA not configured (RAMRoleArnForRRSA/OIDCProviderARN empty); the OOS RRSA SecretStore lifecycle cannot be exercised")
			}

			By("creating a dedicated OOS parameter whose value can be staged")
			oosParamName, err := GlobalResourceManager.CreateOOSSecretParameterForUpdate(ctx, "oos-store-watch-trigger-v1")
			Expect(err).NotTo(HaveOccurred(), "failed to create dedicated OOS parameter for store watch spec")
			DeferCleanup(func() {
				if err := GlobalResourceManager.DeleteOOSSecretParameter(oosParamName); err != nil {
					GinkgoWriter.Printf("WARNING: failed to delete dedicated OOS parameter %s: %v\n", oosParamName, err)
				}
			})

			store := createRRSAOOSSecretStore(ctx, testNamespace.Name, "watch-oos-trigger-store-"+getRandString())
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, store); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete OOS SecretStore %s: %v\n", store.Name, err)
				}
			})

			fixture := setupOOSStoreWatchFixture(ctx, testNamespace.Name, "watch-oos-trigger-es", store.Name, oosParamName)
			genBefore := recordStoreClientGeneration(ctx, testNamespace.Name, store.Name)

			By("staging a new remote OOS parameter value that only a fresh sync can observe")
			Expect(GlobalResourceManager.PutOOSParameterValue(ctx, oosParamName, "oos-store-watch-trigger-v2")).To(Succeed(),
				"failed to stage the new OOS parameter value")

			By("setting the trigger annotation to a fresh UnixNano value")
			setSecretStoreTriggerAnnotation(ctx, testNamespace.Name, store.Name, strconv.FormatInt(time.Now().UnixNano(), 10))

			By("observing the store controller clear the annotation after processing")
			waitForSecretStoreTriggerAnnotationCleared(ctx, testNamespace.Name, store.Name)

			// Ordering contract (synchronous, non-retrying read): the bump is
			// persisted before the annotation removal, so it must already be
			// visible the instant the cleared annotation is observed.
			expectStoreClientGenerationAlreadyAdvanced(ctx, testNamespace.Name, store.Name, genBefore,
				"the non-empty trigger annotation on the OOS SecretStore")

			By("observing the OOS store controller bump Status.ClientGeneration for the rebuilt client")
			expectStoreClientGenerationAdvanced(ctx, testNamespace.Name, store.Name, genBefore,
				"the non-empty trigger annotation on the OOS SecretStore")

			By("observing the ExternalSecret resync within the short watch window")
			waitForExternalSecretSyncTimeAdvance(ctx, testNamespace.Name, fixture.esName, fixture.baseline,
				storeWatchTriggerWindow, "the non-empty trigger annotation on the OOS SecretStore")
			fixture.waitForSecretValue(ctx, testNamespace.Name, "oos-store-watch-trigger-v2", time.Second*30)

			// The store controller cleared the annotation after processing;
			// the clearing itself must not trigger any further resync. Stage
			// a new remote value so a spurious resync would be observable.
			baselineAfterClear := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, fixture.esName)
			Expect(GlobalResourceManager.PutOOSParameterValue(ctx, oosParamName, "oos-store-watch-trigger-v3")).To(Succeed(),
				"failed to stage the third OOS parameter value")
			expectNoResync(ctx, testNamespace.Name, fixture, "oos-store-watch-trigger-v2", baselineAfterClear,
				"the cleared trigger annotation on the OOS SecretStore")
		})
	})
})
