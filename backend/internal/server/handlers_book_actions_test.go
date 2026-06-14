package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/leafvmaple/zreader/internal/store"
)

func TestBookSearchAndBookmarks(t *testing.T) {
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

	uploadTestBook(t, srv, "BookA - AuthorX.txt", "Chapter 1\n\nAlpha beta target text.\n\nChapter 2\n\nMore target text.\n")
	book := onlyBook(t, st, folder.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/books/"+itoa(book.ID)+"/search?q=target", nil)
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%s", rr.Code, rr.Body.String())
	}
	var search struct {
		Matches []struct {
			CharOffset int64  `json:"char_offset"`
			ChapterIdx int64  `json:"chapter_idx"`
			Snippet    string `json:"snippet"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &search); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(search.Matches) != 2 || !strings.Contains(search.Matches[0].Snippet, "target") {
		t.Fatalf("matches = %+v, want two target snippets", search.Matches)
	}

	body := bytes.NewBufferString(`{"char_offset":12,"chapter_idx":1,"note":"mark"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/books/"+itoa(book.ID)+"/bookmarks", body)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("add bookmark status = %d body=%s", rr.Code, rr.Body.String())
	}
	var added struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode bookmark: %v", err)
	}
	if added.ID == 0 {
		t.Fatalf("bookmark id not set: %+v", added)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/books/"+itoa(book.ID)+"/bookmarks", nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list bookmarks status = %d body=%s", rr.Code, rr.Body.String())
	}
	var listed struct {
		Bookmarks []struct {
			ID         int64  `json:"id"`
			CharOffset int64  `json:"char_offset"`
			Note       string `json:"note"`
		} `json:"bookmarks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode bookmarks: %v", err)
	}
	if len(listed.Bookmarks) != 1 || listed.Bookmarks[0].Note != "mark" {
		t.Fatalf("bookmarks = %+v, want saved mark", listed.Bookmarks)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/books/"+itoa(book.ID)+"/bookmarks/"+itoa(added.ID), nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete bookmark status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestBookReparseUsesPersistedSourcePath(t *testing.T) {
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

	uploadTestBook(t, srv, "BookA - AuthorX.txt", "Chapter 1\n\nOld body paragraph.\n")
	book := onlyBook(t, st, folder.ID)
	source := filepath.Join(bookDir, "BookA - AuthorX.txt")
	if err := os.WriteFile(source, []byte("Chapter 1\n\nNew body paragraph.\n"), 0o644); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/books/"+itoa(book.ID)+"/reparse", nil)
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reparse status = %d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/books/"+itoa(book.ID)+"/search?q=New", nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%s", rr.Code, rr.Body.String())
	}
	var search struct {
		Matches []struct {
			Snippet string `json:"snippet"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &search); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(search.Matches) != 1 || !strings.Contains(search.Matches[0].Snippet, "New") {
		t.Fatalf("matches = %+v, want reparsed text", search.Matches)
	}
}

func TestDeleteBookRemovesSourceAndRow(t *testing.T) {
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

	uploadTestBook(t, srv, "BookA - AuthorX.txt", "Chapter 1\n\nBody paragraph.\n")
	book := onlyBook(t, st, folder.ID)
	source := filepath.Join(bookDir, "BookA - AuthorX.txt")
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source missing before delete: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/books/"+itoa(book.ID)+"?source=true", nil)
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists or stat failed unexpectedly: %v", err)
	}
	books, err := st.ListBooks(ctx, folder.ID)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("books = %+v, want empty after delete", books)
	}
}

func uploadTestBook(t *testing.T, srv *Server, name, content string) {
	t.Helper()
	body, contentType := uploadBody(t, name, content)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/upload", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func onlyBook(t *testing.T, st *store.Store, folderID int64) store.Book {
	t.Helper()
	books, err := st.ListBooks(context.Background(), folderID)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("books = %+v, want exactly one", books)
	}
	return books[0]
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
