package externalsecret

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// These tests compose the real template processor with the real Secret operation
// handler. This covers template source lookup, rendering, target-specific key
// filtering, and final Secret create/update persistence while intentionally
// excluding provider fetch, status, and requeue behavior unrelated to key rules.
func TestInlineTemplateMetadataKeysPersistToCreatedSecret(t *testing.T) {
	prefix253 := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("b", 63),
		strings.Repeat("c", 63),
		strings.Repeat("d", 61),
	}, ".")
	prefix254 := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("b", 63),
		strings.Repeat("c", 63),
		strings.Repeat("d", 62),
	}, ".")
	name63 := strings.Repeat("n", 63)
	name64 := strings.Repeat("n", 64)
	if len(prefix253) != 253 || len(prefix254) != 254 {
		t.Fatalf("test setup produced prefix lengths %d and %d, want 253 and 254", len(prefix253), len(prefix254))
	}

	validLabelPrefix := prefix253 + "/n"
	invalidLabelPrefix := prefix254 + "/n"
	validLabelName := "example.com/" + name63
	invalidLabelName := "example.com/" + name64
	validLabelTotal317 := prefix253 + "/" + strings.ToUpper(name63)

	upperPrefix253 := strings.ToUpper(prefix253)
	upperPrefix254 := strings.ToUpper(prefix254)
	validAnnotationPrefix := upperPrefix253 + "/N"
	invalidAnnotationPrefix := upperPrefix254 + "/N"
	validAnnotationName := "Example.com/" + strings.ToUpper(name63)
	invalidAnnotationName := "Example.com/" + strings.ToUpper(name64)
	validAnnotationTotal317 := upperPrefix253 + "/" + strings.ToUpper(name63)
	if len(validLabelTotal317) != 317 || len(validAnnotationTotal317) != 317 {
		t.Fatalf("test setup produced total key lengths %d and %d, want 317", len(validLabelTotal317), len(validAnnotationTotal317))
	}

	labels := map[string]string{
		"istio/multiCluster": "mixed-case-name",
		"Example.com/name":   "must-be-rejected-as-label",
		validLabelPrefix:     "prefix-253",
		invalidLabelPrefix:   "prefix-254",
		validLabelName:       "name-63",
		invalidLabelName:     "name-64",
		validLabelTotal317:   "total-317",
	}
	annotations := map[string]string{
		"Example.com/name":      "original-case",
		validAnnotationPrefix:   "prefix-253",
		invalidAnnotationPrefix: "prefix-254",
		validAnnotationName:     "name-63",
		invalidAnnotationName:   "name-64",
		validAnnotationTotal317: "total-317",
	}
	es := buildTestExternalSecret(&api.ExternalSecretTemplate{
		Metadata: &api.ExternalSecretTemplateMetadata{
			Labels:      labels,
			Annotations: annotations,
		},
	})
	c := newTemplateSourceClient(t)
	got := processTemplatesAndPersistSecret(t, c, es, map[string][]byte{"raw": []byte("raw-value")})

	assertStringMapEntries(t, "Secret labels", got.Labels, map[string]string{
		"istio/multiCluster": "mixed-case-name",
		validLabelPrefix:     "prefix-253",
		validLabelName:       "name-63",
		validLabelTotal317:   "total-317",
	})
	assertStringMapMissing(t, "Secret labels", got.Labels, []string{
		"Example.com/name",
		invalidLabelPrefix,
		invalidLabelName,
	})
	assertStringMapEntries(t, "Secret annotations", got.Annotations, map[string]string{
		"Example.com/name":      "original-case",
		validAnnotationPrefix:   "prefix-253",
		validAnnotationName:     "name-63",
		validAnnotationTotal317: "total-317",
	})
	assertStringMapMissing(t, "Secret annotations", got.Annotations, []string{
		"example.com/name",
		invalidAnnotationPrefix,
		invalidAnnotationName,
	})
	assertDataMapEntries(t, got.Data, map[string]string{"raw": "raw-value"})
}

func TestTemplateFromKeysAndValuesUseTargetValidatorsOnUpdatedSecret(t *testing.T) {
	maxDataKey := strings.Repeat("k", 253)
	tooLongDataKey := strings.Repeat("k", 254)
	tooLongQualifiedName := "example.com/" + strings.Repeat("n", 64)

	labelTemplate := strings.Join([]string{
		"istio/multiCluster=label-value",
		"Example.com/name=invalid-label-value",
	}, "\n")
	annotationTemplate := strings.Join([]string{
		"Example.com/name=annotation-value",
		tooLongQualifiedName + "=invalid-annotation-value",
	}, "\n")
	dataTemplate := strings.Join([]string{
		".=dot-only",
		"..=dot-dot",
		"..hidden=hidden-value",
		".dotfile=dotfile",
		"_underscore=underscore",
		"-dash=dash",
		maxDataKey + "=max-length",
		tooLongDataKey + "=too-long",
		"istio/multiCluster=slash-is-invalid-for-data",
	}, "\n")

	tests := []struct {
		name        string
		useSecret   bool
		mergePolicy api.TemplateMergePolicy
	}{
		{name: "ConfigMap source with Replace policy", mergePolicy: api.MergePolicyReplace},
		{name: "Secret source with Merge policy", useSecret: true, mergePolicy: api.MergePolicyMerge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const sourceName = "key-template-source"
			templateBodies := map[string]string{
				"labels.tpl":      labelTemplate,
				"annotations.tpl": annotationTemplate,
				"data.tpl":        dataTemplate,
			}

			var source client.Object
			if tt.useSecret {
				secretData := make(map[string][]byte, len(templateBodies))
				for key, value := range templateBodies {
					secretData[key] = []byte(value)
				}
				source = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: sourceName, Namespace: "default"},
					Data:       secretData,
				}
			} else {
				source = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: sourceName, Namespace: "default"},
					Data:       templateBodies,
				}
			}

			newTemplateFrom := func(key string, target api.TemplateTarget) api.TemplateFrom {
				ref := &api.TemplateRef{
					Name: sourceName,
					Items: []api.TemplateRefItem{{
						Key:        key,
						TemplateAs: api.TemplateScopeKeysAndValues,
					}},
				}
				entry := api.TemplateFrom{Target: target}
				if tt.useSecret {
					entry.Secret = ref
				} else {
					entry.ConfigMap = ref
				}
				return entry
			}

			es := buildTestExternalSecret(&api.ExternalSecretTemplate{
				MergePolicy: tt.mergePolicy,
				TemplateFrom: []api.TemplateFrom{
					newTemplateFrom("labels.tpl", api.TemplateTargetLabels),
					newTemplateFrom("annotations.tpl", api.TemplateTargetAnnotations),
					newTemplateFrom("data.tpl", api.TemplateTargetData),
				},
			})
			existingTarget := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        es.Spec.Target.Name,
					Namespace:   es.Namespace,
					Labels:      map[string]string{"existing-label": "preserved"},
					Annotations: map[string]string{"existing-annotation": "preserved"},
				},
				Data: map[string][]byte{"existing-data": []byte("replaced")},
			}
			c := newTemplateSourceClient(t, source, existingTarget)
			got := processTemplatesAndPersistSecret(t, c, es, map[string][]byte{"raw": []byte("raw-value")})

			assertStringMapEntries(t, "Secret labels", got.Labels, map[string]string{
				"existing-label":     "preserved",
				"istio/multiCluster": "label-value",
			})
			assertStringMapMissing(t, "Secret labels", got.Labels, []string{"Example.com/name"})
			assertStringMapEntries(t, "Secret annotations", got.Annotations, map[string]string{
				"existing-annotation": "preserved",
				"Example.com/name":    "annotation-value",
			})
			assertStringMapMissing(t, "Secret annotations", got.Annotations, []string{
				"example.com/name",
				tooLongQualifiedName,
			})
			assertDataMapEntries(t, got.Data, map[string]string{
				".dotfile":    "dotfile",
				"_underscore": "underscore",
				"-dash":       "dash",
				maxDataKey:    "max-length",
			})
			assertDataMapMissing(t, got.Data, []string{
				".",
				"..",
				"..hidden",
				tooLongDataKey,
				"istio/multiCluster",
			})

			// Gap 1: Replace clears raw data; Merge preserves it alongside
			// valid template output.
			if tt.mergePolicy == api.MergePolicyReplace {
				assertDataMapMissing(t, got.Data, []string{"raw"})
			} else {
				assertDataMapEntries(t, got.Data, map[string]string{"raw": "raw-value"})
			}
		})
	}
}

// Invalid inline keys are not rendered, persisted, or included in the managed-keys annotation.
func TestInlineTemplateDataKeyValidation(t *testing.T) {
	for _, policy := range []api.TemplateMergePolicy{api.MergePolicyReplace, api.MergePolicyMerge} {
		t.Run(string(policy), func(t *testing.T) {
			es := buildTestExternalSecret(&api.ExternalSecretTemplate{
				MergePolicy: policy,
				Data: map[string]string{
					"valid":                  "{{ .raw }}",
					"bad key":                "{{", // Invalid keys must be filtered before template parsing can fail.
					"..hidden":               "{{",
					strings.Repeat("k", 254): "{{",
				},
			})
			got := processTemplatesAndPersistSecret(t, newTemplateSourceClient(t), es, map[string][]byte{"raw": []byte("value")})
			assertDataMapEntries(t, got.Data, map[string]string{"valid": "value"})
			assertDataMapMissing(t, got.Data, []string{"bad key", "..hidden", strings.Repeat("k", 254)})
			if got.Annotations[ManagedKeysAnnotation] != "valid" {
				t.Fatalf("托管键注解包含非法键：%q", got.Annotations[ManagedKeysAnnotation])
			}
			if policy == api.MergePolicyReplace {
				assertDataMapMissing(t, got.Data, []string{"raw"})
			} else {
				assertDataMapEntries(t, got.Data, map[string]string{"raw": "value"})
			}
		})
	}
}

func processTemplatesAndPersistSecret(
	t *testing.T,
	c client.Client,
	es *api.ExternalSecret,
	rawData map[string][]byte,
) *corev1.Secret {
	t.Helper()
	ctx := context.Background()
	result, err := NewSimpleTemplateProcessor(c).ProcessAllTemplates(ctx, es, rawData)
	if err != nil {
		t.Fatalf("ProcessAllTemplates returned error: %v", err)
	}

	var metadataTargets map[string]map[string]string
	if len(result.Metadata.Labels) > 0 || len(result.Metadata.Annotations) > 0 {
		metadataTargets = make(map[string]map[string]string, 2)
		if len(result.Metadata.Labels) > 0 {
			metadataTargets["labels"] = result.Metadata.Labels
		}
		if len(result.Metadata.Annotations) > 0 {
			metadataTargets["annotations"] = result.Metadata.Annotations
		}
	}

	handler := NewSimpleSecretOperationHandler(c, false, logr.Discard())
	if err := handler.HandleSecretOperation(ctx, es, result.Data, nil, metadataTargets); err != nil {
		t.Fatalf("HandleSecretOperation returned error: %v", err)
	}

	secretName := es.Name
	if es.Spec.Target != nil && es.Spec.Target.Name != "" {
		secretName = es.Spec.Target.Name
	}
	got := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: es.Namespace, Name: secretName}, got); err != nil {
		t.Fatalf("get persisted Secret %s/%s: %v", es.Namespace, secretName, err)
	}
	return got
}

func assertStringMapEntries(t *testing.T, field string, got, want map[string]string) {
	t.Helper()
	for key, wantValue := range want {
		gotValue, exists := got[key]
		if !exists {
			t.Errorf("%s missing expected key %q", field, key)
			continue
		}
		if gotValue != wantValue {
			t.Errorf("%s key %q = %q, want %q", field, key, gotValue, wantValue)
		}
	}
}

func assertStringMapMissing(t *testing.T, field string, got map[string]string, keys []string) {
	t.Helper()
	for _, key := range keys {
		if value, exists := got[key]; exists {
			t.Errorf("%s unexpectedly retained rejected key %q with value %q", field, key, value)
		}
	}
}

func assertDataMapEntries(t *testing.T, got map[string][]byte, want map[string]string) {
	t.Helper()
	for key, wantValue := range want {
		gotValue, exists := got[key]
		if !exists {
			t.Errorf("Secret data missing expected key %q", key)
			continue
		}
		if string(gotValue) != wantValue {
			t.Errorf("Secret data key %q = %q, want %q", key, string(gotValue), wantValue)
		}
	}
}

func assertDataMapMissing(t *testing.T, got map[string][]byte, keys []string) {
	t.Helper()
	for _, key := range keys {
		if value, exists := got[key]; exists {
			t.Errorf("Secret data unexpectedly retained rejected key %q with value %q", key, string(value))
		}
	}
}
