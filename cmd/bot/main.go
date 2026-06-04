package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/botinok/temcshika/internal/app"
	"github.com/botinok/temcshika/internal/config"
	"github.com/botinok/temcshika/internal/store"
	"github.com/botinok/temcshika/internal/tg"
	"github.com/botinok/temcshika/internal/vk"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmsgprefix)

	cfg, err := config.Load()
	if err != nil {
		logger.Fatalf("config: %v", err)
	}
	logger.Printf("config: %s", cfg)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Fatalf("store: %v", err)
	}
	defer st.Close()

	vkc, err := vk.New(cfg.VKToken, cfg.VKGroupID)
	if err != nil {
		logger.Fatalf("vk: %v", err)
	}

	tgb, err := tg.New(cfg.TGToken, cfg.TGAdminID)
	if err != nil {
		logger.Fatalf("tg: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application := app.New(cfg, st, vkc, tgb, logger)
	if err := application.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Fatalf("run: %v", err)
	}
	logger.Printf("shutdown")
}
