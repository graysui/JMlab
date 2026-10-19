package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
)

func TestAllMetadataModulesAdoptExistingIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "identity.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db, nil, nil)
	for _, moduleID := range []string{"reference-provider-javdb", "ordinary-movies"} {
		original, _, err := s.saveModuleMedia(ctx, moduleID, module.MediaDetail{SourceID: "upstream-1", Title: "原始标题", ItemType: module.ItemMovie}, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		preserved := "existing-" + moduleID
		if _, err := db.DB().Exec("UPDATE media_items SET id=? WHERE id=?", preserved, original.ID); err != nil {
			t.Fatal(err)
		}
		updated, created, err := s.saveModuleMedia(ctx, moduleID, module.MediaDetail{SourceID: "upstream-1", Title: "更新标题", ItemType: module.ItemMovie}, "", nil)
		if err != nil || created || updated.ID != preserved || updated.IsAdult {
			t.Fatalf("identity or general content changed: %+v %v", updated, err)
		}
	}
}

func TestModuleEntitiesKeepSourceIdentityAndMultipleRoles(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "entities.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db, nil, nil)
	if err := s.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	detail := module.MediaDetail{SourceID: "movie:42", Title: "Movie", ItemType: module.ItemMovie, Entities: []module.EntityRelation{
		{Name: "同名人物", Role: module.RoleActor, SourceID: "person:1", ProviderIDs: map[string]string{"tmdb": "1"}, OriginalName: "Original", AvatarURL: "https://example.test/avatar", Extra: map[string]string{"department": "Acting"}},
		{Name: "同名人物", Role: module.RoleWriter, SourceID: "person:1", ProviderIDs: map[string]string{"tmdb": "1"}},
		{Name: "同名人物", Role: module.RoleDirector, SourceID: "person:2", ProviderIDs: map[string]string{"tmdb": "2"}},
	}}
	for i := 0; i < 2; i++ {
		if _, _, err := s.saveModuleMedia(ctx, "test", detail, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM entities WHERE entity_kind='people'`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM item_entities`).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	var id string
	if err := db.DB().QueryRow(`SELECT entity_id FROM entity_identifiers WHERE namespace='module:test' AND external_id='person:1'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var original, avatar, extra string
	if err := db.DB().QueryRow(`SELECT original_name,avatar_url,extra_attributes_json FROM entities WHERE entity_kind='people' AND id=?`, id).Scan(&original, &avatar, &extra); err != nil || original != "Original" || avatar != "https://example.test/avatar" || extra != `{"department":"Acting"}` {
		t.Fatal(original, avatar, extra, err)
	}
	if _, err := db.DB().Exec(`UPDATE entities SET name='手工人物',attributes_locked=1 WHERE entity_kind='people' AND id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.saveModuleMedia(ctx, "test", detail, "", nil); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.DB().QueryRow(`SELECT name FROM entities WHERE entity_kind='people' AND id=?`, id).Scan(&name); err != nil || name != "手工人物" {
		t.Fatal(name, err)
	}
}

func TestModuleMediaIdentityHierarchyAndFieldLocks(t *testing.T) {
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
	detail := module.MediaDetail{SourceID: "movie:550", Title: "Movie", ItemType: module.ItemMovie, ProviderIDs: map[string]string{"tmdb": "550"}, ExtraAttributes: map[string]any{"originalLanguage": "en"}}
	first, created, err := s.saveModuleMedia(ctx, "tmdb", detail, "", nil)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	_, err = db.DB().Exec(`INSERT INTO metadata_field_states (item_id,field_name,value_json,source,locked,created_at,updated_at) VALUES (?,'title','"Movie"','manual',1,'now','now')`, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail.Title = "New title"
	detail.Overview = "New synopsis"
	updated, created, err := s.saveModuleMedia(ctx, "tmdb", detail, "", nil)
	if err != nil || created || updated.ID != first.ID || updated.Title != "Movie" || updated.Overview != "New synopsis" || updated.ProviderIDs["tmdb"] != "550" {
		t.Fatalf("identity/lock: %+v %v", updated, err)
	}
	series, _, err := s.saveModuleMedia(ctx, "tmdb", module.MediaDetail{SourceID: "tv:550", Title: "Series", ItemType: module.ItemSeries}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if series.ID == first.ID {
		t.Fatal("movie and series collided")
	}
	one := 1
	season, _, err := s.saveModuleMedia(ctx, "tmdb", module.MediaDetail{SourceID: "tv:550:1", Title: "Season 1", ItemType: module.ItemSeason}, series.ID, &one)
	if err != nil {
		t.Fatal(err)
	}
	episode := module.MediaDetail{SourceID: "tv:550:1:1", Title: "Episode 1", ItemType: module.ItemEpisode}
	if _, _, err := s.saveModuleMedia(ctx, "tmdb", episode, first.ID, &one); err == nil {
		t.Fatal("episode under movie accepted")
	}
	item, _, err := s.saveModuleMedia(ctx, "tmdb", episode, season.ID, &one)
	if err != nil || item.ParentID != season.ID || item.IndexNumber == nil || *item.IndexNumber != 1 {
		t.Fatal(item, err)
	}
	if _, _, err := s.saveModuleMedia(ctx, "tmdb", episode, "", nil); err == nil {
		t.Fatal("orphan episode accepted")
	}
}

func TestGenericMediaUsesRoleAndAdultPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "permissions.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db, nil, nil)
	if err := s.EnsureDefaultLibrary(ctx, ""); err != nil {
		t.Fatal(err)
	}
	viewer, err := auth.NewStore(db).CreateUser(ctx, "viewer", "Viewer", "unused", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := s.saveModuleMedia(ctx, "tmdb", module.MediaDetail{SourceID: "movie:42", ItemType: module.ItemMovie, Title: "Role movie"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.DB().ExecContext(ctx, db.Rebind(query), args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE virtual_libraries SET is_public=0,is_adult=0 WHERE id='library-default'`)
	if _, err := s.GetItemForUser(ctx, item.ID, viewer); err == nil {
		t.Fatal("private media leaked")
	}
	exec(`INSERT INTO role_library_permissions(role_id,library_id,can_view,can_play,created_at,updated_at) SELECT id,'library-default',1,0,'now','now' FROM roles WHERE name='Viewer'`)
	if _, err := s.GetItemForUser(ctx, item.ID, viewer); err != nil {
		t.Fatalf("role access: %v", err)
	}
	exec(`UPDATE user_preferences SET adult_confirmed_at=NULL WHERE user_id=?`, viewer.ID)
	if _, err := s.GetItemForUser(ctx, item.ID, viewer); err != nil {
		t.Fatal("nonadult media hidden")
	}
	exec(`UPDATE media_items SET is_adult=1 WHERE id=?`, item.ID)
	if _, err := s.GetItemForUser(ctx, item.ID, viewer); err == nil {
		t.Fatal("unconfirmed adult access")
	}
	exec(`UPDATE user_preferences SET adult_confirmed_at='now' WHERE user_id=?`, viewer.ID)
	if _, err := s.GetItemForUser(ctx, item.ID, viewer); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE virtual_libraries SET enabled=0 WHERE id='library-default'`)
	if _, err := s.GetItemForUser(ctx, item.ID, viewer); err == nil {
		t.Fatal("disabled library leaked")
	}
}
