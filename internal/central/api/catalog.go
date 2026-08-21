package api

import (
	"context"
	"net/http"
	"strconv"

	contracts "github.com/cineko-org/contracts/v3"
)

const clientInstallationHeader = "X-Cineko-Installation-Id"

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
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
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
	principal, ok := server.authenticatedClient(writer, request)
	if !ok || !server.requireCatalog(writer, request) {
		return
	}
	if err := server.catalog.AuthorizeClientWrite(
		request.Context(), principal, request.Header.Get(clientInstallationHeader),
		contracts.CapabilityCGVCatalogCapture,
	); err != nil {
		server.writeError(writer, request, err)
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

func (server *Server) getClientSeatMapVersion(writer http.ResponseWriter, request *http.Request) {
	server.writeClientSeatMap(writer, request, func(ctx context.Context, auditoriumID string) (any, error) {
		return server.catalog.SeatMapVersion(ctx, auditoriumID)
	})
}

func (server *Server) resolveClientSeatMap(writer http.ResponseWriter, request *http.Request) {
	server.writeClientSeatMap(writer, request, func(ctx context.Context, auditoriumID string) (any, error) {
		return server.catalog.ResolveSeatMap(ctx, auditoriumID)
	})
}

func (server *Server) writeClientSeatMap(
	writer http.ResponseWriter,
	request *http.Request,
	load func(context.Context, string) (any, error),
) {
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	value, err := load(request.Context(), request.PathValue("auditoriumId"))
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, value)
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
