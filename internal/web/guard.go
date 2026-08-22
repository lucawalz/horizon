package web

import (
	"net"
	"net/http"
	"slices"
)

const (
	interfaceHeader = "X-Horizon-Interface"
	fetchSiteHeader = "Sec-Fetch-Site"
	originHeader    = "Origin"
	sameOriginSite  = "same-origin"
	httpScheme      = "http://"

	hostMismatch      = "a mutating request must be addressed to the loopback address this interface is served from"
	headerMissing     = "a mutating request must carry the " + interfaceHeader + " header this interface sets on its own calls"
	fetchSiteMismatch = "a mutating request must carry " + fetchSiteHeader + ": " + sameOriginSite
	originMismatch    = "a mutating request must come from the origin this interface is served from"
	readOnlyInterface = "this process serves a read-only interface and holds no client that may write to the cluster"
)

// a name outside this set can be pointed at the loopback address by whoever owns its zone, which is the whole of a rebinding attack
var servedNames = []string{loopbackHost, "localhost"}

func (s *Server) servedOrigin(host string) (string, bool) {
	if s.port == "" {
		return "", false
	}
	if !slices.ContainsFunc(servedNames, func(name string) bool { return host == net.JoinHostPort(name, s.port) }) {
		return "", false
	}
	return httpScheme + host, true
}

// the interface binds loopback and authenticates as the caller, so any page in the same browser can reach it and a mutation has to prove it came from this interface
func (s *Server) crossOriginRefusal(r *http.Request) (string, bool) {
	served, addressed := s.servedOrigin(r.Host)
	if !addressed {
		return hostMismatch, true
	}
	if r.Header.Get(interfaceHeader) == "" {
		return headerMissing, true
	}
	// an absent Sec-Fetch-Site is a refusal rather than a pass, since that is the direction that fails safe
	if r.Header.Get(fetchSiteHeader) != sameOriginSite {
		return fetchSiteMismatch, true
	}
	if origin := r.Header.Get(originHeader); origin != "" && origin != served {
		return originMismatch, true
	}
	return "", false
}

func (s *Server) mutating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if refusal, refused := s.crossOriginRefusal(r); refused {
			writeAPIError(w, http.StatusForbidden, refusal)
			return
		}
		if s.writer == nil {
			writeAPIError(w, http.StatusNotImplemented, readOnlyInterface)
			return
		}
		next(w, r)
	}
}
