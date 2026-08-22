package api

import (
	"context"
	"net/http"

	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
)

func (server *Server) listAdminUsers(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requirePINService(writer, request) {
		return
	}
	writeProtoCall(server, writer, request, http.StatusOK, server.loadAdminUsers)
}

func (server *Server) loadAdminUsers(ctx context.Context) (*adminpb.ListClientUsersResponse, error) {
	users, err := server.pins.ListUsers(ctx)
	response := &adminpb.ListClientUsersResponse{}
	response.SetUsers(users)
	return response, err
}

func (server *Server) createAdminUser(writer http.ResponseWriter, request *http.Request) {
	if !server.sameOriginAdminRequest(writer, request) {
		return
	}
	if _, ok := server.authenticatedAdmin(writer, request); !ok || !server.requirePINService(writer, request) {
		return
	}
	input := &adminpb.CreateClientUserRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	issue, err := server.pins.CreateUser(request.Context(), input.GetDisplayName())
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	response := &adminpb.CreateClientUserResponse{}
	response.SetIssue(issue)
	server.writeProtoJSON(writer, http.StatusCreated, response)
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
	response := &adminpb.RotateClientPinResponse{}
	response.SetIssue(issue)
	server.writeProtoJSON(writer, http.StatusOK, response)
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
