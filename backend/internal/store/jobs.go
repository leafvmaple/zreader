package store

import (
	"context"
	"database/sql"
)

const (
	JobStatusQueued  = "queued"
	JobStatusRunning = "running"
	JobStatusDone    = "done"
	JobStatusFailed  = "failed"
)

type LibraryJob struct {
	ID         int64
	Type       string
	Status     string
	Label      sql.NullString
	Payload    sql.NullString
	FolderID   sql.NullInt64
	BookID     sql.NullInt64
	Total      int64
	Completed  int64
	Added      int64
	Updated    int64
	Removed    int64
	Failed     sql.NullString
	Error      sql.NullString
	CreatedAt  int64
	StartedAt  sql.NullInt64
	FinishedAt sql.NullInt64
}

type JobResult struct {
	Total     int64
	Completed int64
	Added     int64
	Updated   int64
	Removed   int64
	Failed    string
	Error     string
}

func (s *Store) CreateJob(ctx context.Context, j LibraryJob) (LibraryJob, error) {
	now := s.nowUnix()
	if j.Status == "" {
		j.Status = JobStatusQueued
	}
	res, err := s.db.ExecContext(ctx, `
        INSERT INTO library_jobs(type, status, label, payload, folder_id, book_id,
                                 total, completed, added, updated, removed, failed,
                                 error, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.Type, j.Status, j.Label, j.Payload, j.FolderID, j.BookID,
		j.Total, j.Completed, j.Added, j.Updated, j.Removed, j.Failed, j.Error, now,
	)
	if err != nil {
		return LibraryJob{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetJob(ctx, id)
}

func (s *Store) StartJob(ctx context.Context, id int64) error {
	now := s.nowUnix()
	_, err := s.db.ExecContext(ctx, `
        UPDATE library_jobs
           SET status = ?, started_at = ?, error = NULL
         WHERE id = ?`, JobStatusRunning, now, id)
	return err
}

func (s *Store) FinishJob(ctx context.Context, id int64, res JobResult) error {
	now := s.nowUnix()
	status := JobStatusDone
	errText := sql.NullString{}
	if res.Error != "" {
		status = JobStatusFailed
		errText = sql.NullString{String: res.Error, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
        UPDATE library_jobs
           SET status = ?, total = ?, completed = ?, added = ?, updated = ?,
               removed = ?, failed = ?, error = ?, finished_at = ?
         WHERE id = ?`,
		status, res.Total, res.Completed, res.Added, res.Updated, res.Removed,
		nullString(res.Failed), errText, now, id,
	)
	return err
}

func (s *Store) GetJob(ctx context.Context, id int64) (LibraryJob, error) {
	var j LibraryJob
	err := s.db.QueryRowContext(ctx, `
        SELECT id, type, status, label, payload, folder_id, book_id, total,
               completed, added, updated, removed, failed, error, created_at,
               started_at, finished_at
          FROM library_jobs
         WHERE id = ?`, id).Scan(
		&j.ID, &j.Type, &j.Status, &j.Label, &j.Payload, &j.FolderID, &j.BookID,
		&j.Total, &j.Completed, &j.Added, &j.Updated, &j.Removed, &j.Failed,
		&j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	)
	return j, err
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]LibraryJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, type, status, label, payload, folder_id, book_id, total,
               completed, added, updated, removed, failed, error, created_at,
               started_at, finished_at
          FROM library_jobs
         ORDER BY created_at DESC, id DESC
         LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LibraryJob
	for rows.Next() {
		var j LibraryJob
		if err := rows.Scan(
			&j.ID, &j.Type, &j.Status, &j.Label, &j.Payload, &j.FolderID, &j.BookID,
			&j.Total, &j.Completed, &j.Added, &j.Updated, &j.Removed, &j.Failed,
			&j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
