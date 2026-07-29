package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JoshFarwig/kvstore/store"
)

func newTestServer() (http.Handler, *store.Store) {
	s := store.NewStore()
	return NewServer(s), s
}

// Drives the mux in-process. No port is bound, so these run in parallel with
// anything else and cannot collide with a real server on :8080.
func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body: %q)", rec.Code, want, rec.Body.String())
	}
}

func rfc3339(in time.Duration) string {
	return time.Now().UTC().Add(in).Format(time.RFC3339Nano)
}

func TestPutThenGet(t *testing.T) {
	h, _ := newTestServer()

	assertStatus(t, do(t, h, http.MethodPut, "/kvstore/k1", `{"value":{"a":1}}`), http.StatusNoContent)

	rec := do(t, h, http.MethodGet, "/kvstore/k1", "")
	assertStatus(t, rec, http.StatusOK)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got store.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid json: %v", err)
	}
	if string(got.Value) != `{"a":1}` {
		t.Errorf("value = %s, want {\"a\":1}", got.Value)
	}
}

// A repeated PUT must be indistinguishable from a single one: that is the
// property that lets proxies and Raft retries resend it safely.
func TestPutIsIdempotent(t *testing.T) {
	h, _ := newTestServer()
	body := `{"value":"same"}`

	first := do(t, h, http.MethodPut, "/kvstore/k1", body)
	second := do(t, h, http.MethodPut, "/kvstore/k1", body)

	assertStatus(t, first, http.StatusNoContent)
	assertStatus(t, second, http.StatusNoContent)

	rec := do(t, h, http.MethodGet, "/kvstore/k1", "")
	assertStatus(t, rec, http.StatusOK)
}

func TestPutOverwrites(t *testing.T) {
	h, _ := newTestServer()

	do(t, h, http.MethodPut, "/kvstore/k1", `{"value":"old"}`)
	do(t, h, http.MethodPut, "/kvstore/k1", `{"value":"new"}`)

	var got store.Item
	json.Unmarshal(do(t, h, http.MethodGet, "/kvstore/k1", "").Body.Bytes(), &got)
	if string(got.Value) != `"new"` {
		t.Errorf("value = %s, want \"new\"", got.Value)
	}
}

// time.Time rejects anything that is not RFC 3339 during decoding, so bad
// timestamps are caught by readJSON without any hand-written validation.
func TestPutRejectsBadBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", `hello`},
		{"truncated json", `{"value":`},
		{"expiresAt garbage string", `{"value":"x","expiresAt":"banana"}`},
		{"expiresAt number", `{"value":"x","expiresAt":12345}`},
		{"expiresAt date only", `{"value":"x","expiresAt":"2030-01-01"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestServer()
			assertStatus(t, do(t, h, http.MethodPut, "/kvstore/k1", tt.body), http.StatusBadRequest)
			// a rejected write must not have stored anything
			assertStatus(t, do(t, h, http.MethodGet, "/kvstore/k1", ""), http.StatusNotFound)
		})
	}
}

func TestGetMissing(t *testing.T) {
	h, _ := newTestServer()
	assertStatus(t, do(t, h, http.MethodGet, "/kvstore/nope", ""), http.StatusNotFound)
}

func TestGetExpired(t *testing.T) {
	h, _ := newTestServer()

	body := `{"value":"gone","expiresAt":"` + rfc3339(-time.Hour) + `"}`
	assertStatus(t, do(t, h, http.MethodPut, "/kvstore/k1", body), http.StatusNoContent)
	assertStatus(t, do(t, h, http.MethodGet, "/kvstore/k1", ""), http.StatusNotFound)
}

func TestGetUnexpiredWithTTL(t *testing.T) {
	h, _ := newTestServer()

	body := `{"value":"here","expiresAt":"` + rfc3339(time.Hour) + `"}`
	do(t, h, http.MethodPut, "/kvstore/k1", body)
	assertStatus(t, do(t, h, http.MethodGet, "/kvstore/k1", ""), http.StatusOK)
}

func TestDelete(t *testing.T) {
	h, _ := newTestServer()

	do(t, h, http.MethodPut, "/kvstore/k1", `{"value":"x"}`)
	assertStatus(t, do(t, h, http.MethodDelete, "/kvstore/k1", ""), http.StatusNoContent)
	assertStatus(t, do(t, h, http.MethodGet, "/kvstore/k1", ""), http.StatusNotFound)
}

// Deleting an absent key answers exactly as deleting a present one: the caller
// asked for it to be gone, and it is gone.
func TestDeleteAbsentMatchesPresent(t *testing.T) {
	h, _ := newTestServer()

	do(t, h, http.MethodPut, "/kvstore/present", `{"value":"x"}`)
	present := do(t, h, http.MethodDelete, "/kvstore/present", "")
	absent := do(t, h, http.MethodDelete, "/kvstore/absent", "")

	assertStatus(t, present, http.StatusNoContent)
	if absent.Code != present.Code {
		t.Errorf("delete absent = %d, delete present = %d; want identical", absent.Code, present.Code)
	}
}

func TestUnsupportedMethod(t *testing.T) {
	h, _ := newTestServer()
	assertStatus(t, do(t, h, http.MethodPatch, "/kvstore/k1", `{}`), http.StatusMethodNotAllowed)
}

// The handler must reach the same store the caller holds, not a copy.
func TestHandlerSharesStore(t *testing.T) {
	h, s := newTestServer()

	s.Set("seeded", []byte(`"direct"`), time.Time{})
	assertStatus(t, do(t, h, http.MethodGet, "/kvstore/seeded", ""), http.StatusOK)

	do(t, h, http.MethodPut, "/kvstore/viahttp", `{"value":"x"}`)
	if _, err := s.Get("viahttp"); err != nil {
		t.Errorf("key written over http not visible in store: %v", err)
	}
}
