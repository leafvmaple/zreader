package server

// User-supplied reading fonts.
//
// The reader used to pull Noto Serif SC and LXGW WenKai from Google Fonts
// and jsdelivr on every page load, with Noto Serif SC as the *default* —
// so a zreader on a LAN with no egress silently fell back to whatever the
// device had, and every load leaked a request to two third parties.
//
// Bundling them instead is not an option worth taking: the subsetted
// LXGW WenKai package alone is ~19 MB, most of the size of the whole
// container. So the built-in choices are pure system stacks (see
// ReaderPage.css) and anything beyond that is opt-in: drop woff2/ttf
// files into <data>/fonts/ and they show up in the reader's font picker,
// served from this origin.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fontDirName is the directory under ZREADER_DATA_DIR that user fonts are
// read from. It is not created on boot — its absence just means "no
// custom fonts", which is the common case.
const fontDirName = "fonts"

// fontMediaTypes is also the allowlist: a file whose extension isn't here
// is not listed and not served, so the directory can't be used to serve
// arbitrary content from the data volume.
var fontMediaTypes = map[string]string{
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
}

type fontDTO struct {
	// File is the on-disk basename, and the id the reader passes back to
	// /api/v1/fonts/{file}.
	File string `json:"file"`
	// Name is the display label: the basename without extension, which is
	// what a user naming the file "霞鹜文楷.woff2" expects to see.
	Name string `json:"name"`
	Size int64  `json:"size_bytes"`
}

// handleListFonts enumerates <data>/fonts. A missing directory is not an
// error — it is the default state.
func (s *Server) handleListFonts(w http.ResponseWriter, r *http.Request) {
	dir := s.fontDir()
	out := []fontDTO{}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "read_font_dir", err)
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if fontMediaTypes[strings.ToLower(filepath.Ext(name))] == "" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, fontDTO{
				File: name,
				Name: strings.TrimSuffix(name, filepath.Ext(name)),
				Size: info.Size(),
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	}
	writeJSON(w, http.StatusOK, map[string]any{"fonts": out})
}

// handleGetFont serves one file out of <data>/fonts. The path value is
// reduced to its basename before use, so no traversal can escape the
// directory even if the router's matching ever loosens.
func (s *Server) handleGetFont(w http.ResponseWriter, r *http.Request) {
	dir := s.fontDir()
	if dir == "" {
		writeError(w, http.StatusNotFound, "not_found", errors.New("no font directory configured"))
		return
	}
	name := filepath.Base(filepath.Clean("/" + r.PathValue("file")))
	media := fontMediaTypes[strings.ToLower(filepath.Ext(name))]
	if media == "" || name == "." || name == string(filepath.Separator) {
		writeError(w, http.StatusNotFound, "not_found", errors.New("not a font file"))
		return
	}

	full := filepath.Join(dir, name)
	f, err := os.Open(full)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", errors.New("font not found"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeError(w, http.StatusNotFound, "not_found", errors.New("font not found"))
		return
	}

	w.Header().Set("Content-Type", media)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Fonts are immutable in practice; replacing one means a new filename
	// or a restart, and ServeContent still revalidates on mtime.
	w.Header().Set("Cache-Control", "private, max-age=2592000")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (s *Server) fontDir() string {
	if s.cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.DataDir, fontDirName)
}
