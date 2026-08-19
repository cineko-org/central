package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cineko-org/central/internal/central"
)

type configurationWriteRequest struct {
	Resources []central.ConfigurationResource `json:"resources"`
}

const maximumConfigurationBody = 32 << 20

func (server *Server) getClientConfiguration(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	snapshot, err := server.clients.SnapshotConfiguration(request.Context(), principal)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set("ETag", fmt.Sprintf("\"%d\"", snapshot.Revision))
	server.writeJSON(writer, http.StatusOK, snapshot)
}

func (server *Server) putClientConfiguration(writer http.ResponseWriter, request *http.Request) {
	if !server.requireIdempotencyKey(writer, request) {
		return
	}
	principal, ok := server.authenticatedClient(writer, request)
	if !ok {
		return
	}
	expected, valid := configurationRevision(writer, request)
	if !valid {
		return
	}
	var input configurationWriteRequest
	if !decodeConfigurationJSON(writer, request, &input) {
		return
	}
	revision, err := server.clients.ReplaceConfiguration(
		request.Context(), principal, expected, input.Resources, request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set("ETag", fmt.Sprintf("\"%d\"", revision))
	server.writeJSON(writer, http.StatusOK, map[string]int64{"revision": revision})
}

func decodeConfigurationJSON(writer http.ResponseWriter, request *http.Request, output *configurationWriteRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maximumConfigurationBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		writeInvalidConfiguration(writer, "configuration body is invalid or too large")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeInvalidConfiguration(writer, "configuration body contains trailing data")
		return false
	}
	return true
}

func writeInvalidConfiguration(writer http.ResponseWriter, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
		"code": "invalid_configuration", "message": message, "retryable": false,
	}})
}

func configurationRevision(writer http.ResponseWriter, request *http.Request) (int64, bool) {
	value := request.Header.Get("If-Match")
	if value == "" || request.Header.Get("If-None-Match") != "" {
		writeInvalidConfigurationRevision(writer)
		return 0, false
	}
	value = strings.Trim(value, `"`)
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 0 {
		writeInvalidConfigurationRevision(writer)
		return 0, false
	}
	return revision, true
}

func writeInvalidConfigurationRevision(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
		"code": "invalid_revision", "message": "If-Match must contain the observed configuration revision", "retryable": false,
	}})
}
