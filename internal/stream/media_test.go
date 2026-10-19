package stream

import "testing"

func TestMediaDescriptionRejectsMalformedTracks(t *testing.T) {
	for _, description := range []*MediaDescription{
		{Container: "https://secret.example/token"},
		{Tracks: []MediaTrack{{Index: 0, Type: "video"}, {Index: 0, Type: "audio"}}},
		{Tracks: []MediaTrack{{Index: 1, Type: "credential"}}},
		{Tracks: []MediaTrack{{Index: 1, Type: "video", Codec: "codec\r\nheader"}}},
	} {
		if ValidateMediaDescription(description) == nil {
			t.Fatalf("accepted invalid description: %+v", description)
		}
	}
	size := int64(1 << 40)
	if err := ValidateMediaDescription(&MediaDescription{Container: "mkv", SizeBytes: &size, Tracks: []MediaTrack{{Index: 0, Type: "video", Codec: "hevc"}, {Index: 2, Type: "audio", Codec: "eac3"}}}); err != nil {
		t.Fatal(err)
	}
}
