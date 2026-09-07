package export

// Cleaning rules as data.
//
// This is the extension point. A plugin interface would mean shipping a
// Go plugin (Linux-only, CGO, exact-version-lockstep — all of which this
// project deliberately avoids by using a CGO-free sqlite and a single
// static binary), an out-of-process RPC layer, or an embedded script
// runtime. None of that is warranted for what is a list of regexes over
// text.
//
// So: the defaults below ship built in, and `<data>/clean-rules.json`
// extends them at runtime. The same shape as the `<name>.chapters.json`
// sidecar the scanner already supports — user-owned data files that adjust
// behaviour without a rebuild.
//
//	{
//	  "promo":        ["自定义广告正则"],
//	  "author_notes": ["^本章说"],
//	  "replace":      [["俩", "两"]]
//	}
//
// An unparsable file or a bad pattern is reported to the caller and the
// defaults are used unchanged — a typo in an optional config should never
// take the export endpoint down.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// RuleSet is the compiled form the passes run against.
type RuleSet struct {
	Promo       []*regexp.Regexp
	AuthorNotes []*regexp.Regexp
	// Replace is applied literally, in order, during normalisation.
	Replace [][2]string
}

func (rs *RuleSet) matchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// defaultPromo targets the advertising that pirate-site TXT rips inject
// into the prose. Every entry is deliberately narrow: a false positive
// here silently deletes a line of the book, which is far worse than
// leaving an ad in. Whole-paragraph removal is additionally gated on
// length (promoMaxRunes) so a long paragraph only loses the matched span.
var defaultPromo = []string{
	`(?i)(https?://|www\.)[\w.\-/?=&%#]+`,
	`(?i)[\w\-]+\.(com|net|org|cc|co|top|xyz|info|vip)\b`,
	`(更多|最新|全本|免费|无弹窗|手打)[^。！？]{0,10}(小说|章节|内容|全文|更新)[^。！？]{0,10}(尽在|请上|请到|访问|首发)`,
	`(请)?记住(本站|我们的)?(域名|网址|地址)`,
	`本(书|文|章)由[^。！？]{0,20}(整理|制作|提供|录入|校对)`,
	`(手机|移动|wap)[^。！？]{0,6}(阅读|访问|用户)[^。！？]{0,10}(请|地址|网址)`,
	`^[※*＊]?(声明|免责声明)[:：].*(电子书|下载).*(删除|正版|商业用途).*[※*＊]?$`,
	`^[（(\[【]?(本章完|未完待续|全文完|正文完)[）)\]】]?$`,
	`^[-—=*·~＊]{4,}$`,
}

// defaultAuthorNotes targets the block a serialised web novel appends
// after the prose ends. These are anchored to the paragraph start: the
// words appear in ordinary dialogue too, and an unanchored match would
// eat real text.
var defaultAuthorNotes = []string{
	`^[（(\[【]?(作者(有话说|的话|按)|作者君|ps|PS|Ps)[）)\]】]?[:：、]?`,
	`^(求|跪求|继续求)[^。！？]{0,12}(推荐票|月票|收藏|订阅|打赏|评论|鲜花)`,
	`^感谢[^。！？]{0,20}(打赏|月票|推荐票|订阅|捧场)`,
	`^(今天|明天|本周|下周)[^。！？]{0,10}(加更|两更|三更|爆更|请假|断更)`,
	`^(新书|老书|本书)[^。！？]{0,12}(求支持|已上传|求收藏|推荐一下)`,
}

// DefaultRules compiles the built-in patterns. The patterns are constants,
// so a compile failure is a programming error and panicking at first use
// is the right response.
func DefaultRules() *RuleSet {
	rs := &RuleSet{}
	for _, p := range defaultPromo {
		rs.Promo = append(rs.Promo, regexp.MustCompile(p))
	}
	for _, p := range defaultAuthorNotes {
		rs.AuthorNotes = append(rs.AuthorNotes, regexp.MustCompile(p))
	}
	return rs
}

// RuleOverridesName is the file, under ZREADER_DATA_DIR, that extends the
// built-in rules.
const RuleOverridesName = "clean-rules.json"

type ruleOverrides struct {
	Promo       []string    `json:"promo"`
	AuthorNotes []string    `json:"author_notes"`
	Replace     [][2]string `json:"replace"`
}

// LoadRules returns the built-in rules extended by path, if it exists.
//
// The returned error describes a malformed override file; the RuleSet is
// still usable (defaults only) in that case, so callers can surface the
// problem without failing the export.
func LoadRules(path string) (*RuleSet, error) {
	rs := DefaultRules()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return rs, nil
	}
	if err != nil {
		return rs, fmt.Errorf("read %s: %w", RuleOverridesName, err)
	}

	var ov ruleOverrides
	if err := json.Unmarshal(raw, &ov); err != nil {
		return rs, fmt.Errorf("parse %s: %w", RuleOverridesName, err)
	}
	for _, p := range ov.Promo {
		re, err := regexp.Compile(p)
		if err != nil {
			return rs, fmt.Errorf("%s: bad promo pattern %q: %w", RuleOverridesName, p, err)
		}
		rs.Promo = append(rs.Promo, re)
	}
	for _, p := range ov.AuthorNotes {
		re, err := regexp.Compile(p)
		if err != nil {
			return rs, fmt.Errorf("%s: bad author_notes pattern %q: %w", RuleOverridesName, p, err)
		}
		rs.AuthorNotes = append(rs.AuthorNotes, re)
	}
	rs.Replace = append(rs.Replace, ov.Replace...)
	return rs, nil
}
