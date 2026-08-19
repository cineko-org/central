package api

import (
	"net/http"
)

type createAdminUserRequest struct {
	DisplayName string `json:"displayName"`
}

func (server *Server) listAdminUsers(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requirePINService(writer, request) {
		return
	}
	users, err := server.pins.ListUsers(request.Context())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, map[string]any{"data": users})
}

func (server *Server) createAdminUser(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requirePINService(writer, request) {
		return
	}
	var input createAdminUserRequest
	if !server.decodeJSON(writer, request, &input) {
		return
	}
	issue, err := server.pins.CreateUser(request.Context(), input.DisplayName)
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusCreated, issue)
}

func (server *Server) rotateAdminUserPIN(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requirePINService(writer, request) {
		return
	}
	issue, err := server.pins.Rotate(request.Context(), request.PathValue("userId"))
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeJSON(writer, http.StatusOK, issue)
}

func (server *Server) deleteAdminUser(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requirePINService(writer, request) {
		return
	}
	if err := server.pins.DeleteUser(request.Context(), request.PathValue("userId")); err != nil {
		server.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
