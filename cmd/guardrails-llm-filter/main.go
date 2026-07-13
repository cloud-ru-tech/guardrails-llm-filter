package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/app"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		logging.Fatal(ctx, "Failed to load config", err)
	}

	logging.Setup(cfg.LogLevel, cfg.LogFormat)

	logging.Info(ctx, "Starting guardrails")
	application := app.New(cfg)
	if err := application.Start(ctx); err != nil {
		logging.Fatal(ctx, "Failed to start app", err)
	}

	<-ctx.Done()
	stop()

	logging.Info(context.Background(), "Shutting down")
	if err := application.Stop(); err != nil {
		logging.Error(context.Background(), "Shutdown error", err)
	}
}
