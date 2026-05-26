package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/leafvmaple/zreader/internal/library"
	"github.com/leafvmaple/zreader/internal/store"
)

// folderDTO is the JSON shape sent to the client. We hide sql.Null* by
// flattening to plain optional fields.
type folderDTO struct {
	ID         int64  `json:"id"`
	Path       string `json:"path"`
	AddedAt    int64  `json:"added_at"`
	LastScanAt *int64 `json:"last_scan_at,omitempty"`
	BookCount  int64  `json:"book_count"`
}

func toFolderDTO(f store.Folder) folderDTO {
	d := folderDTO{ID: f.ID, Path: f.Path, AddedAt: f.AddedAt, BookCount: f.BookCount}
	if f.LastScanAt.Valid {
		v := f.LastScanAt.Int64
		d.LastScanAt = &v
	}
	return d
}

func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_folders", err)
		return
	}
	out := make([]folderDTO, 0, len(folders))
	for _, f := range folders {
		out = append(out, toFolderDTO(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": out})
}

func (s *Server) handleAddFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	if body.Path == "" {
		writeError(w, http.StatusBadRequest, "missing_path", errors.New("path is required"))
		return
	}
	f, err := s.store.AddFolder(r.Context(), body.Path)
	if err != nil && !errors.Is(err, store.ErrFolderExists) {
		writeError(w, http.StatusInternalServerError, "add_folder", err)
		return
	}
	status := http.StatusCreated
	if errors.Is(err, store.ErrFolderExists) {
		status = http.StatusOK
	}
	writeJSON(w, status, toFolderDTO(f))
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	if err := s.store.DeleteFolder(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "delete_folder", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	// Body is optional: {"folder_id": <id>} scans one folder, omitted scans all.
	var body struct {
		FolderID int64 `json:"folder_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	scanner := &library.Scanner{Store: s.store, Logger: s.cfg.Logger}

	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_folders", err)
		return
	}

	var results []library.ScanResult
	for _, f := range folders {
		if body.FolderID != 0 && f.ID != body.FolderID {
			continue
		}
		res, err := scanner.ScanFolder(r.Context(), f)
		if err != nil {
			s.cfg.Logger.Printf("scan folder %d (%s) failed: %v", f.ID, f.Path, err)
			// Keep going — we still want partial success info in the response.
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"scans": results})
}
