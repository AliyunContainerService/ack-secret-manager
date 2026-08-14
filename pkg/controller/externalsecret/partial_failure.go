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

// partial_failure.go hosts the partial-failure handling primitives of the
// ExternalSecret reconcile round: per-entry failure accounting for specs
// with duplicate backend-key references, the fail-closed skip decision
// (shouldSkipSecretWrite), and the merge strategy that retains previous
// Secret values of failed keys (mergeWithFailedKeys and its target-key
// derivation helpers).

package externalsecret

import (
	"sort"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
)

// shouldSkipSecretWrite decides whether the secret write must be skipped for
// this reconcile round. allDataSourcesFailed is the data-source-level total
// failure statistic, NOT the post template-rendering secretMapLen (templates
// with static content may still render a non-empty result on total failure).
// Truth table:
//   - no errors (errCount == 0): never skip, behavior unchanged;
//   - total failure with CleanUpSecretOnFailure=true: do NOT skip, regardless
//     of template processing -- the deletion contract takes precedence and the
//     caller forces an empty dataset;
//   - total failure with CleanUpSecretOnFailure=false: skip, the existing
//     Secret is retained;
//   - partial failures without template processing and secretMapLen > 0: do
//     NOT skip; the caller merges fresh values with retained old values of
//     failed keys. An empty successful set (secretMapLen == 0) is treated as
//     skip;
//   - partial failures with template processing: skip (fail-closed) --
//     templates may reference failed keys, so writing partial data could
//     render an incorrect Secret, and deleting would discard still-valid keys.
func shouldSkipSecretWrite(secretMapLen int, errCount int, allDataSourcesFailed bool, cleanupOnFailure bool, templateProcessed bool) bool {
	if errCount == 0 {
		return false
	}
	// Total failure with cleanup enabled: proceed to the deletion contract
	// regardless of template processing. Deletion takes precedence over any
	// template-rendered static content (the caller forces an empty dataset).
	if allDataSourcesFailed && cleanupOnFailure {
		return false
	}
	// Partial failure without template: merge strategy applies, do not skip.
	if secretMapLen > 0 && !templateProcessed {
		return false
	}
	return true
}

// hasDeclaredSourcesButZeroOutput is the zero-output fail-closed guard
// predicate (O-2): declared sources all succeeded yet produced zero keys
// (e.g. an extract whose backend document was emptied); the write is skipped
// and deletion withheld so already-synced data is never destroyed. Must run
// on the PRE-template secretMap (static template content would mask it).
// Requires failedKeyCount==0, so it cannot preempt any data-source failure
// contract.
func hasDeclaredSourcesButZeroOutput(totalDataSources, failedKeyCount, producedKeys int) bool {
	return totalDataSources > 0 && failedKeyCount == 0 && producedKeys == 0
}

// hasNoDeclaredSourcesButExistingData is the zero-output guard predicate
// (O-3): no data source declared but a Secret with data exists; the write is
// withheld so the existing data is retained. templateConfigured exempts
// template-only specs whose output is only known after rendering (covered by
// templateRenderedZeroOutput). Mutually exclusive with the failure contracts
// like O-2.
func hasNoDeclaredSourcesButExistingData(totalDataSources int, templateConfigured bool, existingKeys int) bool {
	return totalDataSources == 0 && !templateConfigured && existingKeys > 0
}

// templateRenderedZeroOutput is the post-template zero-output fail-closed
// guard: no data-source failures, rendering produced zero data keys, and there
// is something to protect (non-empty pre-template data, or an existing Secret
// with data); the write is skipped and deletion withheld. It closes the gap
// the pre-template guard cannot see: Replace-mode execution failures clearing
// result.Data, Data-targeted templateFrom rendering zero keys, and source-less
// template-only specs (the post-template form of O-3). Runs only after
// successful template processing and requires failedKeyCount==0, so it cannot
// preempt the failure contracts.
func templateRenderedZeroOutput(failedKeyCount, postTemplateKeys, preTemplateKeys, existingKeys int) bool {
	return failedKeyCount == 0 && postTemplateKeys == 0 && (preTemplateKeys > 0 || existingKeys > 0)
}

// mergeWithFailedKeys builds the final dataset for a partial-failure round: it
// copies secretMap (successful keys carry their fresh values) and retains
// previous values from currentData -- only the known failed target keys
// (failedEntryKeys) when retainAllUnmapped is false, or every currentData key
// absent from secretMap (a safe superset) when a failed extract entry makes
// the produced key set statically unrecoverable. Known side effect: under
// superset retention, stale keys from removed spec entries persist until the
// next fully successful round (expected behavior). Inputs are never mutated.
func mergeWithFailedKeys(secretMap, currentData map[string][]byte, failedEntryKeys []string, retainAllUnmapped bool) map[string][]byte {
	merged := make(map[string][]byte, len(secretMap)+len(failedEntryKeys))
	for k, v := range secretMap {
		merged[k] = v
	}
	if retainAllUnmapped {
		// Conservative superset retention: keep every old key that this
		// round's successful entries did not overwrite, because the failed
		// extract entry's own key set cannot be enumerated statically.
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
//   - spec.data: jmesPath entries emit one key per ObjectAlias (an empty alias
//     falls back to data.Key), plus the ResolveTargetKey fallback used when
//     jmes parsing fails or yields no match; without jmesPath the target key
//     is common.ResolveTargetKey;
//   - spec.dataProcess: a failed extract entry's produced key set cannot be
//     reconstructed statically (the success path emits the document's full
//     top-level key set), so retainAllUnmapped is returned true and the merge
//     relaxes to a conservative superset. The countFailedSources twin
//     exemption is key-level only; the merge layer stays conservative.
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
			// Success-path fallback of common.ProcessExternalSecretData: when
			// jmes parsing fails or yields no match, the raw value is written
			// under ResolveTargetKey (name, falling back to key) instead of
			// the aliases. Retaining one extra key is safe in the retention
			// direction (the merge never drops a key it should keep).
			keySet[common.ResolveTargetKey(&data)] = struct{}{}
			continue
		}
		// Share the single key-derivation implementation with the success path.
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
		// A failed extract entry's produced key set cannot be reconstructed
		// statically: on success the entry emits the document's full top-level
		// key set, and jmesPath only renames the keys matching jp.Path. Relax
		// the merge to retain all unmapped keys (a safe superset). The known
		// side effect -- stale keys from spec entries removed by the user are
		// temporarily retained until the next fully successful round -- is
		// documented on mergeWithFailedKeys.
		retainAllUnmapped = true
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, retainAllUnmapped
}

// collectFailedKeys returns the sorted list of data keys that failed to sync.
func collectFailedKeys(dataErrorMap, extractDataErrorMap map[string]error) []string {
	keys := make([]string, 0, len(dataErrorMap)+len(extractDataErrorMap))
	for k := range dataErrorMap {
		keys = append(keys, k)
	}
	for k := range extractDataErrorMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jmesPathTargetKeys derives the Secret keys a jmesPath-configured entry
// emits on success: one key per ObjectAlias, where an empty ObjectAlias
// falls back to data.Key (utils.GetJsonSecrets), mirroring
// ResolveTargetKey's empty-Name fallback. Shared by
// collectFailedEntryTargetKeys (failed-key derivation) and
// dataEntryTargetCovered (coverage check) so both stay aligned with the
// success path (common.ProcessExternalSecretData / utils.GetJsonSecrets).
func jmesPathTargetKeys(data *api.DataSource) []string {
	aliases := make([]string, 0, len(data.JMESPath))
	for _, jp := range data.JMESPath {
		if jp.ObjectAlias != "" {
			aliases = append(aliases, jp.ObjectAlias)
		} else {
			// Success-path fallback for an empty ObjectAlias: the value is
			// emitted under data.Key (never under an illegal empty key).
			aliases = append(aliases, data.Key)
		}
	}
	return aliases
}

// dataEntryTargetCovered reports whether a failed spec.data entry is exempt
// from failure counting and status reporting (canonical twin-exemption
// definition). The exemption requires BOTH conditions:
//   - successful twin: the same data.Key was successfully fetched this round
//     (present in succeededDataKeys);
//   - target coverage: the entry's target Secret key(s) already carry
//     successful output in secretMap.
//
// The twin precondition prevents cross-source target-key collisions (secretMap
// merges spec.data and dataProcess outputs) from wrongly exempting a genuinely
// failed entry. Target key derivation mirrors the success path (jmesPath
// aliases with fallback to ResolveTargetKey when parsing fails; coverage
// accepts either form).
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
		// Fallback form of the success path: the twin's jmes parsing failed
		// or matched nothing, so its output landed under ResolveTargetKey.
		// Symmetric with collectFailedEntryTargetKeys, which retains the
		// fallback key on the merge side.
		_, ok := secretMap[common.ResolveTargetKey(data)]
		return ok
	}
	_, ok := secretMap[common.ResolveTargetKey(data)]
	return ok
}

// failedUncoveredEntries evaluates every spec.data entry and returns a slice
// aligned with externalSec.Spec.Data: position i is true when entry i is
// present in dataErrorMap (keyed by data.Key) AND is NOT exempt per
// dataEntryTargetCovered (a successful twin for the same data.Key exists AND
// the target output is covered) -- i.e. the entry is a genuine failure for
// per-entry counting and status reporting purposes.
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
