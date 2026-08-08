package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/JoshFarwig/kvstore/store"
)

func NewServer(KVStore *store.Store) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, KVStore)
	return mux
}

func addRoutes(mux *http.ServeMux, KVStore *store.Store) {
	mux.Handle("GET /kvstore/{key}", denyReservedPrefix(handleGetKV(KVStore)))
	mux.Handle("PUT /kvstore/{key}", handlePutKV(KVStore))
	mux.Handle("DELETE /kvstore/{key}", denyReservedPrefix(handleDeleteKV(KVStore)))
	mux.Handle("GET /vitals", handleGetLocalVitals())
	mux.Handle("GET /vitals/{nodeID}", handleGetVitals(KVStore))
	// NOTE: /throttled is intended to be global by nature and utilized by leader
	mux.Handle("GET /throttled", handleGetThrottledNodes(KVStore))
	mux.Handle("GET /threshold/{nodeID}", handleGetThreshold(KVStore))
	mux.Handle("PUT /threshold/{nodeID}", handlePutThreshold(KVStore))
	mux.Handle("DELETE /threshold/{nodeID}", handleDeleteThreshold(KVStore))
	// mux.Handle("GET /metrics", handleGetMetrics())
	// mux.Handle("GET /readyz", handleIsReady())
	mux.Handle("GET /healthz", handleIsHealthy())
}

func handleGetKV(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			key := r.PathValue("key")
			item, err := KVStore.Get(key)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				slog.Warn("store could not get value for key", "key", key, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			if err := writeJSON(w, http.StatusOK, item); err != nil {
				slog.Warn("could not encode response body", "item", item, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		},
	)
}

func handlePutKV(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			key := r.PathValue("key")
			var requestItem store.Item
			if err := readJSON(r, &requestItem); err != nil {
				slog.Warn("could not decode request body", "key", key, "err", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			KVStore.Set(key, requestItem.Value, requestItem.ExpiresAt)
			w.WriteHeader(http.StatusNoContent)
		},
	)
}

func handleDeleteKV(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			key := r.PathValue("key")
			KVStore.Delete(key)
			w.WriteHeader(http.StatusNoContent)
		},
	)
}

// handleGetLocalVitals() pulls directly from process to show exact
// intime snapshot of cpu and mem % usage.
func handleGetLocalVitals() http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			vitals, err := SampleVitals()
			if err != nil {
				slog.Warn("could not retrieve node vitals", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if err := writeJSON(w, http.StatusOK, vitals); err != nil {
				slog.Warn("could not encode response body", "item", vitals, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		},
	)
}

// handleGetVitals() pulls from KVstore rather rather direct in process,
// values derived from heartbeat ticker
func handleGetVitals(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			nodeID := r.PathValue("nodeID")
			vitals, err := KVStore.Get(vitalsKey + nodeID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				slog.Warn("could not retrieve node vitals", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if err := writeJSON(w, http.StatusOK, vitals); err != nil {
				slog.Warn("could not encode response body", "item", vitals, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		},
	)
}

func handleGetThreshold(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			nodeID := r.PathValue("nodeID")
			item, err := KVStore.Get(throttleThresholdKey + nodeID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				slog.Warn("could not retrieve throttle threshold", "nodeID", nodeID, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if err := writeJSON(w, http.StatusOK, item); err != nil {
				slog.Warn("could not encode response body", "item", item, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		},
	)
}

func handlePutThreshold(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			nodeID := r.PathValue("nodeID")
			var tt ThrottleThreshold
			if err := readJSON(r, &tt); err != nil {
				slog.Warn("could not decode request body", "nodeID", nodeID, "err", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := SetThrottleThreshold(KVStore, nodeID, tt); err != nil {
				slog.Warn("invalid throttle threshold", "nodeID", nodeID, "err", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)
}

func handleDeleteThreshold(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			nodeID := r.PathValue("nodeID")
			KVStore.Delete(throttleThresholdKey + nodeID)
			w.WriteHeader(http.StatusNoContent)
		},
	)
}

func handleGetThrottledNodes(KVStore *store.Store) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			tn := GetThrottledNodes(KVStore)
			if err := writeJSON(w, http.StatusOK, tn); err != nil {
				slog.Warn("could not encode response body", "item", tn, "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		},
	)
}

func handleGetMetrics() http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// TODO: use k6 / prometheusus metrics
		},
	)
}

func handleIsHealthy() http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
}

func handleIsReady() http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// TODO: verify node state is not shutting down and leader is elected, impl 4 raft
		},
	)
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// middlewares
var prefixes = []string{"kvs:"}

func denyReservedPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		for i := range prefixes {
			if strings.HasPrefix(key, prefixes[i]) {
				slog.Warn("forbidden operation on reserved prefix", "method", r.Method, "key", key, "reservedPrefix", prefixes[i])
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		}
	})
}
