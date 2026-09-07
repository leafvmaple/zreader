package server

import (
	"strings"
	"testing"

	"github.com/leafvmaple/zreader/internal/library"
)

func TestPublicScanResultRedactsPaths(t *testing.T) {
	res := library.ScanResult{
		FolderID: 1,
		Path:     `/tmp/zreader-test/books`,
		Added:    1,
		Failed: []library.SourceFailure{
			{
				Path:   `/tmp/zreader-test/books/BookA - AuthorX.txt`,
				Reason: `read epub /tmp/zreader-test/books/BookA - AuthorX.txt: unexpected EOF`,
			},
			{
				Path:   `C:\Users\Example\Books\BookB - AuthorY.epub`,
				Reason: `7: C:\Users\Example\Books\BookB - AuthorY.epub`,
			},
		},
	}

	got := publicScanResult(res)
	if got.Path != "books" {
		t.Fatalf("Path = %q, want folder basename", got.Path)
	}
	if len(got.Failed) != 2 {
		t.Fatalf("Failed = %+v, want two entries", got.Failed)
	}

	// The reason is redacted too, not just the name: error strings routinely
	// quote the path they failed on, so passing them through untouched would
	// undo the redaction the name itself gets.
	for _, failed := range got.Failed {
		for _, leak := range []string{"/tmp/", `C:\Users`} {
			if strings.Contains(failed.Name, leak) || strings.Contains(failed.Reason, leak) {
				t.Fatalf("failure leaked internal path: %+v", failed)
			}
		}
	}
	if got.Failed[0].Name != "BookA - AuthorX.txt" {
		t.Fatalf("Failed[0].Name = %q, want basename", got.Failed[0].Name)
	}
	if !strings.Contains(got.Failed[0].Reason, "unexpected EOF") {
		t.Fatalf("Failed[0].Reason = %q, want the cause preserved", got.Failed[0].Reason)
	}
	if got.Failed[1].Name != "BookB - AuthorY.epub" {
		t.Fatalf("Failed[1].Name = %q, want basename", got.Failed[1].Name)
	}
	if got.Failed[1].Reason != "7: BookB - AuthorY.epub" {
		t.Fatalf("Failed[1].Reason = %q, want prefixed basename", got.Failed[1].Reason)
	}
}
