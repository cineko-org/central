package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"buf.build/go/protovalidate"
	probedomain "github.com/cineko-org/central/internal/domain/probe"
	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	servicepb "github.com/cineko-org/contracts/v3/gen/go/cineko/service"
	"google.golang.org/protobuf/encoding/protojson"
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
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	input := &servicepb.ResolveSeatMapRequest{}
	input.SetAuditoriumId(request.PathValue("auditoriumId"))
	response, err := server.catalog.ResolveSeatMap(request.Context(), input)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) watchClientSeatMap(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedClient(writer, request); !ok || !server.requireCatalog(writer, request) {
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		server.writeAPIError(writer, request, http.StatusInternalServerError, "stream_unavailable", "streaming is unavailable", true)
		return
	}
	// Unbuffered delivery guarantees the initial resolution reaches the HTTP
	// writer before a concurrent watcher failure can win the select.
	updates := make(chan *servicepb.WatchSeatMapResponse)
	errorsCh := make(chan error, 1)
	input := &servicepb.WatchSeatMapRequest{}
	input.SetAuditoriumId(request.PathValue("auditoriumId"))
	go func() {
		errorsCh <- server.catalog.WatchSeatMap(request.Context(), input, func(response *servicepb.WatchSeatMapResponse) error {
			select {
			case updates <- response:
				return nil
			case <-request.Context().Done():
				return request.Context().Err()
			}
		})
	}()
	stream := &seatMapSSEStream{
		server: server, writer: writer, request: request, flusher: flusher,
	}
	defer stream.stop()
	for {
		select {
		case response := <-updates:
			if !stream.writeResponse(response) {
				return
			}
		case <-stream.heartbeatC:
			if !stream.writeHeartbeat() {
				return
			}
		case err := <-errorsCh:
			stream.writeWatchError(err)
			return
		case <-request.Context().Done():
			return
		}
	}
}

type seatMapSSEStream struct {
	server     *Server
	writer     http.ResponseWriter
	request    *http.Request
	flusher    http.Flusher
	heartbeat  *time.Ticker
	heartbeatC <-chan time.Time
	started    bool
}

func (stream *seatMapSSEStream) stop() {
	if stream.heartbeat != nil {
		stream.heartbeat.Stop()
	}
}

func (stream *seatMapSSEStream) writeResponse(response *servicepb.WatchSeatMapResponse) bool {
	if err := protovalidate.Validate(response); err != nil {
		stream.writeErrorBeforeStart(err)
		return false
	}
	payload, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(response)
	if err != nil {
		stream.writeErrorBeforeStart(err)
		return false
	}
	stream.start()
	if _, err := fmt.Fprintf(stream.writer, "event: cineko.seat-map\ndata: %s\n\n", payload); err != nil {
		return false
	}
	stream.flusher.Flush()
	return true
}

func (stream *seatMapSSEStream) start() {
	if stream.started {
		return
	}
	stream.writer.Header().Set("Content-Type", "text/event-stream")
	stream.writer.Header().Set("Cache-Control", "no-cache")
	stream.writer.Header().Set("Connection", "keep-alive")
	stream.writer.WriteHeader(http.StatusOK)
	stream.started = true
	stream.heartbeat = time.NewTicker(stream.server.eventHeartbeat)
	stream.heartbeatC = stream.heartbeat.C
}

func (stream *seatMapSSEStream) writeHeartbeat() bool {
	if _, err := fmt.Fprint(stream.writer, ": heartbeat\n\n"); err != nil {
		return false
	}
	stream.flusher.Flush()
	return true
}

func (stream *seatMapSSEStream) writeWatchError(err error) {
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		stream.writeErrorBeforeStart(err)
	}
}

func (stream *seatMapSSEStream) writeErrorBeforeStart(err error) {
	if !stream.started {
		stream.server.writeError(stream.writer, stream.request, err)
	}
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
