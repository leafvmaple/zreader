package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/leafvmaple/zreader/internal/store"
)

func TestHandleUploadBooks_SavesAndScans(t *testing.T) {
	ctx := context.Background()
	bookDir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	folder, err := st.AddFolder(ctx, bookDir)
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}

	srv := New(Config{Port: 0, Store: st})
	body, contentType := uploadBody(t, "BookA - AuthorX.txt", "Chapter 1\n\nAlpha body paragraph.\n")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/upload", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()

	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	var res struct {
		Uploaded []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"uploaded"`
		Scan struct {
			Added  int      `json:"added"`
			Failed []string `json:"failed"`
		} `json:"scan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(res.Uploaded) != 1 || res.Uploaded[0].Name != "BookA - AuthorX.txt" {
		t.Fatalf("uploaded = %+v", res.Uploaded)
	}
	if res.Scan.Added != 1 || len(res.Scan.Failed) != 0 {
		t.Fatalf("scan = %+v", res.Scan)
	}
	if _, err := os.Stat(filepath.Join(bookDir, "BookA - AuthorX.txt")); err != nil {
		t.Fatalf("uploaded source missing: %v", err)
	}

	books, err := st.ListBooks(ctx, folder.ID)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	if len(books) != 1 || books[0].Title != "BookA" {
		t.Fatalf("books = %+v", books)
	}
}

func TestHandleUploadBooks_OnlyScansUploadedSources(t *testing.T) {
	ctx := context.Background()
	bookDir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	folder, err := st.AddFolder(ctx, bookDir)
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(bookDir, "Other - AuthorY.txt"),
		[]byte("Chapter 1\n\nOther body paragraph.\n"),
		0o644,
	); err != nil {
		t.Fatalf("seed unrelated source: %v", err)
	}

	srv := New(Config{Port: 0, Store: st})
	body, contentType := uploadBody(t, "BookA - AuthorX.txt", "Chapter 1\n\nAlpha body paragraph.\n")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/upload", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()

	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	var res struct {
		Scan struct {
			Added  int      `json:"added"`
			Failed []string `json:"failed"`
		} `json:"scan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Scan.Added != 1 || len(res.Scan.Failed) != 0 {
		t.Fatalf("scan = %+v", res.Scan)
	}

	books, err := st.ListBooks(ctx, folder.ID)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	if len(books) != 1 || books[0].Title != "BookA" {
		t.Fatalf("books = %+v, want only uploaded source", books)
	}
}

func TestHandleUploadBooks_DoesNotOverwriteExistingSource(t *testing.T) {
	bookDir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if _, err := st.AddFolder(context.Background(), bookDir); err != nil {
		t.Fatalf("add folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bookDir, "BookA - AuthorX.txt"), []byte("original"), 0o644); err != nil {
		t.Fatalf("seed existing source: %v", err)
	}

	srv := New(Config{Port: 0, Store: st})
	body, contentType := uploadBody(t, "BookA - AuthorX.txt", "Chapter 1\n\nNew body paragraph.\n")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/upload", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()

	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	original, err := os.ReadFile(filepath.Join(bookDir, "BookA - AuthorX.txt"))
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(original) != "original" {
		t.Fatalf("existing source was overwritten: %q", original)
	}
	if _, err := os.Stat(filepath.Join(bookDir, "BookA - AuthorX (1).txt")); err != nil {
		t.Fatalf("deduplicated upload missing: %v", err)
	}
}

func uploadBody(t *testing.T, name, content string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &body, mw.FormDataContentType()
}
