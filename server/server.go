package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/JoshFarwig/kvstore/store"
)

func NewServer(KVStore *store.Store) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, KVStore)
	return mux
}

func addRoutes(mux *http.ServeMux, KVStore *store.Store) {
	mux.Handle("GET /kvstore/{key}", handleGetKV(KVStore))
	mux.Handle("PUT /kvstore/{key}", handlePutKV(KVStore))
	mux.Handle("DELETE /kvstore/{key}", handleDeleteKV(KVStore))
	mux.Handle("GET /vitals", handleGetVitals())
	mux.Handle("GET /metrics", handleGetMetrics())
	mux.Handle("GET /readyz", handleIsReady())
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

func handleGetVitals() http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			vitals, err := getVitals()
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
