package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/leafvmaple/zreader/internal/library"
	"github.com/leafvmaple/zreader/internal/store"
)

type tagDTO struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color,omitempty"`
	AddedAt int64  `json:"added_at"`
}

type jobDTO struct {
	ID         int64    `json:"id"`
	Type       string   `json:"type"`
	Status     string   `json:"status"`
	Label      string   `json:"label,omitempty"`
	FolderID   int64    `json:"folder_id,omitempty"`
	BookID     int64    `json:"book_id,omitempty"`
	Total      int64    `json:"total"`
	Completed  int64    `json:"completed"`
	Added      int64    `json:"added"`
	Updated    int64    `json:"updated"`
	Removed    int64    `json:"removed"`
	Failed     []string `json:"failed,omitempty"`
	Error      string   `json:"error,omitempty"`
	CreatedAt  int64    `json:"created_at"`
	StartedAt  int64    `json:"started_at,omitempty"`
	FinishedAt int64    `json:"finished_at,omitempty"`
}

type jobPayload struct {
	FolderID      int64    `json:"folder_id,omitempty"`
	SourcePaths   []string `json:"source_paths,omitempty"`
	BookIDs       []int64  `json:"book_ids,omitempty"`
	Action        string   `json:"action,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	ReadingStatus string   `json:"reading_status,omitempty"`
	Favorite      *bool    `json:"favorite,omitempty"`
	DeleteSource  bool     `json:"delete_source,omitempty"`
}

func toTagDTO(t store.Tag) tagDTO {
	d := tagDTO{ID: t.ID, Name: t.Name, AddedAt: t.AddedAt}
	if t.Color.Valid {
		d.Color = t.Color.String
	}
	return d
}

func toJobDTO(j store.LibraryJob) jobDTO {
	d := jobDTO{
		ID: j.ID, Type: j.Type, Status: j.Status, Total: j.Total, Completed: j.Completed,
		Added: j.Added, Updated: j.Updated, Removed: j.Removed, CreatedAt: j.CreatedAt,
	}
	if j.Label.Valid {
		d.Label = j.Label.String
	}
	if j.FolderID.Valid {
		d.FolderID = j.FolderID.Int64
	}
	if j.BookID.Valid {
		d.BookID = j.BookID.Int64
	}
	if j.Failed.Valid && j.Failed.String != "" {
		_ = json.Unmarshal([]byte(j.Failed.String), &d.Failed)
	}
	if j.Error.Valid {
		d.Error = j.Error.String
	}
	if j.StartedAt.Valid {
		d.StartedAt = j.StartedAt.Int64
	}
	if j.FinishedAt.Valid {
		d.FinishedAt = j.FinishedAt.Int64
	}
	return d
}

func (s *Server) handlePatchBook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	var body struct {
		Title         *string   `json:"title"`
		Author        *string   `json:"author"`
		Description   *string   `json:"description"`
		Category      *string   `json:"category"`
		Favorite      *bool     `json:"favorite"`
		ReadingStatus *string   `json:"reading_status"`
		CoverColor    *string   `json:"cover_color"`
		CoverLabel    *string   `json:"cover_label"`
		Tags          *[]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	if body.Tags != nil {
		if err := store.ValidateTagNames(*body.Tags); err != nil {
			writeError(w, http.StatusBadRequest, "bad_tags", err)
			return
		}
	}
	book, err := s.store.UpdateBookMetadata(r.Context(), id, store.BookUpdate{
		Title: body.Title, Author: body.Author, Description: body.Description,
		Category: body.Category, Favorite: body.Favorite, ReadingStatus: body.ReadingStatus,
		CoverColor: body.CoverColor, CoverLabel: body.CoverLabel,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusBadRequest, "update_book", err)
		return
	}
	if body.Tags != nil {
		if _, err := s.store.SetBookTags(r.Context(), id, *body.Tags); err != nil {
			writeError(w, http.StatusInternalServerError, "set_tags", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"book": s.toBookDTOWithTags(r, book)})
}

func (s *Server) handleSetBookTags(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	if err := store.ValidateTagNames(body.Tags); err != nil {
		writeError(w, http.StatusBadRequest, "bad_tags", err)
		return
	}
	tags, err := s.store.SetBookTags(r.Context(), id, body.Tags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "set_tags", err)
		return
	}
	out := make([]tagDTO, 0, len(tags))
	for _, t := range tags {
		out = append(out, toTagDTO(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": out})
}

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.ListTags(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_tags", err)
		return
	}
	out := make([]tagDTO, 0, len(tags))
	for _, t := range tags {
		out = append(out, toTagDTO(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": out})
}

func (s *Server) handleDuplicateBooks(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.DuplicateGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "duplicates", err)
		return
	}
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		books := make([]bookDTO, 0, len(g.Books))
		for _, b := range g.Books {
			books = append(books, s.toBookDTOWithTags(r, b))
		}
		out = append(out, map[string]any{"hash": g.Hash, "books": books})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(r.Context(), parseIntQuery(r, "limit", 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_jobs", err)
		return
	}
	out := make([]jobDTO, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toJobDTO(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	j, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "get_job", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": toJobDTO(j)})
}

func (s *Server) handleBatchBooks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action        string   `json:"action"`
		BookIDs       []int64  `json:"book_ids"`
		Tags          []string `json:"tags"`
		ReadingStatus string   `json:"reading_status"`
		Favorite      *bool    `json:"favorite"`
		DeleteSource  bool     `json:"delete_source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	body.Action = strings.TrimSpace(strings.ToLower(body.Action))
	if len(body.BookIDs) == 0 {
		writeError(w, http.StatusBadRequest, "missing_books", errors.New("book_ids is required"))
		return
	}
	if err := store.ValidateTagNames(body.Tags); err != nil {
		writeError(w, http.StatusBadRequest, "bad_tags", err)
		return
	}
	switch body.Action {
	case "tag", "untag":
		if len(cleanRequestTags(body.Tags)) == 0 {
			writeError(w, http.StatusBadRequest, "missing_tags", errors.New("tags is required"))
			return
		}
	case "status":
		if _, ok := store.NormaliseReadingStatus(body.ReadingStatus); !ok {
			writeError(w, http.StatusBadRequest, "bad_status", errors.New("invalid reading_status"))
			return
		}
	case "favorite":
		if body.Favorite == nil {
			writeError(w, http.StatusBadRequest, "missing_favorite", errors.New("favorite is required"))
			return
		}
	case "reparse", "delete":
	default:
		writeError(w, http.StatusBadRequest, "bad_action", fmt.Errorf("unsupported batch action %q", body.Action))
		return
	}
	payload := jobPayload{
		BookIDs: body.BookIDs, Action: body.Action, Tags: body.Tags,
		ReadingStatus: body.ReadingStatus, Favorite: body.Favorite, DeleteSource: body.DeleteSource,
	}
	job, err := s.createJob(r.Context(), "batch", "Batch books", payload, 0, 0, int64(len(body.BookIDs)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_job", err)
		return
	}
	res := s.runBatchJob(r.Context(), job.ID, payload)
	writeJSON(w, http.StatusOK, map[string]any{"job": toJobDTO(res)})
}

func cleanRequestTags(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (s *Server) runBatchJob(ctx context.Context, jobID int64, payload jobPayload) store.LibraryJob {
	_ = s.store.StartJob(ctx, jobID)
	var result store.JobResult
	result.Total = int64(len(payload.BookIDs))
	result.Completed = result.Total
	var failed []string
	fail := func(bookID int64, err error) {
		failed = append(failed, fmt.Sprintf("%d: %v", bookID, err))
	}
	switch payload.Action {
	case "tag":
		if err := s.store.AddTagsToBooks(ctx, payload.BookIDs, payload.Tags); err != nil {
			result.Error = err.Error()
		} else {
			result.Updated = int64(len(payload.BookIDs))
		}
	case "untag":
		if err := s.store.RemoveTagsFromBooks(ctx, payload.BookIDs, payload.Tags); err != nil {
			result.Error = err.Error()
		} else {
			result.Updated = int64(len(payload.BookIDs))
		}
	case "status":
		status, ok := store.NormaliseReadingStatus(payload.ReadingStatus)
		if !ok {
			result.Error = "invalid reading_status"
			break
		}
		for _, id := range payload.BookIDs {
			if _, err := s.store.UpdateBookMetadata(ctx, id, store.BookUpdate{ReadingStatus: &status}); err != nil {
				fail(id, err)
				continue
			}
			result.Updated++
		}
	case "favorite":
		if payload.Favorite == nil {
			result.Error = "favorite is required"
			break
		}
		for _, id := range payload.BookIDs {
			if _, err := s.store.UpdateBookMetadata(ctx, id, store.BookUpdate{Favorite: payload.Favorite}); err != nil {
				fail(id, err)
				continue
			}
			result.Updated++
		}
	case "reparse":
		scanner := &library.Scanner{Store: s.store, Logger: s.cfg.Logger}
		for _, id := range payload.BookIDs {
			book, folder, err := s.bookAndFolderByID(ctx, id)
			if err != nil {
				fail(id, err)
				continue
			}
			res, err := scanner.ReparseBook(ctx, folder, book)
			if err != nil {
				fail(id, err)
				continue
			}
			result.Added += int64(res.Added)
			result.Updated += int64(res.Updated)
			result.Removed += int64(res.Removed)
		}
	case "delete":
		for _, id := range payload.BookIDs {
			book, folder, err := s.bookAndFolderByID(ctx, id)
			if err != nil {
				fail(id, err)
				continue
			}
			if err := s.deleteBook(ctx, book, folder, payload.DeleteSource); err != nil {
				fail(id, err)
				continue
			}
			result.Removed++
		}
	default:
		result.Error = "unsupported batch action"
	}
	if len(failed) > 0 {
		b, _ := json.Marshal(failed)
		result.Failed = string(b)
	}
	if err := s.store.FinishJob(ctx, jobID, result); err != nil {
		s.cfg.Logger.Printf("finish job %d: %v", jobID, err)
	}
	j, _ := s.store.GetJob(ctx, jobID)
	return j
}

func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	old, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "get_job", err)
		return
	}
	var payload jobPayload
	if old.Payload.Valid && old.Payload.String != "" {
		_ = json.Unmarshal([]byte(old.Payload.String), &payload)
	}
	job, err := s.createJob(r.Context(), old.Type, retryLabel(old), payload, payload.FolderID, 0, old.Total)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_job", err)
		return
	}
	var done store.LibraryJob
	switch old.Type {
	case "scan":
		done = s.runScanJob(r.Context(), job.ID, payload)
	case "import":
		done = s.runImportJob(r.Context(), job.ID, payload)
	case "batch":
		done = s.runBatchJob(r.Context(), job.ID, payload)
	default:
		writeError(w, http.StatusBadRequest, "not_retryable", fmt.Errorf("job type %q is not retryable", old.Type))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": toJobDTO(done)})
}

func retryLabel(j store.LibraryJob) string {
	if j.Label.Valid && j.Label.String != "" {
		return "Retry " + j.Label.String
	}
	return "Retry " + j.Type
}

func (s *Server) createJob(ctx context.Context, typ, label string, payload jobPayload, folderID, bookID, total int64) (store.LibraryJob, error) {
	raw, _ := json.Marshal(payload)
	j := store.LibraryJob{
		Type: typ, Status: store.JobStatusQueued, Label: sql.NullString{String: label, Valid: label != ""},
		Payload: sql.NullString{String: string(raw), Valid: true}, Total: total,
	}
	if folderID > 0 {
		j.FolderID = sql.NullInt64{Int64: folderID, Valid: true}
	}
	if bookID > 0 {
		j.BookID = sql.NullInt64{Int64: bookID, Valid: true}
	}
	return s.store.CreateJob(ctx, j)
}

func scanResultJobResult(results []library.ScanResult, failedErr error) store.JobResult {
	var out store.JobResult
	out.Total = int64(len(results))
	out.Completed = out.Total
	var failed []string
	for _, res := range results {
		out.Added += int64(res.Added)
		out.Updated += int64(res.Updated)
		out.Removed += int64(res.Removed)
		failed = append(failed, res.Failed...)
	}
	if len(failed) > 0 {
		b, _ := json.Marshal(failed)
		out.Failed = string(b)
	}
	if failedErr != nil {
		out.Error = failedErr.Error()
	}
	return out
}

func (s *Server) runScanPayload(ctx context.Context, payload jobPayload) ([]library.ScanResult, error) {
	scanner := &library.Scanner{Store: s.store, Logger: s.cfg.Logger}
	folders, err := s.store.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	var results []library.ScanResult
	var runErr error
	for _, f := range folders {
		if payload.FolderID != 0 && f.ID != payload.FolderID {
			continue
		}
		res, err := scanner.ScanFolder(ctx, f)
		if err != nil {
			s.cfg.Logger.Printf("scan folder %d (%s) failed: %v", f.ID, f.Path, err)
			runErr = err
		}
		results = append(results, res)
	}
	return results, runErr
}

func (s *Server) runImportPayload(ctx context.Context, payload jobPayload) ([]library.ScanResult, error) {
	folder, err := s.store.GetFolder(ctx, payload.FolderID)
	if err != nil {
		return nil, err
	}
	scanner := &library.Scanner{Store: s.store, Logger: s.cfg.Logger}
	res, err := scanner.ScanSourceFiles(ctx, folder, payload.SourcePaths)
	return []library.ScanResult{res}, err
}

func (s *Server) runScanJob(ctx context.Context, jobID int64, payload jobPayload) store.LibraryJob {
	_ = s.store.StartJob(ctx, jobID)
	results, err := s.runScanPayload(ctx, payload)
	if err != nil && len(results) == 0 {
		_ = s.store.FinishJob(ctx, jobID, store.JobResult{Error: err.Error()})
		j, _ := s.store.GetJob(ctx, jobID)
		return j
	}
	if err := s.store.FinishJob(ctx, jobID, scanResultJobResult(results, err)); err != nil {
		s.cfg.Logger.Printf("finish job %d: %v", jobID, err)
	}
	j, _ := s.store.GetJob(ctx, jobID)
	return j
}

func (s *Server) runImportJob(ctx context.Context, jobID int64, payload jobPayload) store.LibraryJob {
	_ = s.store.StartJob(ctx, jobID)
	results, err := s.runImportPayload(ctx, payload)
	if err != nil && len(results) == 0 {
		_ = s.store.FinishJob(ctx, jobID, store.JobResult{Error: err.Error()})
		j, _ := s.store.GetJob(ctx, jobID)
		return j
	}
	if err := s.store.FinishJob(ctx, jobID, scanResultJobResult(results, err)); err != nil {
		s.cfg.Logger.Printf("finish job %d: %v", jobID, err)
	}
	j, _ := s.store.GetJob(ctx, jobID)
	return j
}

func (s *Server) bookAndFolderByID(ctx context.Context, id int64) (store.Book, store.Folder, error) {
	book, err := s.store.GetBook(ctx, id)
	if err != nil {
		return store.Book{}, store.Folder{}, err
	}
	folder, err := s.store.GetFolder(ctx, book.FolderID)
	if err != nil {
		return store.Book{}, store.Folder{}, err
	}
	return book, folder, nil
}
