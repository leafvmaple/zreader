// Tiny diagnostic that calls library.DetectAndDecode on every argv path and
// prints the result. Used to debug encoding-detection issues; not shipped.
package main

import (
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/leafvmaple/zreader/internal/library"
)

func main() {
	for _, path := range os.Args[1:] {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("%s: READ ERROR %v\n", path, err)
			continue
		}
		enc, text, err := library.DetectAndDecode(raw)
		if err != nil {
			fmt.Printf("%s: DECODE ERROR %v\n", path, err)
			continue
		}
		fmt.Printf("=== %s ===\n", path)
		fmt.Printf("bytes=%d encoding=%s runes=%d valid_utf8=%v\n",
			len(raw), enc, utf8.RuneCountInString(text), utf8.ValidString(text))
		preview := text
		if len(preview) > 200 {
			preview = preview[:200]
		}
		fmt.Printf("preview: %q\n\n", preview)
	}
}
