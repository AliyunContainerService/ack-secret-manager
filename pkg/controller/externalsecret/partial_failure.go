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

// partial_failure.go hosts the partial-failure primitives of the reconcile
// round: fail-closed skip decision (shouldSkipSecretWrite) and the merge
// strategy retaining previous values of failed keys (mergeWithFailedKeys).

package externalsecret

import (
	"sort"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
)

// shouldSkipSecretWrite decides whether the secret write must be skipped for
// this round. allDataSourcesFailed is the data-source-level total failure
// statistic, NOT the post-template secretMapLen (static template content may
// still render on total failure). Truth table:
//   - no errors: never skip;
//   - total failure + CleanUpSecretOnFailure=true: do NOT skip -- the
//     deletion contract takes precedence;
//   - total failure + CleanUpSecretOnFailure=false: skip, retain the Secret;
//   - partial failure without template and secretMapLen > 0: do NOT skip
//     (caller merges fresh values with retained failed keys); empty success
//     set skips;
//   - partial failure with template processing: skip (fail-closed) --
//     templates may reference failed keys.
func shouldSkipSecretWrite(secretMapLen int, errCount int, allDataSourcesFailed bool, cleanupOnFailure bool, templateProcessed bool) bool {
	if errCount == 0 {
		return false
	}
	// Total failure with cleanup: deletion contract takes precedence over
	// any template-rendered static content.
	if allDataSourcesFailed && cleanupOnFailure {
		return false
	}
	// Partial failure without template: merge strategy applies.
	if secretMapLen > 0 && !templateProcessed {
		return false
	}
	return true
}

// hasDeclaredSourcesButZeroOutput is the zero-output fail-closed guard (O-2):
// all declared sources succeeded yet produced zero keys; skip the write and
// withhold deletion so synced data is never destroyed. Must run on the
// PRE-template secretMap; requires failedKeyCount==0 so it cannot preempt
// any failure contract.
func hasDeclaredSourcesButZeroOutput(totalDataSources, failedKeyCount, producedKeys int) bool {
	return totalDataSources > 0 && failedKeyCount == 0 && producedKeys == 0
}

// hasNoDeclaredSourcesButExistingData is the zero-output guard (O-3): no data
// source declared but a Secret with data exists; withhold the write to retain
// it. templateConfigured exempts template-only specs (covered by
// templateRenderedZeroOutput). Mutually exclusive with O-2.
func hasNoDeclaredSourcesButExistingData(totalDataSources int, templateConfigured bool, existingKeys int) bool {
	return totalDataSources == 0 && !templateConfigured && existingKeys > 0
}

// templateRenderedZeroOutput is the post-template zero-output fail-closed
// guard: no failures, rendering produced zero keys, and there is something to
// protect. Closes gaps the pre-template guard cannot see (Replace-mode
// execution failures clearing result.Data, Data-targeted templateFrom
// rendering zero keys, source-less template-only specs). Runs only after
// successful template processing with failedKeyCount==0.
func templateRenderedZeroOutput(failedKeyCount, postTemplateKeys, preTemplateKeys, existingKeys int) bool {
	return failedKeyCount == 0 && postTemplateKeys == 0 && (preTemplateKeys > 0 || existingKeys > 0)
}

// mergeWithFailedKeys builds the final dataset for a partial-failure round:
// successful keys carry fresh values; failed keys retain their previous
// values from currentData -- only failedEntryKeys when retainAllUnmapped is
// false, or every unmapped currentData key (safe superset) when a failed
// extract entry makes the produced key set statically unrecoverable. Known
// side effect: under superset retention, stale keys from removed spec entries
// persist until the next fully successful round. Inputs are never mutated.
func mergeWithFailedKeys(secretMap, currentData map[string][]byte, failedEntryKeys []string, retainAllUnmapped bool) map[string][]byte {
	merged := make(map[string][]byte, len(secretMap)+len(failedEntryKeys))
	for k, v := range secretMap {
		merged[k] = v
	}
	if retainAllUnmapped {
		// Superset retention: the failed extract entry's key set cannot be
		// enumerated statically.
		for k, v := range currentData {
			if _, ok := merged[k]; !ok {
				merged[k] = v
			}
		}
		return merged
	}
	for _, k := range failedEntryKeys {
		if _, ok := merged[k]; ok {
			continue
		}
		if old, ok := currentData[k]; ok {
			merged[k] = old
		}
	}
	return merged
}

// collectFailedEntryTargetKeys resolves the target Secret keys that failed
// entries would have written, mirroring the success-path key derivation:
//   - spec.data: jmesPath entries emit one key per ObjectAlias (empty alias
//     falls back to data.Key), plus the ResolveTargetKey fallback for jmes
//     parse failure/no match; without jmesPath the target key is
//     common.ResolveTargetKey;
//   - spec.dataProcess: a failed extract entry's key set cannot be
//     reconstructed statically, so retainAllUnmapped is returned true and the
//     merge relaxes to a conservative superset.
//
// The returned key list is sorted and deduplicated.
func collectFailedEntryTargetKeys(externalSec *api.ExternalSecret, dataErrorsMap, extractDataErrorsMap map[string]error) ([]string, bool) {
	keySet := make(map[string]struct{})
	for _, data := range externalSec.Spec.Data {
		if _, failed := dataErrorsMap[data.Key]; !failed {
			continue
		}
		if len(data.JMESPath) > 0 {
			for _, alias := range jmesPathTargetKeys(&data) {
				keySet[alias] = struct{}{}
			}
			// Success-path fallback: when jmes parsing fails or matches
			// nothing, the raw value lands under ResolveTargetKey. Retaining
			// one extra key is safe in the retention direction.
			keySet[common.ResolveTargetKey(&data)] = struct{}{}
			continue
		}
		keySet[common.ResolveTargetKey(&data)] = struct{}{}
	}
	retainAllUnmapped := false
	for _, dp := range externalSec.Spec.DataProcess {
		if dp.Extract == nil {
			continue
		}
		if _, failed := extractDataErrorsMap[dp.Extract.Key]; !failed {
			continue
		}
		// A failed extract entry's key set cannot be reconstructed statically
		// (on success it emits the document's full top-level key set): relax
		// the merge to retain all unmapped keys.
		retainAllUnmapped = true
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, retainAllUnmapped
}

// collectFailedKeys returns the sorted, deduplicated list of data keys that
// failed to sync across both error maps, so a key failing on both sides
// appears only once. Nil error values are skipped, keeping the key set
// exactly aligned with collectFailedErrors.
func collectFailedKeys(dataErrorMap, extractDataErrorMap map[string]error) []string {
	keySet := make(map[string]struct{}, len(dataErrorMap)+len(extractDataErrorMap))
	for k, v := range dataErrorMap {
		if v == nil {
			continue
		}
		keySet[k] = struct{}{}
	}
	for k, v := range extractDataErrorMap {
		if v == nil {
			continue
		}
		keySet[k] = struct{}{}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectFailedErrors returns a map of data key to error message for every
// failed data source. Used in log fields so that the concrete error reason
// is visible alongside the key name without requiring a separate log-level
// query or status inspection. When the same key fails on both sides, both
// errors are retained ("data: ...; extract: ...") instead of one silently
// overwriting the other; nil error values are skipped.
func collectFailedErrors(dataErrorMap, extractDataErrorMap map[string]error) map[string]string {
	errs := make(map[string]string, len(dataErrorMap)+len(extractDataErrorMap))
	for k, v := range dataErrorMap {
		if v == nil {
			continue
		}
		errs[k] = v.Error()
	}
	for k, v := range extractDataErrorMap {
		if v == nil {
			continue
		}
		if existing, ok := errs[k]; ok {
			errs[k] = "data: " + existing + "; extract: " + v.Error()
		} else {
			errs[k] = v.Error()
		}
	}
	return errs
}

// jmesPathTargetKeys derives the Secret keys a jmesPath-configured entry
// emits on success: one key per ObjectAlias, empty alias falling back to
// data.Key (utils.GetJsonSecrets). Shared by collectFailedEntryTargetKeys
// and dataEntryTargetCovered so both stay aligned with the success path.
func jmesPathTargetKeys(data *api.DataSource) []string {
	aliases := make([]string, 0, len(data.JMESPath))
	for _, jp := range data.JMESPath {
		if jp.ObjectAlias != "" {
			aliases = append(aliases, jp.ObjectAlias)
		} else {
			// Empty ObjectAlias: value is emitted under data.Key
			aliases = append(aliases, data.Key)
		}
	}
	return aliases
}

// dataEntryTargetCovered reports whether a failed spec.data entry is exempt
// from failure counting and status reporting (canonical twin-exemption
// definition). Requires BOTH:
//   - successful twin: the same data.Key was fetched successfully this round;
//   - target coverage: the entry's target key(s) already carry successful
//     output in secretMap.
//
// The twin precondition prevents cross-source target-key collisions from
// wrongly exempting a genuinely failed entry. Target key derivation mirrors
// the success path (jmesPath aliases with ResolveTargetKey fallback).
func dataEntryTargetCovered(data *api.DataSource, secretMap map[string][]byte, succeededDataKeys map[string]struct{}) bool {
	if _, twin := succeededDataKeys[data.Key]; !twin {
		return false
	}
	if len(data.JMESPath) > 0 {
		aliases := jmesPathTargetKeys(data)
		allAliasesCovered := true
		for _, alias := range aliases {
			if _, ok := secretMap[alias]; !ok {
				allAliasesCovered = false
				break
			}
		}
		if allAliasesCovered {
			return true
		}
		// Fallback form: the twin's jmes parsing failed/matched nothing, so
		// its output landed under ResolveTargetKey. Symmetric with
		// collectFailedEntryTargetKeys.
		_, ok := secretMap[common.ResolveTargetKey(data)]
		return ok
	}
	_, ok := secretMap[common.ResolveTargetKey(data)]
	return ok
}

// failedUncoveredEntries returns a slice aligned with externalSec.Spec.Data:
// position i is true when entry i failed (in dataErrorMap) AND is not exempt
// per dataEntryTargetCovered -- a genuine failure for per-entry counting and
// status reporting.
func failedUncoveredEntries(externalSec *api.ExternalSecret, dataErrorMap map[string]error, secretMap map[string][]byte, succeededDataKeys map[string]struct{}) []bool {
	failedUncovered := make([]bool, len(externalSec.Spec.Data))
	for i := range externalSec.Spec.Data {
		data := &externalSec.Spec.Data[i]
		if _, failed := dataErrorMap[data.Key]; failed && !dataEntryTargetCovered(data, secretMap, succeededDataKeys) {
			failedUncovered[i] = true
		}
	}
	return failedUncovered
}
