package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
)

func (server *Server) exchangeClientPIN(writer http.ResponseWriter, request *http.Request) {
	if !server.requirePINService(writer, request) {
		return
	}
	input := &clientpb.PinExchangeRequest{}
	if !server.decodeProtoJSON(writer, request, input) {
		return
	}
	response, err := server.pins.Exchange(request.Context(), input, server.clientAddress(request))
	if err != nil {
		server.writeError(writer, request, err)
		return
	}
	server.writeProtoJSON(writer, http.StatusOK, response)
}

func (server *Server) requirePINService(writer http.ResponseWriter, request *http.Request) bool {
	if server.pins != nil {
		return true
	}
	server.writeAPIError(
		writer, request, http.StatusServiceUnavailable, "pin_auth_unavailable", "PIN authentication is unavailable", true,
	)
	return false
}

type trustedProxySet struct {
	networks []*net.IPNet
}

func parseTrustedProxyCIDRs(value string) (trustedProxySet, error) {
	set := trustedProxySet{}
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return trustedProxySet{}, fmt.Errorf("invalid trusted proxy CIDR %q", raw)
		}
		set.networks = append(set.networks, network)
	}
	return set, nil
}

func (set trustedProxySet) contains(ip net.IP) bool {
	for _, network := range set.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (server *Server) clientAddress(request *http.Request) string {
	peer := addressHost(request.RemoteAddr)
	parsedPeer := net.ParseIP(peer)
	if parsedPeer == nil || !server.trustedProxies.contains(parsedPeer) {
		return peer
	}

	forwardedHeader := strings.TrimSpace(request.Header.Get("X-Forwarded-For"))
	forwarded := strings.Split(forwardedHeader, ",")
	for index := len(forwarded) - 1; index >= 0; index-- {
		candidate := strings.TrimSpace(forwarded[index])
		address := net.ParseIP(candidate)
		if address == nil {
			if forwardedHeader != "" {
				return peer
			}
			break
		}
		if !server.trustedProxies.contains(address) {
			return candidate
		}
	}
	if forwardedHeader == "" {
		if realIP := strings.TrimSpace(request.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
			return realIP
		}
	}
	return peer
}

func addressHost(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return host
	}
	return strings.TrimSpace(value)
}
