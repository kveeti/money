package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"money/backend/auth"
	"money/backend/config"
	"money/backend/data"
	"money/backend/enablebanking"
	"money/backend/endpoints"
	"money/backend/state"
)

func App(config *config.Config, started chan struct{}) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	if config.IsProd {
		slog.Info("starting in prod")
	} else {
		slog.Info("starting in dev")
	}

	d, err := data.NewData(ctx, config.DbUrl, config.ImportStore, config.ImportDir)
	if err != nil {
		stop()
		panic(err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			slog.Error("closing database", "err", err)
		}
	}()
	defer stop()

	oidc, err := auth.NewOIDC(ctx, config.OIDC)
	if err != nil {
		panic(err)
	}

	d.StartImportWorkers(ctx)
	d.StartMaintenance(ctx)
	if !config.DisableRateSync {
		d.StartRateSync(ctx)
	}

	appState := state.NewState(d, config, oidc)
	if config.EnableBanking.Enabled() {
		client, err := enablebanking.New(config.EnableBanking.ApplicationID, config.EnableBanking.PrivateKeyPath, config.EnableBanking.APIOrigin)
		if err != nil {
			panic(fmt.Errorf("creating Enable Banking client: %w", err))
		}
		appState.EnableBankingClient = client
		appState.EnableBankingSyncer = enablebanking.NewSyncer(d, client)
		appState.EnableBankingSyncer.Start(ctx)
	}

	router := endpoints.GetRouter(appState, frontendFS())

	s := NewHttpServer(router, ":"+config.Port)
	if err := s.Start(); err != nil {
		panic(fmt.Errorf("error starting server: %w", err))
	}

	if started != nil {
		started <- struct{}{}
	}

	select {
	case err := <-s.Done:
		if err != nil {
			panic(fmt.Errorf("server failed: %w", err))
		}
	case <-ctx.Done():
		if err := s.Shutdown(); err != nil {
			panic(fmt.Errorf("shutting down server: %w", err))
		}
		if err := <-s.Done; err != nil {
			panic(fmt.Errorf("server failed during shutdown: %w", err))
		}
	}
}
