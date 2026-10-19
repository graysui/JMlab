package image

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestFallbackPosterEscapesAndBoundsTitle(t *testing.T) {
	data := fallbackPosterSVG(`<script>&"'`)
	if strings.Contains(string(data), "<script>") {
		t.Fatal("unescaped SVG content")
	}
	var target any
	if err := xml.Unmarshal(data, &target); err != nil {
		t.Fatal(err)
	}
	bounded := fallbackPosterSVG(strings.Repeat("电影", 100))
	if len(bounded) > 1400 {
		t.Fatal("unbounded title")
	}
}
