package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/repository"
)

func bootstrapTestDatabase(t *testing.T) *repository.Database {
	t.Helper()
	db, err := repository.Open(context.Background(), "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "bootstrap.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestEnsureBootstrapRequiresCredentialsWhenDatabaseHasNoUsers(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		username string
		password string
	}{
		{name: "no credentials"},
		{name: "username only", username: "admin"},
		{name: "password only", password: "fixture-password"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db := bootstrapTestDatabase(t)
			service := NewService(db, config.Config{BootstrapAdminUsername: scenario.username, BootstrapAdminPassword: scenario.password}, nil)
			err := service.EnsureBootstrap(context.Background())
			if err == nil || !strings.Contains(err.Error(), "JMLAB_BOOTSTRAP_ADMIN_PASSWORD") {
				t.Fatalf("empty database must report how to set the first password: %v", err)
			}
			count, err := service.store.CountUsers(context.Background())
			if err != nil || count != 0 {
				t.Fatalf("missing credentials must not create an account: count=%d err=%v", count, err)
			}
		})
	}
}

func TestEnsureBootstrapRejectsShortPasswordWithoutCreatingUser(t *testing.T) {
	db := bootstrapTestDatabase(t)
	service := NewService(db, config.Config{BootstrapAdminUsername: "admin", BootstrapAdminPassword: "12345"}, nil)
	if err := service.EnsureBootstrap(context.Background()); err == nil {
		t.Fatal("five-character bootstrap password was accepted")
	}
	if count, err := service.store.CountUsers(context.Background()); err != nil || count != 0 {
		t.Fatalf("invalid password must not create an account: count=%d err=%v", count, err)
	}
}

func TestEnsureBootstrapCreatesAdminOnceAndPreservesExistingCredentials(t *testing.T) {
	ctx := context.Background()
	db := bootstrapTestDatabase(t)
	service := NewService(db, config.Config{BootstrapAdminUsername: "admin", BootstrapAdminPassword: "123456"}, nil)
	if err := service.EnsureBootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	created, originalHash, err := service.store.FindUserByUsername(ctx, "admin")
	if err != nil || !created.IsAdmin || !VerifyPassword(originalHash, "123456") {
		t.Fatalf("six-character password must create a usable administrator: %v", err)
	}
	for _, scenario := range []struct {
		name string
		cfg  config.Config
	}{
		{name: "removed credentials"},
		{name: "changed password", cfg: config.Config{BootstrapAdminUsername: "admin", BootstrapAdminPassword: "different-password"}},
		{name: "changed username", cfg: config.Config{BootstrapAdminUsername: "replacement", BootstrapAdminPassword: "different-password"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			restarted := NewService(db, scenario.cfg, nil)
			if err := restarted.EnsureBootstrap(ctx); err != nil {
				t.Fatalf("an existing user must not require bootstrap settings: %v", err)
			}
			user, hash, err := restarted.store.FindUserByUsername(ctx, "admin")
			if err != nil || user.ID != created.ID || hash != originalHash {
				t.Fatalf("bootstrap must preserve the existing account and password: %v", err)
			}
			if count, err := restarted.store.CountUsers(ctx); err != nil || count != 1 {
				t.Fatalf("bootstrap must not create a replacement account: count=%d err=%v", count, err)
			}
		})
	}
}
