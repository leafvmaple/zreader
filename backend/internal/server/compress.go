package server

// Response compression.
//
// The payloads that matter here are CJK text in JSON: a chapter slice, and
// the chapter list a long book ships on open — 409 KB of it for the largest
// book in the test corpus, on the critical path before the first character
// is rendered. UTF-8 Chinese in JSON compresses to roughly a fifth of that.
//
// Deliberately narrow. It compresses by declared Content-Type rather than
// by trying and measuring, and it skips anything already compressed: the
// cached EPUBs are ZIPs, covers are JPEG or PNG, and re-deflating those
// costs CPU to make the body slightly larger.

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// compressMinBytes is the size below which compressing is not worth the
// header overhead and the CPU. Most API responses here are a status object
// or a handful of rows and fall under it.
const compressMinBytes = 1 << 10

// compressibleTypes is matched against the Content-Type prefix. Everything
// outside it is passed through untouched.
var compressibleTypes = []string{
	"application/json",
	"application/x-ndjson",
	"text/",
	"image/svg+xml",
	"application/javascript",
}

var gzipWriters = sync.Pool{
	New: func() any {
		// BestSpeed: this runs on a request path and the text is highly
		// redundant, so the levels above it cost noticeably more CPU for a
		// few percent of size.
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// compressResponses gzips eligible responses for clients that accept it.
func compressResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary regardless of what happens below: a cache that stored an
		// uncompressed body must not serve it to a client that got the
		// compressed variant's ETag, or the reverse.
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		cw := &compressWriter{ResponseWriter: w}
		defer cw.Close()
		next.ServeHTTP(cw, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(name, "gzip") {
			return true
		}
	}
	return false
}

// compressWriter decides on the first write whether the response is worth
// compressing, then either streams through gzip or passes bytes straight
// down. The decision is deferred to the first write because Content-Type
// is not known until the handler has set it.
type compressWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
	status  int
	// buf holds the first write until there is enough of it to judge the
	// size, so short responses are not compressed.
	buf []byte
}

func (c *compressWriter) WriteHeader(status int) {
	c.status = status
	// Header is written at decide() time — a compressed response must set
	// Content-Encoding and drop Content-Length before the status goes out.
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.decided {
		c.buf = append(c.buf, p...)
		if len(c.buf) < compressMinBytes {
			// Not yet enough to judge; a handler that stops here gets an
			// uncompressed response from Close.
			return len(p), nil
		}
		// decide flushes the buffer, and the buffer already holds p — so
		// this write is complete and must not fall through below.
		c.decide()
		return len(p), nil
	}
	if c.gz != nil {
		return c.gz.Write(p)
	}
	return c.ResponseWriter.Write(p)
}

// decide commits to a path and flushes whatever was buffered.
func (c *compressWriter) decide() {
	c.decided = true
	compressible := c.compressible()
	if compressible {
		c.Header().Set("Content-Encoding", "gzip")
		// The length of the compressed body is not known up front, and the
		// handler's value describes the original.
		c.Header().Del("Content-Length")
	}
	c.writeStatus()
	if !compressible {
		if len(c.buf) > 0 {
			_, _ = c.ResponseWriter.Write(c.buf)
			c.buf = nil
		}
		return
	}
	gz := gzipWriters.Get().(*gzip.Writer)
	gz.Reset(c.ResponseWriter)
	c.gz = gz
	if len(c.buf) > 0 {
		_, _ = gz.Write(c.buf)
		c.buf = nil
	}
}

func (c *compressWriter) compressible() bool {
	// A handler that set its own encoding (or a 204/304 with no body) is
	// left alone.
	if c.Header().Get("Content-Encoding") != "" {
		return false
	}
	ct := c.Header().Get("Content-Type")
	for _, prefix := range compressibleTypes {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}

func (c *compressWriter) writeStatus() {
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	c.ResponseWriter.WriteHeader(status)
}

// Close finishes the gzip stream, or emits a short uncompressed response
// that never reached the size threshold.
func (c *compressWriter) Close() {
	if !c.decided {
		c.decided = true
		c.writeStatus()
		if len(c.buf) > 0 {
			_, _ = c.ResponseWriter.Write(c.buf)
			c.buf = nil
		}
		return
	}
	if c.gz != nil {
		_ = c.gz.Close()
		gzipWriters.Put(c.gz)
		c.gz = nil
	}
}

// Flush keeps streaming handlers working. Both layers must flush, or the
// bytes sit in the gzip window.
func (c *compressWriter) Flush() {
	if !c.decided {
		c.decide()
	}
	if c.gz != nil {
		_ = c.gz.Flush()
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
