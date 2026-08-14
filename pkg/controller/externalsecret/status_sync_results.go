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

// status_sync_results.go builds and persists status.dataSyncResults: it
// translates per-round data-source errors into DataSyncResults (applying the
// duplicate-key success-coverage exemptions for both spec.data and
// spec.dataProcess), de-bounces status writes via semantic fingerprint
// comparison, and performs the actual status API update.

package externalsecret

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// buildDataSyncResults builds this round's DataSyncResults. Duplicate
// data.Key/extract.Key entries are judged individually; a failed entry is
// exempt when a successful twin covers its target output (dataEntryTargetCovered
// / extractSucceededKeys - key-level inference; the merge layer stays
// conservative). Error-map keys matching no spec entry are reported verbatim.
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
			// Duplicate entries sharing the same data.Key collapse into one
			// error-map entry; report a single Failed record per key,
			// symmetric with the extract side which is keyed by extract.Key.
			continue
		}
		if dataEntryTargetCovered(data, secretMap, dataSucceededKeys) {
			// A successful twin for the same data.Key already produced this
			// entry's target data; reporting Failed here would misrepresent
			// the successful sync.
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

	// Keys that match no spec.data entry: resource management errors
	// (errorType placeholders) or entries outside the spec -- report verbatim.
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
			// Key-level exemption, symmetric with the spec.data side: a twin
			// dataProcess entry succeeded for this extract.Key this round, so
			// reporting Failed would misrepresent the sync. See the function
			// comment for the boundary of this weak inference -- the merge
			// layer deliberately stays conservative.
			continue
		}
		results = append(results, api.DataSyncResult{
			ExternalSecretKey:   k,
			Status:              "Failed",
			Reason:              v.Error(),
			SynchronizationTime: now,
		})
	}

	// if no errors, add a success entry
	if len(results) == 0 {
		results = append(results, api.DataSyncResult{
			Status:              "Succeeded",
			SynchronizationTime: now,
		})
	}

	return results
}

// dataSyncResultsChanged compares two DataSyncResults slices semantically by
// (ExternalSecretKey, Status, Reason), ignoring SynchronizationTime. It returns
// true when the new results carry different information than the old ones.
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

// dataSyncResultFingerprint is a struct-based map key, avoiding any ambiguity
// that string concatenation could introduce when Reason contains arbitrary
// backend error text.
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

// resourceManagementStatusKeys enumerates every synthetic errorType key used
// by updateResourceManagementStatus. These entries are resource-management
// placeholders (not backend data keys); when a new placeholder is reported,
// previous placeholder entries -- including the empty-key Succeeded entry --
// are replaced, while genuine data-key entries are retained (see
// mergeResourceManagementResults).
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
// mergeResourceManagementResults). The empty-key Succeeded entry is never
// retained.
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
// placeholder entry into the existing DataSyncResults: genuine data-key
// entries are retained verbatim, while previous placeholder entries (known
// errorType keys and the empty-key Succeeded entry) are dropped in favor of
// the new placeholder. Inputs are never mutated. This keeps real data-source
// failure records visible alongside resource-management conditions instead of
// being masked by a whole-slice replacement.
func mergeResourceManagementResults(old []api.DataSyncResult, placeholder api.DataSyncResult) []api.DataSyncResult {
	merged := make([]api.DataSyncResult, 0, len(old)+1)
	for _, r := range old {
		if _, isPlaceholder := resourceManagementStatusKeys[r.ExternalSecretKey]; isPlaceholder {
			continue
		}
		if r.ExternalSecretKey == "" {
			// The empty-key Succeeded entry is a round-level placeholder as
			// well; a resource-management condition supersedes it.
			continue
		}
		merged = append(merged, r)
	}
	return append(merged, placeholder)
}

// updateExternalSecretStatus refreshes status.dataSyncResults, comparing the
// fresh results with the current status semantically (ignoring timestamps) and
// skipping the API write when nothing changed. dataSucceededKeys /
// extractSucceededKeys carry this round's successful keys for the twin
// exemptions; callers outside the data-sync path pass nil.
//
// secretMap drives the target-coverage check and MUST be the PRE-template
// snapshot so the status-side coverage evaluation shares the same baseline as
// the counting side; the Secret write decision keeps using the post-template
// data. dropPlaceholder=true supersedes stale placeholders from PREVIOUS
// rounds (full-replacement convergence); false retains the SAME-round
// placeholder alongside fresh results (see retainResourceManagementPlaceholder);
// the empty-key Succeeded verdict is always superseded. force=true bypasses
// the fingerprint debounce so SynchronizationTime advances after an actual
// Secret write even when the fingerprint is unchanged.
func (r *ExternalSecretReconciler) updateExternalSecretStatus(externalSec *api.ExternalSecret, dataErrorMap, extractDataErrorMap map[string]error, secretMap map[string][]byte, dataSucceededKeys, extractSucceededKeys map[string]struct{}, dropPlaceholder bool, force bool) {
	freshResults := buildDataSyncResults(externalSec, dataErrorMap, extractDataErrorMap, secretMap, dataSucceededKeys, extractSucceededKeys)
	if !dropPlaceholder {
		freshResults = retainResourceManagementPlaceholder(freshResults, externalSec.Status.DataSyncResults)
	}

	// Skip the API write (and keep the existing timestamps) when the sync
	// results carry no new information -- unless forced (see above).
	if !force && !dataSyncResultsChanged(externalSec.Status.DataSyncResults, freshResults) {
		return
	}

	externalSec.Status.DataSyncResults = freshResults

	if err := r.Status().Update(r.Ctx, externalSec); err != nil {
		klog.Errorf("update external secret status error %v", err)
	}
}
