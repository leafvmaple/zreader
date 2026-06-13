package library

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	pdf "rsc.io/pdf"
)

// ErrPDFNoText marks a scanned/image-only PDF. Supporting these requires OCR
// or a dedicated page-image reader mode; there is no text layer to feed into
// the existing TXT-style pipeline.
var ErrPDFNoText = errors.New("pdf has no extractable text layer")

const (
	pdfLineYTolerance     = 2.5
	minPDFExtractedRunes  = 20
	pdfInterWordGapFactor = 0.20
)

type PDFText struct {
	Title  string
	Author string
	Text   string
	Pages  int
}

// ExtractPDFText extracts the selectable text layer from a PDF and returns a
// paragraph-ish plain text view. Each visual line becomes a paragraph so the
// downstream EPUB builder does not drop body text when the PDF lacks blank
// paragraph separators.
func ExtractPDFText(pdfPath string) (out PDFText, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("read pdf: %v", v)
		}
	}()

	f, err := os.Open(pdfPath)
	if err != nil {
		return out, fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return out, fmt.Errorf("stat pdf: %w", err)
	}
	r, err := pdf.NewReader(f, st.Size())
	if err != nil {
		return out, fmt.Errorf("open pdf reader: %w", err)
	}

	info := r.Trailer().Key("Info")
	out.Title = strings.TrimSpace(info.Key("Title").Text())
	out.Author = strings.TrimSpace(info.Key("Author").Text())
	out.Pages = r.NumPage()
	if out.Pages == 0 {
		return out, fmt.Errorf("pdf has no pages")
	}

	pages := make([]string, 0, out.Pages)
	for i := 1; i <= out.Pages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text := pdfPageText(page.Content().Text)
		if text != "" {
			pages = append(pages, text)
		}
	}
	out.Text = ensureTrailingLF(strings.TrimSpace(strings.Join(pages, "\n\n")))
	if countNonSpaceRunes(out.Text) < minPDFExtractedRunes {
		return out, fmt.Errorf("%w: scanned/image PDFs need OCR before import", ErrPDFNoText)
	}
	return out, nil
}

func pdfPageText(items []pdf.Text) string {
	if len(items) == 0 {
		return ""
	}
	sort.SliceStable(items, func(i, j int) bool {
		if math.Abs(items[i].Y-items[j].Y) > pdfLineYTolerance {
			return items[i].Y > items[j].Y
		}
		return items[i].X < items[j].X
	})

	var lines []string
	var current []pdf.Text
	currentY := 0.0

	flush := func() {
		if len(current) == 0 {
			return
		}
		sort.SliceStable(current, func(i, j int) bool { return current[i].X < current[j].X })
		line := pdfLineText(current)
		if line != "" {
			lines = append(lines, line)
		}
		current = nil
	}

	for _, item := range items {
		item.S = cleanPDFSpan(item.S)
		if item.S == "" {
			continue
		}
		if len(current) == 0 {
			current = append(current, item)
			currentY = item.Y
			continue
		}
		if math.Abs(item.Y-currentY) > pdfLineYTolerance {
			flush()
			current = append(current, item)
			currentY = item.Y
			continue
		}
		current = append(current, item)
		currentY = (currentY*float64(len(current)-1) + item.Y) / float64(len(current))
	}
	flush()
	return strings.Join(lines, "\n\n")
}

func pdfLineText(items []pdf.Text) string {
	var b strings.Builder
	prevEnd := 0.0
	prevFontSize := 0.0
	prevText := ""
	for i, item := range items {
		if i > 0 && shouldInsertPDFSpace(prevText, item.S, item.X-prevEnd, prevFontSize) {
			b.WriteByte(' ')
		}
		b.WriteString(item.S)
		prevEnd = item.X + item.W
		prevFontSize = item.FontSize
		prevText = item.S
	}
	return strings.TrimSpace(b.String())
}

func cleanPDFSpan(s string) string {
	if s != "" && strings.TrimSpace(s) == "" {
		return " "
	}
	return strings.Join(strings.Fields(s), " ")
}

func shouldInsertPDFSpace(prev, next string, gap, fontSize float64) bool {
	if gap <= math.Max(1.5, fontSize*pdfInterWordGapFactor) {
		return false
	}
	return isASCIIWord(lastRune(prev)) && isASCIIWord(firstRune(next))
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

func isASCIIWord(r rune) bool {
	return r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func countNonSpaceRunes(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}
