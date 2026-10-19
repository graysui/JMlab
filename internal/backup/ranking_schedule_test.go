package backup

import (
	"context"
	"testing"
)

func TestEncryptedConfigPreservesModuleRankingSchedules(t *testing.T) {
	source := openConfigTestService(t, "temporary-key")
	execConfigTest(t, source, `INSERT INTO module_ranking_schedules(module_id,enabled,interval_hours,targets_json,last_run_at,updated_at)VALUES('ordinary-source',1,12,'[{"rankingType":"community_top","period":"today"}]','2026-10-19T00:00:00Z','now')`)
	data, err := source.buildEncryptedConfigExport(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	destination := openConfigTestService(t, "temporary-key")
	bundle, err := destination.decodeConfigBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.RankingSchedules) != 1 {
		t.Fatal("missing module schedules")
	}
	tx, err := destination.db.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := destination.applyConfigTx(context.Background(), tx, bundle, "overwrite"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if countConfigTest(t, destination, "SELECT COUNT(*) FROM module_ranking_schedules WHERE module_id='ordinary-source' AND enabled=1 AND interval_hours=12") != 1 {
		t.Fatal("schedule not restored")
	}
}
