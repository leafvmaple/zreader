package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

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

// handleScan starts a library scan and returns immediately.
//
// It used to run the whole scan inside the request. A few hundred books —
// or one OCR pass — means minutes of a spinning button with nothing behind
// it, and any proxy timeout in between kills the response even though the
// scan itself completes. The work now runs on its own goroutine against a
// job row the client polls.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	// Body is optional: {"folder_id": <id>} scans one folder, omitted scans all.
	var body struct {
		FolderID int64 `json:"folder_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// One scan at a time. Two concurrent passes would format the same cache
	// files from both, and the second would be reporting progress nobody
	// asked for. A caller that double-clicks gets the running job back.
	if !s.scanning.CompareAndSwap(false, true) {
		if active, err := s.store.ActiveJob(r.Context()); err == nil {
			writeJSON(w, http.StatusAccepted, map[string]any{"job": toJobDTO(active)})
			return
		}
		writeError(w, http.StatusConflict, "scan_in_progress", errors.New("a scan is already running"))
		return
	}

	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		s.scanning.Store(false)
		writeError(w, http.StatusInternalServerError, "list_folders", err)
		return
	}
	var total int64
	for _, f := range folders {
		if body.FolderID == 0 || f.ID == body.FolderID {
			total++
		}
	}

	payload := jobPayload{FolderID: body.FolderID}
	job, err := s.createJob(r.Context(), "scan", "Scan library", payload, body.FolderID, 0, total)
	if err != nil {
		s.scanning.Store(false)
		writeError(w, http.StatusInternalServerError, "create_job", err)
		return
	}

	// Deliberately not r.Context(): that is cancelled the moment this
	// response is written, which would abort the scan we just started.
	go s.runScanJobAsync(context.Background(), job.ID, payload)

	writeJSON(w, http.StatusAccepted, map[string]any{"job": toJobDTO(job)})
}

// runScanJobAsync executes a scan on its own goroutine, keeping the job row
// current as it goes. The synchronous runScanJob remains for job retry,
// where the caller is already waiting.
func (s *Server) runScanJobAsync(ctx context.Context, jobID int64, payload jobPayload) {
	defer s.scanning.Store(false)

	if err := s.store.StartJob(ctx, jobID); err != nil {
		s.cfg.Logger.Printf("start job %d: %v", jobID, err)
	}

	// Progress writes are throttled: the scanner reports per file, and a
	// large library would otherwise turn one scan into hundreds of
	// serialised writes competing with the ingest transactions for the
	// same SQLite handle.
	var lastWrite time.Time
	onProgress := func(p library.ScanProgress) {
		if time.Since(lastWrite) < 200*time.Millisecond && p.Done != p.Total {
			return
		}
		lastWrite = time.Now()
		if err := s.store.UpdateJobProgress(ctx, jobID, int64(p.Done), int64(p.Total), p.Current, string(p.Phase)); err != nil {
			s.cfg.Logger.Printf("job %d progress: %v", jobID, err)
		}
	}

	results, runErr := s.runScanPayloadWithProgress(ctx, payload, onProgress)
	if err := s.store.FinishJob(ctx, jobID, scanResultJobResult(results, runErr)); err != nil {
		s.cfg.Logger.Printf("finish job %d: %v", jobID, err)
	}
}
