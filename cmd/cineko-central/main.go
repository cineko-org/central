package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/cineko-org/central/internal/central"
	centralapi "github.com/cineko-org/central/internal/central/api"
	centralpostgres "github.com/cineko-org/central/internal/central/postgres"
	"github.com/cineko-org/central/internal/central/reconcile"
	"github.com/cineko-org/central/internal/telemetry"

	"golang.org/x/sync/errgroup"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cineko-central: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	if config.clientAuthorizer == nil || config.probeBootstrapSigner == nil {
		return errors.New("client Probe bootstrap signing and verification keys are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	telemetrySetup, err := telemetry.New(ctx, "cineko-central", os.Stderr)
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	logger := telemetrySetup.Logger
	slog.SetDefault(logger)
	defer shutdownTelemetry(telemetrySetup.Shutdown)
	store, err := centralpostgres.Open(ctx, config.databaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	server, reconciler, err := buildCentral(ctx, config, store, logger)
	if err != nil {
		return err
	}
	return serveCentral(ctx, server, reconciler)
}

func shutdownTelemetry(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cineko-central: flush telemetry: %v\n", err)
	}
}

func buildCentral(
	ctx context.Context,
	config applicationConfig,
	store *centralpostgres.Store,
	logger *slog.Logger,
) (*http.Server, *reconcile.Engine, error) {
	service, err := central.NewService(store, central.Config{
		EnrollmentToken:        config.enrollmentToken,
		ProbeHeartbeatTTL:      config.reconciler.ProbeHeartbeatTTL,
		MinimumRuntimeVersion:  config.minimumRuntimeVersion,
		MinimumBrowserRevision: config.minimumBrowserRevision,
		ClientAuthorizer:       config.clientAuthorizer,
	})
	if err != nil {
		return nil, nil, err
	}
	clientService, err := central.NewClientService(store, config.clientSessionTTL, config.clientRefreshTTL)
	if err != nil {
		return nil, nil, err
	}
	catalogService, err := central.NewCatalogService(store)
	if err != nil {
		return nil, nil, err
	}
	if err := clientService.BootstrapReleaseRegistry(
		ctx, config.clientReleases, config.browserReleases, config.playwrightReleases, config.launcherReleases,
		config.probeReleases,
	); err != nil {
		return nil, nil, err
	}
	if err := clientService.Provision(ctx, config.clientCredentials); err != nil {
		return nil, nil, err
	}
	pinService, err := central.NewPINService(store, clientService, config.clientPINPepper)
	if err != nil {
		return nil, nil, err
	}
	adminAuth, err := centralapi.NewAdminAuth(
		config.adminCredentials, config.adminPasswordPepper, store, config.adminSessionTTL,
	)
	if err != nil {
		return nil, nil, err
	}
	if err := adminAuth.Bootstrap(ctx); err != nil {
		return nil, nil, err
	}
	config.reconciler.Logger = logger
	reconciler, err := reconcile.New(store, config.reconciler)
	if err != nil {
		return nil, nil, err
	}
	api, err := centralapi.New(
		service,
		centralapi.WithReconciler(reconciler),
		centralapi.WithClientService(clientService),
		centralapi.WithCatalogService(catalogService),
		centralapi.WithPINService(pinService),
		centralapi.WithProbeBootstrapSigner(config.probeBootstrapSigner),
		centralapi.WithAdminAuth(adminAuth),
		centralapi.WithTrustedProxyCIDRs(config.trustedProxyCIDRs),
		centralapi.WithAdminConfiguration(adminConfiguration(config)),
		centralapi.WithAdminOperations(store),
		centralapi.WithReleasePublishToken(config.releasePublishToken),
	)
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{
		Addr: config.listenAddress, Handler: api.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	return server, reconciler, nil
}

func adminConfiguration(config applicationConfig) centralapi.AdminConfiguration {
	return centralapi.AdminConfiguration{
		ListenAddress:             config.listenAddress,
		MinimumRuntimeVersion:     config.minimumRuntimeVersion,
		MinimumBrowserRevision:    config.minimumBrowserRevision,
		ClientSessionSeconds:      int64(config.clientSessionTTL / time.Second),
		ClientRefreshSeconds:      int64(config.clientRefreshTTL / time.Second),
		AdminSessionSeconds:       int64(config.adminSessionTTL / time.Second),
		ReconcileIntervalSeconds:  int64(config.reconciler.TickInterval / time.Second),
		ProbeHeartbeatTTLSeconds:  int64(config.reconciler.ProbeHeartbeatTTL / time.Second),
		ProbeOfflineRetentionDays: int64(config.reconciler.OfflineRetention / (24 * time.Hour)),
		AssignmentRetryMinSeconds: int64(config.reconciler.RetryMinimum / time.Second),
		AssignmentRetryMaxSeconds: int64(config.reconciler.RetryMaximum / time.Second),
		ReconcileBatchSize:        config.reconciler.BatchSize,
	}
}

func serveCentral(ctx context.Context, server *http.Server, reconciler *reconcile.Engine) error {
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
	group.Go(func() error { return reconciler.Run(groupContext) })
	group.Go(func() error {
		<-groupContext.Done()
		return shutdown(server)
	})
	return group.Wait()
}

func shutdown(server *http.Server) error {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown Central HTTP server: %w", err)
	}
	return nil
}
