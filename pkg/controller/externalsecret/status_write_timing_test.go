// Copyright © 2025 Alibaba Cloud. All rights reserved.

// status_write_timing_test.go covers the status-write timing and template
// metadata debounce behaviors: when the deferred status write persists data
// failures, how metadata targets debounce redundant writes, and how the
// resource-management placeholder is retained or superseded across rounds.

package externalsecret

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// --- Template metadata debounce ------------------------------------------------

func TestTemplateMetadataTargetsAppliedTruthTable(t *testing.T) {
	targets := map[string]map[string]string{
		"labels":      {"app": "v1"},
		"annotations": {"managed-by": "ack-secret-manager"},
	}
	tests := []struct {
		name        string
		targets     map[string]map[string]string
		labels      map[string]string
		annotations map[string]string
		expected    bool
	}{
		{"nil targets are not applied", nil, map[string]string{"app": "v1"}, nil, false},
		{"empty targets are not applied", map[string]map[string]string{}, nil, nil, false},
		{"missing current metadata is not applied", targets, nil, nil, false},
		{"missing one key is not applied", targets, map[string]string{"app": "v1"}, map[string]string{}, false},
		{"differing value is not applied", targets, map[string]string{"app": "v2"}, map[string]string{"managed-by": "ack-secret-manager"}, false},
		{"all targets present and equal are applied", targets, map[string]string{"app": "v1"}, map[string]string{"managed-by": "ack-secret-manager"}, true},
		{"extra current keys do not prevent applied", targets, map[string]string{"app": "v1", "extra": "x"}, map[string]string{"managed-by": "ack-secret-manager", "extra": "y"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := templateMetadataTargetsApplied(tt.targets, tt.labels, tt.annotations); got != tt.expected {
				t.Errorf("templateMetadataTargetsApplied() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

// TestMetadataDebounceThreeStates drives syncIfNeedUpdate through the three
// metadata states: first round writes, steady state does NOT rewrite (all
// targets applied), and a changed target writes again.
func TestMetadataDebounceThreeStates(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "metadata-debounce-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "src-key"}},
			Target: &api.ExternalSecretTarget{
				Name: "metadata-debounce-secret",
				Template: &api.ExternalSecretTemplate{
					TemplateFrom: []api.TemplateFrom{
						{Literal: strPtr("app=v1"), Target: api.TemplateTargetAnnotations},
					},
				},
			},
		},
	}
	// Existing Secret carries the SAME data the backend returns: only
	// metadata can drive the update decision.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "metadata-debounce-secret", Namespace: "default"},
		Data:       map[string][]byte{"src-key": []byte("v")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"src-key": {"src-key": []byte("v")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	// State 1 (first/added): the annotation is absent -> the round writes.
	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("round 1 returned error: %v", err)
	}
	if !updated {
		t.Fatalf("first metadata round must write the Secret")
	}
	got := getTestSecret(t, r, "default", "metadata-debounce-secret")
	if got.Annotations["literal"] != "app=v1" {
		t.Fatalf("expected rendered annotation after round 1, got %v", got.Annotations)
	}

	// State 2 (steady): data unchanged and every target applied -> NO write.
	updated, err = r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("round 2 returned error: %v", err)
	}
	if updated {
		t.Fatalf("steady-state round with applied metadata must NOT rewrite the Secret")
	}

	// State 3 (changed): the rendered value differs -> the round writes again.
	es.Spec.Target.Template.TemplateFrom[0].Literal = strPtr("app=v2")
	updated, err = r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("round 3 returned error: %v", err)
	}
	if !updated {
		t.Fatalf("changed metadata target must trigger a write")
	}
	got = getTestSecret(t, r, "default", "metadata-debounce-secret")
	if got.Annotations["literal"] != "app=v2" {
		t.Fatalf("expected updated annotation after round 3, got %v", got.Annotations)
	}
}

// --- Status write timing -------------------------------------------------------

// TestSkippedFailureRoundStillPersistsDataFailures: the deferred status
// write still happens on the fail-closed skip path (total failure with
// cleanup=false), persisting the data-source failure without a Secret write.
func TestSkippedFailureRoundStillPersistsDataFailures(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "skipped-failure-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "bad-key"}},
			Target:   &api.ExternalSecretTarget{Name: "skipped-failure-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "skipped-failure-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing": []byte("value")},
	}
	sc := &fakeSecretClient{failByKey: map[string]error{"bad-key": fmt.Errorf("backend failure")}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if updated {
		t.Fatalf("total failure with cleanup=false must skip the write")
	}
	if !statusHasKey(es, "bad-key") {
		t.Fatalf("the deferred status write must persist the data-source failure, got %+v", es.Status.DataSyncResults)
	}
}

// --- Placeholder retention in the deferred status write ------------------------

func TestRetainResourceManagementPlaceholder(t *testing.T) {
	placeholder := api.DataSyncResult{
		ExternalSecretKey: "template_processing_errors",
		Status:            "Failed",
		Reason:            "parse error",
	}
	dataFailure := api.DataSyncResult{
		ExternalSecretKey: "bad-key",
		Status:            "Failed",
		Reason:            "backend down",
	}

	t.Run("placeholder from old is retained alongside fresh data entries", func(t *testing.T) {
		fresh := []api.DataSyncResult{dataFailure}
		old := []api.DataSyncResult{placeholder}
		got := retainResourceManagementPlaceholder(fresh, old)
		if len(got) != 2 {
			t.Fatalf("expected 2 entries (data failure + placeholder), got %+v", got)
		}
		// Inputs must not be mutated.
		if len(fresh) != 1 || len(old) != 1 {
			t.Fatalf("inputs were mutated: fresh=%v old=%v", fresh, old)
		}
	})

	t.Run("genuine data-key entries in old are not retained as placeholders", func(t *testing.T) {
		got := retainResourceManagementPlaceholder(
			[]api.DataSyncResult{dataFailure},
			[]api.DataSyncResult{{ExternalSecretKey: "other-key", Status: "Failed", Reason: "x"}},
		)
		if len(got) != 1 {
			t.Fatalf("expected only the fresh entry, got %+v", got)
		}
	})

	t.Run("no duplication when fresh already carries the placeholder key", func(t *testing.T) {
		got := retainResourceManagementPlaceholder(
			[]api.DataSyncResult{placeholder},
			[]api.DataSyncResult{placeholder},
		)
		if len(got) != 1 {
			t.Fatalf("expected a single placeholder entry, got %+v", got)
		}
	})

	t.Run("empty-key Succeeded entry in old is superseded, not retained", func(t *testing.T) {
		got := retainResourceManagementPlaceholder(
			[]api.DataSyncResult{dataFailure},
			[]api.DataSyncResult{{ExternalSecretKey: "", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: time.Now()}}},
		)
		if len(got) != 1 || got[0].ExternalSecretKey != "bad-key" {
			t.Fatalf("the stale round verdict must be superseded by the fresh failures, got %+v", got)
		}
	})
}

// TestDegradedRoundRetainsSameRoundPlaceholder drives a full degraded round
// (data-source failure + fatal template error): the same-round
// template_processing_errors placeholder must stay visible alongside the
// real data-key failure entry.
func TestDegradedRoundRetainsSameRoundPlaceholder(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-degraded-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "bad-key"}},
			Target: &api.ExternalSecretTarget{
				Name: "placeholder-degraded-secret",
				Template: &api.ExternalSecretTemplate{
					Data: map[string]string{"out": "{{ if"},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-degraded-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing": []byte("value")},
	}
	sc := &fakeSecretClient{failByKey: map[string]error{"bad-key": fmt.Errorf("backend failure")}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("degraded round must not return an error: %v", err)
	}
	if updated {
		t.Fatalf("degraded round must skip the Secret write")
	}
	if !statusHasKey(es, "template_processing_errors") {
		t.Fatalf("the same-round template_processing_errors placeholder must be retained, got %+v", es.Status.DataSyncResults)
	}
	if !statusHasKey(es, "bad-key") {
		t.Fatalf("the real data-key failure entry must be persisted, got %+v", es.Status.DataSyncResults)
	}
}

// TestRecoveryRoundDropsStalePlaceholder pins the convergence fix: a
// placeholder from a PREVIOUS round must be superseded once a normal
// data-sync round completes.
func TestRecoveryRoundDropsStalePlaceholder(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-recover-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "good-key"}},
			Target:   &api.ExternalSecretTarget{Name: "placeholder-recover-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-recover-secret", Namespace: "default"},
		Data:       map[string][]byte{"good-key": []byte("stale")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"good-key": {"good-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	// Simulate a previous throttled round that persisted the placeholder.
	r.updateResourceManagementStatus(es, "rate_limit", fmt.Errorf("throttled"))
	if !statusHasKey(es, "rate_limit") {
		t.Fatalf("precondition: rate_limit placeholder must be persisted, got %+v", es.Status.DataSyncResults)
	}

	// Recovery round: every data source succeeds.
	if _, err := r.syncIfNeedUpdate(context.Background(), es); err != nil {
		t.Fatalf("recovery round returned error: %v", err)
	}
	if statusHasKey(es, "rate_limit") {
		t.Fatalf("stale rate_limit placeholder from a previous round must be dropped on recovery, got %+v", es.Status.DataSyncResults)
	}
	if len(es.Status.DataSyncResults) != 1 || es.Status.DataSyncResults[0].Status != "Succeeded" {
		t.Fatalf("recovery round must converge to a single Succeeded entry, got %+v", es.Status.DataSyncResults)
	}
}
