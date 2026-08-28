// polling_flags_test.go - Polling flag semantics E2E tests.
//
// Covers the controller-manager polling flags introduced alongside the
// leader-election migration:
//
//   - --polling-interval: global rotation default; a per-ExternalSecret
//     spec.rotationInterval must override it.
//   - --disable-polling: turns the controller fully event-driven; remote
//     value changes must NOT be picked up by any timer, while the store
//     watch still triggers immediate resyncs.
//
// All specs restart the ack-secret-manager Deployment (args patch), so they
// live in an Ordered container. Each polling spec needs a distinct args
// state, but patchDeploymentArgs removes every polling flag before appending
// the new one, so each spec patches directly from the state the previous
// spec left behind; the args baseline is captured once (BeforeAll) and
// restored once (DeferCleanup) at the end of the container. That costs 8
// controller restarts in total (3 chained polling-spec patches + 2
// rate-limit patches + 2 rate-limit restores + 1 final baseline restore)
// instead of per-spec patch + restore for every spec.
package e2e

import (
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const (
	// pollingFreezeWindow is the shared negative observation window of the
	// freeze assertions below ("no timer may sync the staged remote change").
	// The fastest timer any freeze scenario can have is the 15s global
	// polling interval, so 60s still covers 4 full periods of it (and 12
	// --reconcile-period=5s requeues); a broken pin or a spurious resync
	// would surface within one period, so the extra margin only buys
	// resilience against slow status writes. Fixed Consistently cost is
	// thereby reduced conservatively from 90s to 60s without weakening the
	// discrimination between "timer fired" and "no timer exists".
	// Boundary assumption: the discrimination holds only if a single
	// end-to-end sync (KMS fetch + Secret write + status update) completes
	// in < 45s, so one timer period fits inside the window with margin; this
	// precondition is implicitly guaranteed by the 90s baseline-sync wait in
	// the suite setup. If CI shows sporadic false passes, investigate KMS
	// latency first instead of widening this window.
	pollingFreezeWindow = 60 * time.Second
)

var _ = Describe("Polling Flags E2E", Ordered, func() {
	var (
		testNamespace    *corev1.Namespace
		originalDepArgs  []string
		pollingKmsSecret string
		pollingStore     *api.SecretStore
		pollingESName    string
	)

	const pollingDataKey = "polling-data-key"

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-polling-flags-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	// The three specs chain their patches (each patch removes all polling
	// flags before appending its own), so the baseline only needs to be
	// captured and restored once per Ordered container. This eliminates the
	// two intermediate restore-to-baseline rollouts of the previous
	// per-spec patch/restore design (saves 2 full controller restarts,
	// each = rollout wait + leader-election lease re-verification).
	BeforeAll(func() {
		originalDepArgs = getDeploymentArgs(ctx)
		DeferCleanup(func() {
			By("restoring original Deployment args (polling flags baseline)")
			restoreDeploymentArgs(ctx, originalDepArgs)
		})
	})

	// newPollingExternalSecret creates a dedicated remote KMS secret and an
	// ExternalSecret that always fetches its latest version (no VersionId),
	// so any later staged remote version becomes observable through the
	// synced Secret value. Cleanup is registered via DeferCleanup (LIFO: the
	// ExternalSecret and the synced Secret are reclaimed before the remote
	// KMS secret is deleted, which in turn happens before the Deployment
	// args baseline is restored).
	newPollingExternalSecret := func(esName string, rotationInterval *metav1.Duration) {
		var err error
		pollingKmsSecret, err = GlobalResourceManager.CreateKMSSecretForCredentialUpdate(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to create dedicated KMS secret for polling spec")
		DeferCleanup(func() {
			if err := GlobalResourceManager.DeleteKMSSecret(pollingKmsSecret); err != nil {
				GinkgoWriter.Printf("WARNING: failed to delete dedicated KMS secret %s: %v\n", pollingKmsSecret, err)
			}
		})

		pollingStore = createRRSASecretStore(ctx, testNamespace.Name, "polling-store-"+getRandString())
		pollingESName = esName

		externalSecret := &api.ExternalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      esName,
				Namespace: testNamespace.Name,
			},
			Spec: api.ExternalSecretSpec{
				Provider:         "kms",
				RotationInterval: rotationInterval,
				Data: []api.DataSource{
					{
						Key:  pollingKmsSecret,
						Name: pollingDataKey,
						SecretStoreRef: &api.SecretStoreRef{
							Name:      pollingStore.Name,
							Namespace: pollingStore.Namespace,
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

		validateExternalSecretSucceededAndSecretCreated(ctx, testNamespace.Name, esName, time.Second*90)
	}

	secretValueIs := func(expected string) bool {
		secret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, secret); err != nil {
			return false
		}
		return string(secret.Data[pollingDataKey]) == expected
	}

	It("Should honor spec.rotationInterval over a long global --polling-interval", func() {
		// Baseline capture and restore are shared by the whole Ordered
		// container (see BeforeAll); this patch chains from the current state.
		By("patching Deployment to --polling-interval=10m")
		patchDeploymentArgs(ctx, []string{"--polling-interval", "--disable-polling"}, []string{"--polling-interval=10m"})

		By("creating an ExternalSecret with a 15s rotationInterval")
		newPollingExternalSecret("polling-override-es", &metav1.Duration{Duration: 15 * time.Second})

		By("staging a new remote value that only a fresh sync can observe")
		Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, pollingKmsSecret, "v2", "polling-override-v2")).To(Succeed())

		// The 10m global interval can never fire inside this window, so an
		// observed update is proof that the per-secret rotationInterval won.
		// 60s covers 4 cycles of the 15s per-secret rotation; a healthy
		// override syncs within one cycle of the staged change, so the margin
		// only guards slow syncs (shortened from 90s = 6 cycles).
		Eventually(func() bool {
			return secretValueIs("polling-override-v2")
		}).WithTimeout(60*time.Second).WithPolling(5*time.Second).Should(BeTrue(),
			"the 15s per-secret rotationInterval should override the 10m global polling interval")
	})

	It("Should become purely event-driven under --disable-polling", func() {
		// Baseline capture and restore are shared by the whole Ordered
		// container (see BeforeAll); this patch chains from spec1's state.
		By("patching Deployment to --disable-polling=true")
		patchDeploymentArgs(ctx, []string{"--polling-interval", "--disable-polling"}, []string{"--disable-polling=true"})

		By("creating an ExternalSecret without an explicit rotationInterval")
		newPollingExternalSecret("polling-disabled-es", nil)

		By("staging a new remote value that only a fresh sync can observe")
		baseline := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, pollingESName)
		Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, pollingKmsSecret, "v2", "polling-disabled-v2")).To(Succeed())

		// With polling disabled the controller installs no rotation timer at
		// all; the only remaining resync paths are event-driven (ExternalSecret
		// or store changes) and none of those happens inside this window, so
		// the synced value and SynchronizationTime must stay frozen. The
		// shared freeze window (60s, see pollingFreezeWindow) spans 12
		// --reconcile-period=5s requeues, which is ample margin to catch any
		// spuriously scheduled sync while keeping the fixed Consistently cost
		// low (conservatively shortened from 90s).
		Consistently(func() bool {
			// Transient API errors are not evidence of a resync.
			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, secret); err != nil {
				return true
			}
			if string(secret.Data[pollingDataKey]) != CredentialUpdateKMSSecretInitialValue {
				return false
			}
			es := &api.ExternalSecret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, es); err != nil {
				return true
			}
			return !latestSyncTimeOf(es).Time.After(baseline.Time)
		}).WithTimeout(pollingFreezeWindow).WithPolling(10*time.Second).Should(BeTrue(),
			"with --disable-polling no timer may sync the staged remote change")

		By("proving the store watch still triggers an immediate event-driven resync")
		setSecretStoreSessionName(ctx, testNamespace.Name, pollingStore.Name, "e2e-polling-disabled-"+getRandString())
		Eventually(func() bool {
			return secretValueIs("polling-disabled-v2")
		}).WithTimeout(60*time.Second).WithPolling(5*time.Second).Should(BeTrue(),
			"a store spec change must still resync immediately in event-driven mode")
	})

	// Spec2's event-driven leg used a store SPEC change; this spec covers the
	// other immediate event path under --disable-polling: the trigger
	// annotation. Its fan-out enqueue reaches the ExternalSecret controller
	// while the store controller may still be rebuilding the client; the
	// freshness guard (ensureStoreClientFresh) then rejects the stale client
	// and retries after a short backoff which -- unlike rotation timers --
	// must keep firing with polling disabled.
	It("Should resync via trigger-annotation fan-out with freshness-guard retry under --disable-polling", func() {
		// Spec2 left the controller at --disable-polling=true; the idempotent
		// patch self-checks the current args and skips the Update/rollout when
		// they already satisfy the desired state, so no extra restart happens.
		By("self-checking that --disable-polling=true is active (idempotent patch)")
		patchDeploymentArgs(ctx, []string{"--polling-interval", "--disable-polling"}, []string{"--disable-polling=true"})

		By("creating an ExternalSecret with a 30m rotationInterval")
		newPollingExternalSecret("polling-trigger-es", &metav1.Duration{Duration: 30 * time.Minute})
		baseline := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, pollingESName)
		genBefore := recordStoreClientGeneration(ctx, testNamespace.Name, pollingStore.Name)

		By("staging a new remote value that only a fresh sync can observe")
		Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, pollingKmsSecret, "v2", "polling-trigger-v2")).To(Succeed())

		// Negative leg: with polling disabled no rotation timer exists and no
		// store/ES event happens inside this window, so the synced value and
		// SynchronizationTime must stay frozen (same pattern as spec2).
		Consistently(func() bool {
			// Transient API errors are not evidence of a resync.
			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, secret); err != nil {
				return true
			}
			if string(secret.Data[pollingDataKey]) != CredentialUpdateKMSSecretInitialValue {
				return false
			}
			es := &api.ExternalSecret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, es); err != nil {
				return true
			}
			return !latestSyncTimeOf(es).Time.After(baseline.Time)
		}).WithTimeout(pollingFreezeWindow).WithPolling(10*time.Second).Should(BeTrue(),
			"with --disable-polling no timer may sync the staged remote change before the trigger annotation is set")

		By("triggering a client rebuild via the store trigger annotation")
		setSecretStoreTriggerAnnotation(ctx, testNamespace.Name, pollingStore.Name, strconv.FormatInt(time.Now().UnixNano(), 10))

		// 60s is orders of magnitude below the 30m rotationInterval, so an
		// observed resync can only come from the annotation fan-out (retried
		// by the freshness guard when it races the client rebuild) -- never
		// from polling. The wider window (vs the prior 30s) absorbs the
		// controller startup latency after the preceding Deployment rollout.
		Eventually(func() bool {
			if !secretValueIs("polling-trigger-v2") {
				return false
			}
			es := &api.ExternalSecret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, es); err != nil {
				return false
			}
			return latestSyncTimeOf(es).Time.After(baseline.Time)
		}).WithTimeout(60*time.Second).WithPolling(2*time.Second).Should(BeTrue(),
			"the trigger annotation fan-out must sync the staged value and advance SynchronizationTime within 60s under --disable-polling")

		By("observing the store controller bump Status.ClientGeneration for the rebuilt client")
		expectStoreClientGenerationAdvanced(ctx, testNamespace.Name, pollingStore.Name, genBefore,
			"the trigger annotation under --disable-polling")
	})

	// Reverse direction of spec1: while spec1 proves a SHORT per-secret
	// rotationInterval overrides a long global interval, this spec proves a
	// LONG per-secret rotationInterval wins over a short global interval --
	// rotationInterval is authoritative in both directions, so no global
	// polling timer may fire for the pinned ExternalSecret.
	It("Should honor a long spec.rotationInterval over a short global --polling-interval", func() {
		// Baseline capture and restore are shared by the whole Ordered
		// container (see BeforeAll); this patch chains from spec2's state.
		By("patching Deployment to --polling-interval=15s")
		patchDeploymentArgs(ctx, []string{"--polling-interval", "--disable-polling"}, []string{"--polling-interval=15s"})

		By("creating an ExternalSecret with a 30m rotationInterval")
		newPollingExternalSecret("polling-pin-es", &metav1.Duration{Duration: 30 * time.Minute})
		baseline := recordExternalSecretSyncBaseline(ctx, testNamespace.Name, pollingESName)

		By("staging a new remote value that only a fresh sync can observe")
		Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, pollingKmsSecret, "v2", "polling-pin-v2")).To(Succeed())

		// The 15s global polling interval fires several times inside this
		// window; the pinned 30m per-secret rotationInterval must suppress it,
		// keeping both the synced value and SynchronizationTime frozen. The
		// shared freeze window (60s, see pollingFreezeWindow) covers 4 cycles
		// of the 15s global interval; a broken pin would sync within one
		// cycle, so 4 cycles keep full discrimination while the fixed
		// Consistently cost drops from 90s (6 cycles).
		Consistently(func() bool {
			// Transient API errors are not evidence of a resync.
			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, secret); err != nil {
				return true
			}
			if string(secret.Data[pollingDataKey]) != CredentialUpdateKMSSecretInitialValue {
				return false
			}
			es := &api.ExternalSecret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace.Name, Name: pollingESName}, es); err != nil {
				return true
			}
			return !latestSyncTimeOf(es).Time.After(baseline.Time)
		}).WithTimeout(pollingFreezeWindow).WithPolling(10*time.Second).Should(BeTrue(),
			"the 30m per-secret rotationInterval should override the 15s global polling interval")
	})

	Context("Secret pull rate limit flags", func() {
		// newRateLimitExternalSecret creates an RRSA store plus a 30m-rotation
		// ExternalSecret with four data keys (all pulling the shared KMS
		// secret's pinned v1, so no remote staging is needed). Every key must
		// sync successfully while the pull rate limit flag is active, proving
		// the flag is accepted by the manager and the pull path stays
		// functional. The long rotationInterval keeps any active global
		// polling timer out of the assertion.
		newRateLimitExternalSecret := func(esName string) *api.ExternalSecret {
			store := createRRSASecretStore(ctx, testNamespace.Name, "pull-limit-store-"+getRandString())

			data := make([]api.DataSource, 0, 4)
			for i := 1; i <= 4; i++ {
				data = append(data, api.DataSource{
					Key:       CommonKMSSecretName,
					Name:      fmt.Sprintf("pull-limit-key-%d", i),
					VersionId: "v1",
					SecretStoreRef: &api.SecretStoreRef{
						Name:      store.Name,
						Namespace: testNamespace.Name,
						Kind:      ResourceSecretStore,
					},
				})
			}

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 30 * time.Minute},
					Data:             data,
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})
			return externalSecret
		}

		// expectAllRateLimitKeysSynced asserts the flag is accepted and all
		// four data keys sync with the shared KMS secret's value. It
		// deliberately contains NO duration floor: KmsLimiter.Wait is called
		// once per reconcile round and a single token covers every data key
		// of one ExternalSecret (rate.NewLimiter(rate.Limit(1),1) releases
		// its first token immediately), so a multi-key serial-latency floor
		// could never discriminate at the e2e layer. Rate-limit throughput
		// semantics are covered by unit tests instead; here we only verify
		// flag acceptance plus functional sync.
		expectAllRateLimitKeysSynced := func(externalSecret *api.ExternalSecret) {
			validateExternalSecretSucceededAndSecretCreated(ctx, testNamespace.Name, externalSecret.Name, time.Second*90)
			expected := make(map[string]string, 4)
			for i := 1; i <= 4; i++ {
				expected[fmt.Sprintf("pull-limit-key-%d", i)] = CommonKMSSecretValue
			}
			validateParsedSecretContent(ctx, externalSecret, expected)
		}

		It("Should sync all data keys under --max-concurrent-kms-secret-pulls=1", func() {
			// Register the restore BEFORE mutating (suite convention): a
			// mid-spec failure still returns the args to their pre-spec state.
			originalArgs := getDeploymentArgs(ctx)
			DeferCleanup(func() {
				By("restoring Deployment args after the --max-concurrent-kms-secret-pulls spec")
				restoreDeploymentArgs(ctx, originalArgs)
			})

			By("patching Deployment to --max-concurrent-kms-secret-pulls=1")
			patchDeploymentArgs(ctx,
				[]string{"--max-concurrent-kms-secret-pulls", "--max-concurrent-secret-pulls"},
				[]string{"--max-concurrent-kms-secret-pulls=1"})

			By("creating an ExternalSecret with 4 data keys under the kms pull rate limit")
			externalSecret := newRateLimitExternalSecret("pull-limit-kms-es")
			expectAllRateLimitKeysSynced(externalSecret)
		})

		It("Should sync all data keys under the deprecated --max-concurrent-secret-pulls=1", func() {
			originalArgs := getDeploymentArgs(ctx)
			DeferCleanup(func() {
				By("restoring Deployment args after the deprecated --max-concurrent-secret-pulls spec")
				restoreDeploymentArgs(ctx, originalArgs)
			})

			By("patching Deployment to the deprecated --max-concurrent-secret-pulls=1")
			// cmd/manager/main.go: the deprecated flag only takes effect when
			// --max-concurrent-kms-secret-pulls is not set, so the kms flag must
			// be removed for the deprecated value to control the kms rate limit.
			patchDeploymentArgs(ctx,
				[]string{"--max-concurrent-kms-secret-pulls", "--max-concurrent-secret-pulls"},
				[]string{"--max-concurrent-secret-pulls=1"})

			By("creating an ExternalSecret with 4 data keys under the deprecated pull rate limit")
			externalSecret := newRateLimitExternalSecret("pull-limit-deprecated-es")
			expectAllRateLimitKeysSynced(externalSecret)
		})
	})
})
