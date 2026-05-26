// Package config reads runtime env vars and exposes well-known paths.
//
// Env vars (all optional):
//   - ZREADER_DATA_DIR:     where library.db lives (default: ./data, /data in container)
//   - ZREADER_LIBRARY_PATH: one or more book directories, OS-listsep separated
//     (`:` on Linux, `;` on Windows). Seeded into the
//     library_folders table on every boot; duplicates
//     are idempotent.
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Paths holds the resolved on-disk locations for the running process.
type Paths struct {
	Data         string
	LibraryRoots []string
}

// Load reads env vars, falling back to local dev defaults rooted at the
// current working directory. The data dir is guaranteed to exist on return.
func Load() (Paths, error) {
	cwd, _ := os.Getwd()

	p := Paths{
		Data: env("ZREADER_DATA_DIR", filepath.Join(cwd, "data")),
	}

	if raw := os.Getenv("ZREADER_LIBRARY_PATH"); raw != "" {
		p.LibraryRoots = splitPaths(raw)
	}

	if err := os.MkdirAll(p.Data, 0o755); err != nil {
		return p, err
	}
	return p, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitPaths splits ZREADER_LIBRARY_PATH on the OS list separator. On Windows
// that's `;` (so absolute paths like `D:\books` aren't mis-split on the
// drive-letter colon); on Linux/Docker it's `:`.
func splitPaths(raw string) []string {
	parts := strings.Split(raw, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
