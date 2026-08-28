// helpers_test.go - Shared test helpers for the E2E suite.
//
// This file consolidates reusable helper functions that are referenced
// across multiple spec files (store_watch_test.go, reconcile_test.go,
// gateway_test.go, polling_flags_test.go, cluster_store_flag_test.go,
// authentication_test.go, etc.). Centralizing them here improves
// discoverability and avoids the confusion of finding helpers hidden inside
// spec-centric files.
//
// Infrastructure note: this file contains only helper functions and
// constants — no Describe/It blocks. See README.md for the distinction
// between infrastructure files and spec files.
package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const (
	// storeWatchTriggerAnnotation mirrors utils.TriggerReconcileAnnotation
	// (pkg/utils/util.go); the store controller clears it after processing.
	storeWatchTriggerAnnotation = "ack-secret-manager.alibabacloud.com/trigger-reconcile"

	// storeWatchLongInterval pins the ExternalSecret rotation far beyond every
	// assertion window so only the reverse watch can explain a fast refresh.
	storeWatchLongInterval = 30 * time.Minute

	// storeWatchTriggerWindow is the short window within which a
	// watch-triggered resync must become observable.
	storeWatchTriggerWindow = 60 * time.Second

	// storeWatchQuietWindow is the negative observation window for "must NOT
	// trigger" assertions; far smaller than storeWatchLongInterval.
	storeWatchQuietWindow = 45 * time.Second
)

// --------------------------------------------------------------------------
// ExternalSecret status / sync-time helpers
// --------------------------------------------------------------------------

// waitForExternalSecretSucceeded waits until the ExternalSecret's status
// reports at least one Succeeded DataSyncResult and no non-Warning failures.
// Use this BEFORE asserting on the synced Secret data to prevent races with
// the controller's final status write (Fix #3).
func waitForExternalSecretSucceeded(ctx context.Context, namespace, name string, timeout time.Duration) {
	Eventually(func() bool {
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, es); err != nil {
			return false
		}
		if len(es.Status.DataSyncResults) == 0 {
			return false
		}
		hasSucceeded := false
		for _, result := range es.Status.DataSyncResults {
			switch result.Status {
			case "Succeeded":
				hasSucceeded = true
			case "Warning":
				continue
			default:
				return false
			}
		}
		return hasSucceeded
	}).WithTimeout(timeout).WithPolling(5 * time.Second).Should(BeTrue(),
		"ExternalSecret %s/%s should reach Succeeded status", namespace, name)
}

// latestSyncTimeOf returns the newest SynchronizationTime across the
// ExternalSecret's DataSyncResults (zero value when there are none).
func latestSyncTimeOf(es *api.ExternalSecret) metav1.Time {
	var latest metav1.Time
	for i := range es.Status.DataSyncResults {
		if es.Status.DataSyncResults[i].SynchronizationTime.After(latest.Time) {
			latest = es.Status.DataSyncResults[i].SynchronizationTime
		}
	}
	return latest
}

// recordExternalSecretSyncBaseline captures the current newest
// SynchronizationTime of the ExternalSecret as a baseline for advance /
// unchanged assertions. Fails when no non-zero sync time exists.
func recordExternalSecretSyncBaseline(ctx context.Context, namespace, name string) metav1.Time {
	var baseline metav1.Time
	Eventually(func() bool {
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, es); err != nil {
			return false
		}
		latest := latestSyncTimeOf(es)
		if latest.IsZero() {
			return false
		}
		baseline = latest
		return true
	}).WithTimeout(time.Second*30).WithPolling(time.Second*2).Should(BeTrue(),
		"ExternalSecret %s/%s should expose a non-zero SynchronizationTime baseline", namespace, name)
	return baseline
}

// waitForExternalSecretSyncTimeAdvance waits until the newest
// SynchronizationTime is strictly after the baseline, i.e. a fresh sync round
// actually wrote status.
func waitForExternalSecretSyncTimeAdvance(ctx context.Context, namespace, name string, baseline metav1.Time, timeout time.Duration, triggerDesc string) {
	Eventually(func() bool {
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, es); err != nil {
			return false
		}
		return latestSyncTimeOf(es).Time.After(baseline.Time)
	}).WithTimeout(timeout).WithPolling(time.Second*2).Should(BeTrue(),
		fmt.Sprintf("SynchronizationTime of ExternalSecret %s/%s should advance within %s after %s (watch-triggered resync)",
			namespace, name, timeout, triggerDesc))
}

// expectExternalSecretFailedWith waits until every DataSyncResult is Failed
// and its Reason contains the given controller error fragment.
func expectExternalSecretFailedWith(ctx context.Context, namespace, name, reasonFragment string, timeout time.Duration) {
	var lastCheckError string
	Eventually(func() bool {
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, es); err != nil {
			lastCheckError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
			return false
		}
		if len(es.Status.DataSyncResults) == 0 {
			lastCheckError = "DataSyncResults is empty, waiting for sync results..."
			return false
		}
		for i, result := range es.Status.DataSyncResults {
			if result.Status != "Failed" {
				lastCheckError = fmt.Sprintf(
					"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
				return false
			}
			if !strings.Contains(result.Reason, reasonFragment) {
				lastCheckError = fmt.Sprintf(
					"DataSyncResult[%d] Reason should contain %q, got '%s'", i, reasonFragment, result.Reason)
				return false
			}
		}
		lastCheckError = ""
		return true
	}).WithTimeout(timeout).WithPolling(time.Second*2).Should(BeTrue(),
		func() string {
			if lastCheckError != "" {
				return fmt.Sprintf("ExternalSecret %s/%s should fail with reason containing %q, but: %s",
					namespace, name, reasonFragment, lastCheckError)
			}
			return fmt.Sprintf("ExternalSecret %s/%s should fail with reason containing %q", namespace, name, reasonFragment)
		})
}

// --------------------------------------------------------------------------
// SecretStore / ClusterSecretStore helpers
// --------------------------------------------------------------------------

// setSecretStoreSessionName mutates the side-effect-free RAM role session
// name in the SecretStore spec, producing a genuine spec/generation change
// without altering the authentication outcome.
func setSecretStoreSessionName(ctx context.Context, namespace, name, sessionName string) {
	Eventually(func() error {
		store := &api.SecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, store); err != nil {
			return err
		}
		if store.Spec.KMS == nil || store.Spec.KMS.KMS == nil {
			return fmt.Errorf("SecretStore %s/%s has no KMS auth spec to mutate", namespace, name)
		}
		store.Spec.KMS.KMS.RAMRoleSessionName = sessionName
		return k8sClient.Update(ctx, store)
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
		"should update the session name in SecretStore %s/%s spec", namespace, name)
}

// setClusterSecretStoreSessionName is the cluster-scoped counterpart of
// setSecretStoreSessionName.
func setClusterSecretStoreSessionName(ctx context.Context, name, sessionName string) {
	Eventually(func() error {
		store := &api.ClusterSecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, store); err != nil {
			return err
		}
		if store.Spec.KMS == nil || store.Spec.KMS.KMS == nil {
			return fmt.Errorf("ClusterSecretStore %s has no KMS auth spec to mutate", name)
		}
		store.Spec.KMS.KMS.RAMRoleSessionName = sessionName
		return k8sClient.Update(ctx, store)
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
		"should update the session name in ClusterSecretStore %s spec", name)
}

// createRRSAClusterSecretStore creates an OIDC/RRSA ClusterSecretStore and
// waits until it becomes Ready (cluster-scoped counterpart of
// createRRSASecretStore).
func createRRSAClusterSecretStore(ctx context.Context, name string) *api.ClusterSecretStore {
	store := &api.ClusterSecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
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
	Expect(k8sClient.Create(ctx, store)).To(Succeed())
	waitForClusterSecretStoreReady(ctx, name)
	return store
}

// createRRSAOOSSecretStore creates an OIDC/RRSA OOS SecretStore (mirrors the
// OOS store construction in provider_test.go) and waits until it becomes
// Ready.
func createRRSAOOSSecretStore(ctx context.Context, namespace, name string) *api.SecretStore {
	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: api.SecretStoreSpec{
			OOS: &api.OOSProvider{
				OOS: &api.OOSAuth{
					RAMRoleARN:      RAMRoleArnForRRSA,
					OIDCProviderARN: OIDCProviderARN,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, store)).To(Succeed())
	waitForSecretStoreReady(ctx, namespace, name)
	return store
}

// setOOSSecretStoreSessionName mutates the side-effect-free RAM role session
// name in an OOS SecretStore spec (OOS counterpart of
// setSecretStoreSessionName), producing a genuine spec/generation change
// without altering the authentication outcome.
func setOOSSecretStoreSessionName(ctx context.Context, namespace, name, sessionName string) {
	Eventually(func() error {
		store := &api.SecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, store); err != nil {
			return err
		}
		if store.Spec.OOS == nil || store.Spec.OOS.OOS == nil {
			return fmt.Errorf("SecretStore %s/%s has no OOS auth spec to mutate", namespace, name)
		}
		store.Spec.OOS.OOS.RAMRoleSessionName = sessionName
		return k8sClient.Update(ctx, store)
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
		"should update the session name in OOS SecretStore %s/%s spec", namespace, name)
}

// setSecretStoreTriggerAnnotation sets the trigger annotation on a
// SecretStore (retry on conflict). An empty value reproduces the
// "annotation cleared" state the predicate must ignore.
func setSecretStoreTriggerAnnotation(ctx context.Context, namespace, name, value string) {
	Eventually(func() error {
		store := &api.SecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, store); err != nil {
			return err
		}
		if store.Annotations == nil {
			store.Annotations = make(map[string]string)
		}
		store.Annotations[storeWatchTriggerAnnotation] = value
		return k8sClient.Update(ctx, store)
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
		"should set the trigger annotation on SecretStore %s/%s", namespace, name)
}

// --------------------------------------------------------------------------
// ClientGeneration helpers
// --------------------------------------------------------------------------

// recordStoreClientGeneration reads the SecretStore's Status.ClientGeneration
// as a baseline for strict-increase assertions.
func recordStoreClientGeneration(ctx context.Context, namespace, name string) int64 {
	store := &api.SecretStore{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, store)).To(Succeed(),
		"failed to get SecretStore %s/%s for the ClientGeneration baseline", namespace, name)
	return store.Status.ClientGeneration
}

// recordClusterSecretStoreClientGeneration is the cluster-scoped counterpart
// of recordStoreClientGeneration.
func recordClusterSecretStoreClientGeneration(ctx context.Context, name string) int64 {
	store := &api.ClusterSecretStore{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, store)).To(Succeed(),
		"failed to get ClusterSecretStore %s for the ClientGeneration baseline", name)
	return store.Status.ClientGeneration
}

// storeClientGenerationAdvanced reports whether the store persisted a
// clientGeneration strictly above the baseline AND its Ready condition
// observed the current metadata.generation (i.e. the rebuild round fully
// completed).
func storeClientGenerationAdvanced(clientGen, generation, before int64, conditions []api.SecretStoreStatusCondition) bool {
	if clientGen <= before {
		return false
	}
	for _, c := range conditions {
		if c.Type == api.SecretStoreReady {
			return c.ObservedGeneration == generation
		}
	}
	return false
}

// expectStoreClientGenerationAdvanced waits until the SecretStore's
// Status.ClientGeneration is strictly greater than before AND its Ready
// condition's ObservedGeneration equals metadata.generation.
func expectStoreClientGenerationAdvanced(ctx context.Context, namespace, name string, before int64, triggerDesc string) {
	Eventually(func() bool {
		store := &api.SecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, store); err != nil {
			return false
		}
		return storeClientGenerationAdvanced(store.Status.ClientGeneration, store.Generation, before, store.Status.Conditions)
	}).WithTimeout(storeWatchTriggerWindow).WithPolling(time.Second*2).Should(BeTrue(),
		"SecretStore %s/%s should bump Status.ClientGeneration strictly above %d and observe its generation after %s",
		namespace, name, before, triggerDesc)
}

// expectClusterSecretStoreClientGenerationAdvanced is the cluster-scoped
// counterpart of expectStoreClientGenerationAdvanced.
func expectClusterSecretStoreClientGenerationAdvanced(ctx context.Context, name string, before int64, triggerDesc string) {
	Eventually(func() bool {
		store := &api.ClusterSecretStore{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, store); err != nil {
			return false
		}
		return storeClientGenerationAdvanced(store.Status.ClientGeneration, store.Generation, before, store.Status.Conditions)
	}).WithTimeout(storeWatchTriggerWindow).WithPolling(time.Second*2).Should(BeTrue(),
		"ClusterSecretStore %s should bump Status.ClientGeneration strictly above %d and observe its generation after %s",
		name, before, triggerDesc)
}

// expectStoreClientGenerationAlreadyAdvanced pins the ordering contract of
// bumpClientGeneration with a single synchronous (non-Eventually) read: the
// store controller persists the incremented Status.ClientGeneration BEFORE it
// removes the trigger annotation, so the very moment the cleared annotation
// becomes observable the generation bump must already be visible.
func expectStoreClientGenerationAlreadyAdvanced(ctx context.Context, namespace, name string, before int64, triggerDesc string) {
	store := &api.SecretStore{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, store)).To(Succeed(),
		"failed to re-read SecretStore %s/%s right after the trigger annotation was cleared", namespace, name)
	Expect(store.Status.ClientGeneration).To(BeNumerically(">", before),
		"SecretStore %s/%s must have bumped Status.ClientGeneration strictly above %d by the moment the trigger annotation is already cleared after %s (the bump is persisted before the annotation removal)",
		namespace, name, before, triggerDesc)
}

// expectClusterSecretStoreClientGenerationAlreadyAdvanced is the
// cluster-scoped counterpart of expectStoreClientGenerationAlreadyAdvanced.
func expectClusterSecretStoreClientGenerationAlreadyAdvanced(ctx context.Context, name string, before int64, triggerDesc string) {
	store := &api.ClusterSecretStore{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, store)).To(Succeed(),
		"failed to re-read ClusterSecretStore %s right after the trigger annotation was cleared", name)
	Expect(store.Status.ClientGeneration).To(BeNumerically(">", before),
		"ClusterSecretStore %s must have bumped Status.ClientGeneration strictly above %d by the moment the trigger annotation is already cleared after %s (the bump is persisted before the annotation removal)",
		name, before, triggerDesc)
}
