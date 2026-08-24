package web

import (
	"log/slog"
	"net/http"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const namespaceReadFailed = "the namespaces could not be read from the cluster"

type namespaceListResponse struct {
	Namespaces []string `json:"namespaces"`
	ObservedAt string   `json:"observedAt"`
}

func namespaceNames(namespaces []corev1.Namespace) []string {
	names := make([]string, 0, len(namespaces))
	for i := range namespaces {
		names = append(names, namespaces[i].Name)
	}
	slices.Sort(names)
	return names
}

func (s *Server) namespaces(w http.ResponseWriter, r *http.Request) {
	reader, held := requestClient(w, r, s.readers)
	if !held {
		return
	}

	var namespaces corev1.NamespaceList
	if err := reader.List(r.Context(), &namespaces); err != nil {
		if refusedByAuthorisation(w, r, err) {
			return
		}
		slog.Error("list the namespaces", "error", err)
		writeAPIError(w, http.StatusBadGateway, namespaceReadFailed)
		return
	}

	writeJSON(w, http.StatusOK, namespaceListResponse{
		Namespaces: namespaceNames(namespaces.Items),
		ObservedAt: rfc3339(time.Now()),
	})
}
