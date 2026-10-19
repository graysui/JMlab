package catalog

import (
	"context"
	"errors"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
	"path/filepath"
	"testing"
	"time"
)

type genericRanker struct {
	id     string
	fail   bool
	change func()
}

func (f *genericRanker) ModuleID() string { return f.id }
func (f *genericRanker) Caps(context.Context) (module.MetadataCaps, error) {
	return module.MetadataCaps{SupportsRankings: true, SupportedTypes: []module.ItemType{module.ItemMovie}}, nil
}
func (f *genericRanker) Search(context.Context, string, module.ItemType, int, int) ([]module.MediaSummary, int, error) {
	return nil, 0, nil
}
func (f *genericRanker) GetRankings(_ context.Context, kind, period string, page int) (module.RankingSnapshot, error) {
	if f.change != nil {
		f.change()
	}
	if page > 1 {
		return module.RankingSnapshot{}, nil
	}
	return module.RankingSnapshot{RankingType: kind, Period: period, Items: []module.MediaSummary{{SourceID: "movie-1", ItemType: module.ItemMovie, Title: "普通电影"}}}, nil
}
func (f *genericRanker) GetDetail(_ context.Context, id string, _ map[string]string) (module.MediaDetail, error) {
	if f.fail {
		return module.MediaDetail{}, errors.New("unavailable")
	}
	return module.MediaDetail{SourceID: id, ItemType: module.ItemMovie, Title: "普通电影", ProviderIDs: map[string]string{"tmdb": "42"}}, nil
}

func TestGenericModuleRankingsAreScopedAndPreserveSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "rankings.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db, nil, nil)
	registry := module.NewRegistry()
	s.SetModuleRegistry(registry)
	one, two := &genericRanker{id: "source-one"}, &genericRanker{id: "source-two"}
	register := func(f *genericRanker) {
		t.Helper()
		manifest := &module.Manifest{ID: f.id, Name: f.id, Version: "1", Type: module.TypeMetadata, Transport: module.TransportConfig{Type: module.TransportInProcess}, Rankings: []module.RankingDefinition{{ID: "community_top", Name: "社区榜单", Periods: []module.FieldOption{{Value: "today", Label: "今日"}}, MaxPages: 2}}}
		if err := registry.Register(&module.ModuleEntry{Manifest: manifest, Status: module.StatusActive, MetadataInstance: f}); err != nil {
			t.Fatal(err)
		}
	}
	register(one)
	register(two)
	run := func(f *genericRanker, want string) {
		t.Helper()
		id, _, err := s.EnqueueModuleRankingTask(ctx, f.id, "community_top", "today", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB().Exec("UPDATE tasks SET max_retries=1 WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		task, claimed, err := s.claimTask(ctx)
		if err != nil || !claimed {
			t.Fatal(err)
		}
		s.processRankingTask(ctx, task)
		var status string
		_ = db.DB().QueryRow("SELECT status FROM tasks WHERE id=?", id).Scan(&status)
		if status != want {
			t.Fatalf("%s got %s, want %s", f.id, status, want)
		}
	}
	run(one, "succeeded")
	run(two, "succeeded")
	one.fail = true
	run(one, "failed")
	var count int
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM item_rankings WHERE is_current=1").Scan(&count); err != nil || count != 2 {
		t.Fatalf("cross-module overwrite or snapshot lost %d %v", count, err)
	}
	if _, _, err := s.EnqueueModuleRankingTask(ctx, one.id, "community_top", "undeclared", "", ""); err == nil {
		t.Fatal("accepted undeclared period")
	}
	one.fail = false
	one.change = func() { one.change = nil; _ = registry.SetStatus(one.id, module.StatusDisabled, "") }
	run(one, "failed")
	if err := db.DB().QueryRow("SELECT COUNT(*) FROM item_rankings WHERE is_current=1").Scan(&count); err != nil || count != 2 {
		t.Fatal("stale revision changed snapshots")
	}
	if _, _, _, err := parseLibraryFilter([]byte(`{"rankingType":"community_top","rankingPeriod":"today"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestModuleScheduleDailyAndInterval(t *testing.T) {
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	last := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	for _, tc := range []struct {
		cfg  moduleRankingSchedule
		want bool
	}{
		{moduleRankingSchedule{UpdateTime: "09:00"}, false},
		{moduleRankingSchedule{UpdateTime: "07:00", LastRunAt: &last}, true},
		{moduleRankingSchedule{UpdateTime: "04:00", LastRunAt: &last}, false},
		{moduleRankingSchedule{IntervalHours: 3, LastRunAt: &last}, false},
		{moduleRankingSchedule{IntervalHours: 1, LastRunAt: &last}, true},
	} {
		if got := moduleRankingScheduleDue(tc.cfg, now); got != tc.want {
			t.Fatalf("%+v got %v", tc.cfg, got)
		}
	}
}
