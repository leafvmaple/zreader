package library

import "testing"

// A bare index and a title separated by whitespace — `一 灭门` — is how
// "精校版" typesettings of classic wuxia mark chapters: no 第…章 wrapper,
// no bracket, and not the 、 that EnumeratedNumeralPattern requires. Two
// books in a real library were sitting at one synthetic chapter each
// because nothing matched this shape.
func TestParseChapters_SpacedNumeralHeadings(t *testing.T) {
	text := "" +
		"一 甲子\n\n正文第一段，天干地支东南西北。\n\n" +
		"二 乙丑\n\n正文第二段，春夏秋冬风花雪月。\n\n" +
		"三 丙寅\n\n正文第三段，山川湖海。\n\n" +
		"四 丁卯\n\n正文第四段。\n"

	chs := ParseChapters(text, nil)
	if len(chs) != 4 {
		t.Fatalf("got %d chapters, want 4: %+v", len(chs), titles(chs))
	}
	for i, want := range []string{"一 甲子", "二 乙丑", "三 丙寅", "四 丁卯"} {
		if chs[i].Title != want {
			t.Errorf("chapter %d title = %q, want %q", i, chs[i].Title, want)
		}
	}
}

// Full-width and multi-space gaps, and two-character indices, are the same
// shape.
func TestParseChapters_SpacedNumeralVariants(t *testing.T) {
	text := "" +
		"九  甲子\n\n正文段落一。\n\n" +
		"十  乙丑\n\n正文段落二。\n\n" +
		"十一 丙寅\n\n正文段落三。\n\n" +
		"十二　丁卯\n\n正文段落四。\n"

	chs := ParseChapters(text, nil)
	if len(chs) != 4 {
		t.Fatalf("got %d chapters, want 4: %+v", len(chs), titles(chs))
	}
}

// The rule is the loosest in the registry, so the guards matter more than
// the match. minCount rejects a couple of stray hits, and the length cap
// keeps it off prose that merely opens with a numeral.
func TestParseChapters_SpacedNumeralRejectsNoise(t *testing.T) {
	cases := map[string]string{
		"too few matches": "一 甲子\n\n正文段落。\n\n二 乙丑\n\n正文段落。\n",
		"title too long": "一 这一行远远长过任何真实的章节标题所应有的长度上限\n\n正文。\n\n" +
			"二 这一行同样长过任何真实的章节标题所应有的长度上限\n\n正文。\n\n" +
			"三 这一行还是长过任何真实的章节标题所应有的长度上限\n\n正文。\n",
		"numeral mid-sentence": "一 说，甲乙丙丁戊己庚辛，壬癸子丑寅卯辰巳午未申酉戌亥东南西北。\n\n" +
			"二 说，甲乙丙丁戊己庚辛，壬癸子丑寅卯辰巳午未申酉戌亥东南西北。\n\n" +
			"三 说，甲乙丙丁戊己庚辛，壬癸子丑寅卯辰巳午未申酉戌亥东南西北。\n",
	}
	for name, text := range cases {
		chs := ParseChapters(text, nil)
		if len(chs) != 1 {
			t.Errorf("%s: got %d chapters, want the synthetic fallback: %+v",
				name, len(chs), titles(chs))
		}
	}
}

// A book with real 第X章 markers must not have them diluted by the looser
// rule sharing their rank.
func TestParseChapters_StructuredMarkersStillWin(t *testing.T) {
	text := "" +
		"第一章　甲子\n\n正文段落一。\n\n一 这不是标题\n\n正文段落二。\n\n" +
		"第二章　乙丑\n\n正文段落三。\n\n二 这也不是\n\n正文段落四。\n\n" +
		"第三章　丙寅\n\n正文段落五。\n"

	chs := ParseChapters(text, nil)
	if len(chs) != 3 {
		t.Fatalf("got %d chapters, want the 3 structured ones: %+v", len(chs), titles(chs))
	}
	for i, want := range []string{"第一章　甲子", "第二章　乙丑", "第三章　丙寅"} {
		if chs[i].Title != want {
			t.Errorf("chapter %d = %q, want %q", i, chs[i].Title, want)
		}
	}
}

func titles(chs []Chapter) []string {
	out := make([]string, 0, len(chs))
	for _, c := range chs {
		out = append(out, c.Title)
	}
	return out
}
