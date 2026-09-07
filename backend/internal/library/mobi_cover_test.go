package library

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// solidJPEG builds an image of a given size so the tests can control what
// the size guard sees. Encoding rather than checking in a blob keeps the
// fixture readable and the repo free of binaries.
func solidJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x40, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func intPtr(v int) *int { return &v }

func TestMobiCoverFollowsEXTHOffset(t *testing.T) {
	first, second := solidJPEG(t, 300, 450), solidPNG(t, 320, 480)
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: "<p>甲乙丙。</p>", compression: compressionNone, encoding: 65001,
		fullName: "示例书", images: [][]byte{first, second},
		coverOffset: intPtr(1), // the second image, not the first
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.Cover == nil {
		t.Fatal("no cover extracted")
	}
	if got.Cover.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png (EXTH pointed at the second image)", got.Cover.MediaType)
	}
	if !bytes.Equal(got.Cover.Data, second) {
		t.Error("cover bytes are not the record EXTH pointed at")
	}
}

// The offset and the header index are both routinely wrong in the wild,
// so a position that does not decode must not win over one that does.
func TestMobiCoverIgnoresOutOfRangeEXTHOffset(t *testing.T) {
	img := solidJPEG(t, 300, 450)
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: "<p>甲乙丙。</p>", compression: compressionNone, encoding: 65001,
		fullName: "示例书", images: [][]byte{img},
		coverOffset: intPtr(99),
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.Cover == nil {
		t.Fatal("a bad EXTH offset should fall back to the scan, not give up")
	}
	if !bytes.Equal(got.Cover.Data, img) {
		t.Error("fallback scan did not find the only image in the file")
	}
}

// Files that never fill in the header's image index are common; the scan
// has to find the cover without it.
func TestMobiCoverScansWhenHeaderDeclaresNoImages(t *testing.T) {
	img := solidJPEG(t, 300, 450)
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: "<p>甲乙丙丁戊己庚辛。</p>", compression: compressionPalmDoc, encoding: 65001,
		fullName: "示例书", images: [][]byte{img}, noFirstImage: true,
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.Cover == nil || !bytes.Equal(got.Cover.Data, img) {
		t.Error("scan did not recover the cover from an undeclared image run")
	}
}

func TestMobiCoverSkipsFakeCover(t *testing.T) {
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: "<p>甲乙丙。</p>", compression: compressionNone, encoding: 65001,
		fullName: "示例书", images: [][]byte{solidJPEG(t, 300, 450)},
		coverOffset: intPtr(0), fakeCover: true,
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.Cover != nil {
		t.Error("a converter-drawn cover should defer to our own generated one")
	}
}

// An ornament or logo must not win the scan in a file with no real cover.
func TestMobiCoverRejectsTinyImages(t *testing.T) {
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: "<p>甲乙丙。</p>", compression: compressionNone, encoding: 65001,
		fullName: "示例书", images: [][]byte{solidJPEG(t, 32, 32)},
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.Cover != nil {
		t.Errorf("a %d-byte 32×32 image was accepted as a cover", len(got.Cover.Data))
	}
}

func TestMobiCoverAbsentWhenFileHasNoImages(t *testing.T) {
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: "<p>甲乙丙。</p>", compression: compressionNone, encoding: 65001, fullName: "示例书",
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.Cover != nil {
		t.Error("found a cover in a file that has no images")
	}
}

// The cover has to survive into the cached EPUB, because that cache — not
// the source — is what the scanner and the cover endpoint read from.
func TestMobiCoverReachesTheCachedEpub(t *testing.T) {
	dir := t.TempDir()
	img := solidJPEG(t, 300, 450)
	src := writeMobi(t, dir, "book.mobi", mobiFixture{
		text:     "<p>第一章 甲乙</p><p>丙丁戊己庚辛壬癸。</p>",
		fullName: "示例书", author: "佚名",
		compression: compressionNone, encoding: 65001,
		images: [][]byte{img}, coverOffset: intPtr(0),
	})

	cr, err := ImportConvertibleEbookToCache(dir, src)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !HasCover(cr.Path) {
		t.Fatal("cached EPUB reports no cover")
	}
	got, err := ExtractCover(cr.Path)
	if err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}
	if got == nil {
		t.Fatal("no cover in the cached EPUB")
	}
	if !bytes.Equal(got.Data, img) {
		t.Error("the cached cover is not the image the MOBI carried")
	}
}
