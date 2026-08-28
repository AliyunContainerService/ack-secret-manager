// Copyright © 2025 Alibaba Cloud. All rights reserved.

// write_decision_contract_test.go covers the write-decision contracts that
// sit above the zero-output guard: precedence rules (data failures vs template
// fatal) and the cancellation guard that aborts a round before any processing.

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

// --- Chain-level tests: contract precedence (data failures vs template fatal)

// TestContractPrecedenceDataFailureWithTemplateParseError: a round with BOTH
// a data-source failure and a template parse error reports
// template_processing_errors (NOT template_processing_fatal) and the Secret
// follows the data-source failure contract.
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

// TestFatalTemplateRoundZeroWritesAndNoSucceeded: a fatal template round
// performs zero Secret writes, never persists the transient empty-key
// Succeeded entry (only the template_processing_fatal placeholder), and
// returns the fatal error for backoff.
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
