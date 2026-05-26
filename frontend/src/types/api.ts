// Wire shapes returned by the Go backend (see backend/internal/server/handlers_*.go).
// Keep these field names in lockstep with the JSON tags on the DTO structs.

export type Folder = {
  id: number;
  path: string;
  added_at: number;
  last_scan_at?: number;
  book_count: number;
};

export type Book = {
  id: number;
  folder_id: number;
  path: string;
  title: string;
  author?: string;
  format: string;
  encoding?: string;
  size_bytes: number;
  char_count?: number;
  chapter_count?: number;
  file_mtime: number;
  added_at: number;
  scanned_at: number;
};

export type Chapter = {
  idx: number;
  title: string;
  char_offset: number;
};

export type Progress = {
  book_id: number;
  char_offset: number;
  chapter_idx: number;
  chapter_offset: number;
  updated_at: number;
};

export type ContentSlice = {
  text: string;
  from: number;
  len: number;
  total_chars: number;
  eof: boolean;
};

export type ScanResult = {
  folder_id: number;
  path: string;
  added: number;
  updated: number;
  removed: number;
  failed?: string[];
};
