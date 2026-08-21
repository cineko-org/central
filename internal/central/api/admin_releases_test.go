package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/central"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
		releases  proto.Message
	}{
		{component: "launcher", releases: apiLauncherReleaseSet()},
		{component: "client", releases: apiClientReleaseSet()},
		{component: "browser", releases: apiBrowserReleaseSet()},
		{component: "playwright", releases: apiPlaywrightReleaseSet()},
		{component: "probe", releases: apiProbeReleaseSet()},
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
	registry := &releasepb.Registry{}
	if err := protojson.Unmarshal(response.Body.Bytes(), registry); err != nil {
		t.Fatal(err)
	}
	if registry.GetGeneration() != 2 || len(registry.GetLaunchers().GetReleases()) != 3 ||
		len(registry.GetClients().GetReleases()) != 3 || len(registry.GetBrowsers().GetReleases()) != 3 ||
		len(registry.GetPlaywright().GetReleases()) != 3 || len(registry.GetProbes().GetReleases()) != 1 {
		t.Fatalf("admin release registry = %+v", registry)
	}

	// The admin view reads the repository for each request rather than reflecting
	// only the ClientService's last in-memory generation.
	repository.generation = 17
	fresh := requestWithCookie(t, server, http.MethodGet, "/v1/admin/releases", cookie)
	refreshed := &releasepb.Registry{}
	if err := protojson.Unmarshal(fresh.Body.Bytes(), refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.GetGeneration() != 17 || clients.ReleaseGeneration() != 2 {
		t.Fatalf("fresh registry generation = %d, cached generation = %d", refreshed.GetGeneration(), clients.ReleaseGeneration())
	}

	emptyServer, _ := newAdminReleaseServer(t, &apiClientRepository{generation: 6})
	emptyCookie := loginAdminForReleaseRegistry(t, emptyServer)
	empty := requestWithCookie(t, emptyServer, http.MethodGet, "/v1/admin/releases", emptyCookie)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty admin releases = %d, %s", empty.Code, empty.Body.String())
	}
	emptyRegistry := &releasepb.Registry{}
	if err := protojson.Unmarshal(empty.Body.Bytes(), emptyRegistry); err != nil {
		t.Fatal(err)
	}
	if emptyRegistry.GetGeneration() != 6 || !emptyRegistry.HasLaunchers() ||
		!emptyRegistry.HasClients() || !emptyRegistry.HasBrowsers() ||
		!emptyRegistry.HasPlaywright() || !emptyRegistry.HasProbes() {
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
