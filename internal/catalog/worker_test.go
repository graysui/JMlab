package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/graysui/JMlab/internal/metadata"
	"github.com/graysui/JMlab/internal/repository"
)

type rankingFixture struct {
	code string
	fail bool
}

func (a *rankingFixture) Name() string { return "javdb" }
func (a *rankingFixture) Search(context.Context, string) ([]metadata.SourceSummary, error) {
	return nil, nil
}
func (a *rankingFixture) Ranking(context.Context, string, string) (metadata.RankingSnapshot, error) {
	return metadata.RankingSnapshot{Items: []metadata.RankingEntry{{SourceID: a.code, Code: a.code, Title: "Test movie"}}}, nil
}
func (a *rankingFixture) Fetch(context.Context, string, string) (metadata.SourceItem, error) {
	if a.fail {
		return metadata.SourceItem{}, errors.New("upstream unavailable")
	}
	return metadata.SourceItem{SourceID: a.code, Code: a.code, Title: "Test movie", People: []metadata.SourceRelation{{Name: "Test actor", Role: "actor"}}}, nil
}

func TestRankingRetainsUnavailableAndOffListMedia(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db, nil, nil)
	if err := s.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	adapter := &rankingFixture{code: "AUD-001"}
	s.SetSourceAdapter(adapter)
	s.SetSourceSeeder(func(context.Context, Item) error { return errors.New("source temporarily unavailable") })
	run := func(want string) {
		t.Helper()
		id, _, err := s.EnqueueRankingTask(ctx, "ranking", "daily", "", "")
		if err != nil {
			t.Fatal(err)
		}
		task, claimed, err := s.claimTask(ctx)
		if err != nil || !claimed {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		s.processRankingTask(ctx, task)
		var status string
		if err := db.DB().QueryRow("SELECT status FROM tasks WHERE id = ?", id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("task status %s, want %s", status, want)
		}
	}
	run("succeeded")
	var itemID string
	if err := db.DB().QueryRow("SELECT id FROM media_items WHERE code = 'AUD-001'").Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, created_at, updated_at) VALUES ('test-user', 'test-user', 'Test', 'unused', 'now', 'now')",
		"INSERT INTO user_item_states (user_id, item_id, is_favorite, playback_position_ticks, created_at, updated_at) VALUES ('test-user', '" + itemID + "', 1, 123, 'now', 'now')",
	} {
		if _, err := db.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	run("succeeded") // Repeated unavailable source discovery must preserve identity and state.
	adapter.code = "AUD-002"
	run("succeeded") // First media leaves the ranking, but remains in the catalog.
	var favorites, position, count int
	if err := db.DB().QueryRow("SELECT is_favorite, playback_position_ticks FROM user_item_states WHERE item_id = ?", itemID).Scan(&favorites, &position); err != nil {
		t.Fatal(err)
	}
	if favorites != 1 || position != 123 {
		t.Fatal("user state changed")
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM media_items WHERE id = ?",
		"SELECT COUNT(*) FROM library_items WHERE item_id = ? AND library_id = 'library-default'",
		"SELECT COUNT(*) FROM item_people WHERE item_id = ?",
	} {
		if err := db.DB().QueryRow(query, itemID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("lost catalog reference: %s: %d %v", query, count, err)
		}
	}
	adapter.fail = true
	run("queued") // Retryable upstream failure must leave the last snapshot intact.
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM item_rankings WHERE is_current = 1 AND item_id = 'javdb:AUD-002'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("lost last snapshot: %d %v", count, err)
	}
}
