package server

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leafvmaple/zreader/internal/export"
	"github.com/leafvmaple/zreader/internal/store"
)

func exportTestBook(t *testing.T, content string) (*Server, store.Book) {
	t.Helper()
	ctx := context.Background()
	bookDir := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	folder, err := st.AddFolder(ctx, bookDir)
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	srv := New(Config{Port: 0, Store: st, DataDir: t.TempDir()})
	uploadTestBook(t, srv, "Example - Anonymous.txt", content)
	return srv, onlyBook(t, st, folder.ID)
}

func TestExportBookUsesChapterCorpusContract(t *testing.T) {
	srv, book := exportTestBook(t, "第一章 子丑寅卯\n\n甲乙丙丁，戊己庚辛。\n\n第二章 辰巳午未\n\n壬癸子丑，寅卯辰巳。\n")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/books/"+itoa(book.ID)+"/export?preview=1&chunk=200", nil)
	rr := httptest.NewRecorder()
	testRouter(t, srv).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", rr.Code, rr.Body.String())
	}
	var preview struct {
		Filename string                `json:"filename"`
		Stats    export.Stats          `json:"stats"`
		Sample   []export.CorpusRecord `json:"sample"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Stats.Records != 2 || len(preview.Sample) != 2 {
		t.Fatalf("preview = %+v, want one record for each of 2 chapters", preview)
	}
	if !strings.HasPrefix(preview.Filename, "corpus-") || !strings.HasSuffix(preview.Filename, ".reader-v1.jsonl") {
		t.Errorf("preview filename = %q, want an opaque corpus filename", preview.Filename)
	}
	for i, record := range preview.Sample {
		if record.SchemaVersion != export.CorpusSchemaVersion {
			t.Errorf("record %d schema version = %d", i, record.SchemaVersion)
		}
		if record.DocumentID == "" || record.SourceSHA256 == "" || record.CleanedSHA256 == "" {
			t.Errorf("record %d is missing provenance: %+v", i, record)
		}
	}
}

func TestExportBookUsesOpaqueFilenameAndOptionalContentAnonymity(t *testing.T) {
	srv, book := exportTestBook(t, "第一章 子丑寅卯\n\n甲乙丙丁，戊己庚辛。\n")
	router := testRouter(t, srv)

	previewReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/books/"+itoa(book.ID)+"/export?preview=1&anonymize=1", nil)
	previewRecorder := httptest.NewRecorder()
	router.ServeHTTP(previewRecorder, previewReq)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var preview struct {
		Filename string                `json:"filename"`
		Sample   []export.CorpusRecord `json:"sample"`
	}
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(preview.Sample) != 1 {
		t.Fatalf("sample records = %d, want 1", len(preview.Sample))
	}
	record := preview.Sample[0]
	if record.Title != record.ChapterID || record.Metadata.Author != "" ||
		!strings.HasPrefix(record.Metadata.Book, "document-") || !record.Cleaning.Anonymized {
		t.Errorf("record identity was not anonymized: %+v", record)
	}

	downloadReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/books/"+itoa(book.ID)+"/export?anonymize=1", nil)
	downloadRecorder := httptest.NewRecorder()
	router.ServeHTTP(downloadRecorder, downloadReq)
	if downloadRecorder.Code != http.StatusOK {
		t.Fatalf("download status = %d body=%s", downloadRecorder.Code, downloadRecorder.Body.String())
	}
	_, params, err := mime.ParseMediaType(downloadRecorder.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parse Content-Disposition: %v", err)
	}
	if params["filename"] != preview.Filename {
		t.Errorf("download filename = %q, preview advertised %q", params["filename"], preview.Filename)
	}
	if strings.Contains(downloadRecorder.Header().Get("Content-Disposition"), book.Title) {
		t.Errorf("Content-Disposition leaked the book title: %q", downloadRecorder.Header().Get("Content-Disposition"))
	}
}

func TestExportBookBlocksReplacementCharactersBeforeDownload(t *testing.T) {
	srv, book := exportTestBook(t, "\uFEFF正文\n\n甲乙丙丁。\uFFFD\n")

	previewReq := httptest.NewRequest(http.MethodGet, "/api/v1/books/"+itoa(book.ID)+"/export?preview=1", nil)
	previewRecorder := httptest.NewRecorder()
	testRouter(t, srv).ServeHTTP(previewRecorder, previewReq)
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var preview struct {
		Stats export.Stats `json:"stats"`
	}
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Stats.ReplacementCharacters != 1 {
		t.Fatalf("replacement characters = %d, want 1", preview.Stats.ReplacementCharacters)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/v1/books/"+itoa(book.ID)+"/export", nil)
	downloadRecorder := httptest.NewRecorder()
	testRouter(t, srv).ServeHTTP(downloadRecorder, downloadReq)
	if downloadRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("download status = %d body=%s", downloadRecorder.Code, downloadRecorder.Body.String())
	}
	if strings.Contains(downloadRecorder.Body.String(), `"schema_version"`) {
		t.Fatalf("download returned corpus data before rejecting corruption: %s", downloadRecorder.Body.String())
	}
}

// A CJK book title has to survive the Content-Disposition round trip: the
// bare `filename` is ASCII-only by spec, so the real name rides in
// `filename*` as percent-encoded UTF-8 (RFC 5987). Getting that wrong
// silently hands the user a file called "export.jsonl" — or worse, a
// literal "=?utf-8?q?…?=".
func TestContentDisposition(t *testing.T) {
	cases := []struct {
		name      string
		wantASCII string
	}{
		{"甲乙丙.jsonl", "export.jsonl"},
		{"BookA.jsonl", "BookA.jsonl"},
		{"甲乙丙 BookA.jsonl", "BookA.jsonl"},
		{".jsonl", "export.jsonl"},
	}
	for _, c := range cases {
		got := contentDisposition(c.name)

		// The bare filename is the ASCII-only fallback, read straight off
		// the header — mime.ParseMediaType below deliberately shadows it
		// with the decoded extended form, which is the whole point of
		// filename*, so it can't be used to check this half.
		if !strings.Contains(got, `filename="`+c.wantASCII+`"`) {
			t.Errorf("contentDisposition(%q) = %q, want an ASCII fallback of %q",
				c.name, got, c.wantASCII)
		}

		// A correct RFC 5987 parameter decodes back to the original name.
		// Go resolves filename* into params["filename"] when both are set,
		// which is exactly the preference a browser applies.
		_, params, err := mime.ParseMediaType(got)
		if err != nil {
			t.Fatalf("contentDisposition(%q) = %q, unparsable: %v", c.name, got, err)
		}
		if params["filename"] != c.name {
			t.Errorf("contentDisposition(%q): filename* decoded to %q, want the original name",
				c.name, params["filename"])
		}

		if strings.Contains(got, "=?utf-8?") {
			t.Errorf("contentDisposition(%q) used a MIME word, not percent-encoding: %q", c.name, got)
		}
	}
}

func TestRFC5987Escape(t *testing.T) {
	if got := rfc5987Escape("甲"); got != "%E7%94%B2" {
		t.Errorf("rfc5987Escape(甲) = %q, want %%E7%%94%%B2", got)
	}
	if got := rfc5987Escape("a-b_c.d~e"); got != "a-b_c.d~e" {
		t.Errorf("unreserved characters were escaped: %q", got)
	}
	if got := rfc5987Escape("a b"); got != "a%20b" {
		t.Errorf("rfc5987Escape(\"a b\") = %q, want a%%20b", got)
	}
}
