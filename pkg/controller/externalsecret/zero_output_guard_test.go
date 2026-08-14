// Copyright © 2025 Alibaba Cloud. All rights reserved.

// zero_output_guard_test.go covers the zero-output fail-closed guard:
// the pure predicates (truth tables) and chain-level tests that drive
// syncIfNeedUpdate end to end with a fake provider and a fake client,
// asserting that an error-free round producing zero keys never deletes or
// clears an existing Secret, while the established success/failure contracts
// stay unaffected.

package externalsecret

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// --- Truth tables for the pure guard predicates ----------------------------

// TestHasDeclaredSourcesButZeroOutputTruthTable covers all 8 combinations of
// totalDataSources={0,>0} x failedKeyCount={0,>0} x producedKeys={0,>0}: the
// guard fires ONLY on the error-free, source-declaring, zero-output round.
func TestHasDeclaredSourcesButZeroOutputTruthTable(t *testing.T) {
	tests := []struct {
		name             string
		totalDataSources int
		failedKeyCount   int
		producedKeys     int
		expected         bool
	}{
		{"no sources, no failures, no output does not guard (the source-less case is handled separately)", 0, 0, 0, false},
		{"no sources, no failures, with output does not guard", 0, 0, 3, false},
		{"no sources with failures does not guard (failure contracts own the round)", 0, 2, 0, false},
		{"no sources with failures and output does not guard", 0, 2, 3, false},
		{"declared sources, zero failures, zero output guards", 2, 0, 0, true},
		{"declared sources with output does not guard", 2, 0, 3, false},
		{"declared sources with failures does not guard (failure contracts own the round)", 2, 1, 0, false},
		{"declared sources with failures and output does not guard", 2, 1, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasDeclaredSourcesButZeroOutput(tt.totalDataSources, tt.failedKeyCount, tt.producedKeys)
			if got != tt.expected {
				t.Errorf("hasDeclaredSourcesButZeroOutput(%d, %d, %d) = %v, expected %v",
					tt.totalDataSources, tt.failedKeyCount, tt.producedKeys, got, tt.expected)
			}
		})
	}
}

// TestHasNoDeclaredSourcesButExistingDataTruthTable covers all 8 combinations
// of totalDataSources={0,>0} x templateConfigured x existingKeys={0,>0}: the
// guard fires ONLY when no source is declared, no template is configured and
// an existing Secret carries data.
func TestHasNoDeclaredSourcesButExistingDataTruthTable(t *testing.T) {
	tests := []struct {
		name             string
		totalDataSources int
		templateConfig   bool
		existingKeys     int
		expected         bool
	}{
		{"no sources, no template, existing data guards", 0, false, 2, true},
		{"no sources, no template, no existing data does not guard", 0, false, 0, false},
		{"no sources with template does not guard (template output unknown pre-render)", 0, true, 2, false},
		{"no sources with template and no existing data does not guard", 0, true, 0, false},
		{"declared sources never take this guard (the declared-source predicate owns them)", 1, false, 2, false},
		{"declared sources without existing data never take this guard", 1, false, 0, false},
		{"declared sources with template never take this guard", 1, true, 2, false},
		{"declared sources with template and no existing data never take this guard", 1, true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasNoDeclaredSourcesButExistingData(tt.totalDataSources, tt.templateConfig, tt.existingKeys)
			if got != tt.expected {
				t.Errorf("hasNoDeclaredSourcesButExistingData(%d, %v, %d) = %v, expected %v",
					tt.totalDataSources, tt.templateConfig, tt.existingKeys, got, tt.expected)
			}
		})
	}
}

// TestTemplateRenderedZeroOutputTruthTable covers all 16 combinations of
// failedKeyCount={0,>0} x postTemplateKeys={0,>0} x preTemplateKeys={0,>0} x
// existingKeys={0,>0}: the post-template guard fires ONLY on the error-free
// round whose output collapsed to zero keys while there is something to
// protect (non-empty source data -- the declared-source post-template form --
// or an existing Secret carrying data -- the source-less post-template form).
func TestTemplateRenderedZeroOutputTruthTable(t *testing.T) {
	tests := []struct {
		name             string
		failedKeyCount   int
		postTemplateKeys int
		preTemplateKeys  int
		existingKeys     int
		expected         bool
	}{
		{"no failures, zero output, non-empty source guards", 0, 0, 2, 0, true},
		{"no failures, zero output, empty source but existing data guards (source-less post-template form)", 0, 0, 0, 2, true},
		{"no failures, zero output, non-empty source and existing data guards", 0, 0, 2, 2, true},
		{"no failures, zero output, empty source and no existing data does not guard (nothing to protect)", 0, 0, 0, 0, false},
		{"no failures with output does not guard", 0, 3, 2, 0, false},
		{"no failures with output and empty source does not guard", 0, 3, 0, 0, false},
		{"no failures with output does not guard even with existing data", 0, 3, 2, 2, false},
		{"no failures with output and empty source does not guard even with existing data", 0, 3, 0, 2, false},
		{"failures with zero output do not guard (failure contracts own the round)", 1, 0, 2, 0, false},
		{"failures with zero output and empty source do not guard even with existing data", 1, 0, 0, 2, false},
		{"failures with zero output and both protections present do not guard", 1, 0, 2, 2, false},
		{"failures with zero output and nothing present do not guard", 1, 0, 0, 0, false},
		{"failures with output do not guard", 1, 3, 2, 0, false},
		{"failures with output and empty source do not guard", 1, 3, 0, 0, false},
		{"failures with output do not guard even with existing data", 1, 3, 2, 2, false},
		{"failures with output and empty source do not guard even with existing data", 1, 3, 0, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := templateRenderedZeroOutput(tt.failedKeyCount, tt.postTemplateKeys, tt.preTemplateKeys, tt.existingKeys)
			if got != tt.expected {
				t.Errorf("templateRenderedZeroOutput(%d, %d, %d, %d) = %v, expected %v",
					tt.failedKeyCount, tt.postTemplateKeys, tt.preTemplateKeys, tt.existingKeys, got, tt.expected)
			}
		})
	}
}

// --- Chain-level tests: the zero-output guard protects existing Secrets ----

// TestZeroOutputGuardExtractEmptyDocument drives the declared-source guard
// through the full chain: a dataProcess[].extract fetch succeeds but the
// backend document has been emptied, producing zero keys. The existing
// Secret must be neither deleted (cleanup=true) nor cleared (cleanup=false),
// and the round must surface a zero_output_guard status entry instead of a
// false Succeeded write.
func TestZeroOutputGuardExtractEmptyDocument(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		name := fmt.Sprintf("cleanup=%v", cleanup)
		t.Run(name, func(t *testing.T) {
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "zero-output-extract-es", Namespace: "default"},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					DataProcess: []api.DataProcess{
						{Extract: &api.DataSource{Key: "empty-doc"}},
					},
					Target: &api.ExternalSecretTarget{Name: "zero-output-extract-secret"},
				},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "zero-output-extract-secret", Namespace: "default"},
				Data:       map[string][]byte{"existing": []byte("value")},
			}
			// The fetch succeeds; the emptied document parses to zero keys.
			sc := &fakeSecretClient{extractByKey: map[string]map[string][]byte{"empty-doc": {}}}
			r := newTestReconciler(t, cleanup, sc, es, secret)

			updated, err := r.syncIfNeedUpdate(context.Background(), es)
			if err != nil {
				t.Fatalf("syncIfNeedUpdate returned error: %v", err)
			}
			if updated {
				t.Fatalf("expected no secret update on a zero-output round")
			}

			got := getTestSecret(t, r, "default", "zero-output-extract-secret")
			if got == nil {
				t.Fatalf("existing Secret must NOT be deleted on a zero-output round (cleanup=%v)", cleanup)
			}
			if string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
				t.Fatalf("existing Secret data must stay untouched, got %v", got.Data)
			}
			if !statusHasKey(es, "zero_output_guard") {
				t.Fatalf("status must report zero_output_guard, got %+v", es.Status.DataSyncResults)
			}
		})
	}
}

// TestZeroOutputGuardDataEmptyResult is the spec.data-side counterpart of the
// extract test above: a spec.data fetch succeeds but returns zero keys (no
// error), so the round produces nothing. The existing Secret must be neither
// deleted (cleanup=true) nor cleared (cleanup=false), and the round must
// report zero_output_guard.
func TestZeroOutputGuardDataEmptyResult(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		name := fmt.Sprintf("cleanup=%v", cleanup)
		t.Run(name, func(t *testing.T) {
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "zero-output-data-es", Namespace: "default"},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data:     []api.DataSource{{Key: "empty-key"}},
					Target:   &api.ExternalSecretTarget{Name: "zero-output-data-secret"},
				},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "zero-output-data-secret", Namespace: "default"},
				Data:       map[string][]byte{"existing": []byte("value")},
			}
			// The fetch succeeds but yields zero keys (empty payload).
			sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{"empty-key": {}}}
			r := newTestReconciler(t, cleanup, sc, es, secret)

			updated, err := r.syncIfNeedUpdate(context.Background(), es)
			if err != nil {
				t.Fatalf("syncIfNeedUpdate returned error: %v", err)
			}
			if updated {
				t.Fatalf("expected no secret update on a zero-output round")
			}

			got := getTestSecret(t, r, "default", "zero-output-data-secret")
			if got == nil {
				t.Fatalf("existing Secret must NOT be deleted on a zero-output round (cleanup=%v)", cleanup)
			}
			if string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
				t.Fatalf("existing Secret data must stay untouched, got %v", got.Data)
			}
			if !statusHasKey(es, "zero_output_guard") {
				t.Fatalf("status must report zero_output_guard, got %+v", es.Status.DataSyncResults)
			}
		})
	}
}

// TestZeroOutputGuardNoSourcesExistingSecret drives the source-less guard
// through the full chain: the spec declares neither data nor dataProcess
// entries, yet a Secret with data exists. The empty round must not delete or
// clear it.
func TestZeroOutputGuardNoSourcesExistingSecret(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		name := fmt.Sprintf("cleanup=%v", cleanup)
		t.Run(name, func(t *testing.T) {
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "source-less-es", Namespace: "default"},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Target:   &api.ExternalSecretTarget{Name: "source-less-secret"},
				},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "source-less-secret", Namespace: "default"},
				Data:       map[string][]byte{"existing": []byte("value")},
			}
			r := newTestReconciler(t, cleanup, &fakeSecretClient{}, es, secret)

			updated, err := r.syncIfNeedUpdate(context.Background(), es)
			if err != nil {
				t.Fatalf("syncIfNeedUpdate returned error: %v", err)
			}
			if updated {
				t.Fatalf("expected no secret update for a source-less zero-output round")
			}

			got := getTestSecret(t, r, "default", "source-less-secret")
			if got == nil {
				t.Fatalf("existing Secret must NOT be deleted (cleanup=%v)", cleanup)
			}
			if string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
				t.Fatalf("existing Secret data must stay untouched, got %v", got.Data)
			}
			if !statusHasKey(es, "zero_output_guard") {
				t.Fatalf("status must report zero_output_guard, got %+v", es.Status.DataSyncResults)
			}
		})
	}
}

// --- Regression tests: the guard must not disturb the existing contracts ----

// TestZeroOutputGuardTemplateOnlySpecStillWrites: no data entries but a
// templateFrom literal produces data -- totalDataSources=0, so the guard
// must NOT fire and the templated write proceeds as usual.
func TestZeroOutputGuardTemplateOnlySpecStillWrites(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "template-only-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Target: &api.ExternalSecretTarget{
				Name: "template-only-secret",
				Template: &api.ExternalSecretTemplate{
					TemplateFrom: []api.TemplateFrom{
						{Literal: strPtr("tpl=rendered"), Target: api.TemplateTargetData},
					},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "template-only-secret", Namespace: "default"},
		Data:       map[string][]byte{"old": []byte("x")},
	}
	r := newTestReconciler(t, true, &fakeSecretClient{}, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if !updated {
		t.Fatalf("template-only spec with rendered output must still write")
	}
	got := getTestSecret(t, r, "default", "template-only-secret")
	if got == nil {
		t.Fatalf("Secret must exist after the templated write")
	}
	if string(got.Data["literal"]) != "tpl=rendered" {
		t.Fatalf("expected the rendered literal key, got %v", got.Data)
	}
}

// TestZeroOutputGuardFullySuccessfulRoundStillWrites: a fully successful
// round with output writes as usual.
func TestZeroOutputGuardFullySuccessfulRoundStillWrites(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "successful-round-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "good-key"}},
			Target:   &api.ExternalSecretTarget{Name: "successful-round-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "successful-round-secret", Namespace: "default"},
		Data:       map[string][]byte{"good-key": []byte("stale")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"good-key": {"value": []byte("fresh")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if !updated {
		t.Fatalf("fully successful round with output must write")
	}
	got := getTestSecret(t, r, "default", "successful-round-secret")
	if string(got.Data["good-key"]) != "fresh" {
		t.Fatalf("expected fresh value written, got %v", got.Data)
	}
}

// TestZeroOutputGuardTotalFailureDeletionContractUnaffected: total failure
// with cleanup=true still reaches the deletion contract -- the guard
// requires failedKeyCount==0 and never preempts it.
func TestZeroOutputGuardTotalFailureDeletionContractUnaffected(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "total-failure-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "gone-key"}},
			Target:   &api.ExternalSecretTarget{Name: "total-failure-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "total-failure-secret", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	sc := &fakeSecretClient{failByKey: map[string]error{"gone-key": fmt.Errorf("backend gone")}}
	r := newTestReconciler(t, true, sc, es, secret)

	_, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if got := getTestSecret(t, r, "default", "total-failure-secret"); got != nil {
		t.Fatalf("total failure with cleanup=true must still delete the Secret, got %v", got.Data)
	}
}

// TestZeroOutputGuardPartialFailureMergeUnaffected: a partial failure still
// merges fresh values of successful keys with retained old values of failed
// keys -- the guard requires failedKeyCount==0 and never interferes.
func TestZeroOutputGuardPartialFailureMergeUnaffected(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "partial-merge-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data: []api.DataSource{
				{Key: "good-key"},
				{Key: "bad-key"},
			},
			Target: &api.ExternalSecretTarget{Name: "partial-merge-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "partial-merge-secret", Namespace: "default"},
		Data:       map[string][]byte{"good-key": []byte("old"), "bad-key": []byte("old-bad")},
	}
	sc := &fakeSecretClient{
		dataByKey: map[string]map[string][]byte{"good-key": {"value": []byte("fresh")}},
		failByKey: map[string]error{"bad-key": fmt.Errorf("transient failure")},
	}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if !updated {
		t.Fatalf("partial failure without template must merge and write")
	}
	got := getTestSecret(t, r, "default", "partial-merge-secret")
	if string(got.Data["good-key"]) != "fresh" {
		t.Fatalf("successful key must carry the fresh value, got %v", got.Data)
	}
	if string(got.Data["bad-key"]) != "old-bad" {
		t.Fatalf("failed key must retain its old value, got %v", got.Data)
	}
}

// --- Chain-level tests: the post-template zero-output guard -----------------

// TestPostTemplateZeroOutputGuardInlineExecutionFailures covers Replace mode
// where every inline data template fails execution: result.Data is cleared
// and never repopulated, so the guard must skip the write and withhold
// deletion for both cleanup flag values (template_zero_output_guard).
func TestPostTemplateZeroOutputGuardInlineExecutionFailures(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		name := fmt.Sprintf("cleanup=%v", cleanup)
		t.Run(name, func(t *testing.T) {
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "inline-fail-es", Namespace: "default"},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data:     []api.DataSource{{Key: "src-key"}},
					Target: &api.ExternalSecretTarget{
						Name: "inline-fail-secret",
						Template: &api.ExternalSecretTemplate{
							// MergePolicy omitted -> Replace (default).
							// The fail function aborts execution with an error:
							// valid syntax, execution fails (non-fatal grading).
							Data: map[string]string{"out": `{{ fail "simulated rendering failure" }}`},
						},
					},
				},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "inline-fail-secret", Namespace: "default"},
				Data:       map[string][]byte{"existing": []byte("value")},
			}
			sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
				"src-key": {"src-key": []byte("v")},
			}}
			r := newTestReconciler(t, cleanup, sc, es, secret)

			updated, err := r.syncIfNeedUpdate(context.Background(), es)
			if err != nil {
				t.Fatalf("syncIfNeedUpdate returned error: %v", err)
			}
			if updated {
				t.Fatalf("expected no secret update on a post-template zero-output round")
			}
			got := getTestSecret(t, r, "default", "inline-fail-secret")
			if got == nil {
				t.Fatalf("existing Secret must NOT be deleted (cleanup=%v)", cleanup)
			}
			if string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
				t.Fatalf("existing Secret data must stay untouched, got %v", got.Data)
			}
			if !statusHasKey(es, "template_zero_output_guard") {
				t.Fatalf("status must report template_zero_output_guard, got %+v", es.Status.DataSyncResults)
			}
		})
	}
}

// TestPostTemplateZeroOutputGuardTemplateFromDataZeroKeys covers the second
// post-template gap: a Data-targeted templateFrom renders zero valid keys
// (a KeysAndValues ConfigMap template without any key=value line), yet its
// Data target triggers the Replace clear. The guard must protect the Secret.
func TestPostTemplateZeroOutputGuardTemplateFromDataZeroKeys(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		name := fmt.Sprintf("cleanup=%v", cleanup)
		t.Run(name, func(t *testing.T) {
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "templatefrom-zero-es", Namespace: "default"},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data:     []api.DataSource{{Key: "src-key"}},
					Target: &api.ExternalSecretTarget{
						Name: "templatefrom-zero-secret",
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: "templatefrom-zero-cm",
										Items: []api.TemplateRefItem{
											{Key: "tpl", TemplateAs: api.TemplateScopeKeysAndValues},
										},
									},
									Target: api.TemplateTargetData,
								},
							},
						},
					},
				},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "templatefrom-zero-secret", Namespace: "default"},
				Data:       map[string][]byte{"existing": []byte("value")},
			}
			// The template renders to a line without '=': KeysAndValues parses
			// zero valid key-value pairs out of it.
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "templatefrom-zero-cm", Namespace: "default"},
				Data:       map[string]string{"tpl": "no key value pairs here"},
			}
			sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
				"src-key": {"src-key": []byte("v")},
			}}
			r := newTestReconciler(t, cleanup, sc, es, secret, cm)

			updated, err := r.syncIfNeedUpdate(context.Background(), es)
			if err != nil {
				t.Fatalf("syncIfNeedUpdate returned error: %v", err)
			}
			if updated {
				t.Fatalf("expected no secret update on a post-template zero-output round")
			}
			got := getTestSecret(t, r, "default", "templatefrom-zero-secret")
			if got == nil {
				t.Fatalf("existing Secret must NOT be deleted (cleanup=%v)", cleanup)
			}
			if string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
				t.Fatalf("existing Secret data must stay untouched, got %v", got.Data)
			}
			if !statusHasKey(es, "template_zero_output_guard") {
				t.Fatalf("status must report template_zero_output_guard, got %+v", es.Status.DataSyncResults)
			}
		})
	}
}

// TestPostTemplateZeroOutputGuardSourceLessTemplateExistingData covers the
// source-less post-template gap: a template-only spec (no data source,
// exempt from the pre-template source-less guard via templateConfigured)
// renders zero Data keys while an existing Secret holds data; the guard
// must skip the write and withhold deletion for both cleanup flag values
// (template_zero_output_guard).
func TestPostTemplateZeroOutputGuardSourceLessTemplateExistingData(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		name := fmt.Sprintf("cleanup=%v", cleanup)
		t.Run(name, func(t *testing.T) {
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "sourceless-template-es", Namespace: "default"},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Target: &api.ExternalSecretTarget{
						Name: "sourceless-template-secret",
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: "sourceless-template-cm",
										Items: []api.TemplateRefItem{
											{Key: "tpl", TemplateAs: api.TemplateScopeKeysAndValues},
										},
									},
									Target: api.TemplateTargetData,
								},
							},
						},
					},
				},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "sourceless-template-secret", Namespace: "default"},
				Data:       map[string][]byte{"existing": []byte("value")},
			}
			// The template renders to a line without '=': KeysAndValues parses
			// zero valid key-value pairs out of it, so the Data-targeted
			// templateFrom yields zero keys.
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "sourceless-template-cm", Namespace: "default"},
				Data:       map[string]string{"tpl": "no key value pairs here"},
			}
			r := newTestReconciler(t, cleanup, &fakeSecretClient{}, es, secret, cm)

			updated, err := r.syncIfNeedUpdate(context.Background(), es)
			if err != nil {
				t.Fatalf("syncIfNeedUpdate returned error: %v", err)
			}
			if updated {
				t.Fatalf("expected no secret update on a source-less template zero-output round")
			}
			got := getTestSecret(t, r, "default", "sourceless-template-secret")
			if got == nil {
				t.Fatalf("existing Secret must NOT be deleted (cleanup=%v)", cleanup)
			}
			if string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
				t.Fatalf("existing Secret data must stay untouched, got %v", got.Data)
			}
			if !statusHasKey(es, "template_zero_output_guard") {
				t.Fatalf("status must report template_zero_output_guard, got %+v", es.Status.DataSyncResults)
			}
		})
	}
}

// TestPostTemplateZeroOutputGuardMetadataOnlyNotTriggered (no false positive):
// a metadata-only templateFrom keeps the raw data, so result.Data stays
// non-empty and the guard must NOT fire; the write proceeds as usual.
func TestPostTemplateZeroOutputGuardMetadataOnlyNotTriggered(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "metadata-only-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "src-key"}},
			Target: &api.ExternalSecretTarget{
				Name: "metadata-only-secret",
				Template: &api.ExternalSecretTemplate{
					TemplateFrom: []api.TemplateFrom{
						{Literal: strPtr("app=v1"), Target: api.TemplateTargetAnnotations},
					},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "metadata-only-secret", Namespace: "default"},
		Data:       map[string][]byte{"src-key": []byte("old")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"src-key": {"src-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, true, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if !updated {
		t.Fatalf("metadata-only templateFrom with retained raw data must still write")
	}
	got := getTestSecret(t, r, "default", "metadata-only-secret")
	if string(got.Data["src-key"]) != "fresh" {
		t.Fatalf("raw data must be preserved and refreshed, got %v", got.Data)
	}
	if got.Annotations["literal"] != "app=v1" {
		t.Fatalf("expected rendered annotation, got %v", got.Annotations)
	}
	if statusHasKey(es, "template_zero_output_guard") {
		t.Fatalf("guard must NOT fire for a metadata-only templateFrom round, got %+v", es.Status.DataSyncResults)
	}
}

// TestStatusCoverageUsesPreTemplateBaseline is the chain-level regression for
// the status/counting baseline consistency: two spec.data entries reference
// the same backend key, one succeeds and one fails, and a Replace-mode
// template replaces the round's dataset. The status-side coverage check must
// evaluate the PRE-template output (where the successful twin covers the
// shared target key), NOT the template-replaced map -- otherwise the covered
// duplicate-key failure would be misreported as Failed while the counting
// side exempts it.
func TestStatusCoverageUsesPreTemplateBaseline(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "coverage-baseline-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data: []api.DataSource{
				{Key: "shared-key"},                     // successful twin -> secretMap["shared-key"]
				{Key: "shared-key", Name: "shared-key"}, // same target key, fails via failByName
			},
			Target: &api.ExternalSecretTarget{
				Name: "coverage-baseline-secret",
				Template: &api.ExternalSecretTemplate{
					// Replace mode (default): the round's dataset becomes the
					// template output, which does NOT contain "shared-key".
					TemplateFrom: []api.TemplateFrom{
						{Literal: strPtr("rendered=yes"), Target: api.TemplateTargetData},
					},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "coverage-baseline-secret", Namespace: "default"},
		Data:       map[string][]byte{"shared-key": []byte("stale")},
	}
	sc := &fakeSecretClient{
		dataByKey:  map[string]map[string][]byte{"shared-key": {"shared-key": []byte("fresh")}},
		failByName: map[string]error{"shared-key": fmt.Errorf("transient failure")},
	}
	r := newTestReconciler(t, false, sc, es, secret)

	if _, err := r.syncIfNeedUpdate(context.Background(), es); err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	// Counting side: the failed twin is covered by the successful output and
	// exempt; status must agree -- no Failed entry for the shared key.
	for _, res := range es.Status.DataSyncResults {
		if res.Status == "Failed" {
			t.Fatalf("covered duplicate-key failure must NOT be reported Failed in status, got %+v", es.Status.DataSyncResults)
		}
	}
	if len(es.Status.DataSyncResults) != 1 || es.Status.DataSyncResults[0].Status != "Succeeded" {
		t.Fatalf("expected a single Succeeded entry for the covered sync, got %+v", es.Status.DataSyncResults)
	}
}

// --- Chain-level tests: contract precedence (data failures vs template fatal)

// TestContractPrecedenceDataFailureWithTemplateParseError: a round with BOTH a
// data-source failure and a template parse error reports
// template_processing_errors (NOT template_processing_fatal) and the Secret is
// handled by the data-source failure contract -- here total failure with
// cleanup=false retains the Secret.
func TestContractPrecedenceDataFailureWithTemplateParseError(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "precedence-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "bad-key"}},
			Target: &api.ExternalSecretTarget{
				Name: "precedence-secret",
				Template: &api.ExternalSecretTemplate{
					// Unclosed action -> parse error (fatal grading).
					Data: map[string]string{"out": "{{ if"},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "precedence-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing": []byte("value")},
	}
	sc := &fakeSecretClient{failByKey: map[string]error{"bad-key": fmt.Errorf("backend failure")}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if updated {
		t.Fatalf("expected no write on a failed round with a template parse error")
	}
	got := getTestSecret(t, r, "default", "precedence-secret")
	if got == nil || string(got.Data["existing"]) != "value" {
		t.Fatalf("total failure with cleanup=false must retain the Secret, got %v", got)
	}
	if !statusHasKey(es, "template_processing_errors") {
		t.Fatalf("status must report template_processing_errors, got %+v", es.Status.DataSyncResults)
	}
	if statusHasKey(es, "template_processing_fatal") {
		t.Fatalf("status must NOT report template_processing_fatal when data sources failed, got %+v", es.Status.DataSyncResults)
	}
	// The real data-source failure entry survives alongside the placeholder.
	if !statusHasKey(es, "bad-key") {
		t.Fatalf("status must retain the real data-key failure entry, got %+v", es.Status.DataSyncResults)
	}
}

// TestFatalTemplateRoundZeroWritesAndNoSucceeded: when every data source
// succeeded, a template parse error is the round's only problem. The fatal
// template round must perform zero Secret writes, never persist the
// transient empty-key Succeeded entry (only the template_processing_fatal
// placeholder is allowed in status), and return the fatal error for
// backoff.
func TestFatalTemplateRoundZeroWritesAndNoSucceeded(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "fatal-template-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "good-key"}},
			Target: &api.ExternalSecretTarget{
				Name: "fatal-template-secret",
				Template: &api.ExternalSecretTemplate{
					Data: map[string]string{"out": "{{ if"},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "fatal-template-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing": []byte("value")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"good-key": {"good-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, true, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err == nil {
		t.Fatalf("expected a fatal template error to be returned")
	}
	if updated {
		t.Fatalf("expected zero Secret writes on a fatal template round")
	}
	got := getTestSecret(t, r, "default", "fatal-template-secret")
	if got == nil || string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
		t.Fatalf("Secret must stay untouched on a fatal template round, got %v", got)
	}
	for _, res := range es.Status.DataSyncResults {
		if res.Status == "Succeeded" {
			t.Fatalf("fatal template round must not persist any Succeeded entry, got %+v", es.Status.DataSyncResults)
		}
	}
	if !statusHasKey(es, "template_processing_fatal") {
		t.Fatalf("status must report template_processing_fatal, got %+v", es.Status.DataSyncResults)
	}
}

// --- Chain-level test: the cancellation guard --------------------------------

// TestCancellationGuardAbortsRound: a canceled request context aborts the
// round before any processing -- the cancellation error is returned, the
// Secret stays untouched, and no rate_limit status is written.
func TestCancellationGuardAbortsRound(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "canceled-round-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "src-key"}},
			Target:   &api.ExternalSecretTarget{Name: "canceled-round-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "canceled-round-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing": []byte("value")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"src-key": {"src-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, true, sc, es, secret)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate manager shutdown canceling the request context

	updated, err := r.syncIfNeedUpdate(ctx, es)
	if err == nil || !stderrors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if updated {
		t.Fatalf("expected no update on a canceled round")
	}
	got := getTestSecret(t, r, "default", "canceled-round-secret")
	if got == nil || string(got.Data["existing"]) != "value" || len(got.Data) != 1 {
		t.Fatalf("Secret must stay untouched on a canceled round, got %v", got)
	}
	if statusHasKey(es, "rate_limit") {
		t.Fatalf("canceled round must NOT write a rate_limit status, got %+v", es.Status.DataSyncResults)
	}
}
