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
	ID        int64
	Type      string
	Status    string
	Label     sql.NullString
	Payload   sql.NullString
	FolderID  sql.NullInt64
	BookID    sql.NullInt64
	Total     int64
	Completed int64
	Added     int64
	Updated   int64
	Removed   int64
	Failed    sql.NullString
	Error     sql.NullString
	// Detail is what a running job is working on right now — a filename.
	// Only meaningful while Status is running; finished jobs clear it.
	Detail sql.NullString
	// Phase is which pass a running scan is in — see library.ScanPhase.
	Phase      sql.NullString
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
               completed, added, updated, removed, failed, error, detail, phase, created_at,
               started_at, finished_at
          FROM library_jobs
         WHERE id = ?`, id).Scan(
		&j.ID, &j.Type, &j.Status, &j.Label, &j.Payload, &j.FolderID, &j.BookID,
		&j.Total, &j.Completed, &j.Added, &j.Updated, &j.Removed, &j.Failed,
		&j.Error, &j.Detail, &j.Phase, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	)
	return j, err
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]LibraryJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, type, status, label, payload, folder_id, book_id, total,
               completed, added, updated, removed, failed, error, detail, phase, created_at,
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
			&j.Error, &j.Detail, &j.Phase, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// UpdateJobProgress records how far a running job has got.
//
// Called once per file, so it is deliberately a narrow UPDATE rather than a
// full row write: a scan of several hundred books would otherwise rewrite
// every counter, including the ones only FinishJob is allowed to set.
func (s *Store) UpdateJobProgress(ctx context.Context, id, completed, total int64, detail, phase string) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE library_jobs
           SET completed = ?, total = ?, detail = ?, phase = ?
         WHERE id = ?`, completed, total, nullString(detail), nullString(phase), id)
	return err
}

// ActiveJob returns the job currently queued or running, if any.
//
// This is what lets the UI re-attach after a reload: a scan runs on the
// server, not in the tab that started it, so a refresh mid-scan should pick
// the progress back up rather than pretend nothing is happening.
func (s *Store) ActiveJob(ctx context.Context) (LibraryJob, error) {
	var j LibraryJob
	err := s.db.QueryRowContext(ctx, `
        SELECT id, type, status, label, payload, folder_id, book_id, total,
               completed, added, updated, removed, failed, error, detail, phase, created_at,
               started_at, finished_at
          FROM library_jobs
         WHERE status IN (?, ?)
         ORDER BY created_at DESC, id DESC
         LIMIT 1`, JobStatusQueued, JobStatusRunning).Scan(
		&j.ID, &j.Type, &j.Status, &j.Label, &j.Payload, &j.FolderID, &j.BookID,
		&j.Total, &j.Completed, &j.Added, &j.Updated, &j.Removed, &j.Failed,
		&j.Error, &j.Detail, &j.Phase, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	)
	return j, err
}

// FailStaleJobs marks jobs left running by a previous process as failed.
//
// A scan runs in a goroutine, so a restart mid-scan leaves its row saying
// "running" forever — which would make ActiveJob report a job nobody is
// working on, and the UI wait on progress that will never arrive.
func (s *Store) FailStaleJobs(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
        UPDATE library_jobs
           SET status = ?, error = ?, detail = NULL, phase = NULL, finished_at = ?
         WHERE status IN (?, ?)`,
		JobStatusFailed, "interrupted by a server restart", s.nowUnix(),
		JobStatusQueued, JobStatusRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
