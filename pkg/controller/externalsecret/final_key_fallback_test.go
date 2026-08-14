package externalsecret

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
)

// TestFinalKeyFallbackConsistency ensures the success-path key derivation
// (common.ResolveTargetKey, used by getExternalSecret and by every provider's
// ProcessExternalSecretData) matches the failed-entry key derivation in
// collectFailedEntryTargetKeys for non-jmesPath entries, including the
// documented "omit name -> use key" fallback.
func TestFinalKeyFallbackConsistency(t *testing.T) {
	entries := []api.DataSource{
		{Key: "kms-secret-a", Name: "username"}, // explicit name
		{Key: "kms-secret-b"},                   // omitted name -> fallback to key
		{Key: "oos-param", Name: "oos-param"},   // name equal to key
	}

	es := &api.ExternalSecret{
		Spec: api.ExternalSecretSpec{Data: entries},
	}
	dataErrors := make(map[string]error)
	for _, d := range entries {
		dataErrors[d.Key] = fmt.Errorf("boom")
	}

	failedKeys, retainAll := collectFailedEntryTargetKeys(es, dataErrors, map[string]error{})
	if retainAll {
		t.Fatalf("expected retainAllUnmapped=false without failed extract entries")
	}
	if len(failedKeys) != len(entries) {
		t.Fatalf("expected %d failed target keys, got %v", len(entries), failedKeys)
	}

	failedSet := make(map[string]struct{}, len(failedKeys))
	for _, k := range failedKeys {
		failedSet[k] = struct{}{}
	}
	for i := range entries {
		successKey := common.ResolveTargetKey(&entries[i])
		if successKey == "" {
			t.Fatalf("success path produced empty key for entry %v", entries[i])
		}
		if _, ok := failedSet[successKey]; !ok {
			t.Errorf("success-path key %q (entry key=%q name=%q) not produced by failed-path derivation %v",
				successKey, entries[i].Key, entries[i].Name, failedKeys)
		}
	}
}

// TestJmesFailedEntryTargetKeysIncludeFallback verifies the symmetry fix for
// jmesPath entries: common.ProcessExternalSecretData falls back to writing
// the raw value under ResolveTargetKey when jmes parsing fails or yields no
// match, so the failed-entry target-key derivation must include that
// fallback key in addition to the ObjectAlias keys.
func TestJmesFailedEntryTargetKeysIncludeFallback(t *testing.T) {
	entries := []api.DataSource{
		// Fallback key == data.Key (name omitted).
		{Key: "doc-key", JMESPath: []api.JMESPathObject{{Path: "user", ObjectAlias: "alias-user"}}},
		// Fallback key == explicit name, distinct from key and aliases.
		{Key: "doc-key-2", Name: "raw-doc", JMESPath: []api.JMESPathObject{
			{Path: "user", ObjectAlias: "alias-user-2"},
			{Path: "pass", ObjectAlias: ""}, // empty alias -> data.Key on the success path
		}},
	}
	es := &api.ExternalSecret{Spec: api.ExternalSecretSpec{Data: entries}}
	dataErrors := map[string]error{}
	for _, d := range entries {
		dataErrors[d.Key] = fmt.Errorf("boom")
	}

	failedKeys, retainAll := collectFailedEntryTargetKeys(es, dataErrors, map[string]error{})
	if retainAll {
		t.Fatalf("expected retainAllUnmapped=false without failed extract entries")
	}
	expected := []string{
		"alias-user",   // entry 1 alias
		"alias-user-2", // entry 2 alias
		"doc-key",      // entry 1 fallback (ResolveTargetKey)
		"doc-key-2",    // entry 2 empty-alias fallback
		"raw-doc",      // entry 2 fallback (ResolveTargetKey)
	}
	if !reflect.DeepEqual(failedKeys, expected) {
		t.Fatalf("failed target keys = %v, expected %v (sorted)", failedKeys, expected)
	}
}

// TestPartialFailureMergeRetainsJmesFallbackKey is the chain-level regression:
// a failed jmes entry whose value was PREVIOUSLY written under the fallback
// key (a prior round's jmes parsing fell back to ResolveTargetKey) must keep
// that old value on a partial-failure merge round, exactly like any other
// failed entry's retained key.
func TestPartialFailureMergeRetainsJmesFallbackKey(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "jmes-fb-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data: []api.DataSource{
				{Key: "good-key"},
				{Key: "jmes-key", JMESPath: []api.JMESPathObject{{Path: "user", ObjectAlias: "alias-user"}}},
			},
			Target: &api.ExternalSecretTarget{Name: "jmes-fb-secret"},
		},
	}
	// A previous round's jmes parsing fell back to the raw value under the
	// ResolveTargetKey key ("jmes-key"); that old value must survive the
	// partial-failure merge.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "jmes-fb-secret", Namespace: "default"},
		Data: map[string][]byte{
			"good-key": []byte("old-good"),
			"jmes-key": []byte("old-raw-doc"),
		},
	}
	sc := &fakeSecretClient{
		dataByKey: map[string]map[string][]byte{"good-key": {"good-key": []byte("fresh")}},
		failByKey: map[string]error{"jmes-key": fmt.Errorf("transient failure")},
	}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if !updated {
		t.Fatalf("partial failure without template must merge and write")
	}
	got := getTestSecret(t, r, "default", "jmes-fb-secret")
	if string(got.Data["good-key"]) != "fresh" {
		t.Fatalf("successful key must carry the fresh value, got %v", got.Data)
	}
	if string(got.Data["jmes-key"]) != "old-raw-doc" {
		t.Fatalf("failed jmes entry's previous fallback-key value must be retained, got %v", got.Data)
	}
}
