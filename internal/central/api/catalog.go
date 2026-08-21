package api

import (
	"context"
	"net/http"
	"strconv"

	probedomain "github.com/cineko-org/central/internal/domain/probe"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/gen/go/cineko/catalog"
	servicepb "github.com/cineko-org/contracts/gen/go/cineko/service"
	"google.golang.org/protobuf/proto"
)

const clientInstallationHeader = "X-Cineko-Installation-Id"
const catalogGenerationHeader = "X-Cineko-Catalog-Generation"

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
	writeProtoCall(server, writer, request, http.StatusOK, server.loadAdminCatalogRefresh)
}

func (server *Server) loadAdminCatalogRefresh(ctx context.Context) (*adminpb.GetCatalogRefreshStatusResponse, error) {
	status, err := server.catalog.RefreshStatus(ctx)
	response := &adminpb.GetCatalogRefreshStatusResponse{}
	response.SetStatus(status)
	return response, err
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
	response := &adminpb.RequestCatalogRefreshResponse{}
	response.SetStatus(status)
	server.writeProtoJSON(writer, http.StatusAccepted, response)
}

func (server *Server) writeCatalog(writer http.ResponseWriter, request *http.Request) {
	catalog, err := server.catalog.Catalog(request.Context())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set(catalogGenerationHeader, strconv.FormatInt(catalog.GetGeneration(), 10))
	server.writeProtoJSON(writer, http.StatusOK, catalog)
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
		probedomain.CapabilityCGVCatalogCapture,
	); err != nil {
		server.writeError(writer, request, err)
		return
	}
	snapshot := &catalogpb.CatalogSnapshot{}
	if !server.decodeProtoJSON(writer, request, snapshot) {
		return
	}
	generation, err := server.catalog.PutSnapshot(request.Context(), snapshot)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set(catalogGenerationHeader, strconv.FormatInt(generation, 10))
	response := &servicepb.SubmitCatalogSnapshotResponse{}
	response.SetGeneration(generation)
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) getClientSeatMapVersion(writer http.ResponseWriter, request *http.Request) {
	server.writeClientSeatMap(writer, request, func(ctx context.Context, auditoriumID string) (proto.Message, error) {
		return server.catalog.SeatMap(ctx, auditoriumID)
	})
}

func (server *Server) resolveClientSeatMap(writer http.ResponseWriter, request *http.Request) {
	server.writeClientSeatMap(writer, request, func(ctx context.Context, auditoriumID string) (proto.Message, error) {
		return server.catalog.ResolveSeatMap(ctx, auditoriumID)
	})
}

func (server *Server) writeClientSeatMap(
	writer http.ResponseWriter,
	request *http.Request,
	load func(context.Context, string) (proto.Message, error),
) {
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	value, err := load(request.Context(), request.PathValue("auditoriumId"))
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, value)
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
