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
	if configuration.GetListenAddress() != ":9090" || configuration.GetMinimumRuntimeVersion() != "v1.2.3" ||
		configuration.GetMinimumBrowserRevision() != "140" || configuration.GetClientSessionSeconds() != 43_200 ||
		configuration.GetClientRefreshSeconds() != 2_592_000 || configuration.GetAdminSessionSeconds() != 21_600 ||
		configuration.GetReconcileIntervalSeconds() != 5 || configuration.GetProbeHeartbeatTtlSeconds() != 90 ||
		configuration.GetProbeOfflineRetentionDays() != 30 || configuration.GetAssignmentRetryMinSeconds() != 1 ||
		configuration.GetAssignmentRetryMaxSeconds() != 5 || configuration.GetReconcileBatchSize() != 100 {
		t.Fatalf("unexpected admin configuration: %+v", configuration)
	}
}
