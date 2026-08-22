package central

import (
	"errors"
	"testing"
	"time"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
)

func TestLauncherRuntimeRejectsClientAboveAvailableLauncher(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	client := validClientRelease()
	client.SetMinimumLauncherVersion("2.0.0")

	if err := service.ConfigureReleases([]*releasepb.ClientRelease{client}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureBrowserReleases([]*releasepb.BrowserRelease{validBrowserRelease()}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigurePlaywrightReleases([]*releasepb.PlaywrightRelease{validPlaywrightRelease()}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureLauncherReleases([]*releasepb.LauncherRelease{validLauncherRelease()}); err != nil {
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

	request := &clientpb.SessionExchangeRequest{}
	request.SetLaunchTicket("launch-ticket")
	request.SetClientNonce("client-nonce-0001")
	_, err = service.ExchangeLaunchTicket(t.Context(), request)
	if !errors.Is(err, errInjectedClient) {
		t.Fatalf("release generation error = %v", err)
	}
}
