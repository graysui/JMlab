package catalog

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graysui/JMlab/internal/repository"
)

func TestGenerationNormalizesResponsePayloadAndLocks(t *testing.T) {
	db, err := repository.Open(context.Background(), "sqlite://"+filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")), "../../migrations", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db, nil, nil)
	for _, tc := range []struct{ kind, period, wantKind, wantPeriod string }{
		{" HOT ", " Weekly ", "playback", "weekly"},
		{"TOP250", "daily", "top250", "all"},
		{"top250", "", "top250", "all"},
		{"top250", "1900", "top250", "1900"},
		{"top250", "2100", "top250", "2100"},
	} {
		body, _ := json.Marshal(map[string]string{"rankingType": tc.kind, "period": tc.period})
		rec := httptest.NewRecorder()
		s.generateRanking(rec, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
		if rec.Code != 202 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
		var result struct{ TaskID, RankingType, Period string }
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.RankingType != tc.wantKind || result.Period != tc.wantPeriod {
			t.Fatalf("unnormalized response: %s", rec.Body.String())
		}
		var payload string
		if err := db.DB().QueryRow("SELECT payload_json FROM tasks WHERE id = ?", result.TaskID).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var stored map[string]string
		if err := json.Unmarshal([]byte(payload), &stored); err != nil {
			t.Fatal(err)
		}
		if stored["rankingType"] != result.RankingType || stored["period"] != result.Period {
			t.Fatal("response differs from stored task")
		}
	}
	for _, period := range []string{"1899", "2101", "0000", "weekly", "+999"} {
		if _, _, err := s.EnqueueRankingTask(context.Background(), "top250", period, "", ""); err == nil {
			t.Fatalf("invalid year accepted: %s", period)
		}
	}
}
