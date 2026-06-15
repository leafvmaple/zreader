package library

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
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

// InspectPDF reads metadata and page count without requiring a text layer.
func InspectPDF(pdfPath string) (out PDFText, err error) {
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
	return out, nil
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
	decoderCache := map[string]pdfGlyphDecoder{}
	for i := 1; i <= out.Pages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text := pdfPageText(extractPDFPageText(page, decoderCache))
		if text != "" {
			pages = append(pages, text)
		}
	}
	pages = stripRepeatedPDFEdgeLines(pages)
	out.Text = ensureTrailingLF(strings.TrimSpace(strings.Join(pages, "\n\n")))
	if countNonSpaceRunes(out.Text) < minPDFExtractedRunes {
		return out, fmt.Errorf("%w: scanned/image PDFs need OCR before import", ErrPDFNoText)
	}
	return out, nil
}

type pdfContentState struct {
	Tc      float64
	Tw      float64
	Th      float64
	Tl      float64
	Tf      pdf.Font
	Tfs     float64
	Tmode   int
	Trise   float64
	Tm      pdfMatrix
	Tlm     pdfMatrix
	CTM     pdfMatrix
	decoder pdfGlyphDecoder
}

type pdfMatrix [3][3]float64

var pdfIdentityMatrix = pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

func (x pdfMatrix) mul(y pdfMatrix) pdfMatrix {
	var z pdfMatrix
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				z[i][j] += x[i][k] * y[k][j]
			}
		}
	}
	return z
}

func extractPDFPageText(page pdf.Page, decoderCache map[string]pdfGlyphDecoder) []pdf.Text {
	var text []pdf.Text
	g := pdfContentState{
		Th:  1,
		CTM: pdfIdentityMatrix,
	}
	var gstack []pdfContentState

	showText := func(raw string) {
		if g.decoder == nil {
			g.decoder = newPDFGlyphDecoder(g.Tf)
		}
		for _, glyph := range g.decoder.Decode(raw) {
			trm := pdfMatrix{{g.Tfs * g.Th, 0, 0}, {0, g.Tfs, 0}, {0, g.Trise, 1}}.mul(g.Tm).mul(g.CTM)
			w0 := g.Tf.Width(glyph.Code)
			if w0 == 0 {
				w0 = fallbackPDFGlyphWidth(glyph.Text)
			}
			if glyph.Text != "" && glyph.Text != " " {
				font := g.Tf.BaseFont()
				if i := strings.Index(font, "+"); i >= 0 {
					font = font[i+1:]
				}
				text = append(text, pdf.Text{
					Font:     font,
					FontSize: trm[0][0],
					X:        trm[2][0],
					Y:        trm[2][1],
					W:        w0 / 1000 * trm[0][0],
					S:        glyph.Text,
				})
			}
			tx := w0/1000*g.Tfs + g.Tc
			if glyph.Text == " " {
				tx += g.Tw
			}
			tx *= g.Th
			g.Tm = pdfMatrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(g.Tm)
		}
	}

	interpret := func(strm pdf.Value) {
		if strm.Kind() == pdf.Null {
			return
		}
		pdf.Interpret(strm, func(stk *pdf.Stack, op string) {
			args := pdfStackArgs(stk)
			switch op {
			case "cm":
				if len(args) != 6 {
					return
				}
				var m pdfMatrix
				for i := 0; i < 6; i++ {
					m[i/2][i%2] = args[i].Float64()
				}
				m[2][2] = 1
				g.CTM = m.mul(g.CTM)

			case "q":
				gstack = append(gstack, g)

			case "Q":
				if len(gstack) == 0 {
					return
				}
				g = gstack[len(gstack)-1]
				gstack = gstack[:len(gstack)-1]

			case "BT":
				g.Tm = pdfIdentityMatrix
				g.Tlm = g.Tm

			case "ET":

			case "T*":
				x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
				g.Tlm = x.mul(g.Tlm)
				g.Tm = g.Tlm

			case "Tc":
				if len(args) == 1 {
					g.Tc = args[0].Float64()
				}

			case "TD", "Td":
				if len(args) != 2 {
					return
				}
				tx := args[0].Float64()
				ty := args[1].Float64()
				if op == "TD" {
					g.Tl = -ty
				}
				x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {tx, ty, 1}}
				g.Tlm = x.mul(g.Tlm)
				g.Tm = g.Tlm

			case "Tf":
				if len(args) != 2 {
					return
				}
				g.Tf = page.Font(args[0].Name())
				cacheKey := pdfFontDecoderCacheKey(args[0].Name(), g.Tf)
				if cached, ok := decoderCache[cacheKey]; ok {
					g.decoder = cached
				} else {
					g.decoder = newPDFGlyphDecoder(g.Tf)
					decoderCache[cacheKey] = g.decoder
				}
				g.Tfs = args[1].Float64()

			case "\"":
				if len(args) != 3 {
					return
				}
				g.Tw = args[0].Float64()
				g.Tc = args[1].Float64()
				x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
				g.Tlm = x.mul(g.Tlm)
				g.Tm = g.Tlm
				showText(args[2].RawString())

			case "'":
				if len(args) != 1 {
					return
				}
				x := pdfMatrix{{1, 0, 0}, {0, 1, 0}, {0, -g.Tl, 1}}
				g.Tlm = x.mul(g.Tlm)
				g.Tm = g.Tlm
				showText(args[0].RawString())

			case "Tj":
				if len(args) == 1 {
					showText(args[0].RawString())
				}

			case "TJ":
				if len(args) != 1 {
					return
				}
				v := args[0]
				for i := 0; i < v.Len(); i++ {
					x := v.Index(i)
					if x.Kind() == pdf.String {
						showText(x.RawString())
					} else {
						tx := -x.Float64() / 1000 * g.Tfs * g.Th
						g.Tm = pdfMatrix{{1, 0, 0}, {0, 1, 0}, {tx, 0, 1}}.mul(g.Tm)
					}
				}

			case "TL":
				if len(args) == 1 {
					g.Tl = args[0].Float64()
				}

			case "Tm":
				if len(args) != 6 {
					return
				}
				var m pdfMatrix
				for i := 0; i < 6; i++ {
					m[i/2][i%2] = args[i].Float64()
				}
				m[2][2] = 1
				g.Tm = m
				g.Tlm = m

			case "Tr":
				if len(args) == 1 {
					g.Tmode = int(args[0].Int64())
				}

			case "Ts":
				if len(args) == 1 {
					g.Trise = args[0].Float64()
				}

			case "Tw":
				if len(args) == 1 {
					g.Tw = args[0].Float64()
				}

			case "Tz":
				if len(args) == 1 {
					g.Th = args[0].Float64() / 100
				}
			}
		})
	}

	contents := page.V.Key("Contents")
	if contents.Kind() == pdf.Array {
		for i := 0; i < contents.Len(); i++ {
			interpret(contents.Index(i))
		}
	} else {
		interpret(contents)
	}
	return text
}

func pdfFontDecoderCacheKey(name string, font pdf.Font) string {
	return name + "\x00" + font.V.String()
}

func pdfStackArgs(stk *pdf.Stack) []pdf.Value {
	n := stk.Len()
	args := make([]pdf.Value, n)
	for i := n - 1; i >= 0; i-- {
		args[i] = stk.Pop()
	}
	return args
}

func fallbackPDFGlyphWidth(s string) float64 {
	if s == " " {
		return 250
	}
	for _, r := range s {
		if r > unicode.MaxASCII {
			return 1000
		}
	}
	return 450
}

type pdfDecodedGlyph struct {
	Text string
	Code int
}

type pdfGlyphDecoder interface {
	Decode(raw string) []pdfDecodedGlyph
}

func newPDFGlyphDecoder(font pdf.Font) pdfGlyphDecoder {
	if cmap := readPDFToUnicodeCMap(font.V.Key("ToUnicode")); cmap != nil && !cmap.empty() {
		return cmap
	}
	if font.V.Key("Encoding").Name() == "Identity-H" {
		return pdfIdentityHDecoder{}
	}
	return pdfSingleByteDecoder{enc: font.Encoder()}
}

type pdfSingleByteDecoder struct {
	enc pdf.TextEncoding
}

func (d pdfSingleByteDecoder) Decode(raw string) []pdfDecodedGlyph {
	out := make([]pdfDecodedGlyph, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		text := string(raw[i])
		if d.enc != nil {
			text = d.enc.Decode(raw[i : i+1])
		}
		if text == "" {
			text = string(utf8.RuneError)
		}
		out = append(out, pdfDecodedGlyph{Text: text, Code: int(raw[i])})
	}
	return out
}

type pdfIdentityHDecoder struct{}

func (pdfIdentityHDecoder) Decode(raw string) []pdfDecodedGlyph {
	out := make([]pdfDecodedGlyph, 0, len(raw)/2)
	for len(raw) > 0 {
		n := 2
		if len(raw) < n {
			n = len(raw)
		}
		code := raw[:n]
		raw = raw[n:]
		out = append(out, pdfDecodedGlyph{
			Text: decodePDFCMapString(code),
			Code: pdfCodeValue(code),
		})
	}
	return out
}

type pdfCMap struct {
	space   [4][]pdfCodeRange
	chars   map[string]string
	ranges  []pdfBFRange
	hasData bool
}

type pdfCodeRange struct {
	lo string
	hi string
}

type pdfBFRange struct {
	lo       string
	hi       string
	dstStart string
	dstArray []string
}

func (m *pdfCMap) empty() bool {
	return m == nil || !m.hasData
}

func (m *pdfCMap) Decode(raw string) []pdfDecodedGlyph {
	var out []pdfDecodedGlyph
	for len(raw) > 0 {
		n := m.codeLength(raw)
		if n <= 0 || n > len(raw) {
			n = 1
		}
		code := raw[:n]
		raw = raw[n:]
		text := m.lookup(code)
		if text == "" {
			text = fallbackPDFCodeText(code)
		}
		out = append(out, pdfDecodedGlyph{Text: text, Code: pdfCodeValue(code)})
	}
	return out
}

func (m *pdfCMap) codeLength(raw string) int {
	for n := 1; n <= 4 && n <= len(raw); n++ {
		for _, space := range m.space[n-1] {
			if space.lo <= raw[:n] && raw[:n] <= space.hi {
				return n
			}
		}
	}
	return 0
}

func (m *pdfCMap) lookup(code string) string {
	if text, ok := m.chars[code]; ok {
		return text
	}
	for _, r := range m.ranges {
		if len(r.lo) != len(code) || code < r.lo || r.hi < code {
			continue
		}
		offset := pdfCodeValue(code) - pdfCodeValue(r.lo)
		if len(r.dstArray) > 0 {
			if offset >= 0 && offset < len(r.dstArray) {
				return r.dstArray[offset]
			}
			continue
		}
		return decodePDFCMapString(incrementPDFCMapString(r.dstStart, offset))
	}
	return ""
}

func readPDFToUnicodeCMap(v pdf.Value) *pdfCMap {
	if v.Kind() != pdf.Stream {
		return nil
	}
	rd := v.Reader()
	defer rd.Close()
	data, err := io.ReadAll(rd)
	if err != nil {
		return nil
	}

	m := &pdfCMap{chars: map[string]string{}}
	var stack []pdfCMapToken
	var codeSpaceCount, bfCharCount, bfRangeCount int

	for _, tok := range parsePDFCMapTokens(data) {
		if tok.kind != pdfCMapOperator {
			stack = append(stack, tok)
			continue
		}
		switch tok.word {
		case "begincodespacerange":
			codeSpaceCount = popPDFCMapInt(&stack)
			stack = nil
		case "endcodespacerange":
			for i := 0; i < codeSpaceCount && len(stack) >= 2; i++ {
				hi, hiOK := popPDFCMapString(&stack)
				lo, loOK := popPDFCMapString(&stack)
				if !loOK || !hiOK || len(lo) == 0 || len(lo) != len(hi) || len(lo) > 4 {
					continue
				}
				m.space[len(lo)-1] = append(m.space[len(lo)-1], pdfCodeRange{lo: lo, hi: hi})
			}
			stack = nil
		case "beginbfchar":
			bfCharCount = popPDFCMapInt(&stack)
			stack = nil
		case "endbfchar":
			for i := 0; i < bfCharCount && len(stack) >= 2; i++ {
				dst, dstOK := popPDFCMapString(&stack)
				src, srcOK := popPDFCMapString(&stack)
				if !srcOK || !dstOK {
					continue
				}
				m.chars[src] = decodePDFCMapString(dst)
				m.hasData = true
			}
			stack = nil
		case "beginbfrange":
			bfRangeCount = popPDFCMapInt(&stack)
			stack = nil
		case "endbfrange":
			for i := 0; i < bfRangeCount && len(stack) >= 3; i++ {
				dst, dstOK := popPDFCMapToken(&stack)
				hi, hiOK := popPDFCMapString(&stack)
				lo, loOK := popPDFCMapString(&stack)
				if !loOK || !hiOK || !dstOK {
					continue
				}
				r := pdfBFRange{lo: lo, hi: hi}
				switch dst.kind {
				case pdfCMapStringToken:
					r.dstStart = dst.raw
				case pdfCMapArrayToken:
					for _, item := range dst.arr {
						if item.kind == pdfCMapStringToken {
							r.dstArray = append(r.dstArray, decodePDFCMapString(item.raw))
						}
					}
				default:
					continue
				}
				m.ranges = append(m.ranges, r)
				m.hasData = true
			}
			stack = nil
		default:
			stack = nil
		}
	}
	return m
}

type pdfCMapTokenKind int

const (
	pdfCMapStringToken pdfCMapTokenKind = iota
	pdfCMapIntegerToken
	pdfCMapArrayToken
	pdfCMapNameToken
	pdfCMapOperator
)

type pdfCMapToken struct {
	kind pdfCMapTokenKind
	raw  string
	num  int
	arr  []pdfCMapToken
	word string
}

func parsePDFCMapTokens(data []byte) []pdfCMapToken {
	pos := 0
	var tokens []pdfCMapToken
	for {
		tok, ok := nextPDFCMapToken(data, &pos)
		if !ok {
			break
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

func nextPDFCMapToken(data []byte, pos *int) (pdfCMapToken, bool) {
	skipPDFCMapSpace(data, pos)
	if *pos >= len(data) {
		return pdfCMapToken{}, false
	}
	c := data[*pos]
	switch c {
	case '[':
		(*pos)++
		var arr []pdfCMapToken
		for {
			skipPDFCMapSpace(data, pos)
			if *pos >= len(data) {
				break
			}
			if data[*pos] == ']' {
				(*pos)++
				break
			}
			tok, ok := nextPDFCMapToken(data, pos)
			if !ok {
				break
			}
			arr = append(arr, tok)
		}
		return pdfCMapToken{kind: pdfCMapArrayToken, arr: arr}, true
	case ']':
		(*pos)++
		return pdfCMapToken{kind: pdfCMapOperator, word: "]"}, true
	case '<':
		if *pos+1 < len(data) && data[*pos+1] == '<' {
			*pos += 2
			return pdfCMapToken{kind: pdfCMapOperator, word: "<<"}, true
		}
		(*pos)++
		start := *pos
		for *pos < len(data) && data[*pos] != '>' {
			(*pos)++
		}
		hexText := make([]byte, 0, *pos-start)
		for _, b := range data[start:*pos] {
			if !isPDFCMapSpace(b) {
				hexText = append(hexText, b)
			}
		}
		if len(hexText)%2 == 1 {
			hexText = append(hexText, '0')
		}
		raw, err := hex.DecodeString(string(hexText))
		if *pos < len(data) {
			(*pos)++
		}
		if err != nil {
			raw = nil
		}
		return pdfCMapToken{kind: pdfCMapStringToken, raw: string(raw)}, true
	case '>':
		if *pos+1 < len(data) && data[*pos+1] == '>' {
			*pos += 2
			return pdfCMapToken{kind: pdfCMapOperator, word: ">>"}, true
		}
		(*pos)++
		return pdfCMapToken{kind: pdfCMapOperator, word: ">"}, true
	case '(':
		return pdfCMapToken{kind: pdfCMapStringToken, raw: parsePDFLiteralString(data, pos)}, true
	case '/':
		(*pos)++
		word := readPDFCMapWord(data, pos)
		return pdfCMapToken{kind: pdfCMapNameToken, word: word}, true
	}

	word := readPDFCMapWord(data, pos)
	if word == "" {
		(*pos)++
		return pdfCMapToken{kind: pdfCMapOperator, word: ""}, true
	}
	if n, err := strconv.Atoi(word); err == nil {
		return pdfCMapToken{kind: pdfCMapIntegerToken, num: n}, true
	}
	return pdfCMapToken{kind: pdfCMapOperator, word: word}, true
}

func skipPDFCMapSpace(data []byte, pos *int) {
	for *pos < len(data) {
		if isPDFCMapSpace(data[*pos]) {
			(*pos)++
			continue
		}
		if data[*pos] == '%' {
			for *pos < len(data) && data[*pos] != '\n' && data[*pos] != '\r' {
				(*pos)++
			}
			continue
		}
		break
	}
}

func isPDFCMapSpace(b byte) bool {
	return b == 0 || b == '\t' || b == '\n' || b == '\f' || b == '\r' || b == ' '
}

func readPDFCMapWord(data []byte, pos *int) string {
	start := *pos
	for *pos < len(data) && !isPDFCMapSpace(data[*pos]) && !isPDFCMapDelimiter(data[*pos]) {
		(*pos)++
	}
	return string(data[start:*pos])
}

func isPDFCMapDelimiter(b byte) bool {
	switch b {
	case '[', ']', '<', '>', '(', ')', '/', '%':
		return true
	default:
		return false
	}
}

func parsePDFLiteralString(data []byte, pos *int) string {
	if *pos >= len(data) || data[*pos] != '(' {
		return ""
	}
	(*pos)++
	depth := 1
	var b strings.Builder
	for *pos < len(data) && depth > 0 {
		c := data[*pos]
		(*pos)++
		if c == '\\' {
			if *pos >= len(data) {
				break
			}
			esc := data[*pos]
			(*pos)++
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case '\n':
			case '\r':
				if *pos < len(data) && data[*pos] == '\n' {
					(*pos)++
				}
			case '\\', '(', ')':
				b.WriteByte(esc)
			default:
				if esc >= '0' && esc <= '7' {
					val := int(esc - '0')
					for i := 0; i < 2 && *pos < len(data) && data[*pos] >= '0' && data[*pos] <= '7'; i++ {
						val = val*8 + int(data[*pos]-'0')
						(*pos)++
					}
					b.WriteByte(byte(val))
				} else {
					b.WriteByte(esc)
				}
			}
			continue
		}
		switch c {
		case '(':
			depth++
			b.WriteByte(c)
		case ')':
			depth--
			if depth > 0 {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func popPDFCMapToken(stack *[]pdfCMapToken) (pdfCMapToken, bool) {
	if len(*stack) == 0 {
		return pdfCMapToken{}, false
	}
	n := len(*stack) - 1
	tok := (*stack)[n]
	*stack = (*stack)[:n]
	return tok, true
}

func popPDFCMapString(stack *[]pdfCMapToken) (string, bool) {
	tok, ok := popPDFCMapToken(stack)
	if !ok || tok.kind != pdfCMapStringToken {
		return "", false
	}
	return tok.raw, true
}

func popPDFCMapInt(stack *[]pdfCMapToken) int {
	tok, ok := popPDFCMapToken(stack)
	if !ok || tok.kind != pdfCMapIntegerToken {
		return 0
	}
	return tok.num
}

func decodePDFCMapString(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "\xfe\xff") {
		raw = raw[2:]
	}
	if len(raw)%2 == 0 {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 0; i < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
		return string(utf16.Decode(u16))
	}
	if utf8.ValidString(raw) {
		return raw
	}
	runes := make([]rune, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		runes = append(runes, rune(raw[i]))
	}
	return string(runes)
}

func incrementPDFCMapString(raw string, offset int) string {
	if offset <= 0 || raw == "" {
		return raw
	}
	b := []byte(raw)
	for i := len(b) - 1; i >= 0 && offset > 0; i-- {
		sum := int(b[i]) + offset
		b[i] = byte(sum)
		offset = sum >> 8
	}
	return string(b)
}

func pdfCodeValue(code string) int {
	v := 0
	for i := 0; i < len(code); i++ {
		v = v<<8 | int(code[i])
	}
	return v
}

func fallbackPDFCodeText(code string) string {
	if len(code) == 1 && code[0] >= 0x20 && code[0] < 0x7f {
		return code
	}
	return string(utf8.RuneError)
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

func stripRepeatedPDFEdgeLines(pages []string) []string {
	if len(pages) < 2 {
		return pages
	}
	firstCounts := map[string]int{}
	lastCounts := map[string]int{}
	splitPages := make([][]string, len(pages))
	for i, page := range pages {
		lines := splitPDFPageLines(page)
		splitPages[i] = lines
		if len(lines) == 0 {
			continue
		}
		firstCounts[normalisePDFEdgeLine(lines[0])]++
		lastCounts[normalisePDFEdgeLine(lines[len(lines)-1])]++
	}
	repeated := func(count int) bool { return count*2 > len(pages) }
	out := make([]string, 0, len(pages))
	for _, lines := range splitPages {
		if len(lines) == 0 {
			out = append(out, "")
			continue
		}
		if repeated(firstCounts[normalisePDFEdgeLine(lines[0])]) {
			lines = lines[1:]
		}
		if len(lines) > 0 && repeated(lastCounts[normalisePDFEdgeLine(lines[len(lines)-1])]) {
			lines = lines[:len(lines)-1]
		}
		out = append(out, strings.Join(lines, "\n\n"))
	}
	return out
}

func splitPDFPageLines(page string) []string {
	var out []string
	for _, line := range strings.Split(page, "\n\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func normalisePDFEdgeLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
