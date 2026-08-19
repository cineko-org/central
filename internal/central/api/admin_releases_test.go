package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
)

func TestAdminReleasesRequiresCookieAdminAuthentication(t *testing.T) {
	server, _ := newAdminReleaseServer(t, &apiClientRepository{})

	unauthenticated := request(t, server.Handler(), http.MethodGet, "/v1/admin/releases", nil, nil)
	assertAPIError(t, unauthenticated, http.StatusUnauthorized, "unauthorized")

	bearerOnly := request(t, server.Handler(), http.MethodGet, "/v1/admin/releases", nil, map[string]string{
		"Authorization": "Bearer session-token",
	})
	assertAPIError(t, bearerOnly, http.StatusUnauthorized, "unauthorized")
}

func TestAdminReleasesReturnsDatabaseRegistryAndEmptyComponents(t *testing.T) {
	repository := &apiClientRepository{}
	server, clients := newAdminReleaseServer(t, repository)
	for _, publication := range []struct {
		component string
		releases  any
	}{
		{component: "launcher", releases: apiLauncherReleaseSet().Payload.Releases},
		{component: "client", releases: apiClientReleaseSet().Payload.Releases},
		{component: "browser", releases: apiBrowserReleaseSet().Payload.Releases},
		{component: "playwright", releases: apiPlaywrightReleaseSet().Payload.Releases},
		{component: "probe", releases: apiProbeReleaseSet().Payload.Releases},
	} {
		if _, inserted, err := clients.PublishReleaseSet(t.Context(), publication.component, publication.releases); err != nil || !inserted {
			t.Fatalf("publish %s: inserted=%t, error=%v", publication.component, inserted, err)
		}
	}
	cookie := loginAdminForReleaseRegistry(t, server)

	response := requestWithCookie(t, server, http.MethodGet, "/v1/admin/releases", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("admin releases = %d, %s", response.Code, response.Body.String())
	}
	var registry central.ReleaseRegistry
	if err := json.Unmarshal(response.Body.Bytes(), &registry); err != nil {
		t.Fatal(err)
	}
	if registry.Generation != 2 || len(registry.Components.Launcher) != 3 ||
		len(registry.Components.Client) != 3 || len(registry.Components.Browser) != 3 ||
		len(registry.Components.Playwright) != 3 || len(registry.Components.Probe) != 1 {
		t.Fatalf("admin release registry = %+v", registry)
	}

	// The admin view reads the repository for each request rather than reflecting
	// only the ClientService's last in-memory generation.
	repository.generation = 17
	fresh := requestWithCookie(t, server, http.MethodGet, "/v1/admin/releases", cookie)
	var refreshed central.ReleaseRegistry
	if err := json.Unmarshal(fresh.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.Generation != 17 || clients.ReleaseGeneration() != 2 {
		t.Fatalf("fresh registry generation = %d, cached generation = %d", refreshed.Generation, clients.ReleaseGeneration())
	}

	emptyServer, _ := newAdminReleaseServer(t, &apiClientRepository{generation: 6})
	emptyCookie := loginAdminForReleaseRegistry(t, emptyServer)
	empty := requestWithCookie(t, emptyServer, http.MethodGet, "/v1/admin/releases", emptyCookie)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty admin releases = %d, %s", empty.Code, empty.Body.String())
	}
	var emptyRegistry central.ReleaseRegistry
	if err := json.Unmarshal(empty.Body.Bytes(), &emptyRegistry); err != nil {
		t.Fatal(err)
	}
	if emptyRegistry.Generation != 6 || emptyRegistry.Components.Launcher == nil ||
		emptyRegistry.Components.Client == nil || emptyRegistry.Components.Browser == nil ||
		emptyRegistry.Components.Playwright == nil || emptyRegistry.Components.Probe == nil {
		t.Fatalf("empty admin release registry = %+v", emptyRegistry)
	}
}

func newAdminReleaseServer(
	t *testing.T,
	repository *apiClientRepository,
) (*Server, *central.ClientService) {
	t.Helper()
	service, err := central.NewService(&apiRepository{}, central.Config{EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	clients, err := central.NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewAdminAuth([]AdminCredential{{
		UserID: "admin", DisplayName: "Admin", Password: adminTestPassword,
	}}, adminTestPepper, newAdminSessionRepository(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	server, err := New(service, WithClientService(clients), WithAdminAuth(auth))
	if err != nil {
		t.Fatal(err)
	}
	return server, clients
}

func loginAdminForReleaseRegistry(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	login := request(t, server.Handler(), http.MethodPost, "/v1/admin/login", map[string]string{
		"userId": "admin", "password": adminTestPassword,
	}, nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("admin login = %d, cookies=%v", login.Code, login.Result().Cookies())
	}
	return login.Result().Cookies()[0]
}
