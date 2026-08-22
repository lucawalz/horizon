package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	contentTypeHeader  = "Content-Type"
	cacheControlHeader = "Cache-Control"
	jsonContentType    = "application/json; charset=utf-8"
	noStore            = "no-store"
	encodeFailed       = "the response could not be encoded"
)

type apiError struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// the body is encoded in full before any header is written, so an encoding failure can still answer 500
func writeJSON(w http.ResponseWriter, status int, body any) {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(body); err != nil {
		slog.Error("encode the response", "error", err)
		http.Error(w, encodeFailed, http.StatusInternalServerError)
		return
	}

	w.Header().Set(contentTypeHeader, jsonContentType)
	w.Header().Set(cacheControlHeader, noStore)
	w.WriteHeader(status)
	_, _ = encoded.WriteTo(w)
}

func writeAPIError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, apiError{Status: status, Title: http.StatusText(status), Detail: detail})
}

func conditionStatus(conditions []metav1.Condition, name string) *metav1.ConditionStatus {
	for i := range conditions {
		if conditions[i].Type == name {
			status := conditions[i].Status
			return &status
		}
	}
	return nil
}

func rfc3339(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

func instant(at *metav1.Time) *string {
	if at.IsZero() {
		return nil
	}
	return ptr(rfc3339(at.Time))
}

func seconds(elapsed time.Duration) int64 {
	return int64(elapsed / time.Second)
}

func span(elapsed int64) time.Duration {
	return time.Duration(elapsed) * time.Second
}

func ptr[T any](value T) *T {
	return &value
}

func nullable[T ~string](value T) *T {
	if value == "" {
		return nil
	}
	return &value
}

func orEmpty[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
