package modulehost

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/graysui/JMlab/internal/securevalue"
)

var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func storageAllowed(w http.ResponseWriter, r *http.Request) bool {
	entry := caller(r)
	if entry.Manifest.Permissions == nil || !entry.Manifest.Permissions.Storage {
		fail(w, 403, "STORAGE_NOT_GRANTED", "模块未获得专属存储权限")
		return false
	}
	if key := chi.URLParam(r, "key"); key != "" && !keyPattern.MatchString(key) {
		fail(w, 400, "INVALID_STORAGE_KEY", "存储键格式无效")
		return false
	}
	return true
}
func (s *Service) listKeys(w http.ResponseWriter, r *http.Request) {
	if !storageAllowed(w, r) {
		return
	}
	rows, err := s.db.DB().QueryContext(r.Context(), s.db.Rebind("SELECT storage_key FROM module_storage WHERE module_id=? ORDER BY storage_key LIMIT 256"), caller(r).Manifest.ID)
	if err != nil {
		fail(w, 500, "STORAGE_READ_FAILED", "读取模块存储失败")
		return
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			fail(w, 500, "STORAGE_READ_FAILED", "读取模块存储失败")
			return
		}
		keys = append(keys, key)
	}
	if rows.Err() != nil {
		fail(w, 500, "STORAGE_READ_FAILED", "读取模块存储失败")
		return
	}
	respond(w, 200, map[string]any{"keys": keys})
}
func (s *Service) getValue(w http.ResponseWriter, r *http.Request) {
	if !storageAllowed(w, r) {
		return
	}
	id := caller(r).Manifest.ID
	key := chi.URLParam(r, "key")
	var encrypted string
	if err := s.db.DB().QueryRowContext(r.Context(), s.db.Rebind("SELECT value_encrypted FROM module_storage WHERE module_id=? AND storage_key=?"), id, key).Scan(&encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "STORAGE_NOT_FOUND", "存储项不存在")
		} else {
			fail(w, 500, "STORAGE_READ_FAILED", "读取模块存储失败")
		}
		return
	}
	plain, err := securevalue.Decrypt(s.masterKey+":module-storage:"+id, encrypted)
	if err != nil {
		fail(w, 500, "STORAGE_DECRYPT_FAILED", "模块存储无法解密")
		return
	}
	respond(w, 200, map[string]any{"key": key, "value": json.RawMessage(plain)})
}
func (s *Service) putValue(w http.ResponseWriter, r *http.Request) {
	if !storageAllowed(w, r) {
		return
	}
	var input struct {
		Value json.RawMessage `json:"value"`
	}
	if decode(w, r, &input) != nil || len(input.Value) == 0 || len(input.Value) > 16*1024 {
		fail(w, 400, "INVALID_STORAGE_VALUE", "存储值需要是 JSON，且不超过 16 KiB")
		return
	}
	id, key := caller(r).Manifest.ID, chi.URLParam(r, "key")
	encrypted, err := securevalue.Encrypt(s.masterKey+":module-storage:"+id, input.Value)
	if err != nil {
		fail(w, 500, "STORAGE_WRITE_FAILED", "保存模块存储失败")
		return
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	tx, err := s.db.DB().BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "STORAGE_WRITE_FAILED", "保存模块存储失败")
		return
	}
	defer tx.Rollback()
	var count, total int
	if err := tx.QueryRowContext(r.Context(), s.db.Rebind("SELECT COUNT(*),COALESCE(SUM(LENGTH(value_encrypted)),0) FROM module_storage WHERE module_id=? AND storage_key<>?"), id, key).Scan(&count, &total); err != nil {
		fail(w, 500, "STORAGE_WRITE_FAILED", "保存模块存储失败")
		return
	}
	if count >= 256 || total+len(encrypted) > 1024*1024 {
		fail(w, 413, "STORAGE_QUOTA_EXCEEDED", "模块存储达到容量上限")
		return
	}
	_, err = tx.ExecContext(r.Context(), s.db.Rebind("INSERT INTO module_storage(module_id,storage_key,value_encrypted,updated_at) VALUES(?,?,?,?) ON CONFLICT(module_id,storage_key) DO UPDATE SET value_encrypted=excluded.value_encrypted,updated_at=excluded.updated_at"), id, key, encrypted, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil || tx.Commit() != nil {
		fail(w, 500, "STORAGE_WRITE_FAILED", "保存模块存储失败")
		return
	}
	respond(w, 200, map[string]string{"status": "saved"})
}
func (s *Service) deleteValue(w http.ResponseWriter, r *http.Request) {
	if !storageAllowed(w, r) {
		return
	}
	_, err := s.db.DB().ExecContext(r.Context(), s.db.Rebind("DELETE FROM module_storage WHERE module_id=? AND storage_key=?"), caller(r).Manifest.ID, chi.URLParam(r, "key"))
	if err != nil {
		fail(w, 500, "STORAGE_WRITE_FAILED", "删除模块存储失败")
		return
	}
	respond(w, 200, map[string]string{"status": "deleted"})
}
