package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type chapterSidecar struct {
	Chapters []chapterSidecarEntry `json:"chapters"`
}

type chapterSidecarEntry struct {
	Title      string `json:"title"`
	Match      string `json:"match,omitempty"`
	CharOffset *int   `json:"char_offset,omitempty"`
	Level      int    `json:"level,omitempty"`
}

// ChapterSidecarPath returns the optional sidecar path for a source file.
// Example: BookA.txt -> BookA.chapters.json.
func ChapterSidecarPath(sourcePath string) string {
	ext := filepath.Ext(sourcePath)
	return strings.TrimSuffix(sourcePath, ext) + ".chapters.json"
}

func chaptersFromSidecar(sourcePath, text string) ([]Chapter, bool, error) {
	sidecarPath := ChapterSidecarPath(sourcePath)
	raw, err := os.ReadFile(sidecarPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, true, fmt.Errorf("read chapter sidecar: %w", err)
	}
	var cfg chapterSidecar
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, true, fmt.Errorf("parse chapter sidecar: %w", err)
	}
	if len(cfg.Chapters) == 0 {
		return nil, true, fmt.Errorf("chapter sidecar has no chapters")
	}

	chapters := make([]Chapter, 0, len(cfg.Chapters))
	searchByte := 0
	for i, entry := range cfg.Chapters {
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			return nil, true, fmt.Errorf("chapter sidecar entry %d has empty title", i+1)
		}
		byteOffset := -1
		charOffset := -1
		if entry.CharOffset != nil {
			if *entry.CharOffset < 0 {
				return nil, true, fmt.Errorf("chapter sidecar entry %d has negative char_offset", i+1)
			}
			var ok bool
			byteOffset, ok = byteOffsetForCharOffset(text, *entry.CharOffset)
			if !ok {
				return nil, true, fmt.Errorf("chapter sidecar entry %d char_offset out of range", i+1)
			}
			charOffset = *entry.CharOffset
		} else {
			match := strings.TrimSpace(entry.Match)
			if match == "" {
				return nil, true, fmt.Errorf("chapter sidecar entry %d needs match or char_offset", i+1)
			}
			if searchByte > len(text) {
				searchByte = len(text)
			}
			rel := strings.Index(text[searchByte:], match)
			if rel < 0 {
				return nil, true, fmt.Errorf("chapter sidecar entry %d match not found", i+1)
			}
			byteOffset = searchByte + rel
			charOffset = utf8.RuneCountInString(text[:byteOffset])
			searchByte = byteOffset + len(match)
		}
		chapters = append(chapters, Chapter{
			Idx:        len(chapters) + 1,
			Title:      title,
			Level:      entry.Level,
			ByteOffset: byteOffset,
			CharOffset: charOffset,
		})
		nextSearch := byteOffset
		if nextSearch < len(text) {
			nextSearch++
		}
		if nextSearch > searchByte {
			searchByte = nextSearch
		}
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		return chapters[i].CharOffset < chapters[j].CharOffset
	})
	for i := range chapters {
		chapters[i].Idx = i + 1
	}
	return chapters, true, nil
}

func byteOffsetForCharOffset(text string, want int) (int, bool) {
	if want < 0 {
		return 0, false
	}
	count := 0
	for i := range text {
		if count == want {
			return i, true
		}
		count++
	}
	if count == want {
		return len(text), true
	}
	return 0, false
}
