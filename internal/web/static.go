package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/lucawalz/horizon/internal/web/site"
)

const (
	shellFile        = "index.html"
	assetPrefix      = "/assets/"
	htmlContentType  = "text/html; charset=utf-8"
	interfaceAbsent  = "this build carries no interface"
	shellUnavailable = "the interface shell could not be read"
	assetAbsent      = "the bundle holds no such asset"
)

func absentInterface() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, interfaceAbsent, http.StatusNotFound)
	})
}

// asset names are content hashed, so a miss is a stale reference and must fail rather than answer with the shell or a listing of the bundle
func bundleFiles(bundle fs.FS) http.Handler {
	if bundle == nil {
		return absentInterface()
	}
	files := http.FileServerFS(bundle)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bundled(bundle, r.URL.Path) {
			http.Error(w, assetAbsent, http.StatusNotFound)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func siteHandler(bundle fs.FS) http.Handler {
	if bundle == nil {
		return absentInterface()
	}

	files := bundleFiles(bundle)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bundled(bundle, r.URL.Path) {
			files.ServeHTTP(w, r)
			return
		}
		serveShell(w, bundle)
	})
}

func bundled(bundle fs.FS, requested string) bool {
	name := strings.TrimPrefix(path.Clean(requested), "/")
	if !fs.ValidPath(name) {
		return false
	}
	info, err := fs.Stat(bundle, name)
	return err == nil && !info.IsDir()
}

// a deep link is routed inside the bundle, so an unbundled path answers with the shell rather than a 404
func serveShell(w http.ResponseWriter, bundle fs.FS) {
	shell, err := fs.ReadFile(bundle, shellFile)
	if err != nil {
		http.Error(w, shellUnavailable, http.StatusInternalServerError)
		return
	}
	w.Header().Set(contentTypeHeader, htmlContentType)
	w.Header().Set(cacheControlHeader, noStore)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(shell)
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, http.StatusNotFound, "no interface endpoint exists at "+r.URL.Path)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/leases", s.leaseList)
	mux.HandleFunc("GET /api/leases/{name}", s.leaseDetail)
	mux.HandleFunc("GET /api/machines", s.machines)
	mux.HandleFunc("GET /api/", apiNotFound)
	mux.Handle("GET "+assetPrefix, bundleFiles(site.DistDirFS))
	mux.Handle("GET /", siteHandler(site.DistDirFS))
	return mux
}
