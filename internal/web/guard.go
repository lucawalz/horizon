package web

import "net/http"

const (
	interfaceHeader = "X-Horizon-Interface"
	fetchSiteHeader = "Sec-Fetch-Site"
	originHeader    = "Origin"
	sameOriginSite  = "same-origin"
	httpScheme      = "http://"

	headerMissing     = "a mutating request must carry the " + interfaceHeader + " header this interface sets on its own calls"
	fetchSiteMismatch = "a mutating request must carry " + fetchSiteHeader + ": " + sameOriginSite
	originMismatch    = "a mutating request must come from the origin this interface is served from"
	readOnlyInterface = "this process serves a read-only interface and holds no client that may write to the cluster"
)

// the interface binds loopback and authenticates as the caller, so any page in the same browser can reach it and a mutation has to prove it came from this interface
func crossOriginRefusal(r *http.Request) (string, bool) {
	if r.Header.Get(interfaceHeader) == "" {
		return headerMissing, true
	}
	// an absent Sec-Fetch-Site is a refusal rather than a pass, since that is the direction that fails safe
	if r.Header.Get(fetchSiteHeader) != sameOriginSite {
		return fetchSiteMismatch, true
	}
	if origin := r.Header.Get(originHeader); origin != "" && origin != httpScheme+r.Host {
		return originMismatch, true
	}
	return "", false
}

func (s *Server) mutating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if refusal, refused := crossOriginRefusal(r); refused {
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
