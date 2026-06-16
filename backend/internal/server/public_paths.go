package server

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/leafvmaple/zreader/internal/library"
	"github.com/leafvmaple/zreader/internal/store"
)

func publicBookPath(b store.Book) string {
	return publicBaseName(b.Path)
}

func publicSourcePath(path string) string {
	return publicBaseName(path)
}

func publicScanResult(res library.ScanResult) scanResultDTO {
	out := scanResultDTO{
		FolderID: res.FolderID,
		Path:     publicBaseName(res.Path),
		Added:    res.Added,
		Updated:  res.Updated,
		Removed:  res.Removed,
	}
	if len(res.Failed) > 0 {
		out.Failed = make([]string, 0, len(res.Failed))
		for _, failed := range res.Failed {
			out.Failed = append(out.Failed, publicFailureLabel(failed))
		}
	}
	return out
}

func publicScanResults(results []library.ScanResult) []scanResultDTO {
	out := make([]scanResultDTO, 0, len(results))
	for _, res := range results {
		out = append(out, publicScanResult(res))
	}
	return out
}

func publicFailureLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, ": "); i > 0 && i < len(s)-2 {
		return s[:i+2] + publicFailureLabel(s[i+2:])
	}
	base := publicBaseName(s)
	if base == "." || base == string(filepath.Separator) || base == "/" || base == "" {
		return s
	}
	return base
}

func publicBaseName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return path.Base(strings.ReplaceAll(s, `\`, `/`))
}
