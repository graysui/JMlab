package repository

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	goosedb "github.com/pressly/goose/v3/database"
	_ "modernc.org/sqlite"
)

type Database struct {
	db      *sql.DB
	dialect goosedb.Dialect
}

func Open(ctx context.Context, databaseURL, migrationsDir string, logger *slog.Logger) (*Database, error) {
	if logger == nil {
		logger = slog.Default()
	}
	driver, dsn, dialect, err := connectionInfo(databaseURL)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(migrationsDir); err != nil {
		return nil, fmt.Errorf("迁移目录不可用: %w", err)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	database := &Database{db: db, dialect: dialect}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = database.Close()
		}
	}()
	if dialect == goosedb.DialectSQLite3 {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		if err := database.configureSQLite(ctx); err != nil {
			return nil, err
		}
	} else {
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
	}
	if err := database.Ready(ctx); err != nil {
		return nil, err
	}

	provider, err := goose.NewProvider(dialect, db, os.DirFS(migrationsDir))
	if err != nil {
		return nil, fmt.Errorf("创建数据库迁移器失败: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("执行数据库迁移失败: %w", err)
	}
	logger.Info("数据库迁移完成", "appliedMigrations", len(results))
	closeOnError = false
	return database, nil
}

func (d *Database) Ready(ctx context.Context) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := d.db.PingContext(ctx); err != nil {
		return fmt.Errorf("数据库 ping 失败: %w", err)
	}
	var one int
	if err := d.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("数据库查询失败: %w", err)
	}
	return nil
}

func (d *Database) DB() *sql.DB { return d.db }

func (d *Database) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *Database) configureSQLite(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := d.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("执行 SQLite 配置 %q 失败: %w", statement, err)
		}
	}
	return nil
}

func connectionInfo(databaseURL string) (driver, dsn string, dialect goosedb.Dialect, err error) {
	value := strings.TrimSpace(databaseURL)
	if strings.HasPrefix(value, "sqlite://") {
		value = strings.TrimPrefix(value, "sqlite://")
		if strings.TrimSpace(value) == "" {
			return "", "", "", fmt.Errorf("SQLite 数据库路径不能为空")
		}
		return "sqlite", value, goosedb.DialectSQLite3, nil
	} else if strings.HasPrefix(value, "file:") {
		return "sqlite", value, goosedb.DialectSQLite3, nil
	} else if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		return "pgx", value, goosedb.DialectPostgres, nil
	}
	return "", "", "", fmt.Errorf("数据库 URL 必须使用 sqlite://、file:、postgres:// 或 postgresql://，收到 %q", databaseURL)
}
