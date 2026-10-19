package playback

import (
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/graysui/JMlab/internal/module"
	provider "github.com/graysui/JMlab/internal/stream"
)

func inferStreamContainer(protocol, target string) string {
	if protocol == "hls" {
		return "m3u8"
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return ""
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(parsed.Path)), ".")
	switch ext {
	case "mp4", "mkv", "webm", "mov", "m4v", "ts", "avi", "mpeg", "mpg", "m2ts", "m3u8":
		return ext
	}
	return ""
}

func normalizeModuleMedia(resolved *module.ResolvedStream) error {
	if len(resolved.Subtitles) > 64 {
		return errors.New("字幕数量过多")
	}
	for _, sub := range resolved.Subtitles {
		if len(sub.Language) > 32 || len(sub.Label) > 256 || len(sub.URL) > 8192 || sub.Format != "" && sub.Format != "vtt" && sub.Format != "srt" && sub.Format != "ass" && sub.Format != "ssa" {
			return errors.New("字幕描述无效")
		}
	}

	if err := provider.ValidateMediaDescription(resolved.Media); err != nil {
		return err
	}
	if resolved.Media == nil {
		resolved.Media = &module.MediaDescription{}
	} else {
		copy := *resolved.Media
		resolved.Media = &copy
	}
	if resolved.Media.Container == "" {
		resolved.Media.Container = inferStreamContainer(resolved.Protocol, resolved.StreamURL)
	}
	return nil
}

func jellyfinMediaSource(source SourceView, protocol, target string, media *provider.MediaDescription, subtitles []provider.Subtitle) map[string]any {
	result := map[string]any{
		"Id": source.ID, "Name": source.Label, "Path": target, "DirectStreamUrl": target,
		"Protocol": "Http", "Type": "Default", "IsRemote": true,
		"SupportsDirectPlay": true, "SupportsDirectStream": true, "SupportsTranscoding": false,
		"RequiresOpening": false, "RequiresClosing": false, "RequiredHttpHeaders": map[string]string{},
	}
	if protocol == "hls" {
		result["Container"] = "m3u8"
	}
	if source.DurationSeconds != nil {
		result["RunTimeTicks"] = nullableInt64Ticks(source.DurationSeconds)
	}
	if source.SizeBytes != nil {
		result["Size"] = source.SizeBytes
	}
	tracks := []map[string]any{}
	nextIndex := 0
	if media != nil {
		if media.Container != "" {
			result["Container"] = media.Container
		}
		if media.DurationSeconds != nil {
			result["RunTimeTicks"] = nullableInt64Ticks(media.DurationSeconds)
		}
		if media.SizeBytes != nil {
			result["Size"] = media.SizeBytes
		}
		if media.BitRate != nil {
			result["Bitrate"] = media.BitRate
		}
		for _, track := range media.Tracks {
			if track.Index >= nextIndex {
				nextIndex = track.Index + 1
			}
			kind := map[string]string{"video": "Video", "audio": "Audio", "subtitle": "Subtitle"}[track.Type]
			row := map[string]any{"Type": kind, "Index": track.Index, "IsDefault": track.IsDefault, "IsForced": track.IsForced, "IsExternal": false, "SupportsExternalStream": false}
			if track.Codec != "" {
				row["Codec"] = track.Codec
			}
			if track.Language != "" {
				row["Language"] = track.Language
			}
			if track.Title != "" {
				row["Title"] = track.Title
				row["DisplayTitle"] = track.Title
			}
			if track.Width != nil {
				row["Width"] = track.Width
			}
			if track.Height != nil {
				row["Height"] = track.Height
			}
			if track.Channels != nil {
				row["Channels"] = track.Channels
			}
			if track.SampleRate != nil {
				row["SampleRate"] = track.SampleRate
			}
			if track.BitRate != nil {
				row["BitRate"] = track.BitRate
			}
			if track.FrameRate != nil {
				row["RealFrameRate"] = track.FrameRate
				row["AverageFrameRate"] = track.FrameRate
			}
			if track.IsDefault && kind == "Audio" {
				result["DefaultAudioStreamIndex"] = track.Index
			}
			if track.IsDefault && kind == "Subtitle" {
				result["DefaultSubtitleStreamIndex"] = track.Index
			}
			tracks = append(tracks, row)
		}
	}
	for _, sub := range subtitles {
		row := map[string]any{"Type": "Subtitle", "Index": nextIndex, "IsExternal": true, "IsDefault": false, "IsForced": false, "IsTextSubtitleStream": true, "SupportsExternalStream": true, "DeliveryMethod": "External", "DeliveryUrl": sub.URL, "Protocol": "Http"}
		if sub.Format != "" {
			row["Codec"] = sub.Format
		}
		if sub.Language != "" {
			row["Language"] = sub.Language
		}
		if sub.Label != "" {
			row["Title"] = sub.Label
			row["DisplayTitle"] = sub.Label
		}
		tracks = append(tracks, row)
		nextIndex++
	}
	result["MediaStreams"] = tracks
	return result
}

func setRequestedTrack(source map[string]any, kind string, index *int) bool {
	if index == nil {
		return true
	}
	if kind == "Subtitle" && *index == -1 {
		source["DefaultSubtitleStreamIndex"] = -1
		return true
	}
	tracks, _ := source["MediaStreams"].([]map[string]any)
	for _, track := range tracks {
		if track["Type"] == kind && track["Index"] == *index {
			source["Default"+kind+"StreamIndex"] = *index
			return true
		}
	}
	return false
}
