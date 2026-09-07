package library

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Control characters are illegal in XML 1.0 and escaping does not rescue
// them — `&#x1D;` is as invalid as the raw byte. Real TXT rips carry them,
// and before this was handled the format pass wrote a cache our own reader
// then refused, so the book silently never appeared in the library.
func TestBuildEpub_SurvivesControlCharacters(t *testing.T) {
	// U+0005 and U+001D are the two seen in the wild.
	text := "第一章　起\u001d\n\n甲乙丙\u0005丁，戊己庚辛。\n\n壬癸\u001d子丑寅卯。\n"
	chapters := []Chapter{{Idx: 1, Title: "第一章　起\u001d", Level: 0, ByteOffset: 0, CharOffset: 0}}

	var buf bytes.Buffer
	if _, err := BuildEpub(&buf, "示例\u0005书", "佚\u001d名", text, chapters, nil); err != nil {
		t.Fatalf("BuildEpub: %v", err)
	}
	path := filepath.Join(t.TempDir(), "b.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	book, err := ReadEpub(path)
	if err != nil {
		t.Fatalf("BuildEpub produced an EPUB ReadEpub cannot parse: %v", err)
	}

	// The prose survives; only the illegal runes are gone.
	for _, want := range []string{"甲乙丙丁", "戊己庚辛", "壬癸子丑寅卯"} {
		if !strings.Contains(book.FlatText, want) {
			t.Errorf("body text %q was lost: %q", want, book.FlatText)
		}
	}
	for _, r := range "\u0005\u001d" {
		if strings.ContainsRune(book.FlatText, r) {
			t.Errorf("illegal rune %U survived into the flat text", r)
		}
		if strings.ContainsRune(book.Title, r) || strings.ContainsRune(book.Author, r) {
			t.Errorf("illegal rune %U survived into the metadata", r)
		}
	}
}

func TestXMLText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"甲乙丙", "甲乙丙"},
		{"tab\tnewline\n", "tab\tnewline\n"}, // #x9 / #xA are legal and must stay
		{"甲\u0000乙", "甲乙"},                   // NUL
		{"甲\u0005乙\u001d丙", "甲乙丙"},           // the two seen in the wild
		{"甲\u001f乙", "甲乙"},                   // top of the C0 range
		{"a<b>&c", "a&lt;b&gt;&amp;c"},       // escaping still happens
		{"甲\ufffe乙", "甲乙"},                   // non-character
		{"甲\U0001F600乙", "甲\U0001F600乙"},     // astral planes are legal
	}
	for _, c := range cases {
		if got := xmlText(c.in); got != c.want {
			t.Errorf("xmlText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
