package central

import (
	"errors"
	"testing"
	"time"
)

func TestLauncherRuntimeRejectsClientAboveAvailableLauncher(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	client := validClientRelease()
	client.MinimumLauncherVersion = "2.0.0"

	if err := service.ConfigureReleases([]ClientRelease{client}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureBrowserReleases([]BrowserRelease{validBrowserRelease()}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigurePlaywrightReleases([]PlaywrightRelease{validPlaywrightRelease()}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureLauncherReleases([]LauncherRelease{validLauncherRelease()}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.CurrentRuntimeRelease("stable", "darwin", "arm64"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime with outdated Launcher error = %v", err)
	}
}

func TestLaunchTicketExchangePropagatesReleaseGenerationError(t *testing.T) {
	repository := &releaseRepositoryFake{
		clientRepositoryFake: &clientRepositoryFake{},
		listErr:              errInjectedClient,
	}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	service.random = deterministicClientRandom

	_, err = service.ExchangeLaunchTicket(t.Context(), ClientSessionExchangeRequest{
		LaunchTicket: "launch-ticket", ClientNonce: "client-nonce-0001",
	})
	if !errors.Is(err, errInjectedClient) {
		t.Fatalf("release generation error = %v", err)
	}
}
