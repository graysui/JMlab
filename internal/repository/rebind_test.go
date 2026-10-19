package repository

import "testing"

func TestRebindPreservesSQLLiteralsAndComments(t *testing.T) {
	postgres := &Database{dialect: "postgres"}
	for _, test := range []struct{ name, query, want string }{
		{"parameters", `SELECT ? FROM media_items WHERE id=? LIMIT ? OFFSET ?`, `SELECT $1 FROM media_items WHERE id=$2 LIMIT $3 OFFSET $4`},
		{"quoted text", `SELECT 'Why?', 'it''s ?' WHERE title=? AND overview=?`, `SELECT 'Why?', 'it''s ?' WHERE title=$1 AND overview=$2`},
		{"identifiers", `SELECT "what?", "quoted""?" WHERE id=?`, `SELECT "what?", "quoted""?" WHERE id=$1`},
		{"escape string", `SELECT E'it\'s ?' WHERE id=?`, `SELECT E'it\'s ?' WHERE id=$1`},
		{"line comment", "SELECT ? -- why ?\r\nWHERE id=?", "SELECT $1 -- why ?\r\nWHERE id=$2"},
		{"block comment", `SELECT ? /* ? /* nested ? */ ? */ WHERE id=?`, `SELECT $1 /* ? /* nested ? */ ? */ WHERE id=$2`},
		{"dollar text", `SELECT $$why?$$, $body$what's ?$body$ WHERE id=?`, `SELECT $$why?$$, $body$what's ?$body$ WHERE id=$1`},
		{"dollar in identifier", `SELECT foo$bar$ WHERE id=?`, `SELECT foo$bar$ WHERE id=$1`},
		{"unicode", `SELECT '中文？?' WHERE title=?`, `SELECT '中文？?' WHERE title=$1`},
		{"unterminated literal", `SELECT ?, 'unfinished ?`, `SELECT $1, 'unfinished ?`},
		{"unterminated comment", `SELECT ? /* unfinished ?`, `SELECT $1 /* unfinished ?`},
		{"unterminated dollar", `SELECT ?, $body$unfinished ?`, `SELECT $1, $body$unfinished ?`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := postgres.Rebind(test.query); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
			for _, db := range []*Database{nil, {dialect: "sqlite3"}} {
				if got := db.Rebind(test.query); got != test.query {
					t.Fatalf("SQLite query changed: %q", got)
				}
			}
		})
	}
}
