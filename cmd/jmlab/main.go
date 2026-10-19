package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/graysui/JMlab/internal/auth"
	"github.com/graysui/JMlab/internal/backup"
	"github.com/graysui/JMlab/internal/catalog"
	"github.com/graysui/JMlab/internal/config"
	"github.com/graysui/JMlab/internal/home"
	"github.com/graysui/JMlab/internal/httpserver"
	imagecache "github.com/graysui/JMlab/internal/image"
	"github.com/graysui/JMlab/internal/jellyfin"
	"github.com/graysui/JMlab/internal/module"
	"github.com/graysui/JMlab/internal/moduleadmin"
	"github.com/graysui/JMlab/internal/modulehost"
	"github.com/graysui/JMlab/internal/observability"
	"github.com/graysui/JMlab/internal/playback"
	"github.com/graysui/JMlab/internal/relations"
	"github.com/graysui/JMlab/internal/repository"
	"github.com/graysui/JMlab/internal/userdata"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("JMlab 配置校验失败", "error", err)
		os.Exit(1)
	}
	if err := cfg.EnsureDirectories(); err != nil {
		slog.Error("JMlab 数据目录初始化失败", "error", err)
		os.Exit(1)
	}

	logger := observability.NewLogger(cfg.LogLevel)
	logger.Info("JMlab 正在启动", "httpAddr", cfg.HTTPAddr, "environment", cfg.Environment)
	database, err := repository.Open(context.Background(), cfg.DatabaseURL, cfg.MigrationsDir, logger)
	if err != nil {
		logger.Error("JMlab 数据库初始化失败", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	authService := auth.NewService(database, cfg, logger)
	if err := authService.EnsureBootstrap(context.Background()); err != nil {
		logger.Error("初始管理员初始化失败", "error", err)
		os.Exit(1)
	}
	catalogService := catalog.NewService(database, authService, logger)
	moduleManager := module.NewManager(cfg.ModulesDir, logger)
	if err := moduleManager.SetStateStore(context.Background(), moduleadmin.NewStore(database, cfg.MasterKey)); err != nil {
		logger.Error("模块配置初始化失败", "error", err)
		os.Exit(1)
	}
	catalogService.SetModuleRegistry(moduleManager.Registry())
	catalogService.SetEventBus(moduleManager.EventBus())
	imageService := imagecache.NewService(database, authService, cfg, logger)
	imageService.SetModuleRegistry(moduleManager.Registry())
	catalogService.SetSourceImageImporter(imageService.SeedSource)
	userDataService := userdata.NewService(database, authService)
	userDataService.SetEventBus(moduleManager.EventBus())
	relationsService := relations.NewService(database, authService)
	homeService := home.NewService(database, authService, catalogService, logger)
	playbackService := playback.NewService(database, authService, catalogService, cfg.MasterKey)
	playbackService.SetModuleRegistry(moduleManager.Registry())
	playbackService.SetPublicURL(cfg.PublicURL)
	catalogService.SetSourceSeeder(playbackService.DiscoverItemSources)
	jellyfinService := jellyfin.NewService(database, cfg, authService, catalogService, userDataService)
	jellyfinService.SetImageService(imageService)
	jellyfinService.SetMediaSourcesProvider(playbackService.JellyfinSources)
	backupService := backup.NewService(database, authService, cfg.MasterKey, cfg.DatabaseURL, cfg.DataDir)
	backupService.SetModuleManager(moduleManager)
	moduleAdminService := moduleadmin.NewService(database, authService, moduleManager)
	moduleAdminService.SetCatalog(catalogService)
	moduleHostService := modulehost.NewService(database, moduleManager.Registry(), cfg.MasterKey, cfg.PublicURL)
	moduleManager.SetHostBridge(moduleHostService)
	if err := moduleManager.DiscoverAndLoad(context.Background()); err != nil {
		logger.Warn("模块目录首次扫描失败", "error", err)
	}
	if err := catalogService.SyncLibraryMemberships(context.Background(), "", ""); err != nil {
		logger.Error("媒体库内容范围初始化失败", "error", err)
		os.Exit(1)
	}
	moduleCtx, cancelModules := context.WithCancel(context.Background())
	defer cancelModules()
	if err := moduleManager.StartWatcher(moduleCtx, 2*time.Second); err != nil {
		logger.Error("模块热加载启动失败", "error", err)
		os.Exit(1)
	}
	catalogService.StartWorker(context.Background())
	backupService.StartWorker(context.Background())
	go func() {
		healCtx := context.Background()
		if err := imageService.EnsureItemImages(healCtx); err != nil {
			logger.Warn("后台修复媒体图片失败", "error", err)
		}
	}()
	server := httpserver.New(cfg, logger, database.Ready, authService, catalogService, imageService, userDataService, relationsService, homeService, playbackService, jellyfinService, backupService, moduleAdminService, moduleHostService)
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("JMlab HTTP 服务已监听", "address", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	select {
	case err := <-serverErr:
		logger.Error("JMlab HTTP 服务异常退出", "error", err)
		os.Exit(1)
	case sig := <-stop:
		logger.Info("JMlab 收到退出信号", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	cancelModules()
	backupService.StopWorker(ctx)
	catalogService.StopWorker(ctx)
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("JMlab 优雅退出失败", "error", err)
		os.Exit(1)
	}
	if err := moduleManager.Stop(ctx); err != nil {
		logger.Warn("模块关闭超时", "error", err)
	}
	logger.Info("JMlab 已安全退出")
}
