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

// SimpleTemplateProcessor Template processor
type SimpleTemplateProcessor struct {
	Client  client.Client
	funcMap template.FuncMap // Cached function map to avoid recreation on each call
}

// NewSimpleTemplateProcessor creates a new template processor with initialized function map
func NewSimpleTemplateProcessor(client client.Client) *SimpleTemplateProcessor {
	stp := &SimpleTemplateProcessor{Client: client}
	stp.funcMap = stp.createFuncMap()
	return stp
}

const (
	// ManagedKeysAnnotation is the annotation key to store managed secret keys
	ManagedKeysAnnotation = "ack-alibabacloud.com/managed-keys"
)

// Sentinel errors distinguishing template PARSE failures (fatal: the
// template is structurally invalid and can never render) from EXECUTION
// failures (non-fatal per the established grading: the template is valid
// but this round's data cannot render it). executeTemplate wraps both via
// fmt.Errorf %w so callers classify with errors.Is instead of string
// sniffing; the user-visible message format is unchanged.
var (
	errTemplateParse     = stderrors.New("failed to parse template")
	errTemplateExecution = stderrors.New("template execution failed")
)

// UnifiedTemplateResult represents the complete result of all template processing
type UnifiedTemplateResult struct {
	// Processed secret data
	Data map[string][]byte

	// Metadata that will be applied to the target secret
	Metadata struct {
		Labels      map[string]string
		Annotations map[string]string
	}

	// Processing statistics
	Stats struct {
		DataTemplatesProcessed     int
		TemplateFromProcessed      int
		MetadataTemplatesProcessed int
		Errors                     []string
		FatalErrors                []string // New: track fatal errors separately
	}
}

// ProcessAllTemplates processes all template configurations with context
func (stp *SimpleTemplateProcessor) ProcessAllTemplates(
	ctx context.Context,
	externalSec *api.ExternalSecret,
	rawData map[string][]byte,
) (*UnifiedTemplateResult, error) {

	// If ExternalSecret is being deleted, skip template processing.
	// The deletion flow intentionally bypasses templates so that raw data
	// alone drives the round; re-rendering templates here would re-add
	// managed keys that the deletion flow has intentionally removed.
	if externalSec.GetDeletionTimestamp() != nil {
		// Return raw data only, without processing templates
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

		// Preserve existing data to avoid modifying the secret
		for k, v := range rawData {
			result.Data[k] = v
		}

		return result, nil
	}

	// Return raw data if no template configuration exists
	// Check if Target is nil or Template is nil
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

	// Convert to interface{} map for template engine
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

	// Copy raw data to result first as fallback
	for k, v := range rawData {
		result.Data[k] = v
	}

	// Track managed keys for the ManagedKeysAnnotation written below
	managedKeys := make(map[string]bool)

	// Process inline data templates (Template.Data section)
	// Safe to access because we checked Target and Template are not nil above
	if externalSec.Spec.Target.Template.Data != nil {
		// Empty map means "preserve raw data without processing"
		// Non-empty map means "use templates instead of raw data"
		if len(externalSec.Spec.Target.Template.Data) > 0 {
			// Check MergePolicy before clearing data
			if externalSec.Spec.Target.Template.MergePolicy == "" ||
				externalSec.Spec.Target.Template.MergePolicy == api.MergePolicyReplace {
				// Replace mode: clear raw data
				clear(result.Data)
			}
			// Merge mode: keep raw data, templates will override same keys

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
					managedKeys[key] = true // Track managed key
				}
			}
		}
		// If len() == 0, keep raw data and don't process any templates
	}

	// Process TemplateFrom references
	// Safe to access because we checked Target and Template are not nil above
	if len(externalSec.Spec.Target.Template.TemplateFrom) > 0 {
		templateFromResults, err := stp.processTemplateFromByTarget(ctx, externalSec, interfaceData)
		if err != nil {
			result.Stats.FatalErrors = append(result.Stats.FatalErrors,
				fmt.Sprintf("TemplateFrom processing failed: %v", err))
		} else {
			// Apply MergePolicy for TemplateFrom as well
			// If MergePolicy is Replace (default), clear existing data before adding TemplateFrom results
			hasTemplateData := len(externalSec.Spec.Target.Template.Data) > 0
			isReplaceMode := externalSec.Spec.Target.Template.MergePolicy == "" ||
				externalSec.Spec.Target.Template.MergePolicy == api.MergePolicyReplace

			// Presence of the "Data" target is checked by KEY (not output): a
			// Data-targeted templateFrom keeps Replace semantics even when it yields
			// zero keys; Labels/Annotations-only lists must not clear raw data (O-1).
			_, hasDataTarget := templateFromResults[string(api.TemplateTargetData)]

			// Only clear data if:
			// 1. MergePolicy is Replace AND
			// 2. No inline Template.Data was processed (to avoid double-clearing) AND
			// 3. At least one templateFrom entry targets Data
			if isReplaceMode && !hasTemplateData && hasDataTarget {
				clear(result.Data)
			}

			for target, targetData := range templateFromResults {
				switch api.TemplateTarget(target) {
				case api.TemplateTargetAnnotations:
					for k, v := range targetData {
						if stp.isValidAnnotationKey(k) {
							result.Metadata.Annotations[k] = string(v)
						}
					}
				case api.TemplateTargetLabels:
					for k, v := range targetData {
						if stp.isValidLabelKey(k) {
							result.Metadata.Labels[k] = string(v)
						}
					}
				case api.TemplateTargetData, "":
					fallthrough
				default:
					for k, v := range targetData {
						if stp.isValidSecretKey(k) {
							result.Data[k] = v
							result.Stats.DataTemplatesProcessed++
							managedKeys[k] = true // Track dynamically generated key
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
		// Sort for deterministic output
		sort.Strings(keysList)
		result.Metadata.Annotations[ManagedKeysAnnotation] = strings.Join(keysList, ",")
	}

	// Process Template.Metadata
	// Safe to access because we checked Target and Template are not nil above
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
				}
			}
		}
	}

	// Return error if there are fatal errors
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

// createFuncMap creates and caches the function map for template execution
func (stp *SimpleTemplateProcessor) createFuncMap() template.FuncMap {
	funcMap := sprig.TxtFuncMap()

	// Add custom functions that provide real value (not available in Sprig)

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

	// Htpasswd format for basic auth (bcrypt algorithm)
	funcMap["htpasswd"] = func(username, password string) string {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			klog.Warningf("htpasswd bcrypt hash generation failed: %v", err)
			return ""
		}
		return fmt.Sprintf("%s:%s\n", username, string(hash))
	}

	// JSON Path query - extract value from JSON using dot notation
	funcMap["jsonPath"] = func(jsonStr, path string) interface{} {
		var data interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			return ""
		}
		return extractJSONPath(data, path)
	}

	// Merge JSON objects - deep merge two JSON strings
	funcMap["mergeJson"] = func(base, override string) string {
		var baseMap, overrideMap map[string]interface{}
		if err := json.Unmarshal([]byte(base), &baseMap); err != nil {
			return base
		}
		if err := json.Unmarshal([]byte(override), &overrideMap); err != nil {
			return base
		}

		merged := deepMerge(baseMap, overrideMap)
		result, err := json.Marshal(merged)
		if err != nil {
			return base
		}
		return string(result)
	}

	// Parse key=value format into a map
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

	// Split string into lines (array)
	funcMap["toLines"] = func(input string) []string {
		if input == "" {
			return []string{}
		}
		return strings.Split(strings.TrimSpace(input), "\n")
	}

	return funcMap
}

// executeTemplate Execute single template with support for standard Go template syntax and custom functions.
// Parse failures wrap errTemplateParse (fatal), execution failures wrap
// errTemplateExecution (non-fatal); callers classify via errors.Is.
func (stp *SimpleTemplateProcessor) executeTemplate(templateStr string, data map[string]interface{}) ([]byte, error) {
	// Use cached function map instead of recreating it
	tmpl, err := template.New("").Funcs(stp.funcMap).Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errTemplateParse, err)
	}

	var buf bytes.Buffer
	// Pass the underlying data map to enable {{ .key }} syntax.
	// This allows: {{ .password }}, {{ range $k, $v := . }}, {{ if .enabled }}, etc.
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("%w: %v", errTemplateExecution, err)
	}

	return buf.Bytes(), nil
}

// extractJSONPath extracts a value from JSON data using dot notation path
// Returns empty string "" if path is not found or any error occurs
// Logs debug information for troubleshooting
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
				// Log detailed path information for debugging
				currentPath := strings.Join(parts[:i+1], ".")
				klog.V(4).Infof("jsonPath: key '%s' not found in map at path '%s', available keys: %v",
					part, currentPath, getMapKeys(v))
				return ""
			}
		case []interface{}:
			if idx, err := strconv.Atoi(part); err == nil && idx >= 0 && idx < len(v) {
				current = v[idx]
			} else {
				// Log array index error details
				currentPath := strings.Join(parts[:i+1], ".")
				klog.V(4).Infof("jsonPath: invalid array index '%s' at path '%s', array length: %d",
					part, currentPath, len(v))
				return ""
			}
		default:
			// Cannot traverse further (reached a primitive value)
			remainingPath := strings.Join(parts[i:], ".")
			klog.V(4).Infof("jsonPath: cannot traverse path '%s', current value type is %T",
				remainingPath, current)
			return ""
		}
	}

	return current
}

// getMapKeys returns all keys from a map as a slice (for debugging)
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// deepMerge performs deep merge of two maps with cycle detection
func deepMerge(base, override map[string]interface{}) map[string]interface{} {
	return deepMergeWithCycle(base, override, make(map[uintptr]bool))
}

// deepMergeWithCycle performs deep merge with cycle detection to prevent infinite recursion
func deepMergeWithCycle(base, override map[string]interface{}, visited map[uintptr]bool) map[string]interface{} {
	result := make(map[string]interface{})

	// Cycle detection: check if we've already visited this base map
	basePtr := reflect.ValueOf(base).Pointer()
	if visited[basePtr] {
		// Cycle detected, return base as-is to prevent infinite recursion
		return base
	}
	visited[basePtr] = true
	defer delete(visited, basePtr) // Clean up after processing

	// Copy base first
	for k, v := range base {
		result[k] = v
	}

	// Override and merge
	for k, v := range override {
		if baseVal, exists := base[k]; exists {
			// If both are maps, do recursive merge
			if baseMap, ok := baseVal.(map[string]interface{}); ok {
				if overrideMap, ok := v.(map[string]interface{}); ok {
					result[k] = deepMergeWithCycle(baseMap, overrideMap, visited)
					continue
				}
			}
		}
		// Otherwise, override
		result[k] = v
	}

	return result
}

// processTemplateFromByTarget processes TemplateFrom references and separates results by target
func (stp *SimpleTemplateProcessor) processTemplateFromByTarget(
	ctx context.Context,
	externalSec *api.ExternalSecret,
	data map[string]interface{},
) (map[string]map[string][]byte, error) {
	results := make(map[string]map[string][]byte)

	// Target and Template are guaranteed to be non-nil when this method is called
	for _, templateFrom := range externalSec.Spec.Target.Template.TemplateFrom {
		target := string(templateFrom.Target)
		if target == "" {
			target = string(api.TemplateTargetData) // Default target
		}

		// Initialize target map if exists
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

		// Merge template data into target
		for k, v := range templateData {
			results[target][k] = v
		}
	}

	return results, nil
}

// processTemplateFromRef processes a template reference from ConfigMap, Secret, or Literal
// This is the common implementation extracted from processConfigMapTemplate and processSecretTemplate
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
			// Process as value only
			result[item.Key] = processedValue
		case api.TemplateScopeKeysAndValues:
			// Process as key-value pairs - parse the processed value as key=value format
			processedValueStr := string(processedValue)

			// Parse key=value pairs from the processed template
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
			// For KeysAndValues scope, do NOT preserve the original processed value
			// Only the parsed key-value pairs should be included
		default:
			// Default to process as value
			result[item.Key] = processedValue
		}
	}

	return result, nil
}

// processConfigMapTemplate Process ConfigMap template reference
func (stp *SimpleTemplateProcessor) processConfigMapTemplate(
	ctx context.Context,
	templateRef *api.TemplateRef,
	namespace string,
	data map[string]interface{},
) (map[string][]byte, error) {
	// Helper function to get ConfigMap data
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
	// Helper function to get Secret data
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

// processLiteralTemplate Process literal template
func (stp *SimpleTemplateProcessor) processLiteralTemplate(
	literal string,
	data map[string]interface{},
) (map[string][]byte, error) {
	processedValue, err := stp.executeTemplate(literal, data)
	if err != nil {
		return nil, fmt.Errorf("failed to process literal template: %w", err)
	}

	// Literal template generates a default key
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

// validatePrefixedKey validates keys that may have prefixes (like annotations)
// For keys without prefixes, validates as DNS label
// For keys with prefixes, validates prefix as DNS subdomain and name as DNS label
func (stp *SimpleTemplateProcessor) validatePrefixedKey(key string, maxLength int) bool {
	if len(key) == 0 || len(key) > maxLength {
		return false
	}

	// Check for prefix separator
	if strings.Contains(key, "/") {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			return false
		}
		prefix, name := parts[0], parts[1]

		// Validate prefix (DNS subdomain)
		if !stp.isDNSSubdomain(prefix) {
			return false
		}

		// Validate name (DNS label)
		if !stp.isDNSLabel(name) {
			return false
		}
	} else {
		// No prefix, validate as DNS label
		if !stp.isDNSLabel(key) {
			return false
		}
	}

	return true
}

// isDNSSubdomain validates DNS subdomain format (RFC 1123)
func (stp *SimpleTemplateProcessor) isDNSSubdomain(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}

	// Must consist of alphanumeric characters, '-' or '.', and must start and end with alphanumeric
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

	// Must consist of alphanumeric characters or '-', and must start and end with alphanumeric
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

// isValidSecretKey validates secret data key format according to Kubernetes standards
func (stp *SimpleTemplateProcessor) isValidSecretKey(key string) bool {
	// Kubernetes secret keys must consist of alphanumeric characters, '-', '_' or '.'
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
