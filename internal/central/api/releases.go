package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strconv"

	"github.com/cineko-org/central/internal/central"
	releasepb "github.com/cineko-org/contracts/gen/go/cineko/release"
	"google.golang.org/protobuf/proto"
)

func (server *Server) publishRelease(writer http.ResponseWriter, request *http.Request) {
	if !server.requireClientService(writer, request) || !server.requireReleasePublisher(writer, request) {
		return
	}
	component := request.PathValue("component")
	set, ok := releaseSet(component)
	if !ok {
		server.writeError(writer, request, central.ErrNotFound)
		return
	}
	if !server.decodeProtoJSON(writer, request, set) {
		return
	}
	generation, inserted, err := server.clients.PublishReleaseSet(request.Context(), component, set)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.Header().Set(releaseGenerationHeader, strconv.FormatInt(generation, 10))
	status := http.StatusOK
	if inserted {
		status = http.StatusCreated
	}
	writer.WriteHeader(status)
}

func releaseSet(component string) (proto.Message, bool) {
	switch component {
	case "client":
		return &releasepb.ClientReleaseSet{}, true
	case "browser":
		return &releasepb.BrowserReleaseSet{}, true
	case "playwright":
		return &releasepb.PlaywrightReleaseSet{}, true
	case "launcher":
		return &releasepb.LauncherReleaseSet{}, true
	case "probe":
		return &releasepb.ProbeReleaseSet{}, true
	default:
		return nil, false
	}
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
