package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strconv"

	"github.com/cineko-org/central/internal/central"
	contracts "github.com/cineko-org/contracts/v3"
)

func (server *Server) publishRelease(writer http.ResponseWriter, request *http.Request) {
	if !server.requireClientService(writer, request) || !server.requireReleasePublisher(writer, request) ||
		!server.requireProtocol(writer, request) {
		return
	}
	component := request.PathValue("component")
	switch component {
	case "client":
		publishTypedReleaseSet[central.ClientRelease](server, writer, request, component)
	case "browser":
		publishTypedReleaseSet[central.BrowserRelease](server, writer, request, component)
	case "playwright":
		publishTypedReleaseSet[central.PlaywrightRelease](server, writer, request, component)
	case "launcher":
		publishTypedReleaseSet[central.LauncherRelease](server, writer, request, component)
	case "probe":
		publishTypedReleaseSet[central.ProbeRelease](server, writer, request, component)
	default:
		server.writeError(writer, request, central.ErrNotFound)
	}
}

func publishTypedReleaseSet[Release any](
	server *Server,
	writer http.ResponseWriter,
	request *http.Request,
	component string,
) {
	input := contracts.ReleaseEnvelope[contracts.ReleaseSet[Release]]{}
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	if input.SchemaVersion != contracts.ReleasePayloadSchemaVersion {
		server.writeError(writer, request, central.ErrInvalid)
		return
	}
	generation, inserted, err := server.clients.PublishReleaseSet(request.Context(), component, input.Payload.Releases)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set(contracts.ReleaseGenerationHeader, strconv.FormatInt(generation, 10))
	status := http.StatusOK
	if inserted {
		status = http.StatusCreated
	}
	server.writeJSON(writer, status, map[string]int64{"generation": generation})
}

func (server *Server) requireReleasePublisher(writer http.ResponseWriter, request *http.Request) bool {
	if !server.releasePublishReady {
		server.writeAPIError(
			writer, request, http.StatusServiceUnavailable, "release_publisher_unavailable",
			"release publisher is unavailable", true,
		)
		return false
	}
	token := bearerToken(request)
	hash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(hash[:], server.releasePublishHash[:]) != 1 {
		server.writeError(writer, request, central.ErrUnauthorized)
		return false
	}
	return true
}
