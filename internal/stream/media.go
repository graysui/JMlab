package stream

import (
	"errors"
	"math"
	"regexp"
)

// MediaDescription contains observed media properties, never guessed codecs or
// upstream credentials. Unknown values are omitted. Times are in seconds.
type MediaDescription struct {
	Container       string       `json:"container,omitempty"`
	DurationSeconds *int         `json:"durationSeconds,omitempty"`
	SizeBytes       *int64       `json:"sizeBytes,omitempty"`
	BitRate         *int         `json:"bitRate,omitempty"`
	Tracks          []MediaTrack `json:"tracks,omitempty"`
}
type MediaTrack struct {
	Index      int      `json:"index"`
	Type       string   `json:"type"` // video, audio, subtitle (embedded)
	Codec      string   `json:"codec,omitempty"`
	Language   string   `json:"language,omitempty"`
	Title      string   `json:"title,omitempty"`
	Width      *int     `json:"width,omitempty"`
	Height     *int     `json:"height,omitempty"`
	Channels   *int     `json:"channels,omitempty"`
	SampleRate *int     `json:"sampleRate,omitempty"`
	BitRate    *int     `json:"bitRate,omitempty"`
	FrameRate  *float64 `json:"frameRate,omitempty"`
	IsDefault  bool     `json:"isDefault"`
	IsForced   bool     `json:"isForced"`
}

var mediaToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,31}$`)

func ValidateMediaDescription(media *MediaDescription) error {
	if media == nil {
		return nil
	}
	if media.Container != "" && !mediaToken.MatchString(media.Container) || len(media.Tracks) > 64 || !bounded(media.DurationSeconds, 0, 31536000) || !bounded(media.BitRate, 1, 2000000000) || media.SizeBytes != nil && *media.SizeBytes < 0 {
		return errors.New("媒体描述超出有效范围")
	}
	seen := map[int]bool{}
	for _, track := range media.Tracks {
		if track.Index < 0 || track.Index > 255 || seen[track.Index] || (track.Type != "video" && track.Type != "audio" && track.Type != "subtitle") || track.Codec != "" && !mediaToken.MatchString(track.Codec) || len(track.Language) > 32 || len(track.Title) > 256 || !bounded(track.Width, 1, 32768) || !bounded(track.Height, 1, 32768) || !bounded(track.Channels, 1, 64) || !bounded(track.SampleRate, 1, 768000) || !bounded(track.BitRate, 1, 2000000000) || track.FrameRate != nil && (math.IsNaN(*track.FrameRate) || math.IsInf(*track.FrameRate, 0) || *track.FrameRate <= 0 || *track.FrameRate > 1000) {
			return errors.New("媒体轨道描述无效")
		}
		seen[track.Index] = true
	}
	return nil
}
func bounded(value *int, low, high int) bool { return value == nil || *value >= low && *value <= high }
