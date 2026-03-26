package externalsecret

import (
	"strings"
	"testing"
)

// TestBcryptFunction tests the bcrypt password hashing function
func TestBcryptFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:      "bcrypt with default cost",
			template:  `{{ bcrypt "password123" }}`,
			data:      map[string]string{},
			expected:  "$2a$10$", // bcrypt prefix with default cost 10
			expectErr: false,
		},
		{
			name:      "bcrypt with custom cost",
			template:  `{{ bcrypt "password123" 12 }}`,
			data:      map[string]string{},
			expected:  "$2a$12$", // bcrypt prefix with cost 12
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}

			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if !tt.expectErr && !strings.HasPrefix(string(result), tt.expected) {
				t.Errorf("Expected bcrypt hash starting with %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestHtpasswdFunction tests the htpasswd format generation
func TestHtpasswdFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expectErr bool
	}{
		{
			name:      "htpasswd basic auth",
			template:  `{{ htpasswd "admin" "password123" }}`,
			data:      map[string]string{},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if !tt.expectErr && !strings.Contains(string(result), "admin:") {
				t.Errorf("Expected htpasswd format with username, got %q", string(result))
			}
		})
	}
}

// TestJsonPathFunction tests the JSON path extraction function
func TestJsonPathFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "jsonPath simple nested access",
			template: `{{ jsonPath .config "database.host" }}`,
			data: map[string]string{
				"config": `{"database": {"host": "localhost", "port": 5432}}`,
			},
			expected:  "localhost",
			expectErr: false,
		},
		{
			name:     "jsonPath array access",
			template: `{{ jsonPath .users "0.name" }}`,
			data: map[string]string{
				"users": `[{"name": "alice"}, {"name": "bob"}]`,
			},
			expected:  "alice",
			expectErr: false,
		},
		{
			name:     "jsonPath deep nested",
			template: `{{ jsonPath .data "a.b.c" }}`,
			data: map[string]string{
				"data": `{"a": {"b": {"c": "deep_value"}}}`,
			},
			expected:  "deep_value",
			expectErr: false,
		},
		{
			name:     "jsonPath invalid path",
			template: `{{ jsonPath .data "nonexistent" }}`,
			data: map[string]string{
				"data": `{"key": "value"}`,
			},
			expected:  "",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestJsonPathWithNestedAccess tests jsonPath function with nested object access
func TestJsonPathWithNestedAccess(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "jsonPath nested access with double quotes",
			template: `{{ jsonPath .jsonData "json_object.nested.inner" }}`,
			data: map[string]string{
				"jsonData": `{"json_object": {"nested": {"inner": "deep-value"}}}`,
			},
			expected:  "deep-value",
			expectErr: false,
		},
		{
			name:     "jsonPath array and nested access",
			template: `{{ jsonPath .data "users.0.address.city" }}`,
			data: map[string]string{
				"data": `{"users": [{"name": "alice", "address": {"city": "Beijing"}}]}`,
			},
			expected:  "Beijing",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}

			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestMergeJsonFunction tests the JSON merge function
func TestMergeJsonFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "mergeJson combines two objects",
			template: `{{ mergeJson .base .override }}`,
			data: map[string]string{
				"base":     `{"a": 1, "b": 2}`,
				"override": `{"b": 3, "c": 4}`,
			},
			expected:  `{"a":1,"b":3,"c":4}`,
			expectErr: false,
		},
		{
			name:     "mergeJson deep merge",
			template: `{{ mergeJson .base .override }}`,
			data: map[string]string{
				"base":     `{"config": {"a": 1, "b": 2}}`,
				"override": `{"config": {"b": 3}}`,
			},
			expected:  `{"config":{"a":1,"b":3}}`,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestParseKeyValueFunction tests the key=value parsing function
func TestParseKeyValueFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "parseKeyValue basic",
			template: `{{ $kv := parseKeyValue .data }}{{ index $kv "KEY1" }}`,
			data: map[string]string{
				"data": "KEY1=value1\nKEY2=value2",
			},
			expected:  "value1",
			expectErr: false,
		},
		{
			name:     "parseKeyValue with comments",
			template: `{{ $kv := parseKeyValue .data }}{{ index $kv "KEY" }}`,
			data: map[string]string{
				"data": "# Comment\nKEY=value\n# Another comment",
			},
			expected:  "value",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestStandardGoTemplateSyntax tests standard Go template syntax still works
func TestStandardGoTemplateSyntax(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "direct field access",
			template: `{{ .username }}`,
			data: map[string]string{
				"username": "alice",
			},
			expected:  "alice",
			expectErr: false,
		},
		{
			name:     "range over data",
			template: `{{ range $k, $v := . }}{{ $k }}={{ $v }};{{ end }}`,
			data: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			expected:  "key1=value1;key2=value2;",
			expectErr: false,
		},
		{
			name:     "if condition",
			template: `{{ if eq .status "active" }}yes{{ else }}no{{ end }}`,
			data: map[string]string{
				"status": "active",
			},
			expected:  "yes",
			expectErr: false,
		},
		{
			name:     "pipe with Sprig function",
			template: `{{ .name | upper }}`,
			data: map[string]string{
				"name": "alice",
			},
			expected:  "ALICE",
			expectErr: false,
		},
		{
			name:     "fromJson with index",
			template: `{{ index (.config | fromJson) "host" }}`,
			data: map[string]string{
				"config": `{"host": "localhost", "port": 5432}`,
			},
			expected:  "localhost",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}
