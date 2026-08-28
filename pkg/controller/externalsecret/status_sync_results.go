/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// status_sync_results.go builds and persists status.dataSyncResults:
// per-round data-source errors become DataSyncResults (with duplicate-key
// success-coverage exemptions), status writes are de-bounced via semantic
// fingerprints, and the status API update is performed here.

package externalsecret

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// buildDataSyncResults builds this round's DataSyncResults. Duplicate
// data.Key/extract.Key entries are judged individually; a failed entry is
// exempt when a successful twin covers its target output (key-level
// inference; the merge layer stays conservative). Error-map keys matching no
// spec entry are reported verbatim.
func buildDataSyncResults(externalSec *api.ExternalSecret, dataErrorMap, extractDataErrorMap map[string]error, secretMap map[string][]byte, dataSucceededKeys, extractSucceededKeys map[string]struct{}) []api.DataSyncResult {
	results := make([]api.DataSyncResult, 0, len(dataErrorMap)+len(extractDataErrorMap)+1)
	now := metav1.Time{Time: time.Now()}

	reportedDataKeys := make(map[string]struct{}, len(dataErrorMap))
	reportedFailedDataKeys := make(map[string]struct{})
	for i := range externalSec.Spec.Data {
		data := &externalSec.Spec.Data[i]
		err, failed := dataErrorMap[data.Key]
		if !failed {
			continue
		}
		reportedDataKeys[data.Key] = struct{}{}
		if _, dup := reportedFailedDataKeys[data.Key]; dup {
			// Duplicate entries sharing a data.Key collapse into one error-map
			// entry: one Failed record per key, symmetric with the extract side.
			continue
		}
		if dataEntryTargetCovered(data, secretMap, dataSucceededKeys) {
			// A successful twin already produced this entry's target data
			continue
		}
		reportedFailedDataKeys[data.Key] = struct{}{}
		results = append(results, api.DataSyncResult{
			ExternalSecretKey:   data.Key,
			Status:              "Failed",
			Reason:              err.Error(),
			SynchronizationTime: now,
		})
	}

	// Keys matching no spec.data entry (resource-management placeholders or
	// entries outside the spec): report verbatim.
	for k, v := range dataErrorMap {
		if _, handled := reportedDataKeys[k]; handled {
			continue
		}
		results = append(results, api.DataSyncResult{
			ExternalSecretKey:   k,
			Status:              "Failed",
			Reason:              v.Error(),
			SynchronizationTime: now,
		})
	}

	for k, v := range extractDataErrorMap {
		if _, ok := extractSucceededKeys[k]; ok {
			// Key-level twin exemption, symmetric with the spec.data side
			continue
		}
		results = append(results, api.DataSyncResult{
			ExternalSecretKey:   k,
			Status:              "Failed",
			Reason:              v.Error(),
			SynchronizationTime: now,
		})
	}

	// No errors: add a success entry
	if len(results) == 0 {
		results = append(results, api.DataSyncResult{
			Status:              "Succeeded",
			SynchronizationTime: now,
		})
	}

	return results
}

// dataSyncResultsChanged compares two DataSyncResults slices semantically by
// (ExternalSecretKey, Status, Reason), ignoring SynchronizationTime.
func dataSyncResultsChanged(old, fresh []api.DataSyncResult) bool {
	if len(old) != len(fresh) {
		return true
	}
	count := make(map[dataSyncResultFingerprint]int, len(old))
	for _, r := range old {
		count[dataSyncResultFingerprintOf(r)]++
	}
	for _, r := range fresh {
		fp := dataSyncResultFingerprintOf(r)
		count[fp]--
		if count[fp] < 0 {
			return true
		}
	}
	return false
}

// dataSyncResultFingerprint is a struct-based map key, avoiding ambiguity
// string concatenation could introduce with arbitrary backend error text.
type dataSyncResultFingerprint struct {
	key    string
	status string
	reason string
}

func dataSyncResultFingerprintOf(r api.DataSyncResult) dataSyncResultFingerprint {
	return dataSyncResultFingerprint{
		key:    r.ExternalSecretKey,
		status: r.Status,
		reason: r.Reason,
	}
}

// resourceManagementStatusKeys enumerates the synthetic errorType keys used
// by updateResourceManagementStatus. These are resource-management
// placeholders (not backend data keys): a new placeholder replaces previous
// placeholders -- including the empty-key Succeeded entry -- while genuine
// data-key entries are retained (see mergeResourceManagementResults).
//
// cssControllerDisabledStatusKey is deliberately NOT registered here: the
// degraded-mode notice is a persistent marker, not a round-level placeholder.
// Registering it would make mergeResourceManagementResults drop it whenever
// ANY resource-management placeholder is written and let the dropPlaceholder
// global-clear churn it, breaking its stable visibility.
var resourceManagementStatusKeys = map[string]struct{}{
	"operation":                    {},
	"finalizer":                    {},
	"namespace_access":             {},
	"namespace_check":              {},
	"namespace_terminating":        {},
	"endpoint_client_cleanup":      {},
	"provider_configuration":       {},
	"provider_lookup":              {},
	"rate_limit":                   {},
	"state_retrieval":              {},
	"update_operation":             {},
	"zero_output_guard":            {},
	"template_zero_output_guard":   {},
	"template_processing_errors":   {},
	"template_processing_fatal":    {},
	"template_processing_warnings": {},
}

// retainResourceManagementPlaceholder keeps same-round resource-management
// placeholders that fresh results would drop (inverse of
// mergeResourceManagementResults); the empty-key Succeeded entry is never retained.
func retainResourceManagementPlaceholder(fresh, old []api.DataSyncResult) []api.DataSyncResult {
	existing := make(map[string]struct{}, len(fresh))
	for _, r := range fresh {
		existing[r.ExternalSecretKey] = struct{}{}
	}
	var retained []api.DataSyncResult
	for _, r := range old {
		if _, isPlaceholder := resourceManagementStatusKeys[r.ExternalSecretKey]; !isPlaceholder {
			continue
		}
		if _, dup := existing[r.ExternalSecretKey]; dup {
			continue
		}
		retained = append(retained, r)
		existing[r.ExternalSecretKey] = struct{}{}
	}
	if len(retained) == 0 {
		return fresh
	}
	return append(append(make([]api.DataSyncResult, 0, len(fresh)+len(retained)), fresh...), retained...)
}

// mergeResourceManagementResults merges a fresh resource-management
// placeholder into the existing results: genuine data-key entries are kept
// verbatim while previous placeholders (known errorType keys and the
// empty-key Succeeded entry) are dropped. Inputs are never mutated, so real
// data-source failures stay visible alongside resource-management conditions.
func mergeResourceManagementResults(old []api.DataSyncResult, placeholder api.DataSyncResult) []api.DataSyncResult {
	merged := make([]api.DataSyncResult, 0, len(old)+1)
	for _, r := range old {
		if r.ExternalSecretKey == cssControllerDisabledStatusKey {
			// Exempt from the placeholder-supersede semantics: the
			// CSS-controller-disabled notice is a persistent degraded-mode
			// marker treated like a data-key entry. It is refreshed
			// exclusively by recordCSSControllerDisabledStatus (same-key
			// replacement, so no duplicate can appear) and converges away
			// only when the degraded mode no longer applies.
			merged = append(merged, r)
			continue
		}
		if _, isPlaceholder := resourceManagementStatusKeys[r.ExternalSecretKey]; isPlaceholder {
			continue
		}
		if r.ExternalSecretKey == "" {
			// The empty-key Succeeded entry is a round-level placeholder too
			continue
		}
		merged = append(merged, r)
	}
	return append(merged, placeholder)
}

// retainCSSDisabledNotice carries the persistent CSS-controller-disabled
// notice from the previous status into fresh results while the degraded mode
// still applies: the notice key is not a registered placeholder, so neither
// retainResourceManagementPlaceholder nor buildDataSyncResults would
// reproduce it. Once the mode no longer applies this retention stops, and
// the notice converges away through the regular deferred status write
// (dropPlaceholder=true on a placeholder-free round) -- no explicit cleanup
// write is needed.
func retainCSSDisabledNotice(fresh, old []api.DataSyncResult) []api.DataSyncResult {
	for _, r := range fresh {
		if r.ExternalSecretKey == cssControllerDisabledStatusKey {
			return fresh
		}
	}
	for _, r := range old {
		if r.ExternalSecretKey == cssControllerDisabledStatusKey {
			return append(append(make([]api.DataSyncResult, 0, len(fresh)+1), fresh...), r)
		}
	}
	return fresh
}

// updateExternalSecretStatus refreshes status.dataSyncResults, skipping the
// API write when the semantic comparison shows no change. dataSucceededKeys /
// extractSucceededKeys carry this round's successful keys for the twin
// exemptions; callers outside the data-sync path pass nil.
//
// secretMap MUST be the PRE-template snapshot so the status-side coverage
// check shares the counting-side baseline. dropPlaceholder=true supersedes
// stale placeholders from PREVIOUS rounds; false retains the SAME-round
// placeholder (see retainResourceManagementPlaceholder); the empty-key
// Succeeded verdict is always superseded. force=true bypasses the
// fingerprint debounce so SynchronizationTime advances after an actual write.
func (r *ExternalSecretReconciler) updateExternalSecretStatus(externalSec *api.ExternalSecret, dataErrorMap, extractDataErrorMap map[string]error, secretMap map[string][]byte, dataSucceededKeys, extractSucceededKeys map[string]struct{}, dropPlaceholder bool, force bool) {
	freshResults := buildDataSyncResults(externalSec, dataErrorMap, extractDataErrorMap, secretMap, dataSucceededKeys, extractSucceededKeys)
	if !dropPlaceholder {
		freshResults = retainResourceManagementPlaceholder(freshResults, externalSec.Status.DataSyncResults)
	}
	// Persistent degraded-mode notice: retained independently of
	// dropPlaceholder while the CSS controller stays disabled and the
	// ExternalSecret still references a ClusterSecretStore; once the mode no
	// longer applies the retention stops and the entry is superseded (see
	// retainCSSDisabledNotice).
	if r.cssDisabledNoticeActive(externalSec) {
		freshResults = retainCSSDisabledNotice(freshResults, externalSec.Status.DataSyncResults)
	}

	// Skip the API write when nothing changed, unless forced
	if !force && !dataSyncResultsChanged(externalSec.Status.DataSyncResults, freshResults) {
		return
	}

	externalSec.Status.DataSyncResults = freshResults

	if err := r.Status().Update(r.Ctx, externalSec); err != nil {
		klog.Errorf("update external secret %s/%s status error %v", externalSec.Namespace, externalSec.Name, err)
	}
}
