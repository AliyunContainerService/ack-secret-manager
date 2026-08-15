// Copyright © 2025 Alibaba Cloud. All rights reserved.

// managed_metadata_test.go covers the metadata-only templateFrom rule:
// in Replace mode a templateFrom
// list that only targets Labels/Annotations must NOT clear the raw data,
// while a templateFrom containing a Data target still clears it.

package externalsecret

import (
	"context"
	"testing"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// TestReplaceWithMetadataOnlyTemplateFromPreservesRawData pins the
// metadata-only rule: in Replace mode a templateFrom list that only targets
// Labels/Annotations must NOT clear the raw data, while a templateFrom
// containing a Data target still clears it.
func TestReplaceWithMetadataOnlyTemplateFromPreservesRawData(t *testing.T) {
	rawData := map[string][]byte{"raw-key": []byte("raw-value")}

	t.Run("metadata-only templateFrom keeps raw data", func(t *testing.T) {
		stp := NewSimpleTemplateProcessor(nil)
		es := buildTestExternalSecret(&api.ExternalSecretTemplate{
			// MergePolicy omitted -> Replace (default)
			TemplateFrom: []api.TemplateFrom{
				{Literal: strPtr("app=v1"), Target: api.TemplateTargetAnnotations},
				{Literal: strPtr("tier=web"), Target: api.TemplateTargetLabels},
			},
		})
		result, err := stp.ProcessAllTemplates(context.Background(), es, rawData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := string(result.Data["raw-key"]); got != "raw-value" {
			t.Fatalf("raw data must be preserved, got Data=%v", result.Data)
		}
		// Literal templateFrom entries render into the fixed "literal" key per target.
		if result.Metadata.Annotations["literal"] != "app=v1" {
			t.Fatalf("expected annotation literal=app=v1, got %v", result.Metadata.Annotations)
		}
		if result.Metadata.Labels["literal"] != "tier=web" {
			t.Fatalf("expected label literal=tier=web, got %v", result.Metadata.Labels)
		}
	})

	t.Run("templateFrom with Data target still clears raw data", func(t *testing.T) {
		stp := NewSimpleTemplateProcessor(nil)
		es := buildTestExternalSecret(&api.ExternalSecretTemplate{
			TemplateFrom: []api.TemplateFrom{
				{Literal: strPtr("tpl=rendered"), Target: api.TemplateTargetData},
				{Literal: strPtr("app=v1"), Target: api.TemplateTargetAnnotations},
			},
		})
		result, err := stp.ProcessAllTemplates(context.Background(), es, rawData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result.Data["raw-key"]; ok {
			t.Fatalf("Replace mode with a Data target must clear raw data, got %v", result.Data)
		}
		// A literal templateFrom entry renders into the fixed "literal" key.
		if got := string(result.Data["literal"]); got != "tpl=rendered" {
			t.Fatalf("expected literal=tpl=rendered, got %v", result.Data)
		}
	})

	t.Run("merge mode keeps raw data regardless of targets", func(t *testing.T) {
		stp := NewSimpleTemplateProcessor(nil)
		es := buildTestExternalSecret(&api.ExternalSecretTemplate{
			MergePolicy: api.MergePolicyMerge,
			TemplateFrom: []api.TemplateFrom{
				{Literal: strPtr("tpl=rendered"), Target: api.TemplateTargetData},
			},
		})
		result, err := stp.ProcessAllTemplates(context.Background(), es, rawData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := string(result.Data["raw-key"]); got != "raw-value" {
			t.Fatalf("Merge mode must keep raw data, got %v", result.Data)
		}
		// A literal templateFrom entry renders into the fixed "literal" key.
		if got := string(result.Data["literal"]); got != "tpl=rendered" {
			t.Fatalf("expected literal=tpl=rendered, got %v", result.Data)
		}
	})
}
