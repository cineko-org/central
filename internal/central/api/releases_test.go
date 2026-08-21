package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	executionpb "github.com/cineko-org/contracts/gen/go/cineko/execution"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const releasePublisherToken = "0123456789abcdef0123456789abcdef"

func TestReleaseRegistryPublisherAndCurrentEndpoints(t *testing.T) {
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	repository := &apiClientRepository{
		principal: central.ClientPrincipal{UserID: "user", SessionID: "session"},
		device:    apiClientDevice(),
	}
	clients, err := central.NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewAdminAuth([]AdminCredential{{
		UserID: "admin", DisplayName: "Admin", Password: adminTestPassword,
	}}, adminTestPepper, newAdminSessionRepository(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	server, err := New(
		probeService, WithClientService(clients), WithAdminAuth(admin),
		WithReleasePublishToken(releasePublisherToken),
	)
	if err != nil {
		t.Fatal(err)
	}
	withoutPublisher, err := New(probeService, WithClientService(clients))
	if err != nil {
		t.Fatal(err)
	}
	unavailable := request(t, withoutPublisher.Handler(), http.MethodPost, "/v1/release-registry/client", apiClientReleaseSet(), map[string]string{
		"Authorization": "Bearer " + releasePublisherToken,
	})
	assertAPIError(t, unavailable, http.StatusServiceUnavailable, "release_publisher_unavailable")

	publishHeaders := map[string]string{
		"Authorization": "Bearer " + releasePublisherToken,
	}
	for name, headers := range map[string]map[string]string{
		"missing": {},
		"bare": {
			"Authorization": releasePublisherToken,
		},
		"wrong": {
			"Authorization": "Bearer " + strings.Repeat("x", 32),
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := request(t, server.Handler(), http.MethodPost, "/v1/release-registry/client", apiClientReleaseSet(), headers)
			assertAPIError(t, response, http.StatusUnauthorized, "unauthorized")
		})
	}
	unknown := request(t, server.Handler(), http.MethodPost, "/v1/release-registry/unknown", map[string]any{}, publishHeaders)
	assertAPIError(t, unknown, http.StatusNotFound, "not_found")
	invalid := request(t, server.Handler(), http.MethodPost, "/v1/release-registry/client", map[string]any{
		"unknown": true,
	}, publishHeaders)
	assertAPIError(t, invalid, http.StatusBadRequest, "invalid_json")
	clientWithUnknownField := map[string]any{"releases": []any{map[string]any{"unknown": true}}}
	invalidNested := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/client", clientWithUnknownField, publishHeaders,
	)
	assertAPIError(t, invalidNested, http.StatusBadRequest, "invalid_json")
	partial := request(t, server.Handler(), http.MethodPost, "/v1/release-registry/client",
		releasepb.ClientReleaseSet_builder{Releases: []*releasepb.ClientRelease{apiClientRelease()}}.Build(), publishHeaders)
	assertAPIError(t, partial, http.StatusBadRequest, "invalid_request")

	publications := []struct {
		component string
		payload   any
		want      int64
	}{
		{component: "client", payload: apiClientReleaseSet(), want: 0},
		{component: "browser", payload: apiBrowserReleaseSet(), want: 0},
		{component: "playwright", payload: apiPlaywrightReleaseSet(), want: 0},
		{component: "launcher", payload: apiLauncherReleaseSet(), want: 1},
		{component: "probe", payload: apiProbeReleaseSet(), want: 1},
	}
	for _, publication := range publications {
		response := request(
			t, server.Handler(), http.MethodPost, "/v1/release-registry/"+publication.component,
			publication.payload, publishHeaders,
		)
		if response.Code != http.StatusCreated ||
			response.Header().Get(releaseGenerationHeader) != stringGeneration(publication.want) {
			t.Fatalf("publish %s = %d, headers %v, body %s", publication.component, response.Code, response.Header(), response.Body)
		}
	}
	idempotent := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/probe", apiProbeReleaseSet(), publishHeaders,
	)
	if idempotent.Code != http.StatusOK || idempotent.Header().Get(releaseGenerationHeader) != "1" {
		t.Fatalf("idempotent Probe publish = %d, headers %v", idempotent.Code, idempotent.Header())
	}
	idempotentDesktop := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/client", apiClientReleaseSet(), publishHeaders,
	)
	if idempotentDesktop.Code != http.StatusOK ||
		idempotentDesktop.Header().Get(releaseGenerationHeader) != "1" {
		t.Fatalf("idempotent desktop publish = %d, headers %v", idempotentDesktop.Code, idempotentDesktop.Header())
	}
	conflictPayload := apiClientReleaseSet()
	conflictPayload.GetReleases()[0].GetArtifact().SetSha256(strings.Repeat("f", 64))
	conflict := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/client", conflictPayload, publishHeaders,
	)
	assertAPIError(t, conflict, http.StatusConflict, "conflict")

	clientHeaders := map[string]string{
		"Authorization": "Bearer session-token",
	}
	runtimeResponse := request(
		t, server.Handler(), http.MethodGet, "/v1/releases/runtime/current?platform=darwin&arch=arm64", nil,
		clientHeaders,
	)
	if runtimeResponse.Code != http.StatusOK || runtimeResponse.Header().Get(releaseGenerationHeader) != "1" ||
		!strings.Contains(runtimeResponse.Body.String(), `"revision":"1234"`) {
		t.Fatalf("current runtime = %d, headers %v, body %s", runtimeResponse.Code, runtimeResponse.Header(), runtimeResponse.Body)
	}
	launcherResponse := request(
		t, server.Handler(), http.MethodGet, "/v1/releases/launcher/current?platform=darwin&arch=arm64", nil,
		clientHeaders,
	)
	if launcherResponse.Code != http.StatusOK || !strings.Contains(launcherResponse.Body.String(), `"version":"1.0.0"`) {
		t.Fatalf("current Launcher = %d, %s", launcherResponse.Code, launcherResponse.Body)
	}

	nonce := strings.Repeat("n", 16)
	launchContext := &clientpb.LaunchContext{}
	launchContext.SetInstallationId("install")
	launchContext.SetDeviceId("device")
	launchContext.SetReleaseGeneration(3)
	launchContext.SetClientVersion("1.0.0")
	launchContext.SetArtifactSha256(apiClientRelease().GetArtifact().GetSha256())
	launchContext.SetBrowserRevision("1234")
	launchContext.SetBrowserArtifactSha256(apiBrowserRelease().GetArtifact().GetSha256())
	launchContext.SetPlaywrightVersion("1.61.1")
	launchContext.SetPlaywrightArtifactSha256(apiPlaywrightRelease().GetArtifact().GetSha256())
	staleTicketRequest := &clientpb.LaunchTicketRequest{}
	staleTicketRequest.SetContext(launchContext)
	staleTicketRequest.SetNonce(nonce)
	staleTicket := request(t, server.Handler(), http.MethodPost, "/v1/launch-tickets", staleTicketRequest, map[string]string{
		"Authorization":   "Bearer session-token",
		"Idempotency-Key": nonce,
	})
	assertAPIError(t, staleTicket, http.StatusConflict, "stale_release")

	health := request(t, server.Handler(), http.MethodGet, "/health", nil, nil)
	if health.Header().Get(releaseGenerationHeader) != "1" {
		t.Fatalf("health release generation = %q", health.Header().Get(releaseGenerationHeader))
	}

	login := request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": adminTestPassword,
	}, nil)
	probeInventory := requestWithCookie(
		t, server, http.MethodGet, "/v1/admin/releases/probe/current", login.Result().Cookies()[0],
	)
	if probeInventory.Code != http.StatusOK || !strings.Contains(probeInventory.Body.String(), `"imageDigest":"sha256:`) {
		t.Fatalf("current Probe inventory = %d, %s", probeInventory.Code, probeInventory.Body)
	}
}

func TestEventStreamHeartbeatCarriesReleaseGeneration(t *testing.T) {
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	repository := &apiClientRepository{principal: central.ClientPrincipal{UserID: "user", SessionID: "session"}, generation: 7}
	clients, err := central.NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := clients.LoadReleaseRegistry(t.Context()); err != nil {
		t.Fatal(err)
	}
	server, err := New(probeService, WithClientService(clients))
	if err != nil {
		t.Fatal(err)
	}
	server.eventHeartbeat = time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	writer := newCancelResponseWriter(cancel, `"ready"`)
	httpRequest := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/events/stream", nil)
	httpRequest.Header.Set("Authorization", "Bearer session-token")
	server.Handler().ServeHTTP(writer, httpRequest)
	if writer.status != http.StatusOK || !strings.Contains(writer.body.String(), `"releaseGeneration":"7"`) ||
		!strings.Contains(writer.body.String(), `"ready"`) {
		t.Fatalf("event stream = %d, %q", writer.status, writer.body.String())
	}
}

func stringGeneration(generation int64) string {
	return strconv.FormatInt(generation, 10)
}

func apiClientRelease() *releasepb.ClientRelease {
	channel, platform, architecture, version := "stable", "darwin", "arm64", "1.0.0"
	launcherVersion, browserRevision, playwrightVersion := "1.0.0", "1234", "1.61.1"
	return releasepb.ClientRelease_builder{
		Channel: &channel, Platform: &platform, Architecture: &architecture, Version: &version,
		MinimumLauncherVersion: &launcherVersion, MinimumBrowserRevision: &browserRevision,
		PlaywrightVersion: &playwrightVersion,
		Artifact:          apiArtifact("client", strings.Repeat("1", 64)),
		ProbeBootstrapPublicKeys: map[string]string{
			"primary": "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----\n",
		},
		PublishedAt: timestamppb.New(apiPublishedAt()),
	}.Build()
}

func apiClientReleaseSet() *releasepb.ClientReleaseSet {
	base := apiClientRelease()
	return releasepb.ClientReleaseSet_builder{Releases: []*releasepb.ClientRelease{
		base,
		apiClientReleaseForTarget(base, "linux", "amd64", "client"),
		apiClientReleaseForTarget(base, "windows", "amd64", "client.exe"),
	}}.Build()
}

func apiClientReleaseForTarget(
	base *releasepb.ClientRelease,
	platform string,
	architecture string,
	executable string,
) *releasepb.ClientRelease {
	result := proto.CloneOf(base)
	result.SetPlatform(platform)
	result.SetArchitecture(architecture)
	result.GetArtifact().SetUrl("https://downloads.example.com/cineko/releases/client/" + platform + "/artifact.zip")
	result.GetArtifact().SetExecutable(executable)
	return result
}

func apiBrowserRelease() *releasepb.BrowserRelease {
	channel, platform, architecture, revision := "stable", "darwin", "arm64", "1234"
	return releasepb.BrowserRelease_builder{Channel: &channel, Platform: &platform, Architecture: &architecture,
		Revision: &revision, CompatiblePlaywrightVersions: []string{"1.61.1"},
		Artifact: apiArtifact("browser", strings.Repeat("2", 64)), PublishedAt: timestamppb.New(apiPublishedAt())}.Build()
}

func apiBrowserReleaseSet() *releasepb.BrowserReleaseSet {
	base := apiBrowserRelease()
	linux := proto.CloneOf(base)
	linux.SetPlatform("linux")
	linux.SetArchitecture("amd64")
	linux.GetArtifact().SetUrl("https://downloads.example.com/cineko/releases/browser/linux/artifact.zip")
	linux.GetArtifact().SetExecutable("chrome")
	windows := proto.CloneOf(base)
	windows.SetPlatform("windows")
	windows.SetArchitecture("amd64")
	windows.GetArtifact().SetUrl("https://downloads.example.com/cineko/releases/browser/windows/artifact.zip")
	windows.GetArtifact().SetExecutable("chrome.exe")
	return releasepb.BrowserReleaseSet_builder{Releases: []*releasepb.BrowserRelease{base, linux, windows}}.Build()
}

func apiPlaywrightRelease() *releasepb.PlaywrightRelease {
	channel, platform, architecture, version := "stable", "darwin", "arm64", "1.61.1"
	return releasepb.PlaywrightRelease_builder{Channel: &channel, Platform: &platform, Architecture: &architecture,
		Version: &version, Artifact: apiArtifact("playwright", strings.Repeat("3", 64)), PublishedAt: timestamppb.New(apiPublishedAt())}.Build()
}

func apiPlaywrightReleaseSet() *releasepb.PlaywrightReleaseSet {
	base := apiPlaywrightRelease()
	linux := proto.CloneOf(base)
	linux.SetPlatform("linux")
	linux.SetArchitecture("amd64")
	linux.GetArtifact().SetUrl("https://downloads.example.com/cineko/releases/playwright/linux/artifact.zip")
	linux.GetArtifact().SetExecutable("playwright")
	windows := proto.CloneOf(base)
	windows.SetPlatform("windows")
	windows.SetArchitecture("amd64")
	windows.GetArtifact().SetUrl("https://downloads.example.com/cineko/releases/playwright/windows/artifact.zip")
	windows.GetArtifact().SetExecutable("playwright.exe")
	return releasepb.PlaywrightReleaseSet_builder{Releases: []*releasepb.PlaywrightRelease{base, linux, windows}}.Build()
}

func apiLauncherRelease() *releasepb.LauncherRelease {
	channel, platform, architecture, version := "stable", "darwin", "arm64", "1.0.0"
	return releasepb.LauncherRelease_builder{Channel: &channel, Platform: &platform, Architecture: &architecture,
		Version: &version, Launcher: apiArtifact("launcher", strings.Repeat("4", 64)), PublishedAt: timestamppb.New(apiPublishedAt())}.Build()
}

func apiLauncherReleaseSet() *releasepb.LauncherReleaseSet {
	base := apiLauncherRelease()
	linux := proto.CloneOf(base)
	linux.SetPlatform("linux")
	linux.SetArchitecture("amd64")
	linux.GetLauncher().SetUrl("https://downloads.example.com/cineko/releases/launcher/linux/artifact.zip")
	linux.GetLauncher().SetExecutable("cineko-launcher")
	windows := proto.CloneOf(base)
	windows.SetPlatform("windows")
	windows.SetArchitecture("amd64")
	windows.GetLauncher().SetUrl("https://downloads.example.com/cineko/releases/launcher/windows/artifact.zip")
	windows.GetLauncher().SetExecutable("cineko-launcher.exe")
	return releasepb.LauncherReleaseSet_builder{Releases: []*releasepb.LauncherRelease{base, linux, windows}}.Build()
}

func apiProbeRelease() *releasepb.ProbeRelease {
	channel, version, browserRevision := "stable", "1.0.0", "1234"
	image, digest := "registry.example.com/example/cineko-probe", "sha256:"+strings.Repeat("5", 64)
	return releasepb.ProbeRelease_builder{Channel: &channel, Version: &version, BrowserRevision: &browserRevision,
		Image: &image, ImageDigest: &digest, PublishedAt: timestamppb.New(apiPublishedAt())}.Build()
}

func apiProbeReleaseSet() *releasepb.ProbeReleaseSet {
	return releasepb.ProbeReleaseSet_builder{Releases: []*releasepb.ProbeRelease{apiProbeRelease()}}.Build()
}

func apiArtifact(component, digest string) *releasepb.Artifact {
	url, executable, size := "https://downloads.example.com/cineko/releases/"+component+"/artifact.zip", component, int64(1)
	return releasepb.Artifact_builder{Url: &url, Size: &size, Sha256: &digest, Executable: &executable}.Build()
}

func apiPublishedAt() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) }

func apiClientDevice() *clientpb.Device {
	device := &clientpb.Device{}
	device.SetInstallationId("install")
	device.SetUserId("user")
	device.SetDeviceId("device")
	device.SetPlatform("darwin")
	device.SetArchitecture("arm64")
	return device
}

type cancelResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
	cancel context.CancelFunc
	needle string
}

func newCancelResponseWriter(cancel context.CancelFunc, needle string) *cancelResponseWriter {
	return &cancelResponseWriter{header: make(http.Header), cancel: cancel, needle: needle}
}

func (writer *cancelResponseWriter) Header() http.Header { return writer.header }

func (writer *cancelResponseWriter) WriteHeader(status int) { writer.status = status }

func (writer *cancelResponseWriter) Write(contents []byte) (int, error) {
	written, err := writer.body.Write(contents)
	if strings.Contains(writer.body.String(), writer.needle) {
		writer.cancel()
	}
	return written, err
}

func (*cancelResponseWriter) Flush() {}

type apiClientRepository struct {
	records      []central.ReleaseRecord
	generation   int64
	principal    central.ClientPrincipal
	device       *clientpb.Device
	ticket       central.LaunchTicket
	authenticate func([32]byte) (central.ClientPrincipal, error)
}

func (repository *apiClientRepository) ListReleases(context.Context) ([]central.ReleaseRecord, int64, error) {
	return append([]central.ReleaseRecord(nil), repository.records...), repository.generation, nil
}

func (repository *apiClientRepository) CurrentReleaseGeneration(context.Context) (int64, error) {
	return repository.generation, nil
}

func (repository *apiClientRepository) InsertReleaseSet(
	_ context.Context,
	records []central.ReleaseRecord,
) (int64, bool, error) {
	identity := records[0]
	existing := make([]central.ReleaseRecord, 0, len(records))
	for _, stored := range repository.records {
		if stored.Kind == identity.Kind && stored.Channel == identity.Channel && stored.Version == identity.Version {
			existing = append(existing, stored)
		}
	}
	if len(existing) > 0 {
		if len(existing) != len(records) {
			return 0, false, central.ErrConflict
		}
		for _, record := range records {
			matched := false
			for _, stored := range existing {
				matched = matched || stored.Platform == record.Platform && stored.Arch == record.Arch &&
					bytes.Equal(stored.Payload, record.Payload)
			}
			if !matched {
				return 0, false, central.ErrConflict
			}
		}
		return repository.generation, false, nil
	}
	repository.records = append(repository.records, records...)
	before := repository.records[:len(repository.records)-len(records)]
	beforeFingerprint, beforeErr := central.ActiveDesktopManifestFingerprint(before)
	afterFingerprint, afterErr := central.ActiveDesktopManifestFingerprint(repository.records)
	if beforeErr != nil || afterErr != nil {
		if beforeErr != nil {
			return 0, false, beforeErr
		}
		return 0, false, afterErr
	}
	if beforeFingerprint != afterFingerprint {
		repository.generation++
	}
	return repository.generation, true, nil
}

func (*apiClientRepository) ProvisionClientCredential(context.Context, *clientpb.User, [32]byte) error {
	return nil
}

func (*apiClientRepository) ExchangeClientCredential(context.Context, string, [32]byte, time.Time) (*clientpb.User, error) {
	return &clientpb.User{}, nil
}

func (*apiClientRepository) CreateClientSession(context.Context, central.ClientSession) error {
	return nil
}

func (*apiClientRepository) RotateClientSession(context.Context, [32]byte, central.ClientSession, time.Time) (*clientpb.User, error) {
	return &clientpb.User{}, nil
}

func (*apiClientRepository) RevokeClientSession(context.Context, string, time.Time) error { return nil }

func (repository *apiClientRepository) CreateLaunchTicket(_ context.Context, ticket central.LaunchTicket) error {
	repository.ticket = ticket
	return nil
}

func (repository *apiClientRepository) ExchangeLaunchTicket(
	context.Context, [32]byte, string, int64, central.ClientSession, time.Time,
) (central.LaunchedClient, error) {
	return central.LaunchedClient{}, nil
}

func (repository *apiClientRepository) AuthenticateClientSession(_ context.Context, hash [32]byte, _ time.Time) (central.ClientPrincipal, error) {
	if repository.authenticate != nil {
		return repository.authenticate(hash)
	}
	return repository.principal, nil
}

func (repository *apiClientRepository) UpsertClientDevice(_ context.Context, device *clientpb.Device) (*clientpb.Device, error) {
	repository.device = device
	return device, nil
}

func (repository *apiClientRepository) GetClientDevice(context.Context, string, string) (*clientpb.Device, error) {
	return repository.device, nil
}

func (*apiClientRepository) GetClientUser(context.Context, string) (*clientpb.User, error) {
	return &clientpb.User{}, nil
}

func (*apiClientRepository) ClientResourceRevisions(context.Context, string) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (*apiClientRepository) ListClientResources(context.Context, string, string) ([]*clientpb.Resource, error) {
	return nil, nil
}

func (*apiClientRepository) GetClientResource(context.Context, string, string, string) (*clientpb.Resource, error) {
	return nil, central.ErrNotFound
}

func (*apiClientRepository) PutClientResource(context.Context, central.ResourceMutation) (*clientpb.Resource, error) {
	return nil, central.ErrNotFound
}

func (*apiClientRepository) DeleteClientResource(context.Context, central.ResourceMutation) (*clientpb.Resource, error) {
	return nil, central.ErrNotFound
}

func (*apiClientRepository) ListClientEvents(context.Context, string, int64, int) ([]*clientpb.ClientEvent, error) {
	return nil, nil
}

func (*apiClientRepository) ClaimClientExecution(context.Context, central.ExecutionClaim) (*executionpb.Command, error) {
	return nil, central.ErrNotFound
}

func (*apiClientRepository) HeartbeatClientExecution(context.Context, string, string, [32]byte, time.Time, time.Time) error {
	return nil
}

func (*apiClientRepository) CompleteClientExecution(context.Context, central.ExecutionCompletion) error {
	return nil
}

func (*apiClientRepository) RetryClientExecution(context.Context, string, string, time.Time) error {
	return nil
}

var _ central.ClientRepository = (*apiClientRepository)(nil)
var _ central.ReleaseRepository = (*apiClientRepository)(nil)
