// Copyright © 2025 Alibaba Cloud. All rights reserved.

package externalsecret

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/Masterminds/sprig/v3"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// SimpleTemplateProcessor renders ExternalSecret templates
type SimpleTemplateProcessor struct {
	Client  client.Client
	funcMap template.FuncMap // cached to avoid recreation on each call
}

// NewSimpleTemplateProcessor creates a template processor with the function map initialized
func NewSimpleTemplateProcessor(client client.Client) *SimpleTemplateProcessor {
	stp := &SimpleTemplateProcessor{Client: client}
	stp.funcMap = stp.createFuncMap()
	return stp
}

const (
	// ManagedKeysAnnotation stores the managed secret keys
	ManagedKeysAnnotation = "ack-alibabacloud.com/managed-keys"
)

// Sentinel errors distinguishing PARSE failures (fatal: structurally
// invalid, can never render) from EXECUTION failures (non-fatal per the
// established grading). Callers classify via errors.Is.
var (
	errTemplateParse     = stderrors.New("failed to parse template")
	errTemplateExecution = stderrors.New("template execution failed")
)

// UnifiedTemplateResult represents the complete result of all template processing
type UnifiedTemplateResult struct {
	Data map[string][]byte

	// Metadata applied to the target secret
	Metadata struct {
		Labels      map[string]string
		Annotations map[string]string
	}

	Stats struct {
		DataTemplatesProcessed     int
		TemplateFromProcessed      int
		MetadataTemplatesProcessed int
		Errors                     []string
		FatalErrors                []string
	}
}

// ProcessAllTemplates processes all template configurations with context
func (stp *SimpleTemplateProcessor) ProcessAllTemplates(
	ctx context.Context,
	externalSec *api.ExternalSecret,
	rawData map[string][]byte,
) (*UnifiedTemplateResult, error) {

	// Deletion flow intentionally bypasses templates so raw data alone
	// drives the round; re-rendering would re-add managed keys the deletion
	// flow removed.
	if externalSec.GetDeletionTimestamp() != nil {
		result := &UnifiedTemplateResult{
			Data: make(map[string][]byte),
			Metadata: struct {
				Labels      map[string]string
				Annotations map[string]string
			}{
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
		}

		for k, v := range rawData {
			result.Data[k] = v
		}

		return result, nil
	}

	// No template configuration: return raw data
	if externalSec.Spec.Target == nil || externalSec.Spec.Target.Template == nil {
		result := &UnifiedTemplateResult{
			Data: make(map[string][]byte),
			Metadata: struct {
				Labels      map[string]string
				Annotations map[string]string
			}{
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
		}
		for k, v := range rawData {
			result.Data[k] = v
		}
		return result, nil
	}

	// Build unified template context
	templateData := make(map[string]string)
	for k, v := range rawData {
		templateData[k] = string(v)
	}

	interfaceData := make(map[string]interface{})
	for k, v := range templateData {
		interfaceData[k] = v
	}

	result := &UnifiedTemplateResult{
		Data: make(map[string][]byte),
		Metadata: struct {
			Labels      map[string]string
			Annotations map[string]string
		}{
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}

	// Raw data as fallback
	for k, v := range rawData {
		result.Data[k] = v
	}

	// Managed keys feed the ManagedKeysAnnotation written below
	managedKeys := make(map[string]bool)

	// Inline data templates (Template.Data)
	if externalSec.Spec.Target.Template.Data != nil {
		// Empty map preserves raw data; non-empty replaces it with templates
		if len(externalSec.Spec.Target.Template.Data) > 0 {
			if externalSec.Spec.Target.Template.MergePolicy == "" ||
				externalSec.Spec.Target.Template.MergePolicy == api.MergePolicyReplace {
				// Replace mode clears raw data; Merge mode keeps it and
				// templates override same keys
				clear(result.Data)
			}

			for key, templateStr := range externalSec.Spec.Target.Template.Data {
				processedValue, err := stp.executeTemplate(templateStr, interfaceData)
				if err != nil {
					if stderrors.Is(err, errTemplateParse) {
						result.Stats.FatalErrors = append(result.Stats.FatalErrors,
							fmt.Sprintf("data template %s parse failed: %v", key, err))
					} else {
						result.Stats.Errors = append(result.Stats.Errors,
							fmt.Sprintf("data template %s execution failed: %v", key, err))
						continue
					}
				} else {
					result.Data[key] = processedValue
					result.Stats.DataTemplatesProcessed++
					managedKeys[key] = true
				}
			}
		}
	}

	// TemplateFrom references
	if len(externalSec.Spec.Target.Template.TemplateFrom) > 0 {
		templateFromResults, err := stp.processTemplateFromByTarget(ctx, externalSec, interfaceData)
		if err != nil {
			result.Stats.FatalErrors = append(result.Stats.FatalErrors,
				fmt.Sprintf("TemplateFrom processing failed: %v", err))
		} else {
			hasTemplateData := len(externalSec.Spec.Target.Template.Data) > 0
			isReplaceMode := externalSec.Spec.Target.Template.MergePolicy == "" ||
				externalSec.Spec.Target.Template.MergePolicy == api.MergePolicyReplace

			// Presence of the "Data" target is checked by KEY (not output): a
			// Data-targeted templateFrom keeps Replace semantics even when it
			// yields zero keys; Labels/Annotations-only lists must not clear
			// raw data (O-1).
			_, hasDataTarget := templateFromResults[string(api.TemplateTargetData)]

			// Clear only when Replace mode AND no inline Template.Data was
			// processed (avoid double-clearing) AND some entry targets Data
			if isReplaceMode && !hasTemplateData && hasDataTarget {
				clear(result.Data)
			}

			for target, targetData := range templateFromResults {
				switch api.TemplateTarget(target) {
				case api.TemplateTargetAnnotations:
					for k, v := range targetData {
						if stp.isValidAnnotationKey(k) {
							result.Metadata.Annotations[k] = string(v)
						} else {
							klog.Warningf("templateFrom: dropping annotation key %q: invalid annotation key format", k)
						}
					}
				case api.TemplateTargetLabels:
					for k, v := range targetData {
						if stp.isValidLabelKey(k) {
							result.Metadata.Labels[k] = string(v)
						} else {
							klog.Warningf("templateFrom: dropping label key %q: invalid label key format", k)
						}
					}
				case api.TemplateTargetData, "":
					fallthrough
				default:
					for k, v := range targetData {
						if stp.isValidSecretKey(k) {
							result.Data[k] = v
							result.Stats.DataTemplatesProcessed++
							managedKeys[k] = true
						} else {
							klog.Warningf("templateFrom: dropping data key %q: invalid secret data key format", k)
						}
					}
				}
				result.Stats.TemplateFromProcessed++
			}
		}
	}

	// Add managed keys annotation
	if len(managedKeys) > 0 {
		keysList := make([]string, 0, len(managedKeys))
		for key := range managedKeys {
			keysList = append(keysList, key)
		}
		// Deterministic output
		sort.Strings(keysList)
		result.Metadata.Annotations[ManagedKeysAnnotation] = strings.Join(keysList, ",")
	}

	// Template.Metadata
	if externalSec.Spec.Target.Template.Metadata != nil {
		if len(externalSec.Spec.Target.Template.Metadata.Labels) > 0 {
			for key, templateStr := range externalSec.Spec.Target.Template.Metadata.Labels {
				processedValue, err := stp.executeTemplate(templateStr, interfaceData)
				if err != nil {
					result.Stats.Errors = append(result.Stats.Errors,
						fmt.Sprintf("metadata label template %s processing failed: %v", key, err))
					continue
				}
				if stp.isValidLabelKey(key) {
					result.Metadata.Labels[key] = string(processedValue)
					result.Stats.MetadataTemplatesProcessed++
				} else {
					klog.Warningf("metadata template: dropping label key %q: invalid label key format", key)
				}
			}
		}

		if len(externalSec.Spec.Target.Template.Metadata.Annotations) > 0 {
			for key, templateStr := range externalSec.Spec.Target.Template.Metadata.Annotations {
				processedValue, err := stp.executeTemplate(templateStr, interfaceData)
				if err != nil {
					result.Stats.Errors = append(result.Stats.Errors,
						fmt.Sprintf("metadata annotation template %s processing failed: %v", key, err))
					continue
				}
				if stp.isValidAnnotationKey(key) {
					result.Metadata.Annotations[key] = string(processedValue)
					result.Stats.MetadataTemplatesProcessed++
				} else {
					klog.Warningf("metadata template: dropping annotation key %q: invalid annotation key format", key)
				}
			}
		}
	}

	// Fatal errors abort the round
	if len(result.Stats.FatalErrors) > 0 {
		var errorMsg strings.Builder
		errorMsg.WriteString("fatal template processing errors: ")
		for i, errMsg := range result.Stats.FatalErrors {
			if i > 0 {
				errorMsg.WriteString("; ")
			}
			errorMsg.WriteString(errMsg)
		}
		return result, fmt.Errorf("%s", errorMsg.String())
	}

	return result, nil
}

// createFuncMap creates the function map for template execution
func (stp *SimpleTemplateProcessor) createFuncMap() template.FuncMap {
	funcMap := sprig.TxtFuncMap()

	// Custom functions not available in Sprig

	// Bcrypt password hashing
	funcMap["bcrypt"] = func(password string, cost ...int) string {
		hashCost := bcrypt.DefaultCost
		if len(cost) > 0 {
			hashCost = cost[0]
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), hashCost)
		if err != nil {
			klog.Warningf("bcrypt hash generation failed: %v", err)
			return ""
		}
		return string(hash)
	}

	// Htpasswd format for basic auth
	funcMap["htpasswd"] = func(username, password string) string {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			klog.Warningf("htpasswd bcrypt hash generation failed: %v", err)
			return ""
		}
		return fmt.Sprintf("%s:%s\n", username, string(hash))
	}

	// JSON value extraction by dot-notation path
	funcMap["jsonPath"] = func(jsonStr, path string) interface{} {
		var data interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			klog.Warningf("jsonPath: failed to unmarshal input json for path %s: %v", path, err)
			return ""
		}
		return extractJSONPath(data, path)
	}

	// Deep merge of two JSON strings
	funcMap["mergeJson"] = func(base, override string) string {
		var baseMap, overrideMap map[string]interface{}
		if err := json.Unmarshal([]byte(base), &baseMap); err != nil {
			klog.Warningf("mergeJson: failed to unmarshal base json: %v", err)
			return base
		}
		if err := json.Unmarshal([]byte(override), &overrideMap); err != nil {
			klog.Warningf("mergeJson: failed to unmarshal override json: %v", err)
			return base
		}

		merged := deepMerge(baseMap, overrideMap)
		result, err := json.Marshal(merged)
		if err != nil {
			klog.Warningf("mergeJson: failed to marshal merged result: %v", err)
			return base
		}
		return string(result)
	}

	// Parse key=value lines into a map
	funcMap["parseKeyValue"] = func(input string) map[string]string {
		result := make(map[string]string)
		lines := strings.Split(input, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
		return result
	}

	// Split string into lines
	funcMap["toLines"] = func(input string) []string {
		if input == "" {
			return []string{}
		}
		return strings.Split(strings.TrimSpace(input), "\n")
	}

	return funcMap
}

// executeTemplate executes a single template with standard Go template
// syntax and custom functions. Parse failures wrap errTemplateParse (fatal),
// execution failures wrap errTemplateExecution (non-fatal).
func (stp *SimpleTemplateProcessor) executeTemplate(templateStr string, data map[string]interface{}) ([]byte, error) {
	tmpl, err := template.New("").Funcs(stp.funcMap).Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errTemplateParse, err)
	}

	var buf bytes.Buffer
	// Passing the data map enables {{ .key }} syntax
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("%w: %v", errTemplateExecution, err)
	}

	return buf.Bytes(), nil
}

// extractJSONPath extracts a value from JSON data using a dot-notation path;
// returns "" when the path is not found or traversal fails.
func extractJSONPath(data interface{}, path string) interface{} {
	if path == "" {
		return data
	}

	parts := strings.Split(path, ".")
	current := data

	for i, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			if val, exists := v[part]; exists {
				current = val
			} else {
				currentPath := strings.Join(parts[:i+1], ".")
				klog.Warningf("jsonPath: key '%s' not found in map at path '%s', available keys: %v",
					part, currentPath, getMapKeys(v))
				return ""
			}
		case []interface{}:
			if idx, err := strconv.Atoi(part); err == nil && idx >= 0 && idx < len(v) {
				current = v[idx]
			} else {
				currentPath := strings.Join(parts[:i+1], ".")
				klog.Warningf("jsonPath: invalid array index '%s' at path '%s', array length: %d",
					part, currentPath, len(v))
				return ""
			}
		default:
			// Primitive value: cannot traverse further
			remainingPath := strings.Join(parts[i:], ".")
			klog.Warningf("jsonPath: cannot traverse path '%s', current value type is %T",
				remainingPath, current)
			return ""
		}
	}

	return current
}

// getMapKeys returns all keys from a map (for debug logging)
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// deepMerge deep-merges two maps with cycle detection
func deepMerge(base, override map[string]interface{}) map[string]interface{} {
	return deepMergeWithCycle(base, override, make(map[uintptr]bool))
}

// deepMergeWithCycle prevents infinite recursion on cyclic structures
func deepMergeWithCycle(base, override map[string]interface{}, visited map[uintptr]bool) map[string]interface{} {
	result := make(map[string]interface{})

	basePtr := reflect.ValueOf(base).Pointer()
	if visited[basePtr] {
		// Cycle detected: return base as-is
		return base
	}
	visited[basePtr] = true
	defer delete(visited, basePtr)

	for k, v := range base {
		result[k] = v
	}

	for k, v := range override {
		if baseVal, exists := base[k]; exists {
			// Recursive merge when both sides are maps
			if baseMap, ok := baseVal.(map[string]interface{}); ok {
				if overrideMap, ok := v.(map[string]interface{}); ok {
					result[k] = deepMergeWithCycle(baseMap, overrideMap, visited)
					continue
				}
			}
		}
		result[k] = v
	}

	return result
}

// processTemplateFromByTarget processes TemplateFrom references, separating results by target
func (stp *SimpleTemplateProcessor) processTemplateFromByTarget(
	ctx context.Context,
	externalSec *api.ExternalSecret,
	data map[string]interface{},
) (map[string]map[string][]byte, error) {
	results := make(map[string]map[string][]byte)

	for _, templateFrom := range externalSec.Spec.Target.Template.TemplateFrom {
		target := string(templateFrom.Target)
		if target == "" {
			target = string(api.TemplateTargetData) // Default target
		}

		if _, exists := results[target]; !exists {
			results[target] = make(map[string][]byte)
		}

		var templateData map[string][]byte
		var err error

		if templateFrom.ConfigMap != nil {
			templateData, err = stp.processConfigMapTemplate(ctx, templateFrom.ConfigMap, externalSec.Namespace, data)
		} else if templateFrom.Secret != nil {
			templateData, err = stp.processSecretTemplate(ctx, templateFrom.Secret, externalSec.Namespace, data)
		} else if templateFrom.Literal != nil {
			templateData, err = stp.processLiteralTemplate(*templateFrom.Literal, data)
		} else {
			continue
		}

		if err != nil {
			return nil, err
		}

		for k, v := range templateData {
			results[target][k] = v
		}
	}

	return results, nil
}

// processTemplateFromRef is the common implementation behind
// processConfigMapTemplate and processSecretTemplate.
func (stp *SimpleTemplateProcessor) processTemplateFromRef(
	ctx context.Context,
	templateRef *api.TemplateRef,
	namespace string,
	data map[string]interface{},
	getTemplateDataFunc func(ctx context.Context, name, namespace, key string) (string, error),
	resourceType string,
) (map[string][]byte, error) {
	result := make(map[string][]byte)

	for _, item := range templateRef.Items {
		templateValue, err := getTemplateDataFunc(ctx, templateRef.Name, namespace, item.Key)
		if err != nil {
			return nil, err
		}

		processedValue, err := stp.executeTemplate(templateValue, data)
		if err != nil {
			return nil, fmt.Errorf("failed to process %s template key %s: %w", resourceType, item.Key, err)
		}

		switch item.TemplateAs {
		case api.TemplateScopeValues:
			result[item.Key] = processedValue
		case api.TemplateScopeKeysAndValues:
			// Parse the rendered value as key=value lines
			processedValueStr := string(processedValue)

			lines := strings.Split(strings.TrimSpace(processedValueStr), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					value := strings.TrimSpace(parts[1])
					if key != "" {
						result[key] = []byte(value)
					}
				}
			}
			// KeysAndValues scope only includes the parsed pairs, not the raw value
		default:
			result[item.Key] = processedValue
		}
	}

	return result, nil
}

// processConfigMapTemplate processes a ConfigMap template reference
func (stp *SimpleTemplateProcessor) processConfigMapTemplate(
	ctx context.Context,
	templateRef *api.TemplateRef,
	namespace string,
	data map[string]interface{},
) (map[string][]byte, error) {
	getConfigMapData := func(ctx context.Context, name, namespace, key string) (string, error) {
		configMap := &corev1.ConfigMap{}
		err := stp.Client.Get(ctx, types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, configMap)
		if err != nil {
			return "", fmt.Errorf("failed to get ConfigMap %s: %w", name, err)
		}

		templateValue, exists := configMap.Data[key]
		if !exists {
			return "", fmt.Errorf("key %s not found in ConfigMap %s", key, name)
		}
		return templateValue, nil
	}

	return stp.processTemplateFromRef(ctx, templateRef, namespace, data, getConfigMapData, "ConfigMap")
}

func (stp *SimpleTemplateProcessor) processSecretTemplate(
	ctx context.Context,
	templateRef *api.TemplateRef,
	namespace string,
	data map[string]interface{},
) (map[string][]byte, error) {
	getSecretData := func(ctx context.Context, name, namespace, key string) (string, error) {
		secret := &corev1.Secret{}
		err := stp.Client.Get(ctx, types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, secret)
		if err != nil {
			return "", fmt.Errorf("failed to get Secret %s: %w", name, err)
		}

		templateValue, exists := secret.Data[key]
		if !exists {
			return "", fmt.Errorf("key %s not found in Secret %s", key, name)
		}
		return string(templateValue), nil
	}

	return stp.processTemplateFromRef(ctx, templateRef, namespace, data, getSecretData, "Secret")
}

// processLiteralTemplate processes a literal template
func (stp *SimpleTemplateProcessor) processLiteralTemplate(
	literal string,
	data map[string]interface{},
) (map[string][]byte, error) {
	processedValue, err := stp.executeTemplate(literal, data)
	if err != nil {
		return nil, fmt.Errorf("failed to process literal template: %w", err)
	}

	// Literal templates output under the fixed "literal" key
	result := map[string][]byte{
		"literal": processedValue,
	}

	return result, nil
}

// isValidAnnotationKey validates annotation key format according to Kubernetes standards
func (stp *SimpleTemplateProcessor) isValidAnnotationKey(key string) bool {
	return stp.validatePrefixedKey(key, 253) // Total max 253 chars for prefixed keys (DNS_SUBDOMAIN + / + DNS_LABEL)
}

// isValidLabelKey validates label key format according to Kubernetes standards
func (stp *SimpleTemplateProcessor) isValidLabelKey(key string) bool {
	return stp.validatePrefixedKey(key, 253) // Total max 253 chars for prefixed keys (DNS_SUBDOMAIN + / + DNS_LABEL)
}

// validatePrefixedKey validates optionally-prefixed keys: prefix as DNS
// subdomain, name as DNS label; unprefixed keys validate as DNS label.
func (stp *SimpleTemplateProcessor) validatePrefixedKey(key string, maxLength int) bool {
	if len(key) == 0 || len(key) > maxLength {
		return false
	}

	if strings.Contains(key, "/") {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			return false
		}
		prefix, name := parts[0], parts[1]

		if !stp.isDNSSubdomain(prefix) {
			return false
		}

		if !stp.isDNSLabel(name) {
			return false
		}
	} else if !stp.isDNSLabel(key) {
		return false
	}

	return true
}

// isDNSSubdomain validates DNS subdomain format (RFC 1123)
func (stp *SimpleTemplateProcessor) isDNSSubdomain(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}

	// Alphanumeric/'-'/'.' only, must start and end with alphanumeric
	if !unicode.IsLetter(rune(name[0])) && !unicode.IsDigit(rune(name[0])) {
		return false
	}
	if !unicode.IsLetter(rune(name[len(name)-1])) && !unicode.IsDigit(rune(name[len(name)-1])) {
		return false
	}

	for _, char := range name {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) && char != '-' && char != '.' {
			return false
		}
	}

	return true
}

// isDNSLabel validates DNS label format (RFC 1123)
func (stp *SimpleTemplateProcessor) isDNSLabel(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}

	// Lowercase alphanumeric/'-' only, must start and end with alphanumeric
	if !unicode.IsLetter(rune(name[0])) && !unicode.IsDigit(rune(name[0])) {
		return false
	}
	if !unicode.IsLetter(rune(name[len(name)-1])) && !unicode.IsDigit(rune(name[len(name)-1])) {
		return false
	}

	for _, char := range name {
		if !unicode.IsLower(char) && !unicode.IsDigit(char) && char != '-' {
			return false
		}
	}

	return true
}

// isValidSecretKey validates secret data key format (alphanumeric, '-', '_' or '.')
func (stp *SimpleTemplateProcessor) isValidSecretKey(key string) bool {
	if len(key) == 0 {
		return false
	}

	for _, char := range key {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) && char != '-' && char != '_' && char != '.' {
			return false
		}
	}

	return true
}
