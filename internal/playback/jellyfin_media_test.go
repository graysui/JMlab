package playback

import (
	"testing"

	"github.com/graysui/JMlab/internal/module"
)

func TestUnknownModuleMediaDoesNotInventFormatOrCodecs(t *testing.T) {
	resolved := module.ResolvedStream{StreamURL: "https://media.example.test/download?id=42", Protocol: "http"}
	if err := normalizeModuleMedia(&resolved); err != nil {
		t.Fatal(err)
	}
	dto := jellyfinMediaSource(SourceView{ID: "unknown"}, "http", "/Videos/item/stream", resolved.Media, nil)
	if _, ok := dto["Container"]; ok {
		t.Fatal("unknown container invented")
	}
	if len(dto["MediaStreams"].([]map[string]any)) != 0 || dto["SupportsTranscoding"] != false {
		t.Fatal("unknown tracks or transcoding invented")
	}
	if _, ok := dto["TranscodingUrl"]; ok {
		t.Fatal("transcoding URL invented")
	}
	resolved.StreamURL = "https://media.example.test/film.webm?token=hidden"
	resolved.Media = nil
	if err := normalizeModuleMedia(&resolved); err != nil || resolved.Media.Container != "webm" {
		t.Fatal("actual extension lost", err)
	}
}
