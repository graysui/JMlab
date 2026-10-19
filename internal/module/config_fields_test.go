package module

import "testing"

func TestConfigJSONAndDeclaredOptions(t *testing.T) {
	m := &Manifest{ID: "config-example", Name: "Example", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: "http://127.0.0.1:8000"}, ConfigSchema: map[string]ConfigField{
		"mode":  {Type: "string", Label: "模式", Required: true, Options: []FieldOption{{Value: "safe", Label: "默认"}}},
		"rules": {Type: "json", Label: "规则", Required: true},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateConfig(m, map[string]any{"mode": "safe", "rules": map[string]any{"paths": []string{"film"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateConfig(m, map[string]any{"mode": "other", "rules": []string{}}); err == nil {
		t.Fatal("undeclared option accepted")
	}
	if _, err := ValidateConfig(m, map[string]any{"mode": "safe", "rules": make(chan int)}); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	m.ConfigSchema["mode"] = ConfigField{Type: "number", Options: []FieldOption{{Value: "1", Label: "One"}}}
	if err := m.Validate(); err == nil {
		t.Fatal("non-string options accepted")
	}
}
