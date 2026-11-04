package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/config"
)

func TestClientConnectionInfoRequiresAuthAndUsesConfiguredPublicURL(t *testing.T) {
	ctx := context.Background()
	db := bootstrapTestDatabase(t)
	cfg := config.Config{PublicURL: "https://media.example.test/jmlab/", BootstrapAdminUsername: "admin", BootstrapAdminPassword: "test-password"}
	service := NewService(db, cfg, nil)
	if err := service.EnsureBootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	service.RegisterRoutes(router)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/me/connection", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request status = %d, want 401", unauthenticated.Code)
	}

	_, token, err := service.AuthenticateCredentials(ctx, "admin", "test-password", "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/me/connection", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated request status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var result struct {
		ServerURL string `json:"serverUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.ServerURL != cfg.PublicURL {
		t.Fatalf("serverUrl = %q, want configured URL %q", result.ServerURL, cfg.PublicURL)
	}
}

func TestSessionCookieWorksAtConfiguredPublicURL(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		environment string
		publicURL   string
	}{
		{"production over HTTP", "production", "http://192.0.2.10:8097"},
		{"production over HTTPS", "production", "https://media.example.test"},
		{"development over HTTPS", "development", "https://media.example.test"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := &Service{cfg: config.Config{Environment: scenario.environment, PublicURL: scenario.publicURL}}
			response := httptest.NewRecorder()
			service.setSessionCookie(response, "test-only-session")
			origin, err := url.Parse(scenario.publicURL)
			if err != nil {
				t.Fatal(err)
			}
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			jar.SetCookies(origin, response.Result().Cookies())
			imageURL := origin.ResolveReference(&url.URL{Path: "/api/v1/items/example/images/poster"})
			cookies := jar.Cookies(imageURL)
			if len(cookies) != 1 || cookies[0].Name != sessionCookieName || cookies[0].Value != "test-only-session" {
				t.Fatalf("配置地址的图片请求未携带登录 Cookie: %v", cookies)
			}
			if origin.Scheme == "https" {
				imageURL.Scheme = "http"
				if cookies := jar.Cookies(imageURL); len(cookies) != 0 {
					t.Fatal("HTTPS 会话 Cookie 不得发送到 HTTP 地址")
				}
			}
		})
	}
}
