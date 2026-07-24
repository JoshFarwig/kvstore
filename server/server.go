package server

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/JoshFarwig/kvstore/store"
)

type SetRequest struct {
	Value      json.RawMessage `json:"value"`
	TTLSeconds int             `json:"ttl_seconds"`
}

func NewServer(s *store.Store) {
	logger := initLogger()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("root request", "user_agent", r.UserAgent())
	})

	http.HandleFunc("GET /kvstore/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		var item store.Item

		logger.Info("GET request for store", "user_agent", r.UserAgent(), "key", key)

		item, err := s.Get(key)
		if err != nil {
			logger.Warn("store could not get value for key", "key", key, "err", err)
			writeJSON(w, http.StatusNotFound, nil)
			return
		}

		writeJSON(w, http.StatusOK, item)
	})

	http.HandleFunc("POST /kvstore/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		var req SetRequest
		if err := readJSON(r, &req); err != nil {
			logger.Warn("could not decode request body", "key", key, "err", err)
			writeJSON(w, http.StatusBadRequest, nil)
			return
		}
		logger.Info("POST request for store", "user_agent", r.UserAgent(), "key", key)

		var expiresAt time.Time
		if req.TTLSeconds > 0 {
			expiresAt = time.Now().UTC().Add(time.Duration(req.TTLSeconds) * time.Second)
		}

		if err := s.Set(key, req.Value, expiresAt); err != nil {
			logger.Warn("store could not set value for key", "key", key, "err", err)
			writeJSON(w, http.StatusInternalServerError, nil)
			return
		}

		writeJSON(w, http.StatusOK, nil)
	})

	http.HandleFunc("DELETE /kvstore/{id}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		logger.Info("DELETE request for store", "user_agent", r.UserAgent(), "key", key)
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func initLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
