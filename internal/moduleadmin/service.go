package moduleadmin

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

type Service struct {
	catalog *catalog.Service
	db      *repository.Database
	auth    *auth.Service
	manager *module.Manager
}

func NewService(db *repository.Database, authService *auth.Service, manager *module.Manager) *Service {
	contents := catalog.NewService(db, authService, nil)
	contents.SetModuleRegistry(manager.Registry())
	return &Service{db: db, auth: authService, manager: manager, catalog: contents}
}
func (s *Service) SetCatalog(contents *catalog.Service) { s.catalog = contents }
func (s *Service) RegisterRoutes(router chi.Router) {
	admin := router.With(s.auth.RequireAuth, s.auth.RequireRole("Admin"))
	admin.Get("/admin/modules", s.list)
	admin.With(s.auth.RequireCSRF).Post("/admin/modules/{moduleID}/actions/{actionID}", s.invokeAction)
	admin.Get("/admin/modules/templates", s.templates)
	admin.Get("/admin/modules/playback-libraries", s.playbackLibraries)
	admin.Get("/admin/modules/library-templates", s.libraryTemplateModules)
	admin.Get("/admin/modules/{moduleID}/library-templates", s.libraryTemplates)
	admin.With(s.auth.RequireCSRF).Post("/admin/modules/reload", s.reload)
	admin.With(s.auth.RequireCSRF).Patch("/admin/modules/{moduleID}", s.update)
	admin.With(s.auth.RequireCSRF).Put("/admin/modules/{moduleID}/manifest", s.replaceManifest)
	admin.With(s.auth.RequireCSRF).Delete("/admin/modules/{moduleID}", s.uninstall)
	admin.With(s.auth.RequireCSRF).Post("/admin/modules", s.install)
}

func (s *Service) replaceManifest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "moduleID")
	var input struct {
		Manifest module.Manifest `json:"manifest"`
	}
	if err := decode(w, r, &input); err != nil {
		fail(w, r, 400, "INVALID_MODULE", "模块清单格式不正确")
		return
	}
	if err := s.manager.UpdateManifest(r.Context(), id, &input.Manifest); err != nil {
		fail(w, r, 400, "MODULE_UPDATE_FAILED", err.Error())
		return
	}
	s.audit(r, "update_module_manifest", id)
	respond(w, 200, map[string]any{"status": "updated", "moduleId": id})
}

func (s *Service) uninstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "moduleID")
	if err := s.manager.Uninstall(r.Context(), id); err != nil {
		fail(w, r, 400, "MODULE_UNINSTALL_FAILED", err.Error())
		return
	}
	s.audit(r, "uninstall_module", id)
	respond(w, 200, map[string]any{"status": "uninstalled", "moduleId": id})
}

func (s *Service) templates(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]any{"items": []module.Manifest{}})
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	libraries, err := s.catalog.PlaybackLibraries(r.Context())
	if err != nil {
		fail(w, r, 500, "LIBRARIES_UNAVAILABLE", "暂时无法读取媒体库范围")
		return
	}
	names := map[string]string{}
	for _, library := range libraries {
		names[library.ID] = library.Name
	}
	for _, entry := range s.manager.Registry().List() {
		state := s.manager.Settings(entry.Manifest.ID)
		scopeNames := []string{}
		if state.LibraryScope != nil && state.LibraryScope.Mode == "selected" {
			for _, id := range state.LibraryScope.LibraryIDs {
				name := names[id]
				if name == "" {
					name = "已删除的媒体库"
				}
				scopeNames = append(scopeNames, name)
			}
		}
		health := entry.Health
		if health.State == "" {
			health.State = "unknown"
		}
		config := map[string]any{}
		configured := []string{}
		for key, field := range entry.Manifest.ConfigSchema {
			if field.Secret || field.Type == "password" {
				if value, ok := state.Config[key].(string); ok && value != "" {
					configured = append(configured, key)
				}
				field.Default = nil
				entry.Manifest.ConfigSchema[key] = field
			} else if value, ok := state.Config[key]; ok {
				config[key] = value
			} else if field.Default != nil {
				config[key] = field.Default
			}
		}
		items = append(items, map[string]any{"manifest": entry.Manifest, "revision": entry.Revision, "status": entry.Status, "lastError": entry.LastError,
			"enabled": state.Enabled, "config": config, "libraryScope": state.LibraryScope, "libraryScopeNames": scopeNames, "configuredSecrets": configured, "tokenConfigured": state.Token != "", "health": health, "events": s.manager.EventBus().Stats("module:" + entry.Manifest.ID)})
	}
	respond(w, 200, map[string]any{"items": items})
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "moduleID")
	entry, ok := s.manager.Registry().Get(id)
	if !ok {
		fail(w, r, 404, "MODULE_NOT_FOUND", "模块不存在")
		return
	}
	var input struct {
		Enabled      *bool                   `json:"enabled"`
		LibraryScope *module.LibraryScope    `json:"libraryScope"`
		Config       map[string]any          `json:"config"`
		Token        *string                 `json:"token"`
		ClearSecrets []string                `json:"clearSecrets"`
		ClearToken   bool                    `json:"clearToken"`
		Transport    *module.TransportConfig `json:"transport"`
		MediaAccess  *module.MediaAccess     `json:"mediaAccess"`
	}
	if err := decode(w, r, &input); err != nil {
		fail(w, r, 400, "INVALID_MODULE_CONFIG", "配置格式不正确")
		return
	}
	state := s.manager.Settings(id)
	if input.LibraryScope != nil {
		state.LibraryScope = input.LibraryScope
	}
	if state.Config == nil {
		state.Config = map[string]any{}
	}
	if input.Enabled != nil {
		state.Enabled = *input.Enabled
	}
	if input.Token != nil && *input.Token != "" {
		state.Token = *input.Token
	}
	if input.ClearToken {
		state.Token = ""
	}
	for key, value := range input.Config {
		field := entry.Manifest.ConfigSchema[key]
		if text, ok := value.(string); ok && (field.Secret || field.Type == "password") && text == "" {
			continue
		}
		state.Config[key] = value
	}
	for _, key := range input.ClearSecrets {
		field, exists := entry.Manifest.ConfigSchema[key]
		if !exists || (!field.Secret && field.Type != "password") {
			fail(w, r, 400, "INVALID_MODULE_CONFIG", "只能清除已声明的秘密字段")
			return
		}
		delete(state.Config, key)
	}
	if err := s.catalog.WithPlaybackLibraryScope(r.Context(), input.LibraryScope, func() error {
		return s.manager.UpdateConfiguration(r.Context(), id, state, input.Transport, input.MediaAccess)
	}); err != nil {
		fail(w, r, 400, "MODULE_UPDATE_FAILED", err.Error())
		return
	}
	s.audit(r, "configure_module", id)
	respond(w, 200, map[string]any{"status": "saved", "moduleId": id})
}

func (s *Service) reload(w http.ResponseWriter, r *http.Request) {
	report, err := s.manager.Reload(r.Context())
	if err != nil {
		fail(w, r, 500, "MODULE_RELOAD_FAILED", "模块目录暂时不可用")
		return
	}
	s.audit(r, "reload_modules", "all")
	respond(w, 200, report)
}

func (s *Service) install(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Manifest     module.Manifest      `json:"manifest"`
		LibraryScope *module.LibraryScope `json:"libraryScope"`
		Config       map[string]any       `json:"config"`
		Token        string               `json:"token"`
		Enabled      bool                 `json:"enabled"`
	}
	if err := decode(w, r, &input); err != nil {
		fail(w, r, 400, "INVALID_MODULE", "模块清单格式不正确")
		return
	}
	if err := s.catalog.WithPlaybackLibraryScope(r.Context(), input.LibraryScope, func() error {
		return s.manager.Install(r.Context(), &input.Manifest, module.Settings{Enabled: input.Enabled, Config: input.Config, Token: input.Token, LibraryScope: input.LibraryScope})
	}); err != nil {
		fail(w, r, 400, "MODULE_INSTALL_FAILED", err.Error())
		return
	}
	s.audit(r, "install_module", input.Manifest.ID)
	respond(w, 201, map[string]any{"status": "installed", "moduleId": input.Manifest.ID})
}

func (s *Service) audit(r *http.Request, action, id string) {
	principal, _ := auth.PrincipalFromContext(r.Context())
	_, _ = s.db.DB().ExecContext(r.Context(), s.db.Rebind(`INSERT INTO audit_logs
		(id, actor_user_id, action, resource_type, resource_id, request_id, result, metadata_json, created_at)
		VALUES (?, ?, ?, 'module', ?, ?, 'success', '{}', ?)`), "mod_"+rand.Text(), principal.User.ID, action, id, middleware.GetReqID(r.Context()), time.Now().UTC().Format(time.RFC3339Nano))
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}
func respond(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
func fail(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "requestId": middleware.GetReqID(r.Context())}})
}
