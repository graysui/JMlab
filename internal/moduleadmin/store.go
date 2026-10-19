package moduleadmin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/securevalue"
)

type Store struct {
	db        *repository.Database
	masterKey string
}

func NewStore(db *repository.Database, masterKey string) *Store {
	return &Store{db: db, masterKey: masterKey}
}

func (s *Store) Load(ctx context.Context) (map[string]module.Settings, error) {
	rows, err := s.db.DB().QueryContext(ctx, "SELECT module_id, enabled, config_encrypted FROM installed_modules")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[string]module.Settings{}
	for rows.Next() {
		var id, encrypted string
		var enabled int
		if err := rows.Scan(&id, &enabled, &encrypted); err != nil {
			return nil, err
		}
		plain, err := securevalue.Decrypt(s.masterKey+":module:"+id, encrypted)
		if err != nil {
			return nil, errors.New("模块配置无法解密，请检查主密钥与数据库")
		}
		var state module.Settings
		if err := json.Unmarshal(plain, &state); err != nil {
			return nil, errors.New("模块配置格式无效")
		}
		state.Enabled = enabled == 1
		states[id] = state
	}
	return states, rows.Err()
}

func (s *Store) Save(ctx context.Context, id string, state module.Settings) error {
	plain, err := json.Marshal(state)
	if err != nil {
		return err
	}
	encrypted, err := securevalue.Encrypt(s.masterKey+":module:"+id, plain)
	if err != nil {
		return err
	}
	enabled := 0
	if state.Enabled {
		enabled = 1
	}
	_, err = s.db.DB().ExecContext(ctx, s.db.Rebind(`INSERT INTO installed_modules (module_id, enabled, config_encrypted, updated_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(module_id) DO UPDATE SET enabled = excluded.enabled,
		config_encrypted = excluded.config_encrypted, updated_at = excluded.updated_at`), id, enabled, encrypted, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"module_restore_queue", "installed_modules"} {
		if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM "+table+" WHERE module_id=?"), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PendingRestore(ctx context.Context) ([]module.RestoreRecord, error) {
	rows, err := s.db.DB().QueryContext(ctx, "SELECT module_id, record_encrypted FROM module_restore_queue ORDER BY module_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []module.RestoreRecord
	for rows.Next() {
		var id, encrypted string
		if err := rows.Scan(&id, &encrypted); err != nil {
			return nil, err
		}
		plain, err := securevalue.Decrypt(s.masterKey+":module-restore:"+id, encrypted)
		if err != nil {
			return nil, errors.New("模块恢复队列无法解密")
		}
		var record module.RestoreRecord
		if json.Unmarshal(plain, &record) != nil || record.ID != id {
			return nil, errors.New("模块恢复队列格式无效")
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) CompleteRestore(ctx context.Context, id string) error {
	_, err := s.db.DB().ExecContext(ctx, s.db.Rebind("DELETE FROM module_restore_queue WHERE module_id=?"), id)
	return err
}

func (s *Store) SaveRestore(ctx context.Context, record module.RestoreRecord) error {
	settings, err := json.Marshal(record.Settings)
	if err != nil {
		return err
	}
	settingsEncrypted, err := securevalue.Encrypt(s.masterKey+":module:"+record.ID, settings)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encrypted, err := securevalue.Encrypt(s.masterKey+":module-restore:"+record.ID, plain)
	if err != nil {
		return err
	}
	tx, err := s.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	enabled := 0
	if record.Settings.Enabled {
		enabled = 1
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind(`UPDATE installed_modules SET enabled=?,config_encrypted=?,updated_at=? WHERE module_id=?`), enabled, settingsEncrypted, now, record.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.db.Rebind(`UPDATE module_restore_queue SET record_encrypted=?,updated_at=? WHERE module_id=?`), encrypted, now, record.ID); err != nil {
		return err
	}
	return tx.Commit()
}
