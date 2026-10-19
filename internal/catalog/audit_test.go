package catalog

import (
	"encoding/json"
	"testing"
)

func TestSanitizeAuditMetadata(t *testing.T) {
	value := sanitizeAuditMetadata(`{"token":"secret","nested":{"password":"pw","title":"safe"},"items":[{"cookie":"abc"}]}`)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal sanitized metadata: %v", err)
	}
	got := string(encoded)
	for _, forbidden := range []string{"secret", "pw", "abc"} {
		if contains := json.Valid(encoded) && string(encoded) != "" && (len(got) > 0 && indexString(got, forbidden)); contains {
			t.Fatalf("sanitized metadata leaked %q: %s", forbidden, got)
		}
	}
	if !indexString(got, "[已隐藏]") || !indexString(got, "safe") {
		t.Fatalf("sanitized metadata missing redaction or safe value: %s", got)
	}
}

func indexString(value, needle string) bool {
	for offset := 0; offset+len(needle) <= len(value); offset++ {
		if value[offset:offset+len(needle)] == needle {
			return true
		}
	}
	return false
}
