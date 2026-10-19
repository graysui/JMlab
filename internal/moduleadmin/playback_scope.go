package moduleadmin

import "net/http"

func (s *Service) playbackLibraries(w http.ResponseWriter, r *http.Request) {
	items, err := s.catalog.PlaybackLibraries(r.Context())
	if err != nil {
		fail(w, r, 500, "LIBRARIES_UNAVAILABLE", "读取媒体库失败，请重试")
		return
	}
	respond(w, 200, map[string]any{"items": items})
}
