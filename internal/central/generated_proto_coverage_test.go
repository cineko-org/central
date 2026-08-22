package central

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	probedomain "github.com/cineko-org/central/internal/domain/probe"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/v3/gen/go/cineko/common"
	executionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/execution"
	observationpb "github.com/cineko-org/contracts/v3/gen/go/cineko/observation"
	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGeneratedProtoClientServiceBoundaries(t *testing.T) {
	if _, err := NewClientService(nil, time.Hour); err == nil {
		t.Fatal("nil client repository accepted")
	}
	repository := &clientRepositoryFake{}
	if _, err := NewClientService(repository, -time.Second); err == nil {
		t.Fatal("negative client session TTL accepted")
	}
	if _, err := NewClientService(repository, time.Hour, time.Hour); err == nil {
		t.Fatal("refresh TTL equal to session TTL accepted")
	}
	service, err := NewClientService(repository, 0)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	service.random = deterministicClientRandom

	if err := service.Provision(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Provision(t.Context(), []ClientCredentialSeed{{UserID: " ", DisplayName: "name", AccessToken: clientTestToken}}); err == nil {
		t.Fatal("invalid credential seed accepted")
	}
	if err := service.Provision(t.Context(), []ClientCredentialSeed{
		{UserID: "user", DisplayName: "User", AccessToken: clientTestToken},
		{UserID: "user", DisplayName: "Other", AccessToken: clientTestToken},
	}); err == nil {
		t.Fatal("duplicate credential seed accepted")
	}
	repository.err = errInjectedClient
	if err := service.Provision(t.Context(), []ClientCredentialSeed{{UserID: "user", DisplayName: "User", AccessToken: clientTestToken}}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("provision repository error = %v", err)
	}
	repository.err = nil
	if err := service.Provision(t.Context(), []ClientCredentialSeed{{UserID: " user ", DisplayName: " User ", AccessToken: " " + clientTestToken + " "}}); err != nil {
		t.Fatal(err)
	}
	if repository.user.GetId() != "user" || repository.user.GetDisplayName() != "User" {
		t.Fatalf("provisioned user = %+v", repository.user)
	}

	if _, err := service.Exchange(t.Context(), nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("nil exchange error = %v", err)
	}
	repository.err = errInjectedClient
	request := &clientpb.TokenExchangeRequest{}
	request.SetUserId("user")
	request.SetAccessToken(clientTestToken)
	if _, err := service.Exchange(t.Context(), request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("exchange repository error = %v", err)
	}
	repository.err = nil
	repository.fail = "create-session"
	if _, err := service.Exchange(t.Context(), request); !errors.Is(err, errInjectedClient) {
		t.Fatalf("exchange create-session error = %v", err)
	}
	repository.fail = ""
	service.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := service.Exchange(t.Context(), request); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("exchange secret error = %v", err)
	}
	calls := 0
	service.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 2 {
			return 0, io.ErrUnexpectedEOF
		}
		return deterministicClientRandom(buffer)
	}
	if _, err := service.Exchange(t.Context(), request); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("exchange refresh-secret error = %v", err)
	}
	calls = 0
	service.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 3 {
			return 0, io.ErrUnexpectedEOF
		}
		return deterministicClientRandom(buffer)
	}
	if _, err := service.Exchange(t.Context(), request); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("exchange session-secret error = %v", err)
	}
	service.random = deterministicClientRandom

	if _, err := service.Refresh(t.Context(), nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("nil refresh error = %v", err)
	}
	refresh := &clientpb.TokenRefreshRequest{}
	refresh.SetRefreshToken("refresh-token")
	repository.fail = "rotate-session"
	if _, err := service.Refresh(t.Context(), refresh); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("refresh rotation error = %v", err)
	}
	repository.fail = ""
	service.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := service.Refresh(t.Context(), refresh); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("refresh secret error = %v", err)
	}
	service.random = deterministicClientRandom

	if err := service.Logout(t.Context(), ClientPrincipal{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("logout without session = %v", err)
	}
	repository.err = errInjectedClient
	if err := service.Logout(t.Context(), ClientPrincipal{SessionID: "session"}); !errors.Is(err, errInjectedClient) {
		t.Fatalf("logout repository error = %v", err)
	}
	repository.err = nil
	if err := service.Logout(t.Context(), ClientPrincipal{SessionID: "session"}); err != nil || repository.revokedSessionID != "session" {
		t.Fatalf("logout = %v, revoked=%q", err, repository.revokedSessionID)
	}

	if _, err := service.Authenticate(t.Context(), " "); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("blank authenticate = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.Authenticate(t.Context(), "token"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("authenticate repository error = %v", err)
	}
	repository.err = nil
	principal, err := service.Authenticate(t.Context(), " token ")
	if err != nil || principal.UserID != repository.principal.UserID {
		t.Fatalf("authenticate = %+v, %v", principal, err)
	}
}

func TestGeneratedProtoClientResourceBoundaries(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	principal := ClientPrincipal{UserID: "user", SessionID: "session"}

	if _, err := service.Bootstrap(t.Context(), principal, "install"); err != nil {
		t.Fatal(err)
	}
	repository.fail = "device"
	if _, err := service.Bootstrap(t.Context(), principal, "install"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("bootstrap device error = %v", err)
	}
	repository.fail = "device-not-found"
	if _, err := service.Bootstrap(t.Context(), principal, "install"); err != nil {
		t.Fatalf("missing bootstrap device = %v", err)
	}
	repository.fail = "user"
	if _, err := service.Bootstrap(t.Context(), principal, ""); !errors.Is(err, errInjectedClient) {
		t.Fatalf("bootstrap user error = %v", err)
	}
	repository.fail = "revisions"
	if _, err := service.Bootstrap(t.Context(), principal, ""); !errors.Is(err, errInjectedClient) {
		t.Fatalf("bootstrap revisions error = %v", err)
	}
	repository.fail = ""

	incomplete := &clientpb.Device{}
	if _, err := service.UpsertDevice(t.Context(), principal, incomplete); !errors.Is(err, ErrInvalid) {
		t.Fatalf("incomplete device = %v", err)
	}
	device := clientTestDevice()
	device.SetCreatedAt(timestamppb.New(clientTestTime))
	repository.err = errInjectedClient
	if _, err := service.UpsertDevice(t.Context(), principal, device); !errors.Is(err, errInjectedClient) {
		t.Fatalf("device repository error = %v", err)
	}
	repository.err = nil

	if _, err := service.ListResources(t.Context(), principal, "unknown"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid list kind = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.ListResources(t.Context(), principal, "presets"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("list repository error = %v", err)
	}
	repository.err = nil
	resources, err := service.ListResources(t.Context(), principal, "presets")
	if err != nil || len(resources) != 1 {
		t.Fatalf("list resources = %+v, %v", resources, err)
	}
	repository.resource = nil
	if _, err := service.ListResources(t.Context(), principal, "presets"); !errors.Is(err, ErrCorruptResource) {
		t.Fatalf("corrupt list resource = %v", err)
	}
	repository.resource = validClientPresetResource()

	if _, err := service.GetResource(t.Context(), principal, "presets", " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank get id = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.GetResource(t.Context(), principal, "presets", "id"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("get repository error = %v", err)
	}
	repository.err = nil
	if _, err := service.GetResource(t.Context(), principal, "presets", "id"); err != nil {
		t.Fatal(err)
	}
	foreign := validClientPresetResource()
	foreign.GetPreset().SetId("other")
	repository.resource = foreign
	if _, err := service.GetResource(t.Context(), principal, "presets", "id"); !errors.Is(err, ErrCorruptResource) {
		t.Fatalf("corrupt get resource = %v", err)
	}
	repository.resource = validClientPresetResource()

	identityMissing := proto.CloneOf(repository.resource)
	identityMissing.SetIdentity(nil)
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", identityMissing, nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing resource identity = %v", err)
	}
	zero := int64(0)
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", repository.resource, &zero, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero expected revision = %v", err)
	}
	if _, err := service.PutResource(t.Context(), principal, "settings", "id", repository.resource, nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched resource kind = %v", err)
	}
	if _, err := service.PutResource(t.Context(), principal, "presets", "other", repository.resource, nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched resource id = %v", err)
	}
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", repository.resource, nil, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank command id = %v", err)
	}
	repository.err = errInjectedClient
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", repository.resource, nil, "command"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("put repository error = %v", err)
	}
	repository.err = nil
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", repository.resource, nil, "command"); err != nil {
		t.Fatal(err)
	}
	invalidUTF8 := proto.CloneOf(repository.resource)
	invalidUTF8.GetPreset().SetName(string([]byte{0xff}))
	if _, err := service.PutResource(t.Context(), principal, "presets", "id", invalidUTF8, nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid ProtoJSON payload = %v", err)
	}

	if _, err := service.DeleteResource(t.Context(), principal, "unknown", "id", &zero, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid delete kind = %v", err)
	}
	if _, err := service.DeleteResource(t.Context(), principal, "presets", "id", nil, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil delete revision = %v", err)
	}
	if _, err := service.DeleteResource(t.Context(), principal, "presets", "id", &zero, "command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero delete revision = %v", err)
	}
	repository.err = errInjectedClient
	revision := int64(1)
	if _, err := service.DeleteResource(t.Context(), principal, "presets", "id", &revision, "command"); !errors.Is(err, errInjectedClient) {
		t.Fatalf("delete repository error = %v", err)
	}
	repository.err = nil
	if _, err := service.DeleteResource(t.Context(), principal, "presets", "id", &revision, "command"); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"settings", "presets", "monitors", "reservations", "external-operations", "app-events"} {
		if !validClientResourceKind(kind) {
			t.Fatalf("resource kind %q rejected", kind)
		}
	}
	if validClientResourceKind("unknown") {
		t.Fatal("unknown resource kind accepted")
	}
	if err := validateStoredClientResource("user", "presets", "id", validClientPresetResource()); err != nil {
		t.Fatalf("valid stored resource = %v", err)
	}
	invalidStored := validClientPresetResource()
	invalidStored.GetPreset().SetName(string([]byte{0xff}))
	if err := validateStoredClientResource("user", "presets", "id", invalidStored); !errors.Is(err, ErrCorruptResource) {
		t.Fatalf("invalid stored ProtoJSON payload = %v", err)
	}
	for _, resource := range []*clientpb.Resource{nil, {}, identityMissing} {
		if err := validateStoredClientResource("user", "presets", "id", resource); !errors.Is(err, ErrCorruptResource) {
			t.Fatalf("invalid stored resource %v = %v", resource, err)
		}
	}
	if _, err := service.EventPage(t.Context(), principal, -1, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative event cursor = %v", err)
	}
	if _, err := service.EventPage(t.Context(), principal, 0, MaximumEventPageSize+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized event page = %v", err)
	}
}

func TestGeneratedProtoClientReleaseAndExecutionBoundaries(t *testing.T) {
	service, _ := newClientServiceHarness(t)
	if _, _, err := service.issueSession(clientTestUser(), clientTestTime); err != nil {
		t.Fatal(err)
	}
	service.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, _, err := service.issueSession(clientTestUser(), clientTestTime); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("issue session error = %v", err)
	}
	service.random = deterministicClientRandom

	request := &clientpb.LaunchTicketRequest{}
	if normalizeLaunchTicketRequest(nil) != nil || launchTicketRequestValid(nil) {
		t.Fatal("nil launch request accepted")
	}
	request.SetNonce(" nonce ")
	contextMessage := &clientpb.LaunchContext{}
	contextMessage.SetInstallationId(" install ")
	contextMessage.SetDeviceId(" device ")
	contextMessage.SetReleaseGeneration(1)
	contextMessage.SetClientVersion(" 1.0.0 ")
	contextMessage.SetArtifactSha256(strings.Repeat("A", 64))
	contextMessage.SetBrowserRevision(" 1234 ")
	contextMessage.SetBrowserArtifactSha256(strings.Repeat("B", 64))
	contextMessage.SetPlaywrightVersion(" 1.61.1 ")
	contextMessage.SetPlaywrightArtifactSha256(strings.Repeat("C", 64))
	request.SetContext(contextMessage)
	normalized := normalizeLaunchTicketRequest(request)
	if normalized.GetContext().GetInstallationId() != "install" || normalized.GetContext().GetArtifactSha256() != strings.Repeat("a", 64) {
		t.Fatalf("normalized launch request = %+v", normalized)
	}
	if launchTicketRequestValid(normalized) {
		t.Fatal("short launch nonce accepted")
	}
	emptyRequest := &clientpb.LaunchTicketRequest{}
	if normalizeLaunchTicketRequest(emptyRequest) == nil || launchTicketRequestValid(emptyRequest) {
		t.Fatal("launch request without context accepted")
	}
	normalized.SetNonce(strings.Repeat("n", 16))
	if !launchTicketRequestValid(normalized) {
		t.Fatal("valid launch request rejected")
	}
	for _, value := range []string{"", "bad", strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if validSHA256(value) {
			t.Fatalf("invalid SHA256 %q accepted", value)
		}
	}
	if !validSHA256(strings.Repeat("a", 64)) {
		t.Fatal("valid SHA256 rejected")
	}

	if err := service.ConfigureReleases(nil); err == nil {
		t.Fatal("empty client release map accepted")
	}
	if err := service.ConfigureBrowserReleases(nil); err == nil {
		t.Fatal("empty browser release map accepted")
	}
	if err := service.ConfigurePlaywrightReleases(nil); err == nil {
		t.Fatal("empty Playwright release map accepted")
	}
	if err := service.ConfigureLauncherReleases(nil); err == nil {
		t.Fatal("empty launcher release map accepted")
	}
	if err := service.ConfigureProbeReleases(nil); err == nil {
		t.Fatal("empty Probe release map accepted")
	}
	if err := service.ConfigureProbeReleases([]*releasepb.ProbeRelease{validProbeRelease(), validProbeRelease()}); err == nil {
		t.Fatal("duplicate Probe release accepted")
	}
	if _, err := service.CurrentLauncherRelease("stable", "linux", "arm64"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing launcher = %v", err)
	}
	if _, err := service.CurrentProbeRelease("beta"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Probe = %v", err)
	}

	configureService, configureRepository := newClientServiceHarness(t)
	if err := configureValidRuntime(configureService, validClientRelease()); err != nil {
		t.Fatal(err)
	}
	configureService.releaseGeneration.Store(1)
	validRequest := launchRequestForRelease()
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, validRequest); err != nil {
		t.Fatal(err)
	}
	if configureRepository.ticket.ID == "" {
		t.Fatal("launch ticket was not stored")
	}
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil launch ticket request = %v", err)
	}
	badDevice := proto.CloneOf(validRequest)
	badDevice.GetContext().SetDeviceId("other")
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, badDevice); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("foreign launch device = %v", err)
	}
	configureRepository.fail = "device"
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, validRequest); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("launch device lookup error = %v", err)
	}
	configureRepository.fail = ""
	stale := proto.CloneOf(validRequest)
	stale.GetContext().SetReleaseGeneration(2)
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, stale); !errors.Is(err, ErrStaleRelease) {
		t.Fatalf("stale launch generation = %v", err)
	}
	mismatchedRuntime := proto.CloneOf(validRequest)
	mismatchedRuntime.GetContext().SetArtifactSha256(strings.Repeat("f", 64))
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, mismatchedRuntime); !errors.Is(err, ErrStaleRelease) {
		t.Fatalf("mismatched launch runtime = %v", err)
	}
	configureService.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, validRequest); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("launch ticket secret error = %v", err)
	}
	calls := 0
	configureService.random = func(buffer []byte) (int, error) {
		calls++
		if calls == 2 {
			return 0, io.ErrUnexpectedEOF
		}
		return deterministicClientRandom(buffer)
	}
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, validRequest); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("launch ticket id error = %v", err)
	}
	configureService.random = deterministicClientRandom
	configureRepository.fail = "create-launch-ticket"
	if _, err := configureService.IssueLaunchTicket(t.Context(), ClientPrincipal{UserID: "user"}, validRequest); !errors.Is(err, errInjectedClient) {
		t.Fatalf("launch ticket repository error = %v", err)
	}
	configureRepository.fail = ""

	exchange := &clientpb.SessionExchangeRequest{}
	if _, err := configureService.ExchangeLaunchTicket(t.Context(), exchange); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid launch exchange = %v", err)
	}
	exchange.SetLaunchTicket("launch-ticket")
	exchange.SetClientNonce(strings.Repeat("c", 16))
	configureService.random = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	if _, err := configureService.ExchangeLaunchTicket(t.Context(), exchange); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("launch exchange secret error = %v", err)
	}
	configureService.random = deterministicClientRandom
	configureRepository.fail = "exchange-launch-ticket"
	if _, err := configureService.ExchangeLaunchTicket(t.Context(), exchange); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("launch exchange repository error = %v", err)
	}
	configureRepository.fail = "exchange-stale-release"
	if _, err := configureService.ExchangeLaunchTicket(t.Context(), exchange); !errors.Is(err, ErrStaleRelease) {
		t.Fatalf("launch exchange stale error = %v", err)
	}
	configureRepository.fail = ""
	if response, err := configureService.ExchangeLaunchTicket(t.Context(), exchange); err != nil || response.GetLaunch() == nil {
		t.Fatalf("launch exchange = %+v, %v", response, err)
	}
}

func TestGeneratedProtoReleaseValidationBoundaries(t *testing.T) {
	clientCases := []func(*releasepb.ClientRelease){
		func(value *releasepb.ClientRelease) { value.SetVersion("invalid") },
		func(value *releasepb.ClientRelease) { value.SetMinimumLauncherVersion("invalid") },
		func(value *releasepb.ClientRelease) { value.SetPlaywrightVersion("invalid") },
		func(value *releasepb.ClientRelease) { value.SetMinimumBrowserRevision("invalid") },
		func(value *releasepb.ClientRelease) { value.SetArtifact(nil) },
		func(value *releasepb.ClientRelease) { value.SetProbeBootstrapPublicKeys(nil) },
	}
	for _, mutate := range clientCases {
		candidate := proto.CloneOf(validClientRelease())
		mutate(candidate)
		if validateClientRelease(candidate) == nil {
			t.Fatalf("invalid client release accepted: %+v", candidate)
		}
	}
	if err := validateClientRelease(validClientRelease()); err != nil {
		t.Fatalf("valid client release = %v", err)
	}
	for _, keyring := range []map[string]string{
		nil,
		{" ": "-----BEGIN PUBLIC KEY-----"},
		{"primary": "missing marker"},
		{"primary": strings.Repeat("x", 8<<10+1)},
	} {
		if validateProbeBootstrapKeyring(keyring) == nil {
			t.Fatalf("invalid client keyring accepted: %q", keyring)
		}
	}
	if err := validateProbeBootstrapKeyring(map[string]string{"primary": "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"}); err != nil {
		t.Fatalf("valid client keyring = %v", err)
	}

	browserCases := []func(*releasepb.BrowserRelease){
		func(value *releasepb.BrowserRelease) { value.SetCompatiblePlaywrightVersions(nil) },
		func(value *releasepb.BrowserRelease) { value.SetCompatiblePlaywrightVersions([]string{"invalid"}) },
		func(value *releasepb.BrowserRelease) {
			value.SetCompatiblePlaywrightVersions([]string{"1.61.1", "1.61.1"})
		},
		func(value *releasepb.BrowserRelease) { value.SetArtifact(nil) },
	}
	for _, mutate := range browserCases {
		candidate := proto.CloneOf(validBrowserRelease())
		mutate(candidate)
		if validateBrowserRelease(candidate) == nil {
			t.Fatalf("invalid browser release accepted: %+v", candidate)
		}
	}
	if err := validateBrowserRelease(validBrowserRelease()); err != nil {
		t.Fatalf("valid browser release = %v", err)
	}

	probeCases := []func(*releasepb.ProbeRelease){
		func(value *releasepb.ProbeRelease) { value.SetChannel("beta") },
		func(value *releasepb.ProbeRelease) { value.SetVersion("invalid") },
		func(value *releasepb.ProbeRelease) { value.SetBrowserRevision("invalid") },
		func(value *releasepb.ProbeRelease) { value.SetImage("") },
		func(value *releasepb.ProbeRelease) { value.SetImage("https://registry.example.com/image") },
		func(value *releasepb.ProbeRelease) { value.SetImage("registry.example.com/image:tag") },
		func(value *releasepb.ProbeRelease) { value.SetImageDigest("bad") },
		func(value *releasepb.ProbeRelease) { value.SetImageDigest("sha256:" + strings.Repeat("a", 63)) },
	}
	for _, mutate := range probeCases {
		candidate := proto.CloneOf(validProbeRelease())
		mutate(candidate)
		if validateProbeRelease(candidate) == nil {
			t.Fatalf("invalid Probe release accepted: %+v", candidate)
		}
	}
	if err := validateProbeRelease(validProbeRelease()); err != nil {
		t.Fatalf("valid Probe release = %v", err)
	}

	artifact := validClientRelease().GetArtifact()
	artifactCases := []*releasepb.Artifact{
		nil,
		proto.CloneOf(artifact),
		proto.CloneOf(artifact),
		proto.CloneOf(artifact),
		proto.CloneOf(artifact),
	}
	artifactCases[1].SetUrl("http://registry.example.com/artifact.zip")
	artifactCases[2].SetUrl("https://user:pass@registry.example.com/artifact.zip")
	artifactCases[3].SetSha256("bad")
	artifactCases[4].SetExecutable("/absolute/path")
	for _, candidate := range artifactCases {
		if validateReleaseArtifact(candidate) == nil {
			t.Fatalf("invalid artifact accepted: %+v", candidate)
		}
	}
	if err := validateReleaseArtifact(artifact); err != nil {
		t.Fatalf("valid artifact = %v", err)
	}
	if canonicalNumericRevision("") || canonicalNumericRevision("0") || canonicalNumericRevision("01") || canonicalNumericRevision("1a") || !canonicalNumericRevision("1") {
		t.Fatal("numeric revision canonicalization mismatch")
	}
	if cloneClientRelease(nil) == nil || cloneBrowserRelease(nil) == nil || clonePlaywrightRelease(nil) == nil || cloneLauncherRelease(nil) == nil || cloneProbeRelease(nil) == nil {
		t.Fatal("nil release clone returned nil")
	}
}

func launchRequestForRelease() *clientpb.LaunchTicketRequest {
	request := &clientpb.LaunchTicketRequest{}
	request.SetNonce(strings.Repeat("n", 16))
	contextMessage := &clientpb.LaunchContext{}
	contextMessage.SetInstallationId("install")
	contextMessage.SetDeviceId("device")
	contextMessage.SetReleaseGeneration(1)
	contextMessage.SetClientVersion(validClientRelease().GetVersion())
	contextMessage.SetArtifactSha256(validClientRelease().GetArtifact().GetSha256())
	contextMessage.SetBrowserRevision(validBrowserRelease().GetRevision())
	contextMessage.SetBrowserArtifactSha256(validBrowserRelease().GetArtifact().GetSha256())
	contextMessage.SetPlaywrightVersion(validPlaywrightRelease().GetVersion())
	contextMessage.SetPlaywrightArtifactSha256(validPlaywrightRelease().GetArtifact().GetSha256())
	request.SetContext(contextMessage)
	return request
}

func TestGeneratedProtoProbeBootstrapBoundaries(t *testing.T) {
	service, repository := newClientServiceHarness(t)
	if err := configureValidRuntime(service, validClientRelease()); err != nil {
		t.Fatal(err)
	}
	service.releaseGeneration.Store(1)
	principal := ClientPrincipal{UserID: "user"}

	if _, err := service.AuthorizeProbeBootstrap(t.Context(), principal, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil Probe bootstrap request = %v", err)
	}
	request := validProbeBootstrapRequest()
	if _, err := service.AuthorizeProbeBootstrap(t.Context(), principal, request); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*clientpb.ProbeBootstrapTicketRequest){
		func(value *clientpb.ProbeBootstrapTicketRequest) { value.SetInstallationId("") },
		func(value *clientpb.ProbeBootstrapTicketRequest) { value.SetMaxConcurrency(2) },
		func(value *clientpb.ProbeBootstrapTicketRequest) { value.SetCapabilities(nil) },
	} {
		candidate := proto.CloneOf(request)
		mutate(candidate)
		if _, err := service.AuthorizeProbeBootstrap(t.Context(), principal, candidate); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid Probe bootstrap request = %v", err)
		}
	}
	repository.fail = "device"
	if _, err := service.AuthorizeProbeBootstrap(t.Context(), principal, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Probe bootstrap device error = %v", err)
	}
	repository.fail = ""
	foreignRuntime := proto.CloneOf(request)
	foreignRuntime.GetRuntime().SetComponentVersion("2.0.0")
	if _, err := service.AuthorizeProbeBootstrap(t.Context(), principal, foreignRuntime); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Probe bootstrap runtime mismatch = %v", err)
	}

	for _, values := range [][]*observationpb.Capability{
		nil,
		{capabilityMessage("unknown")},
		{capabilityMessage(probedomain.CapabilityCGVCatalogCapture)},
		{capabilityMessage(probedomain.CapabilityCGVScheduleCapture), capabilityMessage(probedomain.CapabilityCGVScheduleCapture)},
	} {
		if validClientProbeCapabilities(values) {
			t.Fatalf("invalid Probe capability set accepted: %+v", values)
		}
	}
	if !validClientProbeCapabilities([]*observationpb.Capability{capabilityMessage(probedomain.CapabilityCGVScheduleCapture)}) {
		t.Fatal("valid Probe capability set rejected")
	}
	if !validClientProbeCapabilities([]*observationpb.Capability{
		capabilityMessage(probedomain.CapabilityCGVScheduleCapture),
		capabilityMessage(probedomain.CapabilityCGVCatalogCapture),
		capabilityMessage(probedomain.CapabilityCGVSeatMapCapture),
		capabilityMessage(probedomain.CapabilityCGVSeatAvailabilityCapture),
	}) {
		t.Fatal("four-capability Probe set rejected")
	}
	if capabilityName(nil) != "" || capabilityName(&observationpb.Capability{}) != "" {
		t.Fatal("empty capability name accepted")
	}
	for _, capability := range []string{probedomain.CapabilityCGVScheduleCapture, probedomain.CapabilityCGVCatalogCapture, probedomain.CapabilityCGVSeatMapCapture, probedomain.CapabilityCGVSeatAvailabilityCapture} {
		if capabilityName(capabilityMessage(capability)) != capability {
			t.Fatalf("capability name = %q", capabilityName(capabilityMessage(capability)))
		}
	}
	if runtimeMatchesRelease(nil, nil) || runtimeMatchesRelease(request.GetRuntime(), nil) {
		t.Fatal("nil runtime/release matched")
	}
	matching := validClientReleaseRuntime()
	if !runtimeMatchesRelease(matching, matchingRelease()) {
		t.Fatal("matching runtime/release rejected")
	}
	mismatch := proto.CloneOf(matching)
	mismatch.SetArchitecture("other")
	if runtimeMatchesRelease(mismatch, matchingRelease()) {
		t.Fatal("mismatched runtime/release accepted")
	}
}

func TestGeneratedProtoCatalogAndExecutionBoundaries(t *testing.T) {
	now := clientTestTime
	service, err := NewCatalogService(&catalogRepositoryFake{generation: 4})
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return now }
	if _, err := service.PutSnapshot(t.Context(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil catalog snapshot = %v", err)
	}
	invalidTimestamp := timestamppb.New(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))
	invalidSnapshot := validCatalogSnapshot(now)
	invalidSnapshot.SetObservedAt(invalidTimestamp)
	if _, err := service.PutSnapshot(t.Context(), invalidSnapshot); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid catalog timestamp = %v", err)
	}
	missingObserved := validCatalogSnapshot(now)
	missingObserved.SetObservedAt(nil)
	if _, err := service.PutSnapshot(t.Context(), missingObserved); err != nil {
		t.Fatalf("missing catalog timestamp normalization = %v", err)
	}
	seatRepository := &catalogRepositoryFake{seatMap: nil, requestError: errInjectedClient}
	seatService, err := NewCatalogService(seatRepository)
	if err != nil {
		t.Fatal(err)
	}
	seatService.clock = func() time.Time { return now }
	request := &servicepb.ResolveSeatMapRequest{}
	request.SetAuditoriumId("auditorium")
	if _, err := seatService.ResolveSeatMap(t.Context(), request); !errors.Is(err, errInjectedClient) {
		t.Fatalf("seat-map backfill error = %v", err)
	}
	if _, err := seatService.SeatMap(t.Context(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank seat-map id = %v", err)
	}
	if _, err := seatService.PutSeatMap(t.Context(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil seat map = %v", err)
	}
	for _, request := range []*executionpb.ResultRequest{nil, {}, resultRequestCompleted(), resultRequestFailed(), resultRequestRetry()} {
		status, reason := executionResult(request)
		switch {
		case request == nil && (status != "" || reason != ""):
			t.Fatalf("nil execution result = %q/%q", status, reason)
		case request != nil && request.GetCompleted() != nil && status != "completed":
			t.Fatalf("completed execution result = %q/%q", status, reason)
		case request != nil && request.GetFailed() != nil && (status != "failed" || reason != "failed"):
			t.Fatalf("failed execution result = %q/%q", status, reason)
		case request != nil && request.GetRetryRequested() != nil && (status != "retry_requested" || reason != "retry"):
			t.Fatalf("retry execution result = %q/%q", status, reason)
		case request != nil && request.GetCompleted() == nil && request.GetFailed() == nil && request.GetRetryRequested() == nil && (status != "" || reason != ""):
			t.Fatalf("empty execution result = %q/%q", status, reason)
		}
	}
}

func resultRequestCompleted() *executionpb.ResultRequest {
	request := &executionpb.ResultRequest{}
	request.SetCompleted(&executionpb.Completed{})
	return request
}

func resultRequestFailed() *executionpb.ResultRequest {
	failed := &executionpb.Failed{}
	failed.SetReasonCode(" failed ")
	request := &executionpb.ResultRequest{}
	request.SetFailed(failed)
	return request
}

func resultRequestRetry() *executionpb.ResultRequest {
	retry := &executionpb.RetryRequested{}
	retry.SetReasonCode(" retry ")
	request := &executionpb.ResultRequest{}
	request.SetRetryRequested(retry)
	return request
}

func validProbeBootstrapRequest() *clientpb.ProbeBootstrapTicketRequest {
	request := &clientpb.ProbeBootstrapTicketRequest{}
	request.SetInstallationId("install")
	request.SetDeviceId("device")
	request.SetCapabilities([]*observationpb.Capability{capabilityMessage(probedomain.CapabilityCGVScheduleCapture)})
	request.SetMaxConcurrency(1)
	request.SetRuntime(validClientReleaseRuntime())
	return request
}

func capabilityMessage(name string) *observationpb.Capability {
	capability := &observationpb.Capability{}
	switch name {
	case probedomain.CapabilityCGVScheduleCapture:
		capability.SetScheduleCapture(&observationpb.ScheduleCapture{})
	case probedomain.CapabilityCGVCatalogCapture:
		capability.SetCatalogCapture(&observationpb.CatalogCapture{})
	case probedomain.CapabilityCGVSeatMapCapture:
		capability.SetSeatMapCapture(&observationpb.SeatMapCapture{})
	case probedomain.CapabilityCGVSeatAvailabilityCapture:
		capability.SetSeatAvailabilityCapture(&observationpb.SeatAvailabilityCapture{})
	}
	return capability
}

func validClientReleaseRuntime() *commonpb.Runtime {
	runtime := &commonpb.Runtime{}
	runtime.SetComponentVersion(validClientRelease().GetVersion())
	runtime.SetBrowserRevision(validBrowserRelease().GetRevision())
	runtime.SetPlatform(validClientRelease().GetPlatform())
	runtime.SetArchitecture(validClientRelease().GetArchitecture())
	return runtime
}

func matchingRelease() *releasepb.RuntimeRelease {
	client := validClientRelease()
	browser := validBrowserRelease()
	runtime := &releasepb.RuntimeRelease{}
	runtime.SetClient(client)
	runtime.SetBrowser(browser)
	return runtime
}
