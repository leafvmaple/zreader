package server

import (
	"mime"
	"strings"
	"testing"
)

// A CJK book title has to survive the Content-Disposition round trip: the
// bare `filename` is ASCII-only by spec, so the real name rides in
// `filename*` as percent-encoded UTF-8 (RFC 5987). Getting that wrong
// silently hands the user a file called "export.jsonl" — or worse, a
// literal "=?utf-8?q?…?=".
func TestContentDisposition(t *testing.T) {
	cases := []struct {
		name      string
		wantASCII string
	}{
		{"甲乙丙.jsonl", "export.jsonl"},
		{"BookA.jsonl", "BookA.jsonl"},
		{"甲乙丙 BookA.jsonl", "BookA.jsonl"},
		{".jsonl", "export.jsonl"},
	}
	for _, c := range cases {
		got := contentDisposition(c.name)

		// The bare filename is the ASCII-only fallback, read straight off
		// the header — mime.ParseMediaType below deliberately shadows it
		// with the decoded extended form, which is the whole point of
		// filename*, so it can't be used to check this half.
		if !strings.Contains(got, `filename="`+c.wantASCII+`"`) {
			t.Errorf("contentDisposition(%q) = %q, want an ASCII fallback of %q",
				c.name, got, c.wantASCII)
		}

		// A correct RFC 5987 parameter decodes back to the original name.
		// Go resolves filename* into params["filename"] when both are set,
		// which is exactly the preference a browser applies.
		_, params, err := mime.ParseMediaType(got)
		if err != nil {
			t.Fatalf("contentDisposition(%q) = %q, unparsable: %v", c.name, got, err)
		}
		if params["filename"] != c.name {
			t.Errorf("contentDisposition(%q): filename* decoded to %q, want the original name",
				c.name, params["filename"])
		}

		if strings.Contains(got, "=?utf-8?") {
			t.Errorf("contentDisposition(%q) used a MIME word, not percent-encoding: %q", c.name, got)
		}
	}
}

func TestRFC5987Escape(t *testing.T) {
	if got := rfc5987Escape("甲"); got != "%E7%94%B2" {
		t.Errorf("rfc5987Escape(甲) = %q, want %%E7%%94%%B2", got)
	}
	if got := rfc5987Escape("a-b_c.d~e"); got != "a-b_c.d~e" {
		t.Errorf("unreserved characters were escaped: %q", got)
	}
	if got := rfc5987Escape("a b"); got != "a%20b" {
		t.Errorf("rfc5987Escape(\"a b\") = %q, want a%%20b", got)
	}
}
