package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cineko-org/central/internal/central"
	contracts "github.com/cineko-org/contracts/v3"
)

func (server *Server) getClientCatalog(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	server.writeCatalog(writer, request)
}

func (server *Server) getAdminCatalog(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	server.writeCatalog(writer, request)
}

func (server *Server) getAdminCatalogRefresh(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	status, err := server.catalog.RefreshStatus(request.Context())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, status)
}

func (server *Server) requestAdminCatalogRefresh(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	if err := server.catalog.RequestRefresh(request.Context()); err != nil {
		server.writeError(writer, request, err)
		return
	}
	status, err := server.catalog.RefreshStatus(request.Context())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusAccepted, status)
}

func (server *Server) writeCatalog(writer http.ResponseWriter, request *http.Request) {
	catalog, err := server.catalog.Catalog(request.Context())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set(contracts.CatalogGenerationHeader, strconv.FormatInt(catalog.Generation, 10))
	server.writeJSON(writer, http.StatusOK, catalog)
}

func (server *Server) putClientCatalogSnapshot(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	var snapshot contracts.CatalogSnapshot
	if !server.decodeJSON(writer, request, &snapshot) {
		return
	}
	generation, err := server.catalog.PutSnapshot(request.Context(), snapshot)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set(contracts.CatalogGenerationHeader, strconv.FormatInt(generation, 10))
	server.writeJSON(writer, http.StatusOK, map[string]int64{"generation": generation})
}

func (server *Server) putClientSeatMapVersion(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	var version contracts.SeatMapVersion
	if !server.decodeJSON(writer, request, &version) {
		return
	}
	versionID := strings.TrimSpace(request.PathValue("versionId"))
	if versionID == "" || strings.TrimSpace(version.ID) != versionID {
		server.writeError(writer, request, fmt.Errorf("%w: seat map version id does not match request path", central.ErrInvalid))
		return
	}
	generation, err := server.catalog.PutSeatMapVersion(request.Context(), version)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set(contracts.CatalogGenerationHeader, strconv.FormatInt(generation, 10))
	server.writeJSON(writer, http.StatusOK, map[string]int64{"generation": generation})
}

func (server *Server) getClientSeatMapVersion(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	version, err := server.catalog.SeatMapVersion(request.Context(), request.PathValue("auditoriumId"))
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, version)
}

func (server *Server) requestClientSeatMapBackfill(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	if err := server.catalog.RequestSeatMapBackfill(request.Context(), request.PathValue("auditoriumId")); err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusAccepted, map[string]string{"status": "waiting"})
}

func (server *Server) requireCatalog(writer http.ResponseWriter, request *http.Request) bool {
	if server.catalog != nil {
		return true
	}
	server.writeAPIError(
		writer, request, http.StatusServiceUnavailable,
		"catalog_unavailable", "catalog is unavailable", true,
	)
	return false
}
