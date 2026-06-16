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
		Failed: []string{
			`/tmp/zreader-test/books/BookA - AuthorX.txt`,
			`7: C:\Users\Example\Books\BookB - AuthorY.epub`,
		},
	}

	got := publicScanResult(res)
	if got.Path != "books" {
		t.Fatalf("Path = %q, want folder basename", got.Path)
	}
	if len(got.Failed) != 2 {
		t.Fatalf("Failed = %+v, want two entries", got.Failed)
	}
	for _, failed := range got.Failed {
		if strings.Contains(failed, "/tmp/") || strings.Contains(failed, `C:\Users`) {
			t.Fatalf("failed label leaked internal path: %q", failed)
		}
	}
	if got.Failed[0] != "BookA - AuthorX.txt" {
		t.Fatalf("Failed[0] = %q, want basename", got.Failed[0])
	}
	if got.Failed[1] != "7: BookB - AuthorY.epub" {
		t.Fatalf("Failed[1] = %q, want prefixed basename", got.Failed[1])
	}
}
