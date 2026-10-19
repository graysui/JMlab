package catalog

import "testing"

func TestNormalizeCode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "normalizes case and whitespace", input: " ipx-123 ", want: "IPX-123"},
		{name: "supports compound code", input: "fc2-ppv-123456", want: "FC2-PPV-123456"},
		{name: "supports date based numeric prefix", input: "060725_100", want: "060725-100"},
		{name: "rejects missing separator", input: "IPX123", wantErr: true},
		{name: "rejects non numeric suffix", input: "123-ABC", wantErr: true},
		{name: "rejects non numeric suffix with letters", input: "IPX-12A", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeCode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeCode(%q) expected an error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeCode(%q) returned error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeSearchQuery(t *testing.T) {
	if got := NormalizeSearchQuery("  ipx   123 "); got != "IPX-123" {
		t.Fatalf("NormalizeSearchQuery code = %q, want IPX-123", got)
	}
	if got := NormalizeSearchQuery("  title   with spaces "); got != "title with spaces" {
		t.Fatalf("NormalizeSearchQuery title = %q, want collapsed whitespace", got)
	}
}

func TestParseOptionalBool(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  bool
		ok    bool
	}{
		{input: "true", want: true, ok: true},
		{input: "YES", want: true, ok: true},
		{input: "0", want: false, ok: true},
		{input: "maybe", want: false, ok: false},
	} {
		got, ok := parseOptionalBool(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("parseOptionalBool(%q) = (%v, %v), want (%v, %v)", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}
