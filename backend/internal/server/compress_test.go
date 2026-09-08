package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func compressed(t *testing.T, h http.Handler, acceptGzip bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if acceptGzip {
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	}
	rec := httptest.NewRecorder()
	compressResponses(h).ServeHTTP(rec, req)
	return rec
}

func jsonHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	})
}

// The payload this exists for: a chapter list or a chapter of CJK prose.
func TestCompressShrinksLargeJSON(t *testing.T) {
	body := `{"text":"` + strings.Repeat("甲乙丙丁戊己庚辛壬癸。", 4000) + `"}`
	rec := compressed(t, jsonHandler(body), true)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if rec.Body.Len() >= len(body)/2 {
		t.Errorf("compressed to %d bytes from %d — barely shrank", rec.Body.Len(), len(body))
	}
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if string(got) != body {
		t.Error("round-trip changed the body")
	}
}

func TestCompressSkipsSmallResponses(t *testing.T) {
	body := `{"status":"ok"}`
	rec := compressed(t, jsonHandler(body), true)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Error("compressed a response far below the threshold")
	}
	if rec.Body.String() != body {
		t.Errorf("body = %q, want %q", rec.Body.String(), body)
	}
}

// Cached EPUBs are ZIPs and covers are JPEG/PNG; re-deflating those spends
// CPU to make the body bigger.
func TestCompressSkipsAlreadyCompressedTypes(t *testing.T) {
	for _, ct := range []string{"image/jpeg", "application/epub+zip", "application/pdf"} {
		t.Run(ct, func(t *testing.T) {
			payload := strings.Repeat("x", 64<<10)
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", ct)
				_, _ = io.WriteString(w, payload)
			})
			rec := compressed(t, h, true)
			if rec.Header().Get("Content-Encoding") != "" {
				t.Errorf("compressed %s", ct)
			}
			if rec.Body.String() != payload {
				t.Error("body was altered")
			}
		})
	}
}

func TestCompressHonoursAcceptEncoding(t *testing.T) {
	body := `{"text":"` + strings.Repeat("甲乙丙丁", 4000) + `"}`
	rec := compressed(t, jsonHandler(body), false)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Error("compressed for a client that did not ask for it")
	}
	if rec.Body.String() != body {
		t.Error("body was altered")
	}
}

// A cache that keyed only on URL would serve a gzip body to a client that
// cannot read it.
func TestCompressAlwaysVaries(t *testing.T) {
	for _, accept := range []bool{true, false} {
		rec := compressed(t, jsonHandler(`{"status":"ok"}`), accept)
		if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
			t.Errorf("accept=%v: Vary missing Accept-Encoding", accept)
		}
	}
}

// Status and headers are written at decide time, not when the handler calls
// WriteHeader, so a non-200 must still arrive intact.
func TestCompressPreservesStatusAndHeaders(t *testing.T) {
	body := `{"error":"` + strings.Repeat("z", 4000) + `"}`
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "kept")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, body)
	})
	rec := compressed(t, h, true)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if rec.Header().Get("X-Custom") != "kept" {
		t.Error("handler header was lost")
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("Content-Length describes the uncompressed body and must be dropped")
	}
}

// A handler that writes nothing at all must not emit a truncated gzip
// stream or a stray 200 body.
func TestCompressEmptyResponse(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := compressed(t, h, true)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}
