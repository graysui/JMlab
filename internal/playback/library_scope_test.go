package playback

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/module"
)

func TestPlaybackScopeEnforcesDiscoveryCachedSourcesAndResolution(t *testing.T) {
	s, item := discoveryService(t)
	ctx := context.Background()
	s.catalog = catalog.NewService(s.db, nil, nil)
	s.catalog.SetModuleRegistry(s.modules)
	for _, q := range []string{
		`INSERT INTO virtual_libraries(id,name,item_type,filter_json,sort_json,is_public,is_adult,enabled,created_at,updated_at) VALUES ('chosen','内容库','mixed','{}','{}',1,0,1,'now','now')`,
		`INSERT INTO library_items(library_id,item_id,position,created_at) VALUES ('chosen','discovery-item',0,'now')`,
	} {
		if _, err := s.db.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	registerDiscovery(t, s, discoveryFixture{id: "scoped-stream", discover: func(context.Context) ([]module.StreamCandidate, error) {
		calls.Add(1)
		return []module.StreamCandidate{{CandidateID: "one"}}, nil
	}})
	registerDiscovery(t, s, discoveryFixture{id: "unrestricted-stream"})
	setScope := func(scope *module.LibraryScope) {
		t.Helper()
		entry, _ := s.modules.Get("scoped-stream")
		entry.LibraryScope = scope
		if err := s.modules.Register(entry); err != nil {
			t.Fatal(err)
		}
	}
	setScope(&module.LibraryScope{Mode: "selected", LibraryIDs: []string{}})
	if err := s.DiscoverItemSources(ctx, item); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("unselected module was called")
	}
	setScope(&module.LibraryScope{Mode: "selected", LibraryIDs: []string{"chosen"}})
	if err := s.DiscoverItemSources(ctx, item); err != nil {
		t.Fatal(err)
	}
	sources, err := s.listSourceRecords(ctx, item.ID)
	if err != nil || len(sources) != 2 || calls.Load() != 1 {
		t.Fatal(len(sources), calls.Load(), err)
	}
	var scoped sourceRecord
	for _, source := range sources {
		if source.ModuleID == "scoped-stream" {
			scoped = source
		}
	}
	assertBlocked := func() {
		t.Helper()
		before := calls.Load()
		if err := s.DiscoverItemSources(ctx, item); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != before {
			t.Fatal("out-of-scope discovery called provider")
		}
		visible, err := s.listSourceRecords(ctx, item.ID)
		if err != nil || len(visible) != 1 || visible[0].ModuleID != "unrestricted-stream" {
			t.Fatal(visible, err)
		}
		if _, err := s.sourceForItem(ctx, item.ID, scoped.ID); err == nil {
			t.Fatal("cached source bypassed scope")
		}
		if _, err := s.resolveModuleSource(ctx, item, scoped, false); err == nil {
			t.Fatal("resolution bypassed scope")
		}
		if _, err := s.probeModuleSource(ctx, item, scoped); err == nil {
			t.Fatal("probe bypassed scope")
		}
		clause, args := s.catalog.AvailableSourceSQL("candidate", "vi")
		var count int
		if err := s.db.DB().QueryRow("SELECT COUNT(*) FROM stream_sources candidate JOIN media_items vi ON vi.id=candidate.item_id WHERE candidate.module_id='scoped-stream' AND ("+clause+")", args...).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	}
	if _, err := s.db.DB().Exec("UPDATE virtual_libraries SET enabled=0 WHERE id='chosen'"); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	if _, err := s.db.DB().Exec("DELETE FROM virtual_libraries WHERE id='chosen'"); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	setScope(&module.LibraryScope{Mode: "all", LibraryIDs: []string{}})
	if err := s.DiscoverItemSources(ctx, item); err != nil {
		t.Fatal(err)
	}
	visible, err := s.listSourceRecords(ctx, item.ID)
	if err != nil || len(visible) != 2 {
		t.Fatal(visible, err)
	}
}
