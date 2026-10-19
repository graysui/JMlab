package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/graysui/JMlab/internal/config"
)

type ReadinessCheck func(context.Context) error

type RouteRegistrar interface {
	RegisterRoutes(chi.Router)
}

// ProtocolRouteRegistrar registers compatibility routes at the HTTP root,
// alongside the versioned JMlab API.
type ProtocolRouteRegistrar interface {
	RegisterProtocolRoutes(chi.Router)
}

type Server struct {
	server    *http.Server
	logger    *slog.Logger
	readiness ReadinessCheck
}

func New(cfg config.Config, logger *slog.Logger, readiness ReadinessCheck, registrars ...RouteRegistrar) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{logger: logger, readiness: readiness}
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(securityHeaders)
	router.Use(requestTimeout(15 * time.Second))
	router.Use(s.requestLogger)
	router.Get("/health/live", s.live)
	router.Get("/health/ready", s.ready)
	apiRouter := chi.NewRouter()
	apiRouter.NotFound(s.notFound)
	router.Mount("/api/v1", apiRouter)
	for _, registrar := range registrars {
		if registrar != nil {
			if apiRegistrar, ok := registrar.(RouteRegistrar); ok {
				apiRegistrar.RegisterRoutes(apiRouter)
			}
			if protocolRegistrar, ok := registrar.(ProtocolRouteRegistrar); ok {
				protocolRegistrar.RegisterProtocolRoutes(router)
			}
		}
	}
	router.Handle("/*", s.staticOrNotFound(cfg.WebDir))
	s.server = &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           embyPrefixHandler(router),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Playback routes stream media for longer than a normal API request;
		// the request context and upstream client still enforce their own
		// connection limits, so an absolute server write timeout would cut
		// valid HLS and HTTP streams short.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

func embyPrefixHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/emby") {
			r = r.WithContext(context.WithValue(r.Context(), protocolPrefixKey{}, true))
		}
		if strings.HasPrefix(r.URL.Path, "/embyhttp://") || strings.HasPrefix(r.URL.Path, "/embyhttps://") {
			raw := strings.TrimPrefix(r.URL.Path, "/emby")
			if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
				r.URL.Path = parsed.Path
				if parsed.RawQuery != "" && r.URL.RawQuery == "" {
					r.URL.RawQuery = parsed.RawQuery
				}
			}
		} else if strings.HasPrefix(r.URL.Path, "/emby/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/emby")
			if r.URL.RawPath != "" && strings.HasPrefix(r.URL.RawPath, "/emby/") {
				r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/emby")
			}
		} else if r.URL.Path == "/emby" {
			r.URL.Path = "/"
			if r.URL.RawPath != "" {
				r.URL.RawPath = "/"
			}
		}
		next.ServeHTTP(w, r)
	})
}

func requestTimeout(timeout time.Duration) func(http.Handler) http.Handler {
	timed := middleware.Timeout(timeout)
	return func(next http.Handler) http.Handler {
		wrapped := timed(next)
		packages := middleware.Timeout(90 * time.Second)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/admin/modules/packages/inspect" || r.URL.Path == "/api/v1/admin/modules/packages/install" {
				packages.ServeHTTP(w, r)
				return
			}
			if isStreamingPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

func isStreamingPath(path string) bool {
	if strings.HasPrefix(path, "/emby/") {
		path = strings.TrimPrefix(path, "/emby")
	}
	if !strings.HasPrefix(path, "/Videos/") {
		return false
	}
	return strings.HasSuffix(path, "/stream") || strings.HasSuffix(path, "/master.m3u8")
}

// securityHeaders keeps the default HTTP surface safe when the container is
// reached directly. TLS termination belongs to the reverse proxy, so HSTS is
// only emitted when the request is already HTTPS (or the proxy tells us so).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.Contains(r.URL.Path, "/images") || strings.HasPrefix(r.URL.Path, "/Videos/") || strings.Contains(r.URL.Path, "/Images/") || strings.HasPrefix(r.URL.Path, "/api/v1/items/") {
			w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		} else {
			w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		}
		if r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ListenAndServe() error { return s.server.ListenAndServe() }

func (s *Server) Shutdown(ctx context.Context) error { return s.server.Shutdown(ctx) }

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.readiness != nil {
		if err := s.readiness(r.Context()); err != nil {
			writeError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "服务尚未就绪")
			s.logger.Warn("就绪检查失败", "error", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "NOT_FOUND", "请求的资源不存在")
}

func (s *Server) staticOrNotFound(webDir string) http.Handler {
	fileServer := http.FileServer(http.Dir(webDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isProtocolRequest(r) {
			s.protocolNotFound(w, r)
			return
		}
		requested := filepath.Join(webDir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(requested); err == nil && !info.IsDir() {
			if strings.EqualFold(filepath.Ext(requested), ".html") {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.notFound(w, r)
			return
		}
		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
			s.notFound(w, r)
			return
		}
		indexPath := filepath.Join(webDir, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			s.notFound(w, r)
			return
		}
		r.URL.Path = "/"
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) protocolNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"Message": "NotFound",
		"Code":    http.StatusNotFound,
	})
}

func isProtocolRequest(r *http.Request) bool {
	if isWebItemNavigation(r) {
		return false
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/emby/") || path == "/emby" {
		return true
	}
	if r.Header.Get("X-Emby-Authorization") != "" ||
		r.Header.Get("X-MediaBrowser-Token") != "" ||
		strings.Contains(r.Header.Get("Authorization"), "MediaBrowser") {
		return true
	}
	for _, prefix := range []string{
		"/System", "/system", "/Users", "/users", "/Items", "/items", "/UserItems", "/Search",
		"/Genres", "/Studios", "/Persons", "/Playlists", "/Sessions", "/Videos", "/Shows",
		"/Plugins", "/Library", "/DisplayPreferences", "/Branding", "/branding",
		"/QuickConnect", "/quickconnect", "/Startup", "/startup", "/Auth", "/auth",
		"/Localization", "/localization", "/Environment", "/environment", "/LiveTv",
		"/Devices", "/Notifications", "/Packages", "/Audio", "/Sync",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

type protocolPrefixKey struct{}

// Only browser document navigation to known Web routes may bypass the
// case-insensitive Jellyfin item namespace. API clients still receive JSON.
func isWebItemNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/html") || r.Context().Value(protocolPrefixKey{}) != nil {
		return false
	}
	for _, key := range []string{"Authorization", "X-Emby-Authorization", "X-Emby-Token", "X-MediaBrowser-Token"} {
		if r.Header.Get(key) != "" {
			return false
		}
	}
	if r.URL.Query().Has("api_key") || r.URL.Query().Has("ApiKey") {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) >= 2 && parts[0] == "items" && parts[1] != "" &&
		(len(parts) == 2 || len(parts) == 3 && parts[2] == "play")
}

func isProtocolPath(path string) bool {
	if strings.HasPrefix(path, "/emby/") || path == "/emby" {
		return true
	}
	for _, prefix := range []string{
		"/System", "/system", "/Users", "/users", "/Items", "/items", "/UserItems", "/Search",
		"/Genres", "/Studios", "/Persons", "/Playlists", "/Sessions", "/Videos", "/Shows",
		"/Plugins", "/Library", "/DisplayPreferences", "/Branding", "/branding",
		"/QuickConnect", "/quickconnect", "/Startup", "/startup", "/Auth", "/auth",
		"/Localization", "/localization", "/Environment", "/environment", "/LiveTv",
		"/Devices", "/Notifications", "/Packages", "/Audio", "/Sync",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		reqPath := r.URL.Path
		ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		userID, _ := r.Context().Value("jmlab.user_id").(string)
		if userID == "" {
			userID = "anonymous"
		}
		errorCode := ww.errorCode
		if errorCode == "" {
			errorCode = "none"
		}
		s.logger.Info("HTTP 请求完成", "requestId", middleware.GetReqID(r.Context()), "userId", userID, "method", r.Method, "route", reqPath, "status", ww.status, "durationMs", time.Since(started).Milliseconds(), "errorCode", errorCode)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	errorCode   string
}

func (w *responseWriter) setErrorCode(code string) { w.errorCode = code }

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if recorder, ok := w.(interface{ setErrorCode(string) }); ok {
		recorder.setErrorCode(code)
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": middleware.GetReqID(r.Context())}})
}
