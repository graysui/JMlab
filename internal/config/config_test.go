package config

import "testing"

func TestRenamedEnvironmentPrioritizesCurrentValues(t *testing.T) {
	t.Setenv("JAVLAB_HTTP_ADDR", "127.0.0.1:1001")
	t.Setenv("JMLAB_HTTP_ADDR", "127.0.0.1:1002")
	if got := environment("JMLAB_HTTP_ADDR"); got != "127.0.0.1:1002" {
		t.Fatalf("current name did not take priority: %q", got)
	}
	t.Setenv("JMLAB_HTTP_ADDR", "")
	if got := environment("JMLAB_HTTP_ADDR"); got != "" {
		t.Fatalf("explicit empty value ignored: %q", got)
	}
	t.Setenv("JAVLAB_PUBLIC_URL", "https://legacy.example.test")
	if got := environment("JMLAB_PUBLIC_URL"); got != "https://legacy.example.test" {
		t.Fatalf("compatible value lost: %q", got)
	}
}
