package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/securevalue"
)

const configFormatVersion = "jmlab.encrypted-config.v2"

var configModuleID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,62}$`)
var configStorageKey = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type encryptedTarget struct {
	ID              string `json:"id"`
	TargetType      string `json:"targetType"`
	DisplayName     string `json:"displayName"`
	Enabled         bool   `json:"enabled"`
	RetentionCount  int    `json:"retentionCount"`
	EncryptedConfig string `json:"encryptedConfig"`
}

type encryptedStorage struct {
	ModuleID string `json:"moduleId"`
	Key      string `json:"key"`
	Value    string `json:"encryptedValue"`
}

type rankingScheduleConfig struct {
	UpdateTime    string  `json:"updateTime,omitempty"`
	ModuleID      string  `json:"moduleId"`
	Enabled       int     `json:"enabled"`
	IntervalHours int     `json:"intervalHours"`
	TargetsJSON   string  `json:"targetsJson"`
	LastRunAt     *string `json:"lastRunAt,omitempty"`
}

type configBundle struct {
	RankingSchedules []rankingScheduleConfig `json:"rankingSchedules,omitempty"`
	Targets          []encryptedTarget       `json:"targets"`
	Modules          []module.RestoreRecord  `json:"modules"`
	Storage          []encryptedStorage      `json:"storage"`
}

type configEnvelope struct {
	FormatVersion string `json:"formatVersion"`
	Payload       string `json:"encryptedPayload"`
	// v1 did not encrypt the envelope and contained targets only.
	Targets []encryptedTarget `json:"targets,omitempty"`
}

type configSummary struct {
	Targets int `json:"targets"`
	Modules int `json:"modules"`
	Keys    int `json:"storageKeys"`
}

func (b configBundle) summary() configSummary {
	return configSummary{Targets: len(b.Targets), Modules: len(b.Modules), Keys: len(b.Storage)}
}

func (s *Service) buildEncryptedConfigExport(ctx context.Context, targetIDs []string) ([]byte, error) {
	var bundle configBundle
	build := func(records []module.RestoreRecord) error {
		// Also include pending definitions: a backup during a retry must retain
		// the restored definition, rather than a stale file/runtime snapshot.
		bundle.Modules = records
		if s.modules != nil {
			pending, err := s.loadPendingConfig(ctx)
			if err != nil {
				return err
			}
			for _, record := range pending {
				found := false
				for i := range bundle.Modules {
					if bundle.Modules[i].ID == record.ID {
						bundle.Modules[i], found = record, true
						break
					}
				}
				if !found {
					bundle.Modules = append(bundle.Modules, record)
				}
			}
		}
		for _, id := range targetIDs {
			var item encryptedTarget
			var enabled int
			if err := s.db.DB().QueryRowContext(ctx, s.db.Rebind("SELECT id,target_type,display_name,enabled,retention_count,encrypted_config FROM backup_targets WHERE id=?"), id).Scan(&item.ID, &item.TargetType, &item.DisplayName, &enabled, &item.RetentionCount, &item.EncryptedConfig); err != nil {
				return err
			}
			item.Enabled = enabled == 1
			bundle.Targets = append(bundle.Targets, item)
		}
		rows, err := s.db.DB().QueryContext(ctx, "SELECT module_id,storage_key,value_encrypted FROM module_storage ORDER BY module_id,storage_key")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value encryptedStorage
			if err := rows.Scan(&value.ModuleID, &value.Key, &value.Value); err != nil {
				return err
			}
			bundle.Storage = append(bundle.Storage, value)
		}

		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		schedules, err := s.db.DB().QueryContext(ctx, "SELECT module_id,enabled,interval_hours,targets_json,last_run_at,update_time FROM module_ranking_schedules ORDER BY module_id")
		if err != nil {
			return err
		}
		defer schedules.Close()
		for schedules.Next() {
			var cfg rankingScheduleConfig
			if err := schedules.Scan(&cfg.ModuleID, &cfg.Enabled, &cfg.IntervalHours, &cfg.TargetsJSON, &cfg.LastRunAt, &cfg.UpdateTime); err != nil {
				return err
			}
			bundle.RankingSchedules = append(bundle.RankingSchedules, cfg)
		}
		return schedules.Err()

	}
	if s.modules != nil {
		if err := s.modules.WithBackupSnapshot(build); err != nil {
			return nil, err
		}
	} else if err := build(nil); err != nil {
		return nil, err
	}
	plain, err := json.Marshal(bundle)
	if err != nil || len(plain) > 32<<20 {
		return nil, errors.New("模块配置备份超过大小限制")
	}
	encrypted, err := securevalue.Encrypt(s.masterKey+":backup-config", plain)
	if err != nil {
		return nil, err
	}
	return json.Marshal(configEnvelope{FormatVersion: configFormatVersion, Payload: encrypted})
}

func (s *Service) loadPendingConfig(ctx context.Context) ([]module.RestoreRecord, error) {
	rows, err := s.db.DB().QueryContext(ctx, "SELECT module_id,record_encrypted FROM module_restore_queue ORDER BY module_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []module.RestoreRecord
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
			return nil, errors.New("模块恢复队列无效")
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *Service) decodeConfigBundle(data []byte) (configBundle, error) {
	var envelope configEnvelope
	var bundle configBundle
	if json.Unmarshal(data, &envelope) != nil {
		return bundle, errors.New("加密配置格式无效")
	}
	switch strings.Replace(envelope.FormatVersion, "javlab.", "jmlab.", 1) {
	case "jmlab.encrypted-config.v1":
		bundle.Targets = envelope.Targets
	case configFormatVersion:
		plain, err := securevalue.Decrypt(s.masterKey+":backup-config", envelope.Payload)
		if err != nil {
			return bundle, errors.New("加密配置无法解密，请使用备份时的主密钥")
		}
		if len(plain) > 32<<20 || json.Unmarshal(plain, &bundle) != nil {
			return bundle, errors.New("加密配置格式或大小无效")
		}
	default:
		return bundle, errors.New("加密配置版本不受支持")
	}
	if len(bundle.Targets) > 256 || len(bundle.Modules) > 256 || len(bundle.Storage) > 65536 {
		return bundle, errors.New("加密配置数量超过限制")
	}

	if len(bundle.RankingSchedules) > 256 {
		return bundle, errors.New("榜单计划数量过多")
	}
	scheduleIDs := map[string]bool{}
	for _, cfg := range bundle.RankingSchedules {
		var targets []struct {
			RankingType string `json:"rankingType"`
			Period      string `json:"period"`
		}
		if !module.ValidRankingUpdateTime(cfg.UpdateTime) || !configModuleID.MatchString(cfg.ModuleID) || scheduleIDs[cfg.ModuleID] || cfg.Enabled < 0 || cfg.Enabled > 1 || cfg.IntervalHours < 1 || cfg.IntervalHours > 720 || json.Unmarshal([]byte(cfg.TargetsJSON), &targets) != nil || len(targets) > 128 {
			return bundle, errors.New("榜单计划无效")
		}
		scheduleIDs[cfg.ModuleID] = true
		for _, target := range targets {
			if !module.ValidRankingValue(target.RankingType) || !module.ValidRankingValue(target.Period) {
				return bundle, errors.New("榜单计划范围无效")
			}
		}
	}
	seen := map[string]bool{}
	for _, target := range bundle.Targets {
		if strings.TrimSpace(target.ID) == "" || len(target.ID) > 200 || seen[target.ID] || validateTargetFields(target.TargetType, target.DisplayName, target.RetentionCount) != nil {
			return bundle, errors.New("备份目标配置无效或重复")
		}
		seen[target.ID] = true
		cfg, err := s.decryptConfig(target.EncryptedConfig)
		if err != nil {
			return bundle, errors.New("备份目标无法解密，请使用备份时的主密钥")
		}
		if _, err := validateTargetConfig(target.TargetType, cfg); err != nil {
			return bundle, errors.New("备份目标连接配置无效")
		}
	}
	seen = map[string]bool{}
	for _, record := range bundle.Modules {
		if !configModuleID.MatchString(record.ID) || seen[record.ID] {
			return bundle, errors.New("模块 ID 无效或重复")
		}
		seen[record.ID] = true
		if s.modules == nil {
			return bundle, errors.New("当前服务未接入模块管理器，无法恢复模块配置")
		}
		if record.Manifest != nil {
			mf := record.Manifest
			if mf.ID != record.ID || mf.Validate() != nil {
				return bundle, errors.New("模块清单无效")
			}
			current, exists := s.modules.Registry().Get(record.ID)
			if mf.Transport.Type == module.TransportInProcess {
				if !exists || current.Manifest.Transport.Type != module.TransportInProcess {
					return bundle, errors.New("备份依赖的内置模块不存在")
				}
				mf = current.Manifest
			} else if mf.Transport.Type != module.TransportHTTP || (exists && current.Manifest.Transport.Type == module.TransportInProcess) {
				return bundle, errors.New("不能替换内置模块或恢复非 HTTP 外部模块")
			}
			if err := module.ValidateSettings(mf, record.Settings); err != nil {
				return bundle, errors.New("模块的配置不完整或类型无效")
			}
			if mf.Transport.Type == module.TransportHTTP {
				client, err := module.NewHTTPModuleClientWithConfig(mf, record.Settings.Config, record.Settings.Token)
				if err != nil {
					return bundle, errors.New("模块连接安全策略或配置大小无效")
				}
				client.Close()
			}
		} else if _, exists := s.modules.Registry().Get(record.ID); exists {
			return bundle, errors.New("已安装模块缺少恢复清单")
		}
	}
	seen = map[string]bool{}
	counts, sizes := map[string]int{}, map[string]int{}
	for _, value := range bundle.Storage {
		identity := value.ModuleID + ":" + value.Key
		if !configModuleID.MatchString(value.ModuleID) || !configStorageKey.MatchString(value.Key) || seen[identity] {
			return bundle, errors.New("模块存储键无效或重复")
		}
		seen[identity] = true
		plain, err := securevalue.Decrypt(s.masterKey+":module-storage:"+value.ModuleID, value.Value)
		if err != nil || len(plain) > 16*1024 || !json.Valid(plain) {
			return bundle, errors.New("模块存储无法解密或格式无效")
		}
		counts[value.ModuleID]++
		sizes[value.ModuleID] += len(value.Value)
		if counts[value.ModuleID] > 256 || sizes[value.ModuleID] > 1024*1024 {
			return bundle, errors.New("模块存储超过容量限制")
		}
	}
	return bundle, nil
}

func (s *Service) applyConfigTx(ctx context.Context, tx *sql.Tx, bundle configBundle, mode string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, target := range bundle.Targets {
		conflict := "DO NOTHING"
		if mode == "overwrite" {
			conflict = "DO UPDATE SET target_type=excluded.target_type,display_name=excluded.display_name,enabled=excluded.enabled,retention_count=excluded.retention_count,encrypted_config=excluded.encrypted_config,updated_at=excluded.updated_at"
		}
		_, err := tx.ExecContext(ctx, s.db.Rebind(`INSERT INTO backup_targets(id,target_type,display_name,enabled,retention_count,encrypted_config,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) `+conflict), target.ID, target.TargetType, target.DisplayName, boolInt(target.Enabled), target.RetentionCount, target.EncryptedConfig, now, now)
		if err != nil {
			return err
		}
	}
	for _, record := range bundle.Modules {
		if mode == "merge" {
			if s.modules != nil {
				if _, exists := s.modules.Registry().Get(record.ID); exists {
					continue
				}
			}
			var count int
			if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*) FROM installed_modules WHERE module_id=?"), record.ID).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				continue
			}
		}
		plain, err := json.Marshal(record.Settings)
		if err != nil {
			return err
		}
		encrypted, err := securevalue.Encrypt(s.masterKey+":module:"+record.ID, plain)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind(`INSERT INTO installed_modules(module_id,enabled,config_encrypted,updated_at) VALUES (?,?,?,?) ON CONFLICT(module_id) DO UPDATE SET enabled=excluded.enabled,config_encrypted=excluded.config_encrypted,updated_at=excluded.updated_at`), record.ID, boolInt(record.Settings.Enabled), encrypted, now); err != nil {
			return err
		}
		plain, err = json.Marshal(record)
		if err != nil {
			return err
		}
		encrypted, err = securevalue.Encrypt(s.masterKey+":module-restore:"+record.ID, plain)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind(`INSERT INTO module_restore_queue(module_id,record_encrypted,updated_at) VALUES (?,?,?) ON CONFLICT(module_id) DO UPDATE SET record_encrypted=excluded.record_encrypted,updated_at=excluded.updated_at`), record.ID, encrypted, now); err != nil {
			return err
		}
	}

	for _, cfg := range bundle.RankingSchedules {
		conflict := "DO NOTHING"
		if mode == "overwrite" {
			conflict = "DO UPDATE SET enabled=excluded.enabled,interval_hours=excluded.interval_hours,targets_json=excluded.targets_json,update_time=excluded.update_time,last_run_at=excluded.last_run_at,updated_at=excluded.updated_at"
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind("INSERT INTO module_ranking_schedules(module_id,enabled,interval_hours,targets_json,last_run_at,updated_at,update_time)VALUES(?,?,?,?,?,?,?) ON CONFLICT(module_id) "+conflict), cfg.ModuleID, cfg.Enabled, cfg.IntervalHours, cfg.TargetsJSON, cfg.LastRunAt, now, cfg.UpdateTime); err != nil {
			return err
		}
	}
	namespaces := map[string]bool{}
	for _, record := range bundle.Modules {
		namespaces[record.ID] = true
	}
	for _, value := range bundle.Storage {
		namespaces[value.ModuleID] = true
	}
	if mode == "overwrite" {
		for id := range namespaces {
			if _, err := tx.ExecContext(ctx, s.db.Rebind("DELETE FROM module_storage WHERE module_id=?"), id); err != nil {
				return err
			}
		}
	}
	for _, value := range bundle.Storage {
		if _, err := tx.ExecContext(ctx, s.db.Rebind(`INSERT INTO module_storage(module_id,storage_key,value_encrypted,updated_at) VALUES (?,?,?,?) ON CONFLICT(module_id,storage_key) DO NOTHING`), value.ModuleID, value.Key, value.Value, now); err != nil {
			return err
		}
	}
	for id := range namespaces {
		var count, size int
		if err := tx.QueryRowContext(ctx, s.db.Rebind("SELECT COUNT(*),COALESCE(SUM(LENGTH(value_encrypted)),0) FROM module_storage WHERE module_id=?"), id).Scan(&count, &size); err != nil || count > 256 || size > 1024*1024 {
			return errors.New("恢复后的模块存储超过容量限制")
		}
	}
	return nil
}

func (s *Service) applyRestore(ctx context.Context, doc ExportDocument, options importOptions, bundle *configBundle) (ImportSummary, module.ReloadReport, error) {
	var summary ImportSummary
	report := module.ReloadReport{Errors: map[string]string{}}
	commit := func() error {
		tx, err := s.db.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		summary, err = s.applyImportTx(ctx, tx, doc, options)
		if err != nil {
			return err
		}
		if bundle != nil {
			if err := s.applyConfigTx(ctx, tx, *bundle, options.Mode); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	if bundle != nil && len(bundle.Modules) > 0 {
		if s.modules == nil {
			return summary, report, errors.New("模块管理器未配置")
		}
		var err error
		report, err = s.modules.CommitRestore(ctx, commit)
		return summary, report, err
	}
	return summary, report, commit()
}
