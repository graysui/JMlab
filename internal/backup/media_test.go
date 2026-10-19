package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/graysui/JMlab/internal/repository"
)

func TestGenericExportRestoreAndLegacyVersion(t *testing.T) {
	ctx := context.Background()
	open := func() *Service {
		t.Helper()
		db, err := repository.Open(ctx, "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return NewService(db, nil, "unused", "", t.TempDir())
	}
	s := open()
	one := 1
	doc := ExportDocument{FormatVersion: exportFormatVersion, Items: []ExportItem{
		{ID: "episode", Code: "media:episode", Source: "tmdb", SourceID: "tv:1:1:1", Title: "Episode", ItemType: "episode", ParentID: "season", IndexNumber: &one},
		{ID: "season", Code: "media:season", Source: "tmdb", SourceID: "tv:1:1", Title: "Season", ItemType: "season", ParentID: "series", IndexNumber: &one},
		{ID: "series", Code: "media:series", Source: "tmdb", SourceID: "tv:1", Title: "Series", ItemType: "series"},
	}}
	options := importOptions{Mode: "merge"}
	if err := validateDocument(doc, options); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyImport(ctx, doc, options); err != nil {
		t.Fatal(err)
	}
	exported, err := s.buildExport(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if exported.FormatVersion != exportFormatVersion || len(exported.Items) != 3 {
		t.Fatal("generic export incomplete")
	}
	restored := open()
	if _, err := restored.applyImport(ctx, exported, options); err != nil {
		t.Fatal(err)
	}
	var parent, kind string
	if err := restored.db.DB().QueryRow(`SELECT parent_id,item_type FROM media_items WHERE id='episode'`).Scan(&parent, &kind); err != nil || parent != "season" || kind != "episode" {
		t.Fatal(parent, kind, err)
	}
	legacy := ExportDocument{FormatVersion: "jmlab.export.v1", Items: []ExportItem{{ID: "javdb:AUD-001", Code: "AUD-001", Source: "javdb", Title: "Legacy"}}}
	if err := validateDocument(legacy, options); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.applyImport(ctx, legacy, options); err != nil {
		t.Fatal(err)
	}
	doc.Items[0].ParentID = "series"
	if _, err := restored.applyImport(ctx, doc, importOptions{Mode: "overwrite"}); err == nil {
		t.Fatal("invalid hierarchy restored")
	}
	if err := restored.db.DB().QueryRow(`SELECT parent_id FROM media_items WHERE id='episode'`).Scan(&parent); err != nil || parent != "season" {
		t.Fatal("failed import modified existing state")
	}
}
