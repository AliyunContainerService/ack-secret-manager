// cluster_store_flag_test.go - --process-cluster-secret-store flag E2E tests.
//
// Covers the flag gating of the ClusterSecretStore processing introduced with
// the Secret/ServiceAccount controller rewrite:
//
//   - flag enabled (default true): a credential Secret change on a
//     ClusterSecretStore must patch the trigger annotation and rebuild the
//     client, recovering referencing ExternalSecrets (positive baseline).
//   - flag disabled: the ClusterSecretStore controller is not started AND the
//     Secret/ServiceAccount reconcilers skip their ClusterSecretStore scans
//     (secret_controller.go / serviceaccount_controller.go), so fixing the
//     credential Secret must NOT recover the ExternalSecret.
//
// Restart budget: exactly two controller restarts for the whole Ordered
// container -- one patchDeploymentArgs (negative leg) and one
// restoreDeploymentArgs (recovery leg). The args baseline is captured once in
// BeforeAll and a DeferCleanup registered there guarantees restoration even
// when a spec fails midway; restoreDeploymentArgs is idempotent, so the
// cleanup is a no-op when the recovery leg already restored the baseline.
package e2e

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const (
	// cssFlagArgPrefix is the exact flag name defined in cmd/manager/main.go
	// (flag.BoolVar(&processClusterSecretStore, "process-cluster-secret-store", true, ...)).
	cssFlagArgPrefix = "--process-cluster-secret-store"

	// cssFlagQuietWindow is the negative observation window for the
	// flag-disabled leg: far beyond any --reconcile-period=5s requeue chain,
	// far below the 30m rotationInterval, so neither a timer nor a requeue
	// storm can mask a genuine trigger-annotation rebuild.
	cssFlagQuietWindow = 45 * time.Second
)

var _ = Describe("Process ClusterSecretStore Flag E2E", Ordered, func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
		originalArgs  []string

		// Shared degraded-mode fixture spanning the disabled leg (spec 2) and
		// the restored leg (spec 3): while the flag is false the successfully
		// syncing ExternalSecret must expose the
		// cluster_secret_store_controller_disabled notice; after the restore
		// the notice must converge away. Cleanup is owned by BeforeAll so the
		// fixture outlives the spec that created it.
		degradedNamespace *corev1.Namespace
		degradedCSS       *api.ClusterSecretStore
		degradedES        *api.ExternalSecret
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-css-flag-"+getRandString())
	})

	AfterEach(func() {
		// Safety net: deletes every remaining namespaced resource (AK Secret,
		// ExternalSecret, synced Secret). The cluster-scoped CSSes are
		// removed by the per-spec DeferCleanup registrations, which run
		// BEFORE this AfterEach (LIFO inside the spec first, then the
		// AfterEach namespace deletion).
		deleteTestNamespace(ctx, testNamespace)
	})

	BeforeAll(func() {
		// Capture the baseline BEFORE registering the restore so the cleanup
		// never depends on a (potentially failing) patch call.
		originalArgs = getDeploymentArgs(ctx)
		DeferCleanup(func() {
			By("restoring the Deployment args baseline (--process-cluster-secret-store)")
			// Escalates to Fail on a double-retry failure: leaking the
			// patched args would silently disable ClusterSecretStore
			// processing for every subsequent suite spec.
			restoreDeploymentArgs(ctx, originalArgs)
		})
		// Registered after the args restore, so LIFO order reclaims the
		// shared degraded fixture FIRST and restores the args second. Every
		// step is nil-guarded: a spec-2 failure mid-way may leave the
		// fixture only partially created.
		DeferCleanup(func() {
			By("cleaning up the shared CSS-degraded fixture")
			if degradedES != nil {
				CleanupExternalSecretAndSyncedSecret(ctx, degradedES)
			}
			if degradedCSS != nil {
				if err := k8sClient.Delete(ctx, degradedCSS); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", degradedCSS.Name, err)
				}
			}
			if degradedNamespace != nil {
				deleteTestNamespace(ctx, degradedNamespace)
			}
		})
	})

	// newCSSWithAKSecret creates an AK Secret (invalid credentials by default)
	// plus a ClusterSecretStore referencing it. Cleanup order requirement
	// (ES/synced Secret -> store -> namespace) is satisfied by the callers:
	// the CSS DeferCleanup is registered here FIRST (runs LAST among the
	// spec's cleanups, but still before the AfterEach namespace deletion).
	newCSSWithAKSecret := func(cssName, secretName, accessKeyID, accessKeySecret string) (*corev1.Secret, *api.ClusterSecretStore) {
		secret := createAKSecret(ctx, testNamespace.Name, secretName, accessKeyID, accessKeySecret)

		css := &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name: cssName,
			},
			Spec: api.ClusterSecretStoreSpec{
				KMS: &api.KMSProvider{
					KMS: &api.KMSAuth{
						AccessKey: &api.SecretRef{
							Name:      secret.Name,
							Namespace: testNamespace.Name,
							Key:       "accessKeyId",
						},
						AccessKeySecret: &api.SecretRef{
							Name:      secret.Name,
							Namespace: testNamespace.Name,
							Key:       "accessKeySecret",
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, css)).To(Succeed())
		DeferCleanup(func() {
			if err := k8sClient.Delete(ctx, css); err != nil && !k8serrors.IsNotFound(err) {
				GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v; "+
					"a leaked cluster-scoped store keeps participating in reference scans, inspect it manually\n", cssName, err)
			}
		})
		return secret, css
	}

	// newLongRotationESForCSS creates an ExternalSecret pinned to a 30m
	// rotationInterval referencing the given CSS, so every observed state
	// change inside the short assertion windows can only come from an event
	// trigger (Secret watch -> trigger annotation -> client rebuild), never
	// from the rotation poll.
	// No VersionId: always fetch the latest KMS version so that a staged
	// remote value becomes observable on the next sync (fingerprint debounce
	// requires a data change for SynchronizationTime to advance).
	newLongRotationESForCSS := func(esName, cssName, kmsSecretName string) *api.ExternalSecret {
		externalSecret := &api.ExternalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      esName,
				Namespace: testNamespace.Name,
			},
			Spec: api.ExternalSecretSpec{
				Provider:         "kms",
				RotationInterval: &metav1.Duration{Duration: 30 * time.Minute},
				Data: []api.DataSource{
					{
						Key:  kmsSecretName,
						Name: "test-secret-key",
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
		return externalSecret
	}

	// updateAKSecretToValidCredentials rewrites the AK Secret with the suite's
	// valid RAM user credentials (same source as the SecretStore variant in
	// reconcile_test.go).
	updateAKSecretToValidCredentials := func(secret *corev1.Secret) {
		Eventually(func() error {
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: secret.Name, Namespace: secret.Namespace,
			}, secret); err != nil {
				return err
			}
			secret.Data["accessKeyId"] = []byte(RAMUserAccessKeyID)
			secret.Data["accessKeySecret"] = []byte(RAMUserAccessKeySecret)
			return k8sClient.Update(ctx, secret)
		}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
			"should update the AK Secret with valid credentials")
	}

	It("Should recover the ExternalSecret when the CSS credential Secret is fixed with the flag at its default (enabled)", func() {
		By("creating a ClusterSecretStore with invalid AK credentials")
		secret, css := newCSSWithAKSecret("flag-baseline-css-"+getRandString(),
			"flag-baseline-aksk-"+getRandString(), "InvalidAccessKeyId", "InvalidAccessKeySecret")
		waitForClusterSecretStoreReady(ctx, css.Name)

		By("creating a long-rotation ExternalSecret referencing the CSS")
		externalSecret := newLongRotationESForCSS("flag-baseline-es-"+getRandString(), css.Name, CommonKMSSecretName)

		By("waiting for the ExternalSecret to fail because of the invalid credentials")
		expectExternalSecretAllResultsFailed(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

		By("fixing the credential Secret and observing the watch-triggered recovery")
		updateAKSecretToValidCredentials(secret)
		validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)
	})

	It("Should NOT recover the ExternalSecret after a CSS credential fix while --process-cluster-secret-store=false", func() {
		By("patching --process-cluster-secret-store=false onto the controller Deployment")
		// The restore is owned by the container (BeforeAll DeferCleanup); the
		// recovery leg below performs the explicit in-body restore so the
		// baseline is re-verified BEFORE any post-restore assertion runs.
		patchDeploymentArgs(ctx, []string{cssFlagArgPrefix}, []string{cssFlagArgPrefix + "=false"})

		By("creating a ClusterSecretStore with invalid AK credentials")
		secret, css := newCSSWithAKSecret("flag-disabled-css-"+getRandString(),
			"flag-disabled-aksk-"+getRandString(), "InvalidAccessKeyId", "InvalidAccessKeySecret")

		By("creating a long-rotation ExternalSecret referencing the CSS")
		externalSecret := newLongRotationESForCSS("flag-disabled-es-"+getRandString(), css.Name, CommonKMSSecretName)

		By("waiting for the ExternalSecret to settle into the Failed state")
		expectExternalSecretAllResultsFailed(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)
		// The Failed round wrote status, so a non-zero SynchronizationTime
		// baseline exists; any later trigger would advance it.
		baseline := recordExternalSecretSyncBaseline(ctx, externalSecret.Namespace, externalSecret.Name)

		// ServiceAccount watcher variant: the SA reconciler must skip its
		// ClusterSecretStore scan as well (serviceaccount_controller.go), so
		// fixing the SA credentials must not recover this second ES either.
		By("creating a CSS backed by an annotation-less ServiceAccount (SA watcher variant)")
		saName := "flag-disabled-sa-" + getRandString()
		roleArn, roleName, err := GlobalResourceManager.CreateRamRoleForServiceAccount(ctx, testNamespace.Name, saName)
		Expect(err).NotTo(HaveOccurred(), "failed to create RAM role for ServiceAccount %s/%s", testNamespace.Name, saName)
		DeferCleanup(func() {
			if err := GlobalResourceManager.DeleteRamRole(roleName); err != nil {
				GinkgoWriter.Printf("WARNING: failed to delete RAM role %s: %v\n", roleName, err)
			}
		})
		// The ServiceAccount itself is namespaced and reclaimed with the
		// spec's test namespace in AfterEach.
		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: testNamespace.Name,
			},
		}
		Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())

		saCSS := &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name: "flag-disabled-sa-css-" + getRandString(),
			},
			Spec: api.ClusterSecretStoreSpec{
				KMS: &api.KMSProvider{
					KMS: &api.KMSAuth{
						ServiceAccountRef: &api.ServiceAccountRef{
							Name:      saName,
							Namespace: testNamespace.Name,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, saCSS)).To(Succeed())
		DeferCleanup(func() {
			if err := k8sClient.Delete(ctx, saCSS); err != nil && !k8serrors.IsNotFound(err) {
				GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", saCSS.Name, err)
			}
		})
		saExternalSecret := newLongRotationESForCSS("flag-disabled-sa-es-"+getRandString(), saCSS.Name, CommonKMSSecretName)

		By("waiting for the SA-variant ExternalSecret to settle into the Failed state")
		expectExternalSecretAllResultsFailed(ctx, saExternalSecret.Namespace, saExternalSecret.Name, time.Second*90)
		saBaseline := recordExternalSecretSyncBaseline(ctx, saExternalSecret.Namespace, saExternalSecret.Name)

		By("fixing BOTH credential paths while the ClusterSecretStore scans are disabled")
		updateAKSecretToValidCredentials(secret)
		Eventually(func() error {
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: saName, Namespace: testNamespace.Name,
			}, serviceAccount); err != nil {
				return err
			}
			if serviceAccount.Annotations == nil {
				serviceAccount.Annotations = make(map[string]string)
			}
			serviceAccount.Annotations[ACKRRSAAnnotation] = roleArn
			return k8sClient.Update(ctx, serviceAccount)
		}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
			"should add the role-arn annotation to the ServiceAccount")

		// Shared negative check for one ExternalSecret: every data key stays
		// Failed, SynchronizationTime stays frozen, and the target Secret is
		// never created. Both variants run it inside ONE shared quiet window
		// (cssFlagQuietWindow) so the fixed Consistently cost does not stack.
		stillFailedAndFrozen := func(es *api.ExternalSecret, baseline metav1.Time) bool {
			current := &api.ExternalSecret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: es.Namespace, Name: es.Name,
			}, current); err != nil {
				// Transient API errors are not evidence of a recovery; keep
				// the window running.
				return true
			}
			if len(current.Status.DataSyncResults) == 0 {
				return false
			}
			for _, result := range current.Status.DataSyncResults {
				if result.ExternalSecretKey == cssControllerDisabledStatusKey {
					// Documented degraded-mode notice (Status=Warning) persisted
					// while the CSS controller is disabled; not a data-key sync
					// verdict, so it does not invalidate the Failed contract.
					continue
				}
				if result.Status != "Failed" {
					return false
				}
			}
			// SynchronizationTime must not advance: status writes are
			// fingerprint-debounced and the 30m rotation rules out the poll,
			// so an advance would prove a spurious trigger path bypassed the
			// flag gate.
			if latestSyncTimeOf(current).After(baseline.Time) {
				return false
			}
			// The target Secret must never be created either.
			syncedSecret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: es.Namespace, Name: es.Name,
			}, syncedSecret); err == nil {
				return false
			}
			return true
		}

		By("observing no recovery across the shared quiet window: both ESes stay Failed with frozen SynchronizationTime")
		Consistently(func() bool {
			return stillFailedAndFrozen(externalSecret, baseline) &&
				stillFailedAndFrozen(saExternalSecret, saBaseline)
		}).WithTimeout(cssFlagQuietWindow).WithPolling(time.Second*5).Should(BeTrue(),
			"with --process-cluster-secret-store=false neither the CSS credential fix nor the SA RRSA annotation fix may trigger a rebuild or recovery")

		// Positive closed loop of the degraded-mode status notice: a CSS that
		// CAN serve the fetch must sync successfully while the controller
		// persists the cluster_secret_store_controller_disabled notice. The
		// fixture survives into the restored leg (spec 3), where the notice
		// must converge away again.
		By("creating a valid RRSA ClusterSecretStore in a dedicated namespace shared with the restored leg")
		degradedNamespace = createTestNamespace(ctx, "test-css-flag-degraded-"+getRandString())
		degradedCSS = &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name: "flag-degraded-css-" + getRandString(),
			},
			Spec: api.ClusterSecretStoreSpec{
				KMS: &api.KMSProvider{
					KMS: &api.KMSAuth{
						RAMRoleARN:      RAMRoleArnForRRSA,
						OIDCProviderARN: OIDCProviderARN,
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, degradedCSS)).To(Succeed())
		// Deliberately NO waitForClusterSecretStoreReady: with the CSS
		// controller disabled no status is ever written and the readiness
		// wait would exhaust its full timeout. The ExternalSecret reconcile
		// builds its own client; the freshness guard treats condition-less
		// stores as unmanaged, so the sync can proceed.

		By("creating a short-rotation ExternalSecret on the valid CSS")
		// Short rotation keeps the fixture ES reconciling: the restored leg
		// relies on a fresh round to drop the notice right after the flag
		// comes back, without any external trigger.
		degradedES = &api.ExternalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "flag-degraded-es-" + getRandString(),
				Namespace: degradedNamespace.Name,
			},
			Spec: api.ExternalSecretSpec{
				Provider:         "kms",
				RotationInterval: &metav1.Duration{Duration: 15 * time.Second},
				Data: []api.DataSource{
					{
						Key:       CommonKMSSecretName,
						Name:      "flag-degraded-secret-key",
						VersionId: "v1",
						SecretStoreRef: &api.SecretStoreRef{
							Name: degradedCSS.Name,
							Kind: ResourceClusterSecretStore,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, degradedES)).To(Succeed())

		By("observing the sync succeed while the degraded-mode notice appears in status.dataSyncResults")
		validateExternalSecretSucceededAndSecretCreated(ctx, degradedES.Namespace, degradedES.Name, time.Second*90)
		expectCSSDisabledNoticePresent(ctx, degradedES.Namespace, degradedES.Name, time.Second*60)
	})

	It("Should restore the baseline: CSS spec changes trigger immediate resyncs again after the flag is re-enabled", func() {
		By("restoring the original Deployment args (re-enabling ClusterSecretStore processing)")
		restoreDeploymentArgs(ctx, originalArgs)

		By("verifying the Deployment args baseline is actually restored")
		currentArgs := getDeploymentArgs(ctx)
		for _, arg := range currentArgs {
			Expect(strings.HasPrefix(arg, cssFlagArgPrefix+"=false")).To(BeFalse(),
				"the Deployment must not carry --process-cluster-secret-store=false after restoration, got args %v", currentArgs)
		}
		Expect(currentArgs).To(Equal(originalArgs),
			"the Deployment args must match the captured baseline element by element")

		// Second half of the degraded-mode notice closed loop: with the CSS
		// controller re-enabled the retention predicate stops holding, so the
		// next reconcile round of the shared fixture ES (15s rotation) drops
		// the notice from status.dataSyncResults.
		if degradedES != nil {
			By("observing the degraded-mode notice converge away on the shared fixture ExternalSecret")
			expectCSSDisabledNoticeAbsent(ctx, degradedES.Namespace, degradedES.Name, time.Second*90)
		} else {
			GinkgoWriter.Printf("WARNING: shared degraded fixture missing (the disabled leg did not complete); skipping the notice-absence assertion\n")
		}

		// First create the store: the ES helper below references it, so the
		// store has to exist before we point the fixture ES at it.
		css := createRRSAClusterSecretStore(ctx, "flag-restored-css-"+getRandString())
		DeferCleanup(func() {
			if err := k8sClient.Delete(ctx, css); err != nil && !k8serrors.IsNotFound(err) {
				GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", css.Name, err)
			}
		})

		// Dedicated KMS secret: the restored leg stages a new version that
		// becomes the "latest" version of whatever secret it writes; writing
		// to the shared CommonKMSSecretName would poison it for unrelated
		// suites/specs (e.g. the endpoint-change test) that read CommonKMSSecretName
		// without a VersionId and assert the exact pre-existing value.
		By("creating a dedicated KMS secret for the restored-flag spec")
		dedicatedKMSName, err := GlobalResourceManager.CreateKMSSecretForCredentialUpdate(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to create dedicated KMS secret for restored-flag spec")
		DeferCleanup(func() {
			if err := GlobalResourceManager.DeleteKMSSecret(dedicatedKMSName); err != nil {
				GinkgoWriter.Printf("WARNING: failed to delete dedicated KMS secret %s: %v\n", dedicatedKMSName, err)
			}
		})

		By("creating a long-rotation ExternalSecret pinned to the dedicated KMS secret")
		externalSecret := newLongRotationESForCSS("flag-restored-es-"+getRandString(), css.Name, dedicatedKMSName)
		validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)
		baseline := recordExternalSecretSyncBaseline(ctx, externalSecret.Namespace, externalSecret.Name)

		By("staging a new remote value that only a fresh sync can observe")
		Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, dedicatedKMSName, "v2", CommonKMSSecretValue+"-restored")).To(Succeed(),
			"failed to stage new remote version for the CSS flag restoration spec")

		By("changing a side-effect-free CSS spec field (RAM role session name)")
		setClusterSecretStoreSessionName(ctx, css.Name, "e2e-css-flag-restored-"+getRandString())

		By("observing SynchronizationTime advance within the short watch window")
		// The 30m rotation rules out the poll: the advance can only come from
		// the re-enabled CSS controller bumping status.clientGeneration and
		// the ES reverse watch picking it up.
		waitForExternalSecretSyncTimeAdvance(ctx, externalSecret.Namespace, externalSecret.Name, baseline,
			60*time.Second, "the ClusterSecretStore spec change after flag restoration")
	})
})

// expectCSSDisabledNoticePresent waits until the ExternalSecret status carries
// the cluster_secret_store_controller_disabled notice with Status=Warning:
// the documented degraded-mode entry persisted while consuming a
// ClusterSecretStore with --process-cluster-secret-store=false.
func expectCSSDisabledNoticePresent(ctx context.Context, namespace, name string, timeout time.Duration) {
	Eventually(func() bool {
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, es); err != nil {
			return false
		}
		for _, result := range es.Status.DataSyncResults {
			if result.ExternalSecretKey == cssControllerDisabledStatusKey {
				return result.Status == "Warning"
			}
		}
		return false
	}).WithTimeout(timeout).WithPolling(time.Second*2).Should(BeTrue(),
		"ExternalSecret %s/%s should expose the %q degraded-mode notice (Status=Warning) while --process-cluster-secret-store=false",
		namespace, name, cssControllerDisabledStatusKey)
}

// expectCSSDisabledNoticeAbsent waits until the degraded-mode notice has
// converged away from the ExternalSecret status after the CSS controller was
// re-enabled: retention stops applying and the next status write drops it.
func expectCSSDisabledNoticeAbsent(ctx context.Context, namespace, name string, timeout time.Duration) {
	Eventually(func() bool {
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, es); err != nil {
			return false
		}
		for _, result := range es.Status.DataSyncResults {
			if result.ExternalSecretKey == cssControllerDisabledStatusKey {
				return false
			}
		}
		return true
	}).WithTimeout(timeout).WithPolling(time.Second*2).Should(BeTrue(),
		"the %q degraded-mode notice of ExternalSecret %s/%s must converge away after --process-cluster-secret-store is restored",
		cssControllerDisabledStatusKey, namespace, name)
}
