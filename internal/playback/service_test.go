package playback

import "testing"

func TestValidateRange(t *testing.T) {
	for _, raw := range []string{"bytes=0-0", "bytes=100-", "bytes=-1024", "bytes=0-65535"} {
		if err := validateRange(raw); err != nil {
			t.Errorf("validateRange(%q) returned error: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "bytes=", "bytes=2-1", "bytes=0-67108864", "bytes=0-1,2-3", "items=0-1"} {
		if err := validateRange(raw); err == nil {
			t.Errorf("validateRange(%q) accepted invalid range", raw)
		}
	}
}

func TestValidateHeaders(t *testing.T) {
	if err := validateHeaders(map[string]string{"Referer": "https://media.example.test"}); err != nil {
		t.Fatalf("validateHeaders rejected safe header: %v", err)
	}
	if err := validateHeaders(map[string]string{"Authorization": "Basic server-side-only"}); err != nil {
		t.Fatal(err)
	}
	for _, headers := range []map[string]string{
		{"Proxy-Authorization": "Bearer secret"},
		{"Connection": "keep-alive"},
		{"X-Test": "line\nbreak"},
	} {
		if err := validateHeaders(headers); err == nil {
			t.Errorf("validateHeaders accepted unsafe headers: %#v", headers)
		}
	}
}
