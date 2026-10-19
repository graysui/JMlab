package module

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type actionFunc func(context.Context, string, map[string]any) (ActionResult, error)

func (f actionFunc) InvokeAction(c context.Context, id string, in map[string]any) (ActionResult, error) {
	return f(c, id, in)
}

func TestActionsPersistSecretsAndRejectStaleRevision(t *testing.T) {
	ctx := context.Background()
	m := NewManager("", nil)
	manifest := &Manifest{ID: "generic-source", Name: "普通影视", Version: "1", Type: TypeStream, Transport: TransportConfig{Type: TransportInProcess}, ConfigSchema: map[string]ConfigField{"apiToken": {Type: "password", Secret: true}}, Actions: []ActionDescriptor{{ID: "login", Name: "登录", Fields: map[string]ConfigField{"password": {Type: "password", Required: true, Secret: true}}}}}
	calls := 0
	factory := func(context.Context, Settings) (MetadataProvider, StreamProvider, ActionProvider, error) {
		return nil, nil, actionFunc(func(context.Context, string, map[string]any) (ActionResult, error) {
			calls++
			return ActionResult{Message: "登录成功", ConfigPatch: map[string]any{"apiToken": "new-token"}}, nil
		}), nil
	}
	if err := m.RegisterFactory(ctx, manifest, factory); err != nil {
		t.Fatal(err)
	}
	entry, _ := m.Registry().Get(manifest.ID)
	if _, err := m.InvokeAction(ctx, manifest.ID, "login", entry.Revision, map[string]any{"unknown": "x"}); err == nil || calls != 0 {
		t.Fatal("undeclared input reached module")
	}
	result, err := m.InvokeAction(ctx, manifest.ID, "login", entry.Revision, map[string]any{"password": "test-only"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if string(data) != "{\"message\":\"登录成功\"}" || m.Settings(manifest.ID).Config["apiToken"] != "new-token" {
		t.Fatalf("secret patch not consumed: %s", data)
	}
	if _, err := m.InvokeAction(ctx, manifest.ID, "login", entry.Revision, map[string]any{"password": "x"}); !errors.Is(err, ErrRevisionChanged) {
		t.Fatal(err)
	}
}

func TestExternalActionsUseSameProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/actions/check" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer private-test" {
			t.Error("wrong action transport")
			http.Error(w, "bad", 400)
			return
		}
		var input struct {
			Input map[string]any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Input["query"] != "普通电影" {
			t.Error("missing input")
		}
		_ = json.NewEncoder(w).Encode(ActionResult{Message: "连接正常"})
	}))
	defer server.Close()
	manifest := &Manifest{ID: "external-source", Name: "外部源", Version: "1", Type: TypeMetadata, Transport: TransportConfig{Type: TransportHTTP, URL: server.URL}, Actions: []ActionDescriptor{{ID: "check", Name: "检查连接"}}}
	client, err := NewHTTPModuleClientWithConfig(manifest, nil, "private-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.InvokeAction(context.Background(), "check", map[string]any{"query": "普通电影"})
	if err != nil || result.Message != "连接正常" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestActionResponsesOnlyExposeDeclaredNonsecretFields(t *testing.T) {
	ctx := context.Background()
	m := NewManager("", nil)
	manifest := &Manifest{ID: "private-action", Name: "Private", Version: "1", Type: TypeStream, Transport: TransportConfig{Type: TransportInProcess}, Actions: []ActionDescriptor{{ID: "inspect", Name: "Inspect", Fields: map[string]ConfigField{"password": {Type: "password", Secret: true}}, Columns: map[string]string{"state": "State", "password": "Forbidden"}}}}
	if err := m.RegisterFactory(ctx, manifest, func(context.Context, Settings) (MetadataProvider, StreamProvider, ActionProvider, error) {
		return nil, nil, actionFunc(func(context.Context, string, map[string]any) (ActionResult, error) {
			return ActionResult{Values: map[string]any{"state": "ok", "password": "private", "undeclared": "private"}}, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	entry, _ := m.registry.Get(manifest.ID)
	result, err := m.InvokeAction(ctx, manifest.ID, "inspect", entry.Revision, nil)
	if err != nil || len(result.Values) != 1 || result.Values["state"] != "ok" {
		t.Fatal(result, err)
	}
	if err := m.DisableModule(manifest.ID); err != nil {
		t.Fatal(err)
	}
	entry, _ = m.registry.Get(manifest.ID)
	if _, err := m.InvokeAction(ctx, manifest.ID, "inspect", entry.Revision, nil); err == nil {
		t.Fatal("disabled module action executed")
	}
}
