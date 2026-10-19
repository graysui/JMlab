package modulehost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/securevalue"
)

func TestHostQuotasRateConcurrencyAndTickets(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "limits.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := module.NewRegistry()
	for _, id := range []string{"count-limit", "bytes-limit", "concurrent-module", "rate-module"} {
		if err := registry.Register(&module.ModuleEntry{Manifest: &module.Manifest{ID: id, Permissions: &module.HostPermissions{Storage: true}}, Status: module.StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(db, registry, strings.Repeat("key", 16), "http://127.0.0.1:1")
	router := chi.NewRouter()
	s.RegisterRoutes(router)
	request := func(id, method, path, body, ticket string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/module-host/"+id+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+s.headers(ctx, id)["X-JMlab-Host-Token"])
		r.Header.Set("X-JMlab-Call-Ticket", ticket)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	seed := func(id string, count int, plain []byte) {
		t.Helper()
		encrypted, err := securevalue.Encrypt(s.masterKey+":module-storage:"+id, plain)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for i := 0; i < count; i++ {
			if _, err := tx.Exec("INSERT INTO module_storage VALUES(?,?,?,?)", id, fmt.Sprintf("key-%03d", i), encrypted, "2026-10-17"); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	seed("count-limit", 256, []byte(`1`))
	if w := request("count-limit", "PUT", "/storage/overflow", `{"value":2}`, ""); w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("count-limit", "PUT", "/storage/key-000", `{"value":2}`, ""); w.Code != 200 {
		t.Fatal("overwrite at quota failed", w.Code)
	}
	if w := request("count-limit", "DELETE", "/storage/key-000", "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request("count-limit", "PUT", "/storage/replacement", `{"value":2}`, ""); w.Code != 200 {
		t.Fatal("quota not released", w.Code)
	}
	plain := []byte(`"` + strings.Repeat("x", 16000) + `"`)
	encrypted, _ := securevalue.Encrypt(s.masterKey+":module-storage:bytes-limit", plain)
	seed("bytes-limit", 1024*1024/len(encrypted), plain)
	if w := request("bytes-limit", "PUT", "/storage/overflow", `{"value":`+string(plain)+`}`, ""); w.Code != 413 {
		t.Fatal("byte quota not enforced", w.Code)
	}
	if w := request("bytes-limit", "PUT", "/storage/key-000", `{"value":1}`, ""); w.Code != 200 {
		t.Fatal("smaller replacement failed", w.Code)
	}
	if w := request("bytes-limit", "PUT", "/storage/overflow", `{"value":1}`, ""); w.Code != 200 {
		t.Fatal("space not released", w.Code)
	}
	if w := request("rate-module", "PUT", "/storage/bad", `{"value":1} {"extra":2}`, ""); w.Code != 400 {
		t.Fatal("trailing input accepted")
	}
	for i := 1; i < 120; i++ {
		if w := request("rate-module", "GET", "/storage", "", ""); w.Code != 200 {
			t.Fatalf("premature rate limit at %d: %d", i, w.Code)
		}
	}
	if w := request("rate-module", "GET", "/storage", "", ""); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit missing", w.Code)
	}
	s.mu.Lock()
	s.gates["rate-module"].window = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	if w := request("rate-module", "GET", "/storage", "", ""); w.Code != 200 {
		t.Fatal("window did not reset")
	}
	// Four real handlers are blocked on storage; a fifth is rejected without waiting.
	s.storageMu.Lock()
	var unlock sync.Once
	defer unlock.Do(s.storageMu.Unlock)
	completed := make(chan *httptest.ResponseRecorder, 4)
	for i := 0; i < 4; i++ {
		go func() { completed <- request("concurrent-module", "PUT", "/storage/key", `{"value":1}`, "") }()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		gate := s.gates["concurrent-module"]
		active := 0
		if gate != nil {
			active = len(gate.active)
		}
		s.mu.Unlock()
		if active == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("handlers did not enter")
		}
		time.Sleep(time.Millisecond)
	}
	if w := request("concurrent-module", "PUT", "/storage/fifth", `{"value":1}`, ""); w.Code != 429 || !strings.Contains(w.Body.String(), "CONCURRENCY") {
		t.Fatal("concurrency limit missing", w.Code)
	}
	unlock.Do(s.storageMu.Unlock)
	for i := 0; i < 4; i++ {
		select {
		case w := <-completed:
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("storage handlers blocked")
		}
	}
	entry, _ := registry.Get("concurrent-module")
	for _, claims := range []ticket{
		{Audience: entry.Manifest.ID, Revision: entry.Revision, Chain: []string{entry.Manifest.ID}, Expires: time.Now().Add(-time.Second).Unix()},
		{Audience: "other-module", Revision: entry.Revision, Chain: []string{"other-module"}, Expires: time.Now().Add(time.Minute).Unix()},
		{Audience: entry.Manifest.ID, Revision: "old-revision", Chain: []string{entry.Manifest.ID}, Expires: time.Now().Add(time.Minute).Unix()},
	} {
		encoded, _ := json.Marshal(claims)
		data := base64.RawURLEncoding.EncodeToString(encoded)
		if w := request("concurrent-module", "POST", "/invoke", `{}`, data+"."+s.signature("call", data)); w.Code != 401 {
			t.Fatal("invalid claims accepted", w.Code)
		}
	}
	if w := request("concurrent-module", "POST", "/invoke", `{}`, "tampered.signature"); w.Code != 401 {
		t.Fatal("forged ticket accepted")
	}
	unsafe, err := module.NewHTTPModuleClient(&module.Manifest{ID: "unsafe-module", Name: "Unsafe", Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportHTTP, URL: "http://192.0.2.1:9091"}})
	if err != nil {
		t.Fatal(err)
	}
	defer unsafe.Close()
	if unsafe.SetHostBridge(s) == nil {
		t.Fatal("callback credentials allowed on remote plaintext HTTP")
	}
}
