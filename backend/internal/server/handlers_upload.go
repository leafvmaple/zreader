package server

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leafvmaple/zreader/internal/library"
	"github.com/leafvmaple/zreader/internal/store"
)

const (
	maxUploadRequestBytes = 250 * 1024 * 1024
	maxMultipartMemory    = 32 * 1024 * 1024
)

type uploadedFileDTO struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

func (s *Server) handleUploadBooks(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		writeError(w, http.StatusBadRequest, "bad_upload", err)
		return
	}

	folder, err := s.uploadTargetFolder(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_folder", err)
		return
	}

	files := uploadFileHeaders(r.MultipartForm)
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "missing_file", errors.New("file is required"))
		return
	}
	for _, fh := range files {
		if !library.IsSupportedSource(fh.Filename) {
			writeError(w, http.StatusBadRequest, "unsupported_format", fmt.Errorf("unsupported file type %q", strings.ToLower(filepath.Ext(fh.Filename))))
			return
		}
	}

	uploaded := make([]uploadedFileDTO, 0, len(files))
	for _, fh := range files {
		u, err := saveUploadedBook(folder.Path, fh)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "save_upload", err)
			return
		}
		uploaded = append(uploaded, u)
	}

	scanner := &library.Scanner{Store: s.store, Logger: s.cfg.Logger}
	scan, err := scanner.ScanFolder(r.Context(), folder)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_uploaded", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"uploaded": uploaded,
		"scan":     scan,
	})
}

func (s *Server) uploadTargetFolder(r *http.Request) (store.Folder, error) {
	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		return store.Folder{}, fmt.Errorf("list folders: %w", err)
	}
	if len(folders) == 0 {
		return store.Folder{}, errors.New("no library folder configured")
	}

	raw := strings.TrimSpace(r.FormValue("folder_id"))
	if raw == "" {
		return folders[0], nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return store.Folder{}, fmt.Errorf("invalid folder_id")
	}
	for _, f := range folders {
		if f.ID == id {
			return f, nil
		}
	}
	return store.Folder{}, fmt.Errorf("folder %d not found", id)
}

func uploadFileHeaders(form *multipart.Form) []*multipart.FileHeader {
	if form == nil || form.File == nil {
		return nil
	}
	var out []*multipart.FileHeader
	out = append(out, form.File["file"]...)
	out = append(out, form.File["files"]...)
	return out
}

func saveUploadedBook(folder string, fh *multipart.FileHeader) (uploadedFileDTO, error) {
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return uploadedFileDTO{}, fmt.Errorf("mkdir library folder: %w", err)
	}
	target, err := uniqueUploadPath(folder, fh.Filename)
	if err != nil {
		return uploadedFileDTO{}, err
	}

	src, err := fh.Open()
	if err != nil {
		return uploadedFileDTO{}, fmt.Errorf("open upload: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return uploadedFileDTO{}, fmt.Errorf("create target: %w", err)
	}
	n, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return uploadedFileDTO{}, fmt.Errorf("copy upload: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return uploadedFileDTO{}, fmt.Errorf("close upload: %w", closeErr)
	}

	return uploadedFileDTO{
		Name:      filepath.Base(target),
		Path:      target,
		SizeBytes: n,
	}, nil
}

func uniqueUploadPath(folder, filename string) (string, error) {
	name := sanitizeUploadFilename(filename)
	ext := strings.ToLower(filepath.Ext(name))
	if !library.IsSupportedSource("x" + ext) {
		return "", fmt.Errorf("unsupported file type %q", ext)
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	for i := 0; i < 1000; i++ {
		candidate := stem + ext
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(folder, candidate)
		if filepath.Dir(p) != filepath.Clean(folder) {
			return "", fmt.Errorf("invalid upload target")
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p, nil
		} else if err != nil {
			return "", fmt.Errorf("stat target: %w", err)
		}
	}
	return "", fmt.Errorf("too many duplicate filenames")
}

func sanitizeUploadFilename(filename string) string {
	name := filepath.Base(strings.TrimSpace(filename))
	ext := strings.ToLower(filepath.Ext(name))
	stem := strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
	stem = strings.Trim(stem, ".")
	if stem == "" {
		stem = "book"
	}
	var b strings.Builder
	b.Grow(len(stem))
	for _, r := range stem {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	stem = strings.TrimSpace(b.String())
	if stem == "" {
		stem = "book"
	}
	return stem + ext
}
