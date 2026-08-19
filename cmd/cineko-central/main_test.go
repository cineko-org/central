package main

import (
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central/reconcile"
)

func TestAdminConfigurationSanitizesRuntimeSettings(t *testing.T) {
	configuration := adminConfiguration(applicationConfig{
		listenAddress:          ":9090",
		minimumRuntimeVersion:  "v1.2.3",
		minimumBrowserRevision: "140",
		clientSessionTTL:       12 * time.Hour,
		clientRefreshTTL:       30 * 24 * time.Hour,
		adminSessionTTL:        6 * time.Hour,
		reconciler: reconcile.Config{
			TickInterval:      5 * time.Second,
			ProbeHeartbeatTTL: 90 * time.Second,
			OfflineRetention:  30 * 24 * time.Hour,
			RetryMinimum:      time.Second,
			RetryMaximum:      5 * time.Second,
			BatchSize:         100,
		},
	})
	if configuration.ListenAddress != ":9090" || configuration.MinimumRuntimeVersion != "v1.2.3" ||
		configuration.MinimumBrowserRevision != "140" || configuration.ClientSessionSeconds != 43_200 ||
		configuration.ClientRefreshSeconds != 2_592_000 || configuration.AdminSessionSeconds != 21_600 ||
		configuration.ReconcileIntervalSeconds != 5 || configuration.ProbeHeartbeatTTLSeconds != 90 ||
		configuration.ProbeOfflineRetentionDays != 30 || configuration.AssignmentRetryMinSeconds != 1 ||
		configuration.AssignmentRetryMaxSeconds != 5 || configuration.ReconcileBatchSize != 100 {
		t.Fatalf("unexpected admin configuration: %+v", configuration)
	}
}
