// Copyright © 2025 Alibaba Cloud. All rights reserved.

// sync_timestamp_refresh_test.go covers the post-Secret-write forced status
// refresh: when an actual Secret write succeeds, status.dataSyncResults must
// be rewritten (bypassing the semantic-fingerprint debounce) so that
// SynchronizationTime advances together with the data -- both when the user
// changed spec.data on an all-success round and when a backend value changed
// between polls. Unchanged polling rounds keep the original debounce and do
// NOT rewrite the status. Reuses the shared chain-level harness (fake
// provider + fake client) from helpers_test.go.

package externalsecret

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// succeededTimestamp returns the SynchronizationTime of the empty-key
// Succeeded round entry, failing the test when it is absent.
func succeededTimestamp(t *testing.T, es *api.ExternalSecret) metav1.Time {
	t.Helper()
	for _, res := range es.Status.DataSyncResults {
		if res.ExternalSecretKey == "" && res.Status == "Succeeded" {
			return res.SynchronizationTime
		}
	}
	t.Fatalf("expected an empty-key Succeeded entry in status, got %+v", es.Status.DataSyncResults)
	return metav1.Time{}
}

// TestSyncTimestampAdvancesWhenSpecDataChangedAllSucceed simulates the user
// editing spec.data: the existing Secret carries a key from the OLD spec,
// the current spec fetches a different key, and every source succeeds. The
// round's fingerprint (single empty-key Succeeded entry) is identical to the
// persisted one, so the pre-write debounced status write is skipped -- the
// forced post-write refresh must still advance SynchronizationTime.
func TestSyncTimestampAdvancesWhenSpecDataChangedAllSucceed(t *testing.T) {
	staleTime := metav1.Time{Time: time.Now().Add(-24 * time.Hour)}
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "spec-change-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "new-key"}},
			Target:   &api.ExternalSecretTarget{Name: "spec-change-secret"},
		},
		Status: api.ExternalSecretStatus{
			DataSyncResults: []api.DataSyncResult{
				{Status: "Succeeded", SynchronizationTime: staleTime},
			},
		},
	}
	// Secret written by the previous (old) spec.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "spec-change-secret", Namespace: "default"},
		Data:       map[string][]byte{"removed-key": []byte("old-value")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"new-key": {"new-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if !updated {
		t.Fatalf("changed spec.data must trigger a Secret write")
	}
	got := getTestSecret(t, r, "default", "spec-change-secret")
	if got == nil || string(got.Data["new-key"]) != "fresh" {
		t.Fatalf("expected the new key written, got %v", got)
	}
	// Fingerprint unchanged (single Succeeded entry both before and after),
	// so only the forced post-write refresh can have moved the timestamp.
	if ts := succeededTimestamp(t, es); !ts.After(staleTime.Time) {
		t.Fatalf("SynchronizationTime must advance after a successful Secret write, stayed at %v", ts)
	}
}

// TestSyncTimestampAdvancesWhenBackendValueChanges covers the second trigger:
// the spec is untouched but the backend credential value changed, so the
// DeepEqual write decision fires and the timestamp must advance as well.
func TestSyncTimestampAdvancesWhenBackendValueChanges(t *testing.T) {
	staleTime := metav1.Time{Time: time.Now().Add(-24 * time.Hour)}
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "value-change-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "good-key"}},
			Target:   &api.ExternalSecretTarget{Name: "value-change-secret"},
		},
		Status: api.ExternalSecretStatus{
			DataSyncResults: []api.DataSyncResult{
				{Status: "Succeeded", SynchronizationTime: staleTime},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "value-change-secret", Namespace: "default"},
		Data:       map[string][]byte{"good-key": []byte("stale")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"good-key": {"good-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if !updated {
		t.Fatalf("changed backend value must trigger a Secret write")
	}
	got := getTestSecret(t, r, "default", "value-change-secret")
	if got == nil || string(got.Data["good-key"]) != "fresh" {
		t.Fatalf("expected the fresh value written, got %v", got)
	}
	if ts := succeededTimestamp(t, es); !ts.After(staleTime.Time) {
		t.Fatalf("SynchronizationTime must advance after a successful Secret write, stayed at %v", ts)
	}
}

// TestSyncTimestampRetainedWhenNothingChanged guards the debounce: an
// unchanged polling round (Secret data equal AND fingerprint identical) must
// NOT rewrite the status -- the persisted SynchronizationTime stays frozen,
// preserving the original write-reduction behavior.
func TestSyncTimestampRetainedWhenNothingChanged(t *testing.T) {
	fixedTime := metav1.Time{Time: time.Now().Add(-24 * time.Hour)}
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "unchanged-round-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "good-key"}},
			Target:   &api.ExternalSecretTarget{Name: "unchanged-round-secret"},
		},
		Status: api.ExternalSecretStatus{
			DataSyncResults: []api.DataSyncResult{
				{Status: "Succeeded", SynchronizationTime: fixedTime},
			},
		},
	}
	// The Secret already carries exactly the value the backend returns.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "unchanged-round-secret", Namespace: "default"},
		Data:       map[string][]byte{"good-key": []byte("fresh")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"good-key": {"good-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if updated {
		t.Fatalf("unchanged round must not write the Secret")
	}
	// Debounce retained: the status entry stays byte-identical, timestamp
	// included.
	if len(es.Status.DataSyncResults) != 1 {
		t.Fatalf("expected the single persisted entry to remain, got %+v", es.Status.DataSyncResults)
	}
	if ts := succeededTimestamp(t, es); !ts.Equal(&fixedTime) {
		t.Fatalf("unchanged round must keep the persisted SynchronizationTime %v, got %v", fixedTime, ts)
	}
}
