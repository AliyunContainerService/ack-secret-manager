package externalsecret

import (
	"fmt"
	"testing"

	"github.com/go-logr/logr"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// newDuplicateKeyES builds an ExternalSecret whose two spec.data entries
// reference the SAME backend key "shared-key": the first entry succeeds and
// produces target key "shared-key" in secretMap, while the second entry fails
// (dataErrorsMap carries the error under "shared-key"). succeededDataKeys
// records the successful twin's data.Key, as getExternalSecret does.
func newDuplicateKeyES() (*api.ExternalSecret, map[string]error, map[string][]byte, map[string]struct{}) {
	es := &api.ExternalSecret{
		Spec: api.ExternalSecretSpec{
			Data: []api.DataSource{
				{Key: "shared-key"}, // successful twin -> secretMap["shared-key"]
				{Key: "shared-key"}, // failed twin -> dataErrorsMap["shared-key"]
			},
		},
	}
	dataErrors := map[string]error{"shared-key": fmt.Errorf("client env get data error: boom")}
	secretMap := map[string][]byte{"shared-key": []byte("fresh-value")}
	succeeded := map[string]struct{}{"shared-key": {}}
	return es, dataErrors, secretMap, succeeded
}

// TestDataEntryTargetCovered verifies the exemption predicate used to
// distinguish genuinely failed entries from failed twins of a successful
// duplicate entry: the exemption requires BOTH a successful twin for the
// same data.Key AND target-key coverage.
func TestDataEntryTargetCovered(t *testing.T) {
	secretMap := map[string][]byte{
		"k":            []byte("v"),
		"alias-user":   []byte("u"),
		"other-target": []byte("x"),
	}

	tests := []struct {
		name      string
		data      api.DataSource
		succeeded map[string]struct{}
		expected  bool
	}{
		{"covered entry with successful twin is exempt", api.DataSource{Key: "k"}, map[string]struct{}{"k": {}}, true},
		{"covered entry WITHOUT a successful twin stays a failure", api.DataSource{Key: "k"}, nil, false},
		{"coverage by a DIFFERENT data.Key does not exempt", api.DataSource{Key: "k"}, map[string]struct{}{"other-key": {}}, false},
		{"named entry with twin covered by its own target name", api.DataSource{Key: "missing", Name: "k"}, map[string]struct{}{"missing": {}}, true},
		{"named entry with twin uncovered when target name absent", api.DataSource{Key: "k", Name: "missing"}, map[string]struct{}{"k": {}}, false},
		{
			"jmesPath entry with twin covered when all aliases present",
			api.DataSource{Key: "missing", JMESPath: []api.JMESPathObject{{Path: "user", ObjectAlias: "alias-user"}}},
			map[string]struct{}{"missing": {}},
			true,
		},
		{
			"jmesPath entry with twin uncovered when one alias is missing",
			api.DataSource{Key: "missing", JMESPath: []api.JMESPathObject{
				{Path: "user", ObjectAlias: "alias-user"},
				{Path: "pass", ObjectAlias: "alias-pass"},
			}},
			map[string]struct{}{"missing": {}},
			false,
		},
		{
			"jmesPath entry with all empty aliases yields no valid output and stays uncovered",
			api.DataSource{Key: "missing", JMESPath: []api.JMESPathObject{{Path: "user", ObjectAlias: ""}}},
			map[string]struct{}{"missing": {}},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dataEntryTargetCovered(&tt.data, secretMap, tt.succeeded); got != tt.expected {
				t.Errorf("dataEntryTargetCovered() = %v, expected %v", got, tt.expected)
			}
		})
	}

	t.Run("nil secretMap covers nothing even with a twin", func(t *testing.T) {
		data := api.DataSource{Key: "k"}
		if dataEntryTargetCovered(&data, nil, map[string]struct{}{"k": {}}) {
			t.Errorf("expected nil secretMap to cover nothing")
		}
	})
}

// TestFailedUncoveredEntriesDuplicateKey: two entries share one data.Key,
// one succeeds and one fails; the covered failure must not flip
// allDataSourcesFailed.
func TestFailedUncoveredEntriesDuplicateKey(t *testing.T) {
	t.Run("same target key: failed twin is covered by successful output", func(t *testing.T) {
		es, dataErrors, secretMap, succeeded := newDuplicateKeyES()
		r := &ExternalSecretReconciler{CleanUpSecretOnFailure: true}

		failedUncovered := failedUncoveredEntries(es, dataErrors, secretMap, succeeded)
		for i, uncovered := range failedUncovered {
			if uncovered {
				t.Fatalf("entry %d wrongly counted as genuine failure although its target key has successful output", i)
			}
		}

		// Exercise the controller's actual counting path: with no genuine
		// failures, allDataSourcesFailed must stay false even though every
		// spec entry appears in the error map via its shared key.
		totalDataSources, failedSources, failedKeyCount := r.countFailedSources(es, failedUncovered, dataErrors, map[string]error{}, nil)
		if totalDataSources != len(es.Spec.Data) {
			t.Fatalf("totalDataSources = %d, expected %d", totalDataSources, len(es.Spec.Data))
		}
		if failedSources != 0 {
			t.Fatalf("failedSources = %d, expected 0 while secretMap carries successful data", failedSources)
		}
		allDataSourcesFailed := totalDataSources > 0 && failedSources == totalDataSources
		if allDataSourcesFailed {
			t.Fatalf("allDataSourcesFailed must be false while secretMap carries successful data")
		}

		// Deletion-contract check: with CleanUpSecretOnFailure=true the
		// total-failure branch (writeData = nil -> handleProviderDeletion)
		// must NOT fire, and the skip guard must not retain-and-skip either;
		// the flow proceeds to write the successful data.
		templateProcessed := false
		if shouldSkipSecretWrite(len(secretMap), failedKeyCount, allDataSourcesFailed, r.CleanUpSecretOnFailure, templateProcessed) {
			t.Fatalf("shouldSkipSecretWrite must be false for the mixed-outcome duplicate key case")
		}
		writeData := r.resolveWriteData(logr.Discard(), es, dataErrors, map[string]error{}, secretMap, nil, allDataSourcesFailed, failedKeyCount, templateProcessed)
		if writeData == nil {
			t.Fatalf("Secret must not be deleted: successful data exists, writeData must carry it")
		}
		if string(writeData["shared-key"]) != "fresh-value" {
			t.Fatalf("expected fresh successful value to be written, got %q", writeData["shared-key"])
		}
	})

	t.Run("different target names: failed twin stays a genuine failure", func(t *testing.T) {
		es := &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				Data: []api.DataSource{
					{Key: "shared-key", Name: "target-a"}, // succeeds -> secretMap["target-a"]
					{Key: "shared-key", Name: "target-b"}, // fails, target "target-b" missing
				},
			},
		}
		dataErrors := map[string]error{"shared-key": fmt.Errorf("boom")}
		secretMap := map[string][]byte{"target-a": []byte("v")}
		succeeded := map[string]struct{}{"shared-key": {}}

		coverage := failedUncoveredEntries(es, dataErrors, secretMap, succeeded)
		if coverage[0] {
			t.Errorf("successful entry (covered target) must not count as failure")
		}
		if !coverage[1] {
			t.Errorf("failed entry with uncovered target must count as genuine failure")
		}
	})

	t.Run("no successful output: both duplicates remain genuine failures", func(t *testing.T) {
		es := &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				Data: []api.DataSource{{Key: "shared-key"}, {Key: "shared-key"}},
			},
		}
		dataErrors := map[string]error{"shared-key": fmt.Errorf("boom")}

		coverage := failedUncoveredEntries(es, dataErrors, map[string][]byte{}, nil)
		for i, uncovered := range coverage {
			if !uncovered {
				t.Errorf("entry %d must stay a genuine failure without successful output", i)
			}
		}
	})
}

// TestCrossSourceTargetKeyCollision verifies the tightened exemption
// precondition: when a failed entry's target key is populated by a DIFFERENT
// data.Key (or any other data source -- secretMap merges spec.data and
// spec.dataProcess outputs), the entry must NOT be exempted. Exemption is
// reserved for true twins (same data.Key), symmetric with the extract side.
func TestCrossSourceTargetKeyCollision(t *testing.T) {
	es := &api.ExternalSecret{
		Spec: api.ExternalSecretSpec{
			Data: []api.DataSource{
				{Key: "winner-key", Name: "collided-target"}, // succeeds -> secretMap["collided-target"]
				{Key: "loser-key", Name: "collided-target"},  // fails, target covered by foreign data
			},
		},
	}
	dataErrors := map[string]error{"loser-key": fmt.Errorf("boom")}
	secretMap := map[string][]byte{"collided-target": []byte("foreign-data")}
	succeeded := map[string]struct{}{"winner-key": {}}

	t.Run("failed entry is not exempted and still counted", func(t *testing.T) {
		r := &ExternalSecretReconciler{}

		failedUncovered := failedUncoveredEntries(es, dataErrors, secretMap, succeeded)
		if !failedUncovered[1] {
			t.Fatalf("failed entry whose target key carries foreign data must stay a genuine failure")
		}

		totalDataSources, failedSources, _ := r.countFailedSources(es, failedUncovered, dataErrors, map[string]error{}, nil)
		if totalDataSources != 2 || failedSources != 1 {
			t.Fatalf("expected (totalDataSources, failedSources) = (2, 1), got (%d, %d)", totalDataSources, failedSources)
		}
	})

	t.Run("status still reports Failed for the collided key", func(t *testing.T) {
		results := buildDataSyncResults(es, dataErrors, map[string]error{}, secretMap, succeeded, nil)
		failedCount := 0
		for _, res := range results {
			if res.Status == "Failed" {
				failedCount++
				if res.ExternalSecretKey != "loser-key" {
					t.Errorf("expected Failed result keyed by loser-key, got %q", res.ExternalSecretKey)
				}
			}
		}
		if failedCount != 1 {
			t.Fatalf("expected exactly 1 Failed entry despite the target-key collision, got %+v", results)
		}
	})
}

// TestBuildDataSyncResultsDuplicateKeyDedup verifies that two failed entries
// sharing the same data.Key produce a single Failed record, symmetric with
// the extract side which collapses by extract.Key.
func TestBuildDataSyncResultsDuplicateKeyDedup(t *testing.T) {
	es := &api.ExternalSecret{
		Spec: api.ExternalSecretSpec{
			Data: []api.DataSource{{Key: "dup-key"}, {Key: "dup-key"}},
		},
	}
	dataErrors := map[string]error{"dup-key": fmt.Errorf("boom")}

	results := buildDataSyncResults(es, dataErrors, map[string]error{}, map[string][]byte{}, nil, nil)
	failedCount := 0
	for _, res := range results {
		if res.Status == "Failed" {
			failedCount++
			if res.ExternalSecretKey != "dup-key" {
				t.Errorf("expected Failed result keyed by dup-key, got %q", res.ExternalSecretKey)
			}
		}
	}
	if failedCount != 1 {
		t.Fatalf("expected exactly 1 Failed record for the duplicated failed entries, got %+v", results)
	}
}

// TestTwinExtractKeyExemption: two spec.dataProcess entries share one
// extract.Key, one succeeds and one fails; the failed twin must not flip
// allDataSourcesFailed nor be reported Failed.
func TestTwinExtractKeyExemption(t *testing.T) {
	newTwinExtractES := func() *api.ExternalSecret {
		return &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				DataProcess: []api.DataProcess{
					{Extract: &api.DataSource{Key: "shared-extract"}}, // successful twin
					{Extract: &api.DataSource{Key: "shared-extract"}}, // failed twin
				},
			},
		}
	}
	extractErrors := map[string]error{"shared-extract": fmt.Errorf("client env get data failed: boom")}
	extractSucceededKeys := map[string]struct{}{"shared-extract": {}}

	t.Run("twin exemption: mixed-outcome twins do not count as all failed", func(t *testing.T) {
		es := newTwinExtractES()
		r := &ExternalSecretReconciler{CleanUpSecretOnFailure: true}

		totalDataSources, failedSources, failedKeyCount := r.countFailedSources(es, nil, map[string]error{}, extractErrors, extractSucceededKeys)
		if totalDataSources != 2 {
			t.Fatalf("totalDataSources = %d, expected 2", totalDataSources)
		}
		if failedSources != 0 {
			t.Fatalf("failedSources = %d, expected 0 (failed twin exempted by successful twin)", failedSources)
		}
		allDataSourcesFailed := totalDataSources > 0 && failedSources == totalDataSources
		if allDataSourcesFailed {
			t.Fatalf("allDataSourcesFailed must be false when a twin entry succeeded")
		}
		// Merge layer stays conservative: the raw error still counts toward
		// failedKeyCount so skip/merge decisions see the failure.
		if failedKeyCount != 1 {
			t.Fatalf("failedKeyCount = %d, expected 1 (raw error map still counted)", failedKeyCount)
		}
	})

	t.Run("no successful twin: the failure is still counted", func(t *testing.T) {
		es := newTwinExtractES()
		r := &ExternalSecretReconciler{}

		totalDataSources, failedSources, _ := r.countFailedSources(es, nil, map[string]error{}, extractErrors, nil)
		if totalDataSources != 2 {
			t.Fatalf("totalDataSources = %d, expected 2", totalDataSources)
		}
		// Both entries collapse into one error-map key; per-entry counting
		// must still report every failed entry.
		if failedSources != 2 {
			t.Fatalf("failedSources = %d, expected 2 without a successful twin", failedSources)
		}
	})

	t.Run("status does not report Failed for the exempted extract key", func(t *testing.T) {
		es := newTwinExtractES()

		results := buildDataSyncResults(es, map[string]error{}, extractErrors, map[string][]byte{"doc": []byte("v")}, nil, extractSucceededKeys)
		for _, res := range results {
			if res.Status == "Failed" {
				t.Fatalf("no Failed entry expected when a twin succeeded for the same extract.Key, got %+v", results)
			}
		}
		if len(results) != 1 || results[0].Status != "Succeeded" {
			t.Fatalf("expected a single Succeeded entry, got %+v", results)
		}

		// Without the success set (e.g. updateResourceManagementStatus call
		// path passing nil) the error is still reported verbatim.
		results = buildDataSyncResults(es, map[string]error{}, extractErrors, nil, nil, nil)
		failedCount := 0
		for _, res := range results {
			if res.Status == "Failed" && res.ExternalSecretKey == "shared-extract" {
				failedCount++
			}
		}
		if failedCount != 1 {
			t.Fatalf("expected the extract error to be reported Failed without the twin exemption, got %+v", results)
		}
	})

	t.Run("merge layer stays conservative: retainAllUnmapped still set", func(t *testing.T) {
		es := newTwinExtractES()

		_, retainAllUnmapped := collectFailedEntryTargetKeys(es, map[string]error{}, extractErrors)
		if !retainAllUnmapped {
			t.Fatalf("retainAllUnmapped must stay true for a failed extract entry even with a successful twin")
		}

		// The raw error map is untouched, so the merge retains the superset
		// of previous keys that this round's successes did not overwrite.
		secretMap := map[string][]byte{"doc": []byte("fresh")}
		currentData := map[string][]byte{"doc": []byte("old"), "exclusive-key": []byte("keep")}
		merged := mergeWithFailedKeys(secretMap, currentData, nil, retainAllUnmapped)
		if string(merged["doc"]) != "fresh" {
			t.Fatalf("successful key must carry its fresh value, got %q", merged["doc"])
		}
		if string(merged["exclusive-key"]) != "keep" {
			t.Fatalf("unmapped previous key must be retained by the conservative superset, got %q", merged["exclusive-key"])
		}
	})
}

// TestBuildDataSyncResultsDuplicateKey verifies status reporting for the
// duplicate data.Key mixed-outcome case: the successful entry must not be
// reported as Failed -- covered failures are suppressed and the round is
// reported Succeeded.
func TestBuildDataSyncResultsDuplicateKey(t *testing.T) {
	t.Run("covered failure is not reported Failed", func(t *testing.T) {
		es, dataErrors, secretMap, succeeded := newDuplicateKeyES()

		results := buildDataSyncResults(es, dataErrors, map[string]error{}, secretMap, succeeded, nil)
		for _, r := range results {
			if r.Status == "Failed" {
				t.Fatalf("no Failed entry expected when the target key carries successful output, got %+v", results)
			}
		}
		if len(results) != 1 || results[0].Status != "Succeeded" {
			t.Fatalf("expected a single Succeeded entry for the successful sync, got %+v", results)
		}
	})

	t.Run("uncovered failure with distinct target name is still reported", func(t *testing.T) {
		es := &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				Data: []api.DataSource{
					{Key: "shared-key", Name: "target-a"},
					{Key: "shared-key", Name: "target-b"},
				},
			},
		}
		dataErrors := map[string]error{"shared-key": fmt.Errorf("boom")}
		secretMap := map[string][]byte{"target-a": []byte("v")}
		succeeded := map[string]struct{}{"shared-key": {}}

		results := buildDataSyncResults(es, dataErrors, map[string]error{}, secretMap, succeeded, nil)
		failedCount := 0
		for _, r := range results {
			if r.Status == "Failed" {
				failedCount++
				if r.ExternalSecretKey != "shared-key" {
					t.Errorf("expected Failed result keyed by the source key, got %q", r.ExternalSecretKey)
				}
			}
		}
		if failedCount != 1 {
			t.Fatalf("expected exactly 1 Failed entry (the uncovered twin), got %+v", results)
		}
	})

	t.Run("resource management errors are still reported verbatim", func(t *testing.T) {
		es := &api.ExternalSecret{
			Spec: api.ExternalSecretSpec{
				Data: []api.DataSource{{Key: "some-key"}},
			},
		}
		// updateResourceManagementStatus injects errorType placeholders that
		// match no spec.data entry; nil secretMap mirrors that call path.
		resourceErrors := map[string]error{"rate_limit": fmt.Errorf("pull limit exceeded")}

		results := buildDataSyncResults(es, resourceErrors, map[string]error{}, nil, nil, nil)
		if len(results) != 1 || results[0].Status != "Failed" || results[0].ExternalSecretKey != "rate_limit" {
			t.Fatalf("expected the resource management error to be reported verbatim, got %+v", results)
		}
	})
}
