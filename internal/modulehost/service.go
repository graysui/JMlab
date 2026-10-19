// Package modulehost implements the authenticated, capability-limited callback
// surface for installed modules. User sessions cannot authenticate here.
package modulehost

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

const maximumDepth = 4

type gate struct {
	active chan struct{}
	window time.Time
	count  int
}
type Service struct {
	db                   *repository.Database
	registry             *module.Registry
	masterKey, publicURL string
	mu                   sync.Mutex
	gates                map[string]*gate
	storageMu            sync.Mutex
}

func NewService(db *repository.Database, registry *module.Registry, masterKey, publicURL string) *Service {
	return &Service{db: db, registry: registry, masterKey: masterKey, publicURL: strings.TrimRight(publicURL, "/"), gates: map[string]*gate{}}
}
func (s *Service) RegisterRoutes(router chi.Router) {
	router.Route("/module-host/{moduleID}", func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/storage", s.listKeys)
		r.Get("/storage/{key}", s.getValue)
		r.Put("/storage/{key}", s.putValue)
		r.Delete("/storage/{key}", s.deleteValue)
		r.Post("/invoke", s.invoke)
	})
}
func (s *Service) signature(purpose, value string) string {
	hash := hmac.New(sha256.New, []byte(s.masterKey+":module-host:"+purpose))
	_, _ = hash.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

type ticket struct {
	Audience string   `json:"aud"`
	Revision string   `json:"revision"`
	Chain    []string `json:"chain"`
	Expires  int64    `json:"exp"`
}

// Bind credentials to the exact published client. An old snapshot must never
// receive the replacement's credentials when its address/permissions change.
func (s *Service) Headers(ctx context.Context, client *module.HTTPModuleClient) map[string]string {
	entry, ok := s.registry.Get(client.ModuleID())
	if !ok || entry.Status != module.StatusActive || (entry.MetadataInstance != client && entry.StreamInstance != client) {
		return nil
	}
	return s.headers(ctx, client.ModuleID())
}

func (s *Service) headers(ctx context.Context, id string) map[string]string {
	chain := append(module.CallChain(ctx), id)
	revision := ""
	if entry, ok := s.registry.Get(id); ok {
		revision = entry.Revision
	}
	data, _ := json.Marshal(ticket{Audience: id, Revision: revision, Chain: chain, Expires: time.Now().Add(45 * time.Second).Unix()})
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return map[string]string{"X-JMlab-Host-URL": s.publicURL + "/api/v1/module-host/" + id, "X-JMlab-Host-Token": s.signature("auth", id+":"+revision), "X-JMlab-Call-Ticket": encoded + "." + s.signature("call", encoded)}
}
func (s *Service) parseTicket(raw string, entry *module.ModuleEntry) (ticket, error) {
	var claims ticket
	if len(raw) > 4096 {
		return claims, errors.New("invalid ticket")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.signature("call", parts[0]))) != 1 {
		return claims, errors.New("invalid ticket")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, err
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return claims, err
	}
	if claims.Audience != entry.Manifest.ID || claims.Revision != entry.Revision || claims.Expires < time.Now().Unix() || len(claims.Chain) == 0 || len(claims.Chain) > maximumDepth || claims.Chain[len(claims.Chain)-1] != claims.Audience {
		return claims, errors.New("invalid ticket")
	}
	return claims, nil
}

type callerKey struct{}

func caller(r *http.Request) *module.ModuleEntry {
	return r.Context().Value(callerKey{}).(*module.ModuleEntry)
}
func (s *Service) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "moduleID")
		entry, ok := s.registry.Get(id)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || entry.Status != module.StatusActive || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.signature("auth", id+":"+entry.Revision))) != 1 {
			fail(w, 401, "MODULE_AUTH_REQUIRED", "模块身份无效或已停用")
			return
		}
		s.mu.Lock()
		g := s.gates[id]
		if g == nil {
			g = &gate{active: make(chan struct{}, 4)}
			s.gates[id] = g
		}
		if time.Since(g.window) >= time.Minute {
			g.window = time.Now()
			g.count = 0
		}
		if g.count >= 120 {
			s.mu.Unlock()
			w.Header().Set("Retry-After", "60")
			fail(w, 429, "MODULE_RATE_LIMIT", "模块请求过于频繁")
			return
		}
		g.count++
		s.mu.Unlock()
		select {
		case g.active <- struct{}{}:
			defer func() { <-g.active }()
		default:
			fail(w, 429, "MODULE_CONCURRENCY_LIMIT", "模块并发请求过多")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, entry)))
	})
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
