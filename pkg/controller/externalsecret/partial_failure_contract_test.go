// partial_failure_contract_test.go contains the partial-failure contract
// truth table tests: shouldSkipSecretWrite, status result merging, and
// failed-key collection contracts.
//
// Related coverage lives in sibling files for discoverability:
//   - duplicate-key-specific coverage: duplicate_key_coverage_test.go
//   - JMESPath fallback-key coverage: final_key_fallback_test.go
package externalsecret

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// ---------------------------------------------------------------------------
// Section 1: contract truth table
// ---------------------------------------------------------------------------

// TestShouldSkipSecretWriteTruthTable covers the skip-guard truth table
// documented on shouldSkipSecretWrite (partial_failure.go), including
// template interactions.
func TestShouldSkipSecretWriteTruthTable(t *testing.T) {
	tests := []struct {
		name                 string
		secretMapLen         int
		errCount             int
		allDataSourcesFailed bool
		cleanupOnFailure     bool
		templateProcessed    bool
		expected             bool
	}{
		{"partial failure without template does not skip", 2, 1, false, false, false, false},
		{"partial failure without template does not skip even with cleanup enabled", 2, 1, false, true, false, false},
		{"partial failure with template skips write", 2, 1, false, false, true, true},
		{"partial failure with template skips write even with cleanup enabled", 2, 1, false, true, true, true},
		{"partial failure with empty success set skips write without cleanup", 0, 1, false, false, false, true},
		{"partial failure with empty success set skips write even with cleanup enabled", 0, 1, false, true, false, true},
		{"partial failure with empty success set and template skips write even with cleanup enabled", 0, 1, false, true, true, true},
		{"total failure without cleanup skips write", 0, 3, true, false, false, true},
		{"total failure with cleanup proceeds to deletion", 0, 3, true, true, false, false},
		{"total failure with template skips write without cleanup", 0, 3, true, false, true, true},
		{"total failure with template proceeds to deletion with cleanup enabled", 0, 3, true, true, true, false},
		{"total failure with template-rendered static content proceeds to deletion with cleanup enabled", 2, 3, true, true, true, false},
		{"total failure with template-rendered static content skips write without cleanup", 2, 3, true, false, true, true},
		{"no errors with data never skips", 3, 0, false, false, false, false},
		{"no errors with data never skips even with cleanup enabled", 3, 0, false, true, false, false},
		{"no errors with template never skips", 3, 0, false, false, true, false},
		{"no errors with template never skips even with cleanup enabled", 3, 0, false, true, true, false},
		{"no errors with empty data never skips (legitimate empty spec.Data)", 0, 0, false, false, false, false},
		{"no errors with empty data never skips even with cleanup enabled", 0, 0, false, true, false, false},
		{"no errors empty data with template never skips", 0, 0, false, false, true, false},
		{"no errors empty data with template never skips even with cleanup enabled", 0, 0, false, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipSecretWrite(tt.secretMapLen, tt.errCount, tt.allDataSourcesFailed, tt.cleanupOnFailure, tt.templateProcessed)
			if got != tt.expected {
				t.Errorf("shouldSkipSecretWrite(%d, %d, %v, %v, %v) = %v, expected %v",
					tt.secretMapLen, tt.errCount, tt.allDataSourcesFailed, tt.cleanupOnFailure, tt.templateProcessed, got, tt.expected)
			}
		})
	}
}

func TestDataSyncResultsChanged(t *testing.T) {
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	later := base.Add(3 * time.Hour)

	oldResults := []api.DataSyncResult{
		{ExternalSecretKey: "username", Status: "Failed", Reason: "get error", SynchronizationTime: metav1.Time{Time: base}},
		{ExternalSecretKey: "password", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: base}},
	}

	tests := []struct {
		name     string
		fresh    []api.DataSyncResult
		expected bool
	}{
		{
			name: "same semantics with different timestamps reports no change",
			fresh: []api.DataSyncResult{
				{ExternalSecretKey: "password", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: later}},
				{ExternalSecretKey: "username", Status: "Failed", Reason: "get error", SynchronizationTime: metav1.Time{Time: later}},
			},
			expected: false,
		},
		{
			name: "status change reports change",
			fresh: []api.DataSyncResult{
				{ExternalSecretKey: "username", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: later}},
				{ExternalSecretKey: "password", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: later}},
			},
			expected: true,
		},
		{
			name: "reason change reports change",
			fresh: []api.DataSyncResult{
				{ExternalSecretKey: "username", Status: "Failed", Reason: "timeout", SynchronizationTime: metav1.Time{Time: later}},
				{ExternalSecretKey: "password", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: later}},
			},
			expected: true,
		},
		{
			name: "key change reports change",
			fresh: []api.DataSyncResult{
				{ExternalSecretKey: "token", Status: "Failed", Reason: "get error", SynchronizationTime: metav1.Time{Time: later}},
				{ExternalSecretKey: "password", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: later}},
			},
			expected: true,
		},
		{
			name: "length change reports change",
			fresh: []api.DataSyncResult{
				{ExternalSecretKey: "username", Status: "Failed", Reason: "get error", SynchronizationTime: metav1.Time{Time: later}},
			},
			expected: true,
		},
		{
			name: "swapped entries are semantically equal",
			fresh: []api.DataSyncResult{
				{ExternalSecretKey: "password", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: later}},
				{ExternalSecretKey: "username", Status: "Failed", Reason: "get error", SynchronizationTime: metav1.Time{Time: later}},
			},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataSyncResultsChanged(oldResults, tt.fresh)
			if got != tt.expected {
				t.Errorf("dataSyncResultsChanged() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

// TestMergeResourceManagementResults verifies that reporting a
// resource-management placeholder MERGES into the existing dataSyncResults
// instead of replacing them: genuine data-key entries survive, previous
// placeholders (including the empty-key Succeeded entry) are superseded, and
// the inputs are never mutated.
func TestMergeResourceManagementResults(t *testing.T) {
	placeholder := api.DataSyncResult{
		ExternalSecretKey:   "template_zero_output_guard",
		Status:              "Failed",
		Reason:              "zero output",
		SynchronizationTime: metav1.Time{Time: time.Now()},
	}

	t.Run("real data-key entries coexist with the placeholder", func(t *testing.T) {
		old := []api.DataSyncResult{
			{ExternalSecretKey: "bad-key", Status: "Failed", Reason: "backend down"},
			{ExternalSecretKey: "password", Status: "Succeeded"},
		}
		merged := mergeResourceManagementResults(old, placeholder)
		if len(merged) != 3 {
			t.Fatalf("expected 3 entries (2 data keys + 1 placeholder), got %+v", merged)
		}
		seen := map[string]bool{}
		for _, r := range merged {
			seen[r.ExternalSecretKey] = true
		}
		for _, want := range []string{"bad-key", "password", "template_zero_output_guard"} {
			if !seen[want] {
				t.Fatalf("expected entry %q in merged results, got %+v", want, merged)
			}
		}
		// Inputs must not be mutated.
		if len(old) != 2 {
			t.Fatalf("input slice must stay untouched, got %d entries", len(old))
		}
	})

	t.Run("previous placeholder is superseded, not accumulated", func(t *testing.T) {
		old := []api.DataSyncResult{
			{ExternalSecretKey: "bad-key", Status: "Failed", Reason: "backend down"},
			{ExternalSecretKey: "rate_limit", Status: "Failed", Reason: "throttled"},
		}
		merged := mergeResourceManagementResults(old, placeholder)
		if len(merged) != 2 {
			t.Fatalf("expected old placeholder to be replaced, got %+v", merged)
		}
		for _, r := range merged {
			if r.ExternalSecretKey == "rate_limit" {
				t.Fatalf("stale placeholder must be dropped, got %+v", merged)
			}
		}
	})

	t.Run("empty-key Succeeded entry is superseded", func(t *testing.T) {
		old := []api.DataSyncResult{
			{ExternalSecretKey: "", Status: "Succeeded"},
		}
		merged := mergeResourceManagementResults(old, placeholder)
		if len(merged) != 1 || merged[0].ExternalSecretKey != "template_zero_output_guard" {
			t.Fatalf("expected only the new placeholder, got %+v", merged)
		}
	})

	t.Run("debounce: repeating the same placeholder reports no change", func(t *testing.T) {
		first := mergeResourceManagementResults(nil, placeholder)
		second := mergeResourceManagementResults(first, placeholder)
		if dataSyncResultsChanged(first, second) {
			t.Fatalf("identical placeholder re-report must not trigger a status write")
		}
		// But a different placeholder (or reason) does.
		other := placeholder
		other.ExternalSecretKey = "zero_output_guard"
		third := mergeResourceManagementResults(first, other)
		if !dataSyncResultsChanged(first, third) {
			t.Fatalf("a different placeholder must trigger a status write")
		}
	})
}

// TestBuildDataSyncResults verifies result construction for both failure and
// success paths. Resource-management style error keys (matching no spec.data
// entry) are reported verbatim.
func TestBuildDataSyncResults(t *testing.T) {
	es := &api.ExternalSecret{}
	dataErrors := map[string]error{"key-a": fmt.Errorf("kms error")}
	extractErrors := map[string]error{"key-b": fmt.Errorf("oos error")}

	results := buildDataSyncResults(es, dataErrors, extractErrors, nil, nil, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 failed results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "Failed" {
			t.Errorf("expected status Failed, got %s", r.Status)
		}
		if r.SynchronizationTime.IsZero() {
			t.Errorf("expected non-zero SynchronizationTime for key %s", r.ExternalSecretKey)
		}
	}

	success := buildDataSyncResults(es, map[string]error{}, map[string]error{}, nil, nil, nil)
	if len(success) != 1 || success[0].Status != "Succeeded" {
		t.Fatalf("expected a single Succeeded entry, got %+v", success)
	}
}

// TestCollectFailedKeys verifies deterministic ordering and cross-map
// deduplication of failed keys.
func TestCollectFailedKeys(t *testing.T) {
	t.Run("sorted keys without conflict", func(t *testing.T) {
		keys := collectFailedKeys(
			map[string]error{"zeta": fmt.Errorf("e1"), "alpha": fmt.Errorf("e2")},
			map[string]error{"mid": fmt.Errorf("e3")},
		)
		expected := []string{"alpha", "mid", "zeta"}
		assertKeysEqual(t, expected, keys)
	})

	t.Run("cross-map duplicate key appears once", func(t *testing.T) {
		keys := collectFailedKeys(
			map[string]error{"shared": fmt.Errorf("data err"), "alpha": fmt.Errorf("e2")},
			map[string]error{"shared": fmt.Errorf("extract err"), "zeta": fmt.Errorf("e3")},
		)
		expected := []string{"alpha", "shared", "zeta"}
		assertKeysEqual(t, expected, keys)
	})

	t.Run("empty maps yield empty result", func(t *testing.T) {
		keys := collectFailedKeys(map[string]error{}, nil)
		if len(keys) != 0 {
			t.Fatalf("expected empty result, got %v", keys)
		}
	})

	t.Run("nil error values are skipped (aligned with collectFailedErrors)", func(t *testing.T) {
		keys := collectFailedKeys(
			map[string]error{"data-nil": nil, "data-ok": fmt.Errorf("e1")},
			map[string]error{"extract-nil": nil, "extract-ok": fmt.Errorf("e2")},
		)
		expected := []string{"data-ok", "extract-ok"}
		assertKeysEqual(t, expected, keys)
	})
}

func assertKeysEqual(t *testing.T, expected, got []string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	}
}

// TestCollectFailedErrors verifies the merge contract of collectFailedErrors:
// disjoint maps merge verbatim, cross-map key collisions retain both errors
// instead of silently overwriting, and nil error values never panic.
func TestCollectFailedErrors(t *testing.T) {
	t.Run("disjoint maps merge verbatim", func(t *testing.T) {
		errs := collectFailedErrors(
			map[string]error{"data-key": fmt.Errorf("kms error")},
			map[string]error{"extract-key": fmt.Errorf("oos error")},
		)
		if len(errs) != 2 {
			t.Fatalf("expected 2 entries, got %v", errs)
		}
		if errs["data-key"] != "kms error" {
			t.Errorf("expected verbatim data error, got %q", errs["data-key"])
		}
		if errs["extract-key"] != "oos error" {
			t.Errorf("expected verbatim extract error, got %q", errs["extract-key"])
		}
	})

	t.Run("cross-map key collision retains both errors", func(t *testing.T) {
		errs := collectFailedErrors(
			map[string]error{"shared": fmt.Errorf("kms error")},
			map[string]error{"shared": fmt.Errorf("oos error")},
		)
		if len(errs) != 1 {
			t.Fatalf("expected 1 entry, got %v", errs)
		}
		expected := "data: kms error; extract: oos error"
		if errs["shared"] != expected {
			t.Errorf("expected %q, got %q", expected, errs["shared"])
		}
	})

	t.Run("empty and single-map inputs", func(t *testing.T) {
		if got := collectFailedErrors(nil, nil); len(got) != 0 {
			t.Errorf("expected empty result for nil maps, got %v", got)
		}
		if got := collectFailedErrors(map[string]error{}, map[string]error{}); len(got) != 0 {
			t.Errorf("expected empty result for empty maps, got %v", got)
		}
		got := collectFailedErrors(map[string]error{"only-data": fmt.Errorf("e1")}, nil)
		if len(got) != 1 || got["only-data"] != "e1" {
			t.Errorf("expected single data error, got %v", got)
		}
		got = collectFailedErrors(nil, map[string]error{"only-extract": fmt.Errorf("e2")})
		if len(got) != 1 || got["only-extract"] != "e2" {
			t.Errorf("expected single extract error, got %v", got)
		}
	})

	t.Run("nil error values are skipped without panic", func(t *testing.T) {
		errs := collectFailedErrors(
			map[string]error{"data-nil": nil, "data-ok": fmt.Errorf("e1")},
			map[string]error{"extract-nil": nil, "extract-ok": fmt.Errorf("e2")},
		)
		if len(errs) != 2 {
			t.Fatalf("expected 2 entries (nil values skipped), got %v", errs)
		}
		if errs["data-ok"] != "e1" {
			t.Errorf("expected data error preserved, got %q", errs["data-ok"])
		}
		if errs["extract-ok"] != "e2" {
			t.Errorf("expected extract error preserved, got %q", errs["extract-ok"])
		}
	})

	t.Run("data-side nil with extract-side error yields verbatim extract error", func(t *testing.T) {
		errs := collectFailedErrors(
			map[string]error{"shared": nil},
			map[string]error{"shared": fmt.Errorf("oos error")},
		)
		if len(errs) != 1 {
			t.Fatalf("expected 1 entry, got %v", errs)
		}
		// The nil data-side error is skipped before collision handling, so no
		// "data: ...; extract: ..." prefix form appears.
		if errs["shared"] != "oos error" {
			t.Errorf("expected verbatim extract error, got %q", errs["shared"])
		}
	})
}

// TestMergeWithFailedKeys verifies the partial-failure merge strategy:
// successful keys carry fresh values, failed keys retain old values when
// present, and inputs are never mutated.
func TestMergeWithFailedKeys(t *testing.T) {
	t.Run("successful keys take fresh values, failed keys retain old values", func(t *testing.T) {
		secretMap := map[string][]byte{"ok-key": []byte("fresh")}
		currentData := map[string][]byte{"ok-key": []byte("stale"), "failed-key": []byte("old-value")}
		merged := mergeWithFailedKeys(secretMap, currentData, []string{"failed-key"}, false)

		if !bytes.Equal(merged["ok-key"], []byte("fresh")) {
			t.Errorf("expected fresh value for ok-key, got %q", merged["ok-key"])
		}
		if !bytes.Equal(merged["failed-key"], []byte("old-value")) {
			t.Errorf("expected old value retained for failed-key, got %q", merged["failed-key"])
		}
		if len(merged) != 2 {
			t.Errorf("expected 2 merged keys, got %d", len(merged))
		}
	})

	t.Run("failed key without old value stays absent", func(t *testing.T) {
		secretMap := map[string][]byte{"ok-key": []byte("fresh")}
		currentData := map[string][]byte{"unrelated": []byte("x")}
		merged := mergeWithFailedKeys(secretMap, currentData, []string{"no-old-value-key"}, false)

		if _, ok := merged["no-old-value-key"]; ok {
			t.Errorf("expected failed key without old value to be absent")
		}
		if _, ok := merged["unrelated"]; ok {
			t.Errorf("expected unrelated keys not to be pulled in")
		}
	})

	t.Run("nil currentData yields secretMap copy", func(t *testing.T) {
		secretMap := map[string][]byte{"ok-key": []byte("fresh")}
		merged := mergeWithFailedKeys(secretMap, nil, []string{"failed-key"}, false)

		if len(merged) != 1 || !bytes.Equal(merged["ok-key"], []byte("fresh")) {
			t.Errorf("expected merged to equal secretMap when currentData is nil, got %v", merged)
		}
	})

	t.Run("empty failed keys yields secretMap copy", func(t *testing.T) {
		secretMap := map[string][]byte{"a": []byte("1"), "b": []byte("2")}
		currentData := map[string][]byte{"c": []byte("3")}
		merged := mergeWithFailedKeys(secretMap, currentData, nil, false)

		if len(merged) != 2 {
			t.Errorf("expected 2 keys, got %d", len(merged))
		}
	})

	t.Run("failed key already present in secretMap keeps fresh value", func(t *testing.T) {
		secretMap := map[string][]byte{"shared": []byte("fresh")}
		currentData := map[string][]byte{"shared": []byte("old")}
		merged := mergeWithFailedKeys(secretMap, currentData, []string{"shared"}, false)

		if !bytes.Equal(merged["shared"], []byte("fresh")) {
			t.Errorf("expected secretMap value to win, got %q", merged["shared"])
		}
	})

	t.Run("inputs are not mutated", func(t *testing.T) {
		secretMap := map[string][]byte{"ok-key": []byte("fresh")}
		currentData := map[string][]byte{"failed-key": []byte("old-value")}
		_ = mergeWithFailedKeys(secretMap, currentData, []string{"failed-key"}, false)

		if len(secretMap) != 1 {
			t.Errorf("secretMap was mutated, now %v", secretMap)
		}
		if len(currentData) != 1 {
			t.Errorf("currentData was mutated, now %v", currentData)
		}
	})

	t.Run("retainAllUnmapped keeps every currentData key absent from secretMap", func(t *testing.T) {
		secretMap := map[string][]byte{"ok-key": []byte("fresh")}
		currentData := map[string][]byte{
			"ok-key":      []byte("stale"),
			"extracted-a": []byte("old-a"),
			"extracted-b": []byte("old-b"),
			"renamed":     []byte("old-renamed"),
		}
		// failedEntryKeys is irrelevant in this mode.
		merged := mergeWithFailedKeys(secretMap, currentData, nil, true)

		if !bytes.Equal(merged["ok-key"], []byte("fresh")) {
			t.Errorf("expected fresh value for ok-key, got %q", merged["ok-key"])
		}
		for _, k := range []string{"extracted-a", "extracted-b", "renamed"} {
			if !bytes.Equal(merged[k], currentData[k]) {
				t.Errorf("expected old value retained for %s, got %q", k, merged[k])
			}
		}
		if len(merged) != 4 {
			t.Errorf("expected 4 merged keys, got %d", len(merged))
		}
	})

	t.Run("retainAllUnmapped with nil currentData yields secretMap copy", func(t *testing.T) {
		secretMap := map[string][]byte{"ok-key": []byte("fresh")}
		merged := mergeWithFailedKeys(secretMap, nil, nil, true)

		if len(merged) != 1 || !bytes.Equal(merged["ok-key"], []byte("fresh")) {
			t.Errorf("expected merged to equal secretMap when currentData is nil, got %v", merged)
		}
	})
}

// TestCollectFailedEntryTargetKeys verifies the mapping from failed entries to
// their target Secret keys, mirroring the success-path key derivation.
func TestCollectFailedEntryTargetKeys(t *testing.T) {
	t.Run("data entries: name/key fallback plus jmesPath aliases", func(t *testing.T) {
		es := &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				Data: []api.DataSource{
					{Key: "kms-secret-a", Name: "username"},  // failed, named
					{Key: "kms-secret-b"},                    // failed, unnamed -> fallback to Key
					{Key: "kms-secret-c", Name: "untouched"}, // succeeded
					{
						// failed with jmesPath: success path emits keys by
						// ObjectAlias, so target keys are the aliases; an empty
						// alias falls back to the data.Key (success path parity).
						Key: "kms-secret-jmes",
						JMESPath: []api.JMESPathObject{
							{Path: "user", ObjectAlias: "alias-user"},
							{Path: "pass", ObjectAlias: "alias-pass"},
							{Path: "skip", ObjectAlias: ""}, // empty alias falls back to data.Key
						},
					},
				},
			},
		}
		dataErrors := map[string]error{
			"kms-secret-a":    fmt.Errorf("boom"),
			"kms-secret-b":    fmt.Errorf("boom"),
			"kms-secret-jmes": fmt.Errorf("boom"),
		}

		got, retainAll := collectFailedEntryTargetKeys(es, dataErrors, map[string]error{})
		expected := []string{"alias-pass", "alias-user", "kms-secret-b", "kms-secret-jmes", "username"}
		if retainAll {
			t.Fatalf("expected retainAllUnmapped=false without failed extract entries")
		}
		if len(got) != len(expected) {
			t.Fatalf("expected %v, got %v", expected, got)
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Fatalf("expected %v, got %v", expected, got)
			}
		}
	})

	t.Run("failed extract entry without jmesPath relaxes merge (retainAllUnmapped)", func(t *testing.T) {
		es := &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				Data: []api.DataSource{
					{Key: "kms-secret-a", Name: "username"}, // failed
				},
				DataProcess: []api.DataProcess{
					// extract without jmesPath: success path emits the
					// document's full top-level key set, which cannot be
					// reconstructed statically on failure.
					{Extract: &api.DataSource{Key: "plain-extract"}},
					{Extract: nil},
				},
			},
		}
		dataErrors := map[string]error{"kms-secret-a": fmt.Errorf("boom")}
		extractErrors := map[string]error{"plain-extract": fmt.Errorf("boom")}

		got, retainAll := collectFailedEntryTargetKeys(es, dataErrors, extractErrors)
		if !retainAll {
			t.Fatalf("expected retainAllUnmapped=true with a failed extract entry")
		}
		// Data-entry keys are still collected alongside the relaxation flag.
		expected := []string{"username"}
		if len(got) != len(expected) || got[0] != expected[0] {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	})

	t.Run("failed extract entry with jmesPath also relaxes merge", func(t *testing.T) {
		// Even with jmesPath, the extract success path passes through every
		// top-level key (only keys matching jp.Path are renamed to the alias),
		// so the produced key set remains statically unrecoverable.
		es := &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				DataProcess: []api.DataProcess{
					{
						Extract: &api.DataSource{
							Key: "kms-json-secret",
							JMESPath: []api.JMESPathObject{
								{Path: "user", ObjectAlias: "db-user"},
								{Path: "pass", ObjectAlias: "db-pass"},
							},
						},
					},
				},
			},
		}
		extractErrors := map[string]error{"kms-json-secret": fmt.Errorf("boom")}

		got, retainAll := collectFailedEntryTargetKeys(es, map[string]error{}, extractErrors)
		if !retainAll {
			t.Fatalf("expected retainAllUnmapped=true with a failed extract entry")
		}
		if len(got) != 0 {
			t.Fatalf("expected no statically known target keys, got %v", got)
		}
	})
}

// TestCollectFailedEntryTargetKeysNoErrors verifies an empty result when no
// entry failed.
func TestCollectFailedEntryTargetKeysNoErrors(t *testing.T) {
	es := &api.ExternalSecret{
		Spec: api.ExternalSecretSpec{
			Data: []api.DataSource{{Key: "k", Name: "n"}},
		},
	}
	got, retainAll := collectFailedEntryTargetKeys(es, map[string]error{}, map[string]error{})
	if len(got) != 0 {
		t.Fatalf("expected no failed target keys, got %v", got)
	}
	if retainAll {
		t.Fatalf("expected retainAllUnmapped=false without failed extract entries")
	}
}
