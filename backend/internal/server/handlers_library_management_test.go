package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leafvmaple/zreader/internal/store"
)

func TestLibraryManagementMetadataTagsAndReparsePreservesEdits(t *testing.T) {
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

	patch := `{
		"title":"EditedTitle",
		"author":"EditedAuthor",
		"description":"Short description",
		"category":"CategoryA",
		"favorite":true,
		"reading_status":"reading",
		"tags":["TagA","TagB"]
	}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/books/"+itoa(book.ID), bytes.NewBufferString(patch))
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", rr.Code, rr.Body.String())
	}
	var patched struct {
		Book struct {
			Title         string   `json:"title"`
			Author        string   `json:"author"`
			Description   string   `json:"description"`
			Category      string   `json:"category"`
			Favorite      bool     `json:"favorite"`
			ReadingStatus string   `json:"reading_status"`
			CoverColor    string   `json:"cover_color"`
			CoverLabel    string   `json:"cover_label"`
			Tags          []string `json:"tags"`
		} `json:"book"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Book.Title != "EditedTitle" || patched.Book.Author != "EditedAuthor" ||
		patched.Book.Description != "Short description" || patched.Book.Category != "CategoryA" ||
		!patched.Book.Favorite || patched.Book.ReadingStatus != store.ReadingStatusReading ||
		patched.Book.CoverColor == "" || patched.Book.CoverLabel == "" ||
		strings.Join(patched.Book.Tags, ",") != "TagA,TagB" {
		t.Fatalf("patched book = %+v", patched.Book)
	}

	source := filepath.Join(bookDir, "BookA - AuthorX.txt")
	if err := os.WriteFile(source, []byte("Chapter 1\n\nNew body paragraph.\n"), 0o644); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/books/"+itoa(book.ID)+"/reparse", nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reparse status = %d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/books/"+itoa(book.ID), nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Book struct {
			Title         string   `json:"title"`
			Author        string   `json:"author"`
			ReadingStatus string   `json:"reading_status"`
			Tags          []string `json:"tags"`
		} `json:"book"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Book.Title != "EditedTitle" || got.Book.Author != "EditedAuthor" ||
		got.Book.ReadingStatus != store.ReadingStatusReading ||
		strings.Join(got.Book.Tags, ",") != "TagA,TagB" {
		t.Fatalf("book after reparse = %+v", got.Book)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/library/tags", nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tags status = %d body=%s", rr.Code, rr.Body.String())
	}
	var listed struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	if len(listed.Tags) != 2 || listed.Tags[0].Name != "TagA" || listed.Tags[1].Name != "TagB" {
		t.Fatalf("tags = %+v", listed.Tags)
	}
}

func TestLibraryManagementDuplicatesBatchAndJobs(t *testing.T) {
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

	bodyText := "Chapter 1\n\nDuplicate body paragraph.\n"
	firstJobID := uploadTestBookWithJob(t, srv, "BookA - AuthorX.txt", bodyText)
	uploadTestBook(t, srv, "BookB - AuthorY.txt", bodyText)
	books, err := st.ListBooks(ctx, folder.ID)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("books = %+v, want two", books)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/books/duplicates", nil)
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("duplicates status = %d body=%s", rr.Code, rr.Body.String())
	}
	var duplicates struct {
		Groups []struct {
			Books []struct {
				ID int64 `json:"id"`
			} `json:"books"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &duplicates); err != nil {
		t.Fatalf("decode duplicates: %v", err)
	}
	if len(duplicates.Groups) != 1 || len(duplicates.Groups[0].Books) != 2 {
		t.Fatalf("duplicates = %+v, want one group of two", duplicates.Groups)
	}

	batch := `{"action":"tag","book_ids":[` + itoa(books[0].ID) + `,` + itoa(books[1].ID) + `],"tags":["BatchTag"]}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/books/batch", bytes.NewBufferString(batch))
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("batch tag status = %d body=%s", rr.Code, rr.Body.String())
	}
	var batchRes struct {
		Job struct {
			Status  string `json:"status"`
			Updated int64  `json:"updated"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &batchRes); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if batchRes.Job.Status != store.JobStatusDone || batchRes.Job.Updated != 2 {
		t.Fatalf("batch job = %+v", batchRes.Job)
	}

	statusBody := `{"action":"status","book_ids":[` + itoa(books[0].ID) + `,` + itoa(books[1].ID) + `],"reading_status":"finished"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/books/batch", bytes.NewBufferString(statusBody))
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("batch status status = %d body=%s", rr.Code, rr.Body.String())
	}

	emptyTagBody := `{"action":"tag","book_ids":[` + itoa(books[0].ID) + `],"tags":["  "]}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/books/batch", strings.NewReader(emptyTagBody))
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty batch tag status = %d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/library/jobs/"+itoa(firstJobID)+"/retry", nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("retry job status = %d body=%s", rr.Code, rr.Body.String())
	}
	var retry struct {
		Job struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &retry); err != nil {
		t.Fatalf("decode retry: %v", err)
	}
	if retry.Job.Type != "import" || retry.Job.Status != store.JobStatusDone {
		t.Fatalf("retry job = %+v", retry.Job)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/library/jobs?limit=10", nil)
	rr = httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("jobs status = %d body=%s", rr.Code, rr.Body.String())
	}
	var jobs struct {
		Jobs []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	if len(jobs.Jobs) < 4 {
		t.Fatalf("jobs = %+v, want import/import retry/batch history", jobs.Jobs)
	}
}

func uploadTestBookWithJob(t *testing.T, srv *Server, name, content string) int64 {
	t.Helper()
	body, contentType := uploadBody(t, name, content)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/upload", body)
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Job struct {
			ID     int64  `json:"id"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if res.Job.ID == 0 || res.Job.Type != "import" || res.Job.Status != store.JobStatusDone {
		t.Fatalf("upload job = %+v", res.Job)
	}
	return res.Job.ID
}
