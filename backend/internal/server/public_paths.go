package server

import (
	"path"
	"regexp"
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
	out.Failed = publicFailures(res.Failed)
	return out
}

// publicFailures redacts a scan's failure list for the API. The reason is
// redacted as well as the path — error strings routinely embed the file they
// failed on, so passing them through untouched would undo the redaction the
// path itself gets.
func publicFailures(in []library.SourceFailure) []failureDTO {
	if len(in) == 0 {
		return nil
	}
	out := make([]failureDTO, 0, len(in))
	for _, f := range in {
		out = append(out, failureDTO{
			Name:   publicBaseName(f.Path),
			Reason: publicFailureLabel(f.Reason),
		})
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

// dirPrefix matches the directory portion of a path — everything up to and
// including the final separator — for both POSIX and Windows shapes. The
// component pattern excludes whitespace and `:` so it stops at word
// boundaries and drive letters, while the trailing separator requirement
// means the basename itself is never consumed, spaces and all.
var dirPrefix = regexp.MustCompile(`(?:[A-Za-z]:)?(?:[\\/][^\\/\s:]+)*[\\/]`)

// publicFailureLabel strips internal directory layout out of a message
// bound for the API.
//
// It rewrites every path-looking run down to its basename, wherever the run
// appears. Only redacting the trailing segment — which is what this did
// originally — misses the common Go error shape `context <path>: cause`,
// where the path sits in the middle and the tail is prose.
func publicFailureLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.TrimSpace(dirPrefix.ReplaceAllString(s, ""))
}

func publicBaseName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return path.Base(strings.ReplaceAll(s, `\`, `/`))
}
