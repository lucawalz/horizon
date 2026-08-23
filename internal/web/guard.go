package web

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
)

const (
	interfaceHeader = "X-Horizon-Interface"
	fetchSiteHeader = "Sec-Fetch-Site"
	originHeader    = "Origin"
	sameOriginSite  = "same-origin"
	httpName        = "http"
	httpsName       = "https"
	schemeMark      = "://"
	httpScheme      = httpName + schemeMark

	hostMismatch      = "a mutating request must be addressed to the origin this interface is served from"
	headerMissing     = "a mutating request must carry the " + interfaceHeader + " header this interface sets on its own calls"
	fetchSiteMismatch = "a mutating request must carry " + fetchSiteHeader + ": " + sameOriginSite
	originMismatch    = "a mutating request must come from the origin this interface is served from"
	readOnlyInterface = "this process serves a read-only interface and holds no client that may write to the cluster"
)

// a name outside this set can be pointed at the loopback address by whoever owns its zone, which is the whole of a rebinding attack
var servedNames = []string{loopbackHost, "localhost"}

var originSchemes = []string{httpName, httpsName}

type originAnchor struct {
	origin string
	host   string
}

func (a originAnchor) configured() bool { return a.origin != "" }

// the anchor has to be something the process knows apart from the request, so a proxied deployment states its origin rather than letting a header imply one
func parseExternalOrigin(value string) (originAnchor, error) {
	malformed := fmt.Errorf("%s must be an absolute origin such as https://horizon.example, not %q", externalOriginSetting, value)

	parsed, err := url.Parse(value)
	if err != nil {
		return originAnchor{}, malformed
	}
	if !slices.Contains(originSchemes, parsed.Scheme) || parsed.Host == "" || parsed.User != nil {
		return originAnchor{}, malformed
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return originAnchor{}, malformed
	}
	return originAnchor{origin: parsed.Scheme + schemeMark + parsed.Host, host: parsed.Host}, nil
}

func (s *Server) servedOrigin(host string) (string, bool) {
	if s.external.configured() {
		return s.external.origin, host == s.external.host
	}
	if s.port == "" {
		return "", false
	}
	if !slices.ContainsFunc(servedNames, func(name string) bool { return host == net.JoinHostPort(name, s.port) }) {
		return "", false
	}
	return httpScheme + host, true
}

// any page in the same browser can reach this interface, so a mutation has to prove it came from the origin this process was configured to serve
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
