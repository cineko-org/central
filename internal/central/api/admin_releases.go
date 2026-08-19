package api

import "net/http"

func (server *Server) adminReleases(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok ||
		!server.requireClientService(writer, request) {
		return
	}
	registry, err := server.clients.ReleaseRegistry(request.Context())
	if err != nil {
		server.writeAPIError(
			writer, request, http.StatusInternalServerError,
			"admin_releases_failed", "load releases failed", true,
		)
		return
	}
	server.writeJSON(writer, http.StatusOK, registry)
}
