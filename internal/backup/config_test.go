package backup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/moduleadmin"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/securevalue"
)

func openConfigTestService(t *testing.T, key string) *Service {
	t.Helper()
	root := t.TempDir()
	db, err := repository.Open(context.Background(), "sqlite://"+filepath.ToSlash(filepath.Join(root, "backup.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := NewService(db, nil, key, "", root)
	mgr := module.NewManager(filepath.Join(root, "modules"), nil)
	if err := mgr.SetStateStore(context.Background(), moduleadmin.NewStore(db, key)); err != nil {
		t.Fatal(err)
	}
	s.SetModuleManager(mgr)
	t.Cleanup(func() { mgr.Stop(context.Background()) })
	return s
}

func execConfigTest(t *testing.T, s *Service, query string, args ...any) {
	t.Helper()
	if _, err := s.db.DB().Exec(s.db.Rebind(query), args...); err != nil {
		t.Fatal(err)
	}
}

func testCipher(t *testing.T, key, text string) string {
	t.Helper()
	cipher, err := securevalue.Encrypt(key, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func countConfigTest(t *testing.T, s *Service, query string) int {
	t.Helper()
	var count int
	if err := s.db.DB().QueryRow(query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestConfigBackupRestoresModulesStorageAndRuntimeThroughAuthenticatedAPI(t *testing.T) {
	ctx := context.Background()
	const key = "temporary-config-backup-key"
	var offline atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "private-upstream-secret", 503)
			return
		}
		if r.Header.Get("Authorization") != "Bearer module-token-secret" {
			http.Error(w, "bad auth", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/caps":
			json.NewEncoder(w).Encode(module.MetadataCaps{SupportsSearch: true, SupportedTypes: []module.ItemType{module.ItemMovie}})
		case "/api/v1/search":
			configJSON, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-JMlab-Module-Config"))
			if !bytes.Contains(configJSON, []byte("module-password-secret")) {
				t.Error("restored settings were not sent to module")
			}
			json.NewEncoder(w).Encode(map[string]any{"items": []module.MediaSummary{{SourceID: "one", Title: "Restored movie", ItemType: module.ItemMovie}}, "total": 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	source := openConfigTestService(t, key)
	mf := &module.Manifest{ID: "restore-provider", Name: "Restore provider", Version: "1", Type: module.TypeMetadata,
		Transport: module.TransportConfig{Type: module.TransportHTTP, URL: upstream.URL}, Permissions: &module.HostPermissions{Storage: true},
		ConfigSchema: map[string]module.ConfigField{"password": {Type: "password", Required: true, Secret: true, Label: "密码"}}}
	state := module.Settings{Enabled: true, Config: map[string]any{"password": "module-password-secret"}, Token: "module-token-secret"}
	if err := source.modules.Install(ctx, mf, state); err != nil {
		t.Fatal(err)
	}
	storageCipher := testCipher(t, key+":module-storage:"+mf.ID, `{"cursor":"namespace-secret"}`)
	execConfigTest(t, source, `INSERT INTO module_storage VALUES(?,?,?,'now')`, mf.ID, "cursor", storageCipher)
	targetCipher, err := source.encryptConfig(targetConfig{URL: "https://backup.invalid/store", AuthType: "bearer", Token: "backup-token-secret"})
	if err != nil {
		t.Fatal(err)
	}
	execConfigTest(t, source, `INSERT INTO backup_targets(id,target_type,display_name,enabled,retention_count,encrypted_config,created_at,updated_at) VALUES ('target','webdav','Test',1,7,?,'now','now')`, targetCipher)
	doc := ExportDocument{FormatVersion: exportFormatVersion, Items: []ExportItem{{ID: "restored-movie", Code: "media:restored-movie", Source: mf.ID, SourceID: "one", Title: "Restored movie", ItemType: "movie", MetadataModuleID: mf.ID}}}
	if _, err := source.applyImport(ctx, doc, importOptions{Mode: "merge"}); err != nil {
		t.Fatal(err)
	}
	artifact, err := source.buildBackupArtifact(ctx, backupTaskPayload{RunID: "test", TargetIDs: []string{"target"}, IncludeEncryptedConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := parseRestoreArchive(artifact.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"module-password-secret", "module-token-secret", "namespace-secret", "backup-token-secret", upstream.URL} {
		if bytes.Contains(archive.EncryptedConfig, []byte(secret)) {
			t.Fatalf("config archive leaked secret %q", secret)
		}
	}
	destination := openConfigTestService(t, key)
	store := auth.NewStore(destination.db)
	admin, err := store.CreateUser(ctx, "admin", "Admin", "unused", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, admin, "test", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	destination.auth = auth.NewService(destination.db, config.Config{MasterKey: key, DataDir: destination.dataDir}, nil)
	router := chi.NewRouter()
	router.Route("/api/v1", destination.RegisterRoutes)
	request := func(path string, include bool, want int) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"archiveBase64": base64.StdEncoding.EncodeToString(artifact.Data), "mode": "overwrite", "includeEncryptedConfig": include, "confirmation": restoreConfirmation})
		r := httptest.NewRequest("POST", path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), upstream.URL) {
			t.Fatal("restore response exposed module secret", w.Body.String())
		}
		return w.Body.String()
	}
	preview := request("/api/v1/backup/restore/preview", true, 200)
	if !strings.Contains(preview, `"modules":1`) || countConfigTest(t, destination, "SELECT COUNT(*) FROM installed_modules") != 0 {
		t.Fatal("preview changed data or omitted summary", preview)
	}
	destination.masterKey = "wrong-key"
	request("/api/v1/backup/restore/preview", true, 400)
	request("/api/v1/backup/restore", true, 409)
	if countConfigTest(t, destination, "SELECT COUNT(*) FROM media_items") != 0 {
		t.Fatal("wrong key restore modified media")
	}
	destination.masterKey = key
	// Omitting the explicit option restores media only, preserving connections.
	request("/api/v1/backup/restore", false, 200)
	if countConfigTest(t, destination, "SELECT COUNT(*) FROM installed_modules") != 0 {
		t.Fatal("configuration restored without opt-in")
	}
	result := request("/api/v1/backup/restore", true, 200)
	if !strings.Contains(result, `"moduleReloadPending":false`) {
		t.Fatal(result)
	}
	entry, ok := destination.modules.Registry().Get(mf.ID)
	if !ok || entry.Status != module.StatusActive {
		t.Fatal("restored module not active", entry)
	}
	items, _, err := entry.MetadataInstance.Search(ctx, "restored", module.ItemMovie, 1, 20)
	if err != nil || len(items) != 1 || items[0].Title != "Restored movie" {
		t.Fatal(items, err)
	}
	var encrypted string
	if err := destination.db.DB().QueryRow(`SELECT value_encrypted FROM module_storage WHERE module_id='restore-provider' AND storage_key='cursor'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := securevalue.Decrypt(key+":module-storage:"+mf.ID, encrypted)
	if err != nil || string(plain) != `{"cursor":"namespace-secret"}` {
		t.Fatal("namespace not restored", err)
	}
	if _, err := destination.loadTarget(ctx, "target"); err != nil {
		t.Fatal("target config not restored", err)
	}
	if countConfigTest(t, destination, "SELECT COUNT(*) FROM module_restore_queue") != 0 {
		t.Fatal("successful reload left queue")
	}
	// Restoring while offline must revoke the old instance and retain a retry.
	offline.Store(true)
	result = request("/api/v1/backup/restore", true, 200)
	if !strings.Contains(result, `"moduleReloadPending":true`) {
		t.Fatal(result)
	}
	failed, _ := destination.modules.Registry().Get(mf.ID)
	if failed.Status != module.StatusError || failed.MetadataInstance != nil {
		t.Fatal("old instance remained callable after config restore")
	}
	if err := destination.modules.UpdateSettings(ctx, mf.ID, state); err == nil {
		t.Fatal("pending restore allowed conflicting configuration edit")
	}
	// Recreate the manager to prove the retry survives a process restart.
	offline.Store(false)
	restarted := module.NewManager(filepath.Join(destination.dataDir, "modules"), nil)
	defer restarted.Stop(ctx)
	if err := restarted.SetStateStore(ctx, moduleadmin.NewStore(destination.db, key)); err != nil {
		t.Fatal(err)
	}
	if report, err := restarted.Reload(ctx); err != nil || len(report.Errors) != 0 {
		t.Fatal(report, err)
	}
	reloaded, _ := restarted.Registry().Get(mf.ID)
	if reloaded == nil || reloaded.Status != module.StatusActive || restarted.Settings(mf.ID).Token != state.Token {
		t.Fatal("pending restore failed after restart")
	}
	// Persisted disabled state must also restore without network access.
	if err := source.modules.DisableModule(mf.ID); err != nil {
		t.Fatal(err)
	}
	configData, err := source.buildEncryptedConfigExport(ctx, []string{"target"})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := destination.decodeConfigBundle(configData)
	if err != nil {
		t.Fatal(err)
	}
	offline.Store(true)
	destination.modules = restarted
	if _, report, err := destination.applyRestore(ctx, doc, importOptions{Mode: "overwrite"}, &bundle); err != nil || len(report.Errors) != 0 {
		t.Fatal(report, err)
	}
	disabled, _ := restarted.Registry().Get(mf.ID)
	if disabled.Status != module.StatusDisabled || restarted.Settings(mf.ID).Enabled {
		t.Fatal("disabled state not restored")
	}
	// The restored manifest was written only to the configured temporary root.
	if _, err := os.Stat(filepath.Join(destination.dataDir, "modules", mf.ID, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	// A moved module process can be repaired from the ordinary settings API;
	// it must not be permanently locked behind a pending restore.
	bundle.Modules[0].Settings.Enabled = true
	bundle.Modules[0].Manifest.Transport.URL = "http://127.0.0.1:1"
	if _, report, err := destination.applyRestore(ctx, doc, importOptions{Mode: "overwrite"}, &bundle); err != nil || len(report.Errors) == 0 {
		t.Fatal(report, err)
	}
	offline.Store(false)
	connection := module.TransportConfig{Type: module.TransportHTTP, URL: upstream.URL}
	if err := restarted.UpdateConfiguration(ctx, mf.ID, state, &connection, nil); err != nil {
		t.Fatal("cannot repair pending source address", err)
	}
	repaired, _ := restarted.Registry().Get(mf.ID)
	if repaired.Status != module.StatusActive || repaired.Manifest.Transport.URL != upstream.URL || countConfigTest(t, destination, "SELECT COUNT(*) FROM module_restore_queue") != 0 {
		t.Fatal("repaired definition not published", repaired)
	}
}

func TestConfigRestoreRejectsWrongKeysAndRollsBackMediaAndConfigurationTogether(t *testing.T) {
	ctx := context.Background()
	source := openConfigTestService(t, "source-key")
	mf := &module.Manifest{ID: "offline-provider", Name: "Offline", Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: "http://127.0.0.1:1"}}
	if err := source.modules.Install(ctx, mf, module.Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	data, err := source.buildEncryptedConfigExport(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wrong := openConfigTestService(t, "other-key")
	if _, err := wrong.decodeConfigBundle(data); err == nil || !strings.Contains(err.Error(), "主密钥") {
		t.Fatal("wrong master key accepted", err)
	}
	destination := openConfigTestService(t, "source-key")
	bundle, err := destination.decodeConfigBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	doc := ExportDocument{FormatVersion: exportFormatVersion, Items: []ExportItem{{ID: "rollback", Source: "offline-provider", SourceID: "1", Title: "Rollback", ItemType: "movie"}}}
	// Inject a SQL failure after the media and module settings were written.
	execConfigTest(t, destination, `CREATE TRIGGER reject_module_restore BEFORE INSERT ON module_restore_queue BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if _, _, err := destination.applyRestore(ctx, doc, importOptions{Mode: "overwrite"}, &bundle); err == nil {
		t.Fatal("failed config write accepted")
	}
	for _, table := range []string{"media_items", "installed_modules", "module_restore_queue", "module_storage"} {
		if countConfigTest(t, destination, "SELECT COUNT(*) FROM "+table) != 0 {
			t.Fatal("restore failed to roll back", table)
		}
	}
	if _, ok := destination.modules.Registry().Get(mf.ID); ok {
		t.Fatal("failed transaction changed runtime")
	}
	execConfigTest(t, destination, "DROP TRIGGER reject_module_restore")
	// A filesystem failure after commit is reported honestly and queued, then
	// reconciled on the next scan without rerunning the media import.
	if err := os.WriteFile(filepath.Join(destination.dataDir, "modules"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, report, err := destination.applyRestore(ctx, doc, importOptions{Mode: "overwrite"}, &bundle); err != nil || len(report.Errors) == 0 {
		t.Fatal("file failure not retained", report, err)
	}
	if countConfigTest(t, destination, "SELECT COUNT(*) FROM media_items") != 1 || countConfigTest(t, destination, "SELECT COUNT(*) FROM module_restore_queue") != 1 {
		t.Fatal("committed restore was incorrectly rolled back or lost retry")
	}
	if err := os.Remove(filepath.Join(destination.dataDir, "modules")); err != nil {
		t.Fatal(err)
	}
	if report, err := destination.modules.Reload(ctx); err != nil || len(report.Errors) != 0 {
		t.Fatal("retry did not recover", report, err)
	}
	var routing string
	if err := destination.db.DB().QueryRow(`SELECT metadata_module_id FROM media_items WHERE id='rollback'`).Scan(&routing); err != nil || routing != "offline-provider" {
		t.Fatal("generic media restored with JavDB routing", routing, err)
	}
	// Invalid metadata in an authenticated envelope is still rejected.
	bundle.Modules[0].Manifest.ID = "../escape"
	plain, _ := json.Marshal(bundle)
	bad := configEnvelope{FormatVersion: configFormatVersion, Payload: testCipher(t, "source-key:backup-config", string(plain))}
	encoded, _ := json.Marshal(bad)
	if _, err := destination.decodeConfigBundle(encoded); err == nil {
		t.Fatal("unsafe manifest accepted")
	}
}

func TestLegacyConfigRestoreAndMergePreserveExistingSettings(t *testing.T) {
	s := openConfigTestService(t, "legacy-key")
	ctx := context.Background()
	cipher, err := s.encryptConfig(targetConfig{Path: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(configEnvelope{FormatVersion: "jmlab.encrypted-config.v1", Targets: []encryptedTarget{{ID: "legacy", TargetType: "local", DisplayName: "Legacy", Enabled: true, RetentionCount: 7, EncryptedConfig: cipher}}})
	bundle, err := s.decodeConfigBundle(legacy)
	if err != nil || len(bundle.Modules) != 0 || len(bundle.Targets) != 1 {
		t.Fatal(bundle, err)
	}
	doc := ExportDocument{FormatVersion: exportFormatVersion}
	if _, _, err := s.applyRestore(ctx, doc, importOptions{Mode: "merge"}, &bundle); err != nil {
		t.Fatal(err)
	}
	execConfigTest(t, s, `UPDATE backup_targets SET display_name='Keep me' WHERE id='legacy'`)
	if _, _, err := s.applyRestore(ctx, doc, importOptions{Mode: "merge"}, &bundle); err != nil {
		t.Fatal(err)
	}
	target, err := s.loadTarget(ctx, "legacy")
	if err != nil || target.DisplayName != "Keep me" {
		t.Fatal("merge replaced existing connection", target, err)
	}
	// Validate merged namespace quotas in the transaction, not just the archive.
	values := []encryptedStorage{}
	for i := 0; i < 256; i++ {
		key := strings.Repeat("x", i%100) + time.Unix(int64(i), 0).Format("150405")
		execConfigTest(t, s, `INSERT INTO module_storage VALUES ('namespace',?,?,'now')`, key, testCipher(t, "legacy-key:module-storage:namespace", `{}`))
	}
	values = append(values, encryptedStorage{ModuleID: "namespace", Key: "new", Value: testCipher(t, "legacy-key:module-storage:namespace", `{}`)})
	if _, _, err := s.applyRestore(ctx, doc, importOptions{Mode: "merge"}, &configBundle{Storage: values}); err == nil {
		t.Fatal("merge exceeded namespace quota")
	}
	if countConfigTest(t, s, "SELECT COUNT(*) FROM module_storage WHERE module_id='namespace'") != 256 {
		t.Fatal("quota failure did not roll back")
	}
}
