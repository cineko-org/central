package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	contracts "github.com/cineko-org/contracts/v3"
)

const releasePublisherToken = "0123456789abcdef0123456789abcdef"

func TestReleaseRegistryPublisherAndCurrentEndpoints(t *testing.T) {
	probeService, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	repository := &apiClientRepository{
		principal: central.ClientPrincipal{UserID: "user", SessionID: "session"},
		device: central.ClientDevice{
			InstallationID: "install", UserID: "user", DeviceID: "device", Platform: "darwin", Arch: "arm64",
		},
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
		"Authorization":          "Bearer " + releasePublisherToken,
		contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
	})
	assertAPIError(t, unavailable, http.StatusServiceUnavailable, "release_publisher_unavailable")

	publishHeaders := map[string]string{
		"Authorization":          "Bearer " + releasePublisherToken,
		contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
	}
	for name, headers := range map[string]map[string]string{
		"missing": {contracts.ProtocolHeader: contracts.ProtocolHeaderValue()},
		"bare": {
			"Authorization": releasePublisherToken, contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
		},
		"wrong": {
			"Authorization":          "Bearer " + strings.Repeat("x", 32),
			contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
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
	clientWithUnknownField := releaseSetPayload(t, apiClientReleaseSet())
	envelopePayload, payloadOK := clientWithUnknownField["payload"].(map[string]any)
	if !payloadOK {
		t.Fatal("encoded release envelope has an unexpected shape")
	}
	releaseValues, valuesOK := envelopePayload["releases"].([]any)
	if !valuesOK || len(releaseValues) == 0 {
		t.Fatal("encoded release set has an unexpected shape")
	}
	firstRelease, releaseOK := releaseValues[0].(map[string]any)
	if !releaseOK {
		t.Fatal("encoded release has an unexpected shape")
	}
	firstRelease["unknown"] = true
	invalidNested := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/client", clientWithUnknownField, publishHeaders,
	)
	assertAPIError(t, invalidNested, http.StatusBadRequest, "invalid_json")
	partial := request(t, server.Handler(), http.MethodPost, "/v1/release-registry/client", contracts.ReleaseEnvelope[contracts.ReleaseSet[central.ClientRelease]]{
		SchemaVersion: contracts.ReleasePayloadSchemaVersion,
		Payload: contracts.ReleaseSet[central.ClientRelease]{
			Releases: []central.ClientRelease{apiClientRelease()},
		},
	}, publishHeaders)
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
			response.Header().Get(contracts.ReleaseGenerationHeader) != stringGeneration(publication.want) ||
			!strings.Contains(response.Body.String(), `"generation":`+stringGeneration(publication.want)) {
			t.Fatalf("publish %s = %d, headers %v, body %s", publication.component, response.Code, response.Header(), response.Body)
		}
	}
	idempotent := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/probe", apiProbeReleaseSet(), publishHeaders,
	)
	if idempotent.Code != http.StatusOK || idempotent.Header().Get(contracts.ReleaseGenerationHeader) != "1" {
		t.Fatalf("idempotent Probe publish = %d, headers %v", idempotent.Code, idempotent.Header())
	}
	idempotentDesktop := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/client", apiClientReleaseSet(), publishHeaders,
	)
	if idempotentDesktop.Code != http.StatusOK ||
		idempotentDesktop.Header().Get(contracts.ReleaseGenerationHeader) != "1" {
		t.Fatalf("idempotent desktop publish = %d, headers %v", idempotentDesktop.Code, idempotentDesktop.Header())
	}
	conflictPayload := apiClientReleaseSet()
	conflictPayload.Payload.Releases[0].Artifact.SHA256 = strings.Repeat("f", 64)
	conflict := request(
		t, server.Handler(), http.MethodPost, "/v1/release-registry/client", conflictPayload, publishHeaders,
	)
	assertAPIError(t, conflict, http.StatusConflict, "conflict")

	clientHeaders := map[string]string{
		"Authorization": "Bearer session-token", contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
	}
	runtimeResponse := request(
		t, server.Handler(), http.MethodGet, "/v1/releases/runtime/current?platform=darwin&arch=arm64", nil,
		clientHeaders,
	)
	if runtimeResponse.Code != http.StatusOK || runtimeResponse.Header().Get(contracts.ReleaseGenerationHeader) != "1" ||
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
	staleTicket := request(t, server.Handler(), http.MethodPost, "/v1/launch-tickets", map[string]any{
		"installationId": "install", "deviceId": "device", "releaseGeneration": 3,
		"clientVersion": "1.0.0", "artifactSha256": apiClientRelease().Artifact.SHA256,
		"protocol": central.ProtocolVersion, "browserRevision": "1234",
		"browserArtifactSha256": apiBrowserRelease().Artifact.SHA256,
		"playwrightVersion":     "1.61.1", "playwrightArtifactSha256": apiPlaywrightRelease().Artifact.SHA256,
		"nonce": nonce,
	}, map[string]string{
		"Authorization": "Bearer session-token", contracts.ProtocolHeader: contracts.ProtocolHeaderValue(),
		"Idempotency-Key": nonce,
	})
	assertAPIError(t, staleTicket, http.StatusConflict, "stale_release")

	health := request(t, server.Handler(), http.MethodGet, "/health", nil, nil)
	if health.Header().Get(contracts.ReleaseGenerationHeader) != "1" {
		t.Fatalf("health release generation = %q", health.Header().Get(contracts.ReleaseGenerationHeader))
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
	writer := newCancelResponseWriter(cancel, `"releaseGeneration":7`)
	httpRequest := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/events/stream", nil)
	httpRequest.Header.Set("Authorization", "Bearer session-token")
	httpRequest.Header.Set(contracts.ProtocolHeader, contracts.ProtocolHeaderValue())
	server.Handler().ServeHTTP(writer, httpRequest)
	if writer.status != http.StatusOK || !strings.Contains(writer.body.String(), `"releaseGeneration":7`) ||
		!strings.Contains(writer.body.String(), `"action":"ready"`) {
		t.Fatalf("event stream = %d, %q", writer.status, writer.body.String())
	}
}

func stringGeneration(generation int64) string {
	return strconv.FormatInt(generation, 10)
}

func releaseSetPayload(t *testing.T, releaseSet any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(releaseSet)
	if err != nil {
		t.Fatal(err)
	}
	payload := make(map[string]any)
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func apiClientRelease() central.ClientRelease {
	return central.ClientRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0",
		MinimumLauncherVersion: "1.0.0", MinimumBrowserRevision: "1234",
		PlaywrightVersion: "1.61.1", Protocol: central.ProtocolVersion,
		Artifact: apiArtifact("client", strings.Repeat("1", 64)),
		ProbeBootstrapPublicKeys: map[string]string{
			"primary": "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----\n",
		},
		PublishedAt: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
	}
}

func apiClientReleaseSet() contracts.ReleaseEnvelope[contracts.ReleaseSet[central.ClientRelease]] {
	base := apiClientRelease()
	return contracts.ReleaseEnvelope[contracts.ReleaseSet[central.ClientRelease]]{
		SchemaVersion: contracts.ReleasePayloadSchemaVersion,
		Payload: contracts.ReleaseSet[central.ClientRelease]{Releases: []central.ClientRelease{
			base,
			apiClientReleaseForTarget(base, "linux", "amd64", "client"),
			apiClientReleaseForTarget(base, "windows", "amd64", "client.exe"),
		}},
	}
}

func apiClientReleaseForTarget(
	base central.ClientRelease,
	platform string,
	architecture string,
	executable string,
) central.ClientRelease {
	base.Platform, base.Arch = platform, architecture
	base.Artifact.URL = "https://downloads.example.com/cineko/releases/client/" + platform + "/artifact.zip"
	base.Artifact.Executable = executable
	return base
}

func apiBrowserRelease() central.BrowserRelease {
	return central.BrowserRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Revision: "1234",
		CompatiblePlaywrightVersions: []string{"1.61.1"},
		Artifact:                     apiArtifact("browser", strings.Repeat("2", 64)), PublishedAt: apiClientRelease().PublishedAt,
	}
}

func apiBrowserReleaseSet() contracts.ReleaseEnvelope[contracts.ReleaseSet[central.BrowserRelease]] {
	base := apiBrowserRelease()
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Artifact.URL, linux.Artifact.Executable = "https://downloads.example.com/cineko/releases/browser/linux/artifact.zip", "chrome"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Artifact.URL, windows.Artifact.Executable = "https://downloads.example.com/cineko/releases/browser/windows/artifact.zip", "chrome.exe"
	return releaseEnvelope([]central.BrowserRelease{base, linux, windows})
}

func apiPlaywrightRelease() central.PlaywrightRelease {
	return central.PlaywrightRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.61.1",
		Artifact: apiArtifact("playwright", strings.Repeat("3", 64)), PublishedAt: apiClientRelease().PublishedAt,
	}
}

func apiPlaywrightReleaseSet() contracts.ReleaseEnvelope[contracts.ReleaseSet[central.PlaywrightRelease]] {
	base := apiPlaywrightRelease()
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Artifact.URL, linux.Artifact.Executable = "https://downloads.example.com/cineko/releases/playwright/linux/artifact.zip", "playwright"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Artifact.URL, windows.Artifact.Executable = "https://downloads.example.com/cineko/releases/playwright/windows/artifact.zip", "playwright.exe"
	return releaseEnvelope([]central.PlaywrightRelease{base, linux, windows})
}

func apiLauncherRelease() central.LauncherRelease {
	return central.LauncherRelease{
		Channel: "stable", Platform: "darwin", Arch: "arm64", Version: "1.0.0", Protocol: central.ProtocolVersion,
		Launcher: apiArtifact("launcher", strings.Repeat("4", 64)), PublishedAt: apiClientRelease().PublishedAt,
	}
}

func apiLauncherReleaseSet() contracts.ReleaseEnvelope[contracts.ReleaseSet[central.LauncherRelease]] {
	base := apiLauncherRelease()
	linux, windows := base, base
	linux.Platform, linux.Arch = "linux", "amd64"
	linux.Launcher.URL, linux.Launcher.Executable = "https://downloads.example.com/cineko/releases/launcher/linux/artifact.zip", "cineko-launcher"
	windows.Platform, windows.Arch = "windows", "amd64"
	windows.Launcher.URL, windows.Launcher.Executable = "https://downloads.example.com/cineko/releases/launcher/windows/artifact.zip", "cineko-launcher.exe"
	return releaseEnvelope([]central.LauncherRelease{base, linux, windows})
}

func apiProbeRelease() central.ProbeRelease {
	return central.ProbeRelease{
		Channel: "stable", Version: "1.0.0", Protocol: central.ProtocolVersion, BrowserRevision: "1234",
		Image: "registry.example.com/example/cineko-probe", ImageDigest: "sha256:" + strings.Repeat("5", 64),
		PublishedAt: apiClientRelease().PublishedAt,
	}
}

func apiProbeReleaseSet() contracts.ReleaseEnvelope[contracts.ReleaseSet[central.ProbeRelease]] {
	return releaseEnvelope([]central.ProbeRelease{apiProbeRelease()})
}

func releaseEnvelope[Release any](releases []Release) contracts.ReleaseEnvelope[contracts.ReleaseSet[Release]] {
	return contracts.ReleaseEnvelope[contracts.ReleaseSet[Release]]{
		SchemaVersion: contracts.ReleasePayloadSchemaVersion,
		Payload:       contracts.ReleaseSet[Release]{Releases: releases},
	}
}

func apiArtifact(component, digest string) central.ReleaseArtifact {
	return central.ReleaseArtifact{
		URL:  "https://downloads.example.com/cineko/releases/" + component + "/artifact.zip",
		Size: 1, SHA256: digest, Executable: component,
	}
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
	device       central.ClientDevice
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

func (*apiClientRepository) ProvisionClientCredential(context.Context, central.ClientUser, [32]byte) error {
	return nil
}

func (*apiClientRepository) ExchangeClientCredential(context.Context, string, [32]byte, time.Time) (central.ClientUser, error) {
	return central.ClientUser{}, nil
}

func (*apiClientRepository) CreateClientSession(context.Context, central.ClientSession) error {
	return nil
}

func (*apiClientRepository) RotateClientSession(context.Context, [32]byte, central.ClientSession, time.Time) (central.ClientUser, error) {
	return central.ClientUser{}, nil
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

func (repository *apiClientRepository) UpsertClientDevice(_ context.Context, device central.ClientDevice) (central.ClientDevice, error) {
	repository.device = device
	return device, nil
}

func (repository *apiClientRepository) GetClientDevice(context.Context, string, string) (central.ClientDevice, error) {
	return repository.device, nil
}

func (*apiClientRepository) GetClientUser(context.Context, string) (central.ClientUser, error) {
	return central.ClientUser{}, nil
}

func (*apiClientRepository) ClientResourceRevisions(context.Context, string) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (*apiClientRepository) ListClientResources(context.Context, string, string) ([]central.ClientResource, error) {
	return nil, nil
}

func (*apiClientRepository) GetClientResource(context.Context, string, string, string) (central.ClientResource, error) {
	return central.ClientResource{}, nil
}

func (*apiClientRepository) PutClientResource(context.Context, central.ResourceMutation) (central.ClientResource, error) {
	return central.ClientResource{}, nil
}

func (*apiClientRepository) DeleteClientResource(context.Context, central.ResourceMutation) (central.ClientResource, error) {
	return central.ClientResource{}, nil
}

func (*apiClientRepository) ListClientEvents(context.Context, string, int64, int) ([]central.ClientEvent, error) {
	return nil, nil
}

func (*apiClientRepository) ClaimClientExecution(context.Context, central.ExecutionClaim) (central.ExecutionCommand, error) {
	return central.ExecutionCommand{}, central.ErrNotFound
}

func (*apiClientRepository) HeartbeatClientExecution(context.Context, string, string, [32]byte, time.Time, time.Time) error {
	return nil
}

func (*apiClientRepository) CompleteClientExecution(context.Context, central.ExecutionCompletion) error {
	return nil
}

var _ central.ClientRepository = (*apiClientRepository)(nil)
var _ central.ReleaseRepository = (*apiClientRepository)(nil)
