package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/acme/obs-notification-router/internal/alertmanager"
	"github.com/acme/obs-notification-router/internal/api"
	"github.com/acme/obs-notification-router/internal/catalog"
	"github.com/acme/obs-notification-router/internal/config"
	"github.com/acme/obs-notification-router/internal/notify"
	"github.com/acme/obs-notification-router/internal/repository"
	"github.com/acme/obs-notification-router/internal/router"
	"github.com/acme/obs-notification-router/internal/service"
	"github.com/acme/obs-notification-router/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration invalid", "error", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	repo, err := repository.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer repo.Pool.Close()
	if err = repo.Migrate(ctx); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	httpClient := &http.Client{Timeout: 8 * time.Second}
	catalogClient := catalog.New(httpClient, cfg.CatalogURL, cfg.CatalogToken, cfg.CatalogCacheTTL)
	routing := router.New(router.Policy{FallbackSlackChannel: cfg.FallbackSlackChannel, FallbackPagerDutyService: cfg.FallbackPagerDutyService, OpsgenieEnabled: cfg.OpsgenieEnabled})
	processor := service.NewProcessor(catalogClient, routing, repo, cfg.EscalationAfter, cfg.DedupWindow, logger)
	strategies := []notify.Strategy{notify.NewSlack(httpClient, cfg.SlackBotToken, cfg.SlackAPIURL), notify.NewPagerDuty(httpClient, cfg.PagerDutyAPIURL, cfg.PagerDutyRoutingKeys)}
	if cfg.OpsgenieEnabled {
		strategies = append(strategies, notify.NewOpsgenie(httpClient, cfg.OpsgenieAPIURL, cfg.OpsgenieAPIKey))
	}
	registry := notify.NewRegistry(strategies...)
	background := worker.New(repo, registry, cfg.WorkerPollInterval, cfg.WorkerConcurrency, cfg.RateLimitPerMinute, cfg.FallbackSlackChannel, cfg.FallbackPagerDutyService, logger)
	go background.Run(ctx)
	serverAPI := api.New(processor, catalogClient, repo, alertmanager.New(httpClient, cfg.AlertmanagerURL), cfg.WebhookToken, cfg.SlackSigningSecret, cfg.APITokens, logger)
	server := &http.Server{Addr: cfg.Address, Handler: serverAPI.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		logger.Info("server listening", "address", cfg.Address)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
			cancel()
		}
	}()
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
