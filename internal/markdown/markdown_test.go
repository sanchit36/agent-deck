package markdown

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// markerStyle wraps each styled run in a visible tag so the tests can see
// which text got which style without decoding ANSI.
var markerStyle = Style{
	Bold: func(s string) string { return "<b>" + s + "</b>" },
	Code: func(s string) string { return "<c>" + s + "</c>" },
	Dim:  func(s string) string { return "<d>" + s + "</d>" },
}

func TestRender(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		width int
		pan   int
		plain bool // render without markerStyle
		want  []string
	}{
		{name: "heading bold without hashes", src: "# Title ##", want: []string{"<b>Title</b>"}},
		{name: "heading keeps inline code", src: "## Use `go`", want: []string{"<b>Use </b><c>go</c>"}},
		{name: "deep heading", src: "###### small", want: []string{"<b>small</b>"}},
		{name: "hash without space is prose", src: "#12 is fixed", want: []string{"#12 is fixed"}},
		{name: "bold and code inline", src: "a **b c** `d`", want: []string{"a <b>b c</b> <c>d</c>"}},
		{name: "underscore bold", src: "__x__", want: []string{"<b>x</b>"}},
		{name: "code span keeps markup", src: "`**x** <b>`", want: []string{"<c>**x** <b></c>"}},
		{name: "double backtick code span", src: "``a ` b``", want: []string{"<c>a ` b</c>"}},
		{name: "unclosed bold stays literal", src: "**open", want: []string{"**open"}},
		{name: "italic left as written", src: "*it*", want: []string{"*it*"}},
		{name: "escaped markers", src: `\*\*not bold\*\*`, want: []string{"**not bold**"}},
		{name: "link as text and url", src: "see [the docs](https://x.io/a) now", want: []string{"see the docs (https://x.io/a) now"}},
		{name: "link with title", src: `[t](https://x.io "Title")`, want: []string{"t (https://x.io)"}},
		{name: "link whose text is its url", src: "[https://x.io](https://x.io)", want: []string{"https://x.io"}},
		{name: "bold link text", src: "[**b**](u)", want: []string{"<b>b</b> (u)"}},
		{name: "image", src: "![a cat](cat.png)", want: []string{"[image: a cat]"}},
		{name: "image without alt", src: "![](cat.png)", want: []string{"[image]"}},
		{name: "linked image", src: "[![badge](b.svg)](https://ci)", want: []string{"[image: badge] (https://ci)"}},
		{name: "html img tag", src: `<img width="300" alt="Screenshot 1" src="https://u/x.png">`, want: []string{"[image: Screenshot 1]"}},
		{name: "html img without alt", src: `<img src="x.png" />`, want: []string{"[image]"}},
		{name: "autolink", src: "<https://a.b/c>", want: []string{"https://a.b/c"}},
		{name: "unmatched bracket literal", src: "a [b] c < d", want: []string{"a [b] c < d"}},
		{name: "inline html comment", src: "a <!-- hidden --> b", want: []string{"a b"}},
		{
			name: "multi-line html comment",
			src:  "x\n<!--\nsecret **text**\n-->\ny",
			want: []string{"x", "y"},
		},
		{
			name: "html comment opener inside a code span does not hide later text",
			src:  "use `<!--` to start a comment\nshown",
			want: []string{"use <c><!--</c> to start a comment", "shown"},
		},
		{name: "whole html comment inside a code span kept", src: "``<!-- x -->`` kept", want: []string{"<c><!-- x --></c> kept"}},
		{name: "html comment after a code span stripped", src: "`a` <!-- h --> b", want: []string{"<c>a</c> b"}},
		{name: "escaped backtick opens no code span", src: "\\`<!-- h -->` b", want: []string{"`` b"}},
		{
			name: "code span after a fence keeps its comment opener",
			src:  "```\nx\n```\n`<!--`\nshown",
			want: []string{"<d>x</d>", "<c><!--</c>", "shown"},
		},
		{
			name: "template comment then blank",
			src:  "<!-- Describe the bug -->\n\nIt crashes",
			want: []string{"It crashes"},
		},
		{
			name: "tags reduced to text",
			src:  "<details><summary>More <b>info</b></summary>\n\nbody<br>\n</details>",
			want: []string{"More info", "", "body"},
		},
		{
			name: "bullets and indentation kept",
			src:  "- one\n  - two\n    * three\n1. four\n2) five\n+ six",
			want: []string{"- one", "  - two", "    * three", "1. four", "2) five", "+ six"},
		},
		{name: "task list kept", src: "- [x] done\n- [ ] todo", want: []string{"- [x] done", "- [ ] todo"}},
		{
			name:  "bullet wraps with hanging indent",
			src:   "- alpha beta gamma",
			width: 10,
			want:  []string{"- alpha", "  beta", "  gamma"},
		},
		{
			name:  "nested numbered wraps under its text",
			src:   "  10. alpha beta",
			width: 12,
			want:  []string{"  10. alpha", "      beta"},
		},
		{
			name:  "quote keeps its marker on every line",
			src:   "> hello world",
			width: 8,
			want:  []string{"> hello", "> world"},
		},
		{
			name:  "indented prose keeps its indent",
			src:   "   one two three",
			width: 10,
			want:  []string{"   one two", "   three"},
		},
		{
			name:  "prose wraps at width",
			src:   "the quick brown fox jumps",
			width: 10,
			want:  []string{"the quick", "brown fox", "jumps"},
		},
		{
			name:  "single newlines are line breaks",
			src:   "line one\nline two",
			width: 40,
			want:  []string{"line one", "line two"},
		},
		{
			name:  "long word is broken",
			src:   "abcdefghijkl",
			width: 5,
			want:  []string{"abcde", "fghij", "kl"},
		},
		{
			name:  "bold run split across lines stays bold",
			src:   "**aaa bbb**",
			width: 4,
			want:  []string{"<b>aaa</b>", "<b>bbb</b>"},
		},
		{
			name: "prose capped at 100 columns",
			src:  strings.Repeat("word ", 30),
			// 20 words of 5 cells = 99 cells, the cap, then the rest.
			width: 200,
			want: []string{
				strings.TrimSpace(strings.Repeat("word ", 20)),
				strings.TrimSpace(strings.Repeat("word ", 10)),
			},
		},
		{
			name:  "fenced code dim and unwrapped",
			src:   "```go\nfunc main() { return }\n```",
			width: 40,
			want:  []string{"<d>func main() { return }</d>"},
		},
		{
			name: "fenced code keeps markdown raw",
			src:  "~~~\n**x** <b> [a](b) <!-- c -->\n~~~",
			want: []string{"<d>**x** <b> [a](b) <!-- c --></d>"},
		},
		{
			name: "fenced code keeps blank lines and indent",
			src:  "```\nif x {\n\n\treturn\n}\n```",
			want: []string{"<d>if x {</d>", "", "<d>    return</d>", "<d>}</d>"},
		},
		{
			name: "html comment opener inside code does not hide later text",
			src:  "```\n<!--\n```\nshown",
			want: []string{"<d><!--</d>", "shown"},
		},
		{
			name: "unclosed fence runs to the end",
			src:  "```\na\nb",
			want: []string{"<d>a</d>", "<d>b</d>"},
		},
		{
			name: "shorter fence does not close a longer one",
			src:  "````\n```\nx\n````\nafter",
			want: []string{"<d>```</d>", "<d>x</d>", "after"},
		},
		{
			name:  "long code cut with marker",
			src:   "```\n0123456789abc\n```",
			width: 8,
			want:  []string{"<d>0123456›</d>"},
		},
		{
			name:  "code panned",
			src:   "```\n0123456789abc\n```",
			width: 8,
			pan:   3,
			want:  []string{"<d>3456789›</d>"},
		},
		{
			name:  "code panned to its end has no marker",
			src:   "```\n0123456789abc\n```",
			width: 8,
			pan:   6,
			want:  []string{"<d>6789abc</d>"},
		},
		{
			name:  "code panned past a short line is empty",
			src:   "```\nab\n0123456789abc\n```",
			width: 8,
			pan:   6,
			want:  []string{"", "<d>6789abc</d>"},
		},
		{
			name:  "code is not capped at 100 columns",
			src:   "```\n" + strings.Repeat("x", 120) + "\n```",
			width: 200,
			plain: true,
			want:  []string{strings.Repeat("x", 120)},
		},
		{
			name: "table left raw",
			src:  "| a | **b** |\n|---|---|\n| [l](u) | `c` |",
			want: []string{"| a | **b** |", "|---|---|", "| [l](u) | `c` |"},
		},
		{
			name: "table without leading pipes left raw",
			src:  "a | **b**\n---|---\n[l](u) | `c`",
			want: []string{"a | **b**", "---|---", "[l](u) | `c`"},
		},
		{
			name: "aligned delimiter row",
			src:  " **a** | b |\n:--|--:|\n1 | `2` |",
			want: []string{" **a** | b |", ":--|--:|", "1 | `2` |"},
		},
		{
			name: "table ends at a line without a pipe",
			src:  "a | b\n-- | --\n1 | **2**\nafter **x**",
			want: []string{"a | b", "-- | --", "1 | **2**", "after <b>x</b>"},
		},
		{
			name: "table ends at a blank line",
			src:  "**a** | b\n--|--\n\nx | **y**",
			want: []string{"**a** | b", "--|--", "", "x | <b>y</b>"},
		},
		{
			name: "delimiter with another cell count is no table",
			src:  "**a** | b | c\n--|--",
			want: []string{"<b>a</b> | b | c", "--|--"},
		},
		{
			name: "pipe in prose without a delimiter row is no table",
			src:  "**a** | b\nnext | **c**",
			want: []string{"<b>a</b> | b", "next | <b>c</b>"},
		},
		{
			name:  "wide table cut and panned like code",
			src:   "| aaaa | bbbb |",
			width: 6,
			pan:   2,
			want:  []string{"aaaa ›"},
		},
		{name: "horizontal rule", src: "---", width: 5, want: []string{"<d>─────</d>"}},
		{name: "starred rule", src: "* * *", width: 3, want: []string{"<d>───</d>"}},
		{
			name: "blank lines collapsed and trimmed",
			src:  "\n\na\n\n\n\nb\n\n",
			want: []string{"a", "", "b"},
		},
		{name: "crlf line endings", src: "a\r\nb\r\n", want: []string{"a", "b"}},
		{name: "empty source", src: "", want: nil},
		{name: "control sequences dropped", src: "a\x1b[31mred\x07", want: []string{"a[31mred"}},
		{name: "control sequences dropped in code", src: "```\n\x1b]52;c;aGk=\x07x\n```", want: []string{"<d>]52;c;aGk=x</d>"}},
		{name: "bidi override dropped", src: "evil‮gnp.exe", want: []string{"evilgnp.exe"}},
		{
			name:  "CJK wraps by cell width",
			src:   "日本語のテキスト",
			width: 6,
			want:  []string{"日本語", "のテキ", "スト"},
		},
		{
			name:  "CJK at odd width never overflows",
			src:   "日本語のテキスト",
			width: 5,
			want:  []string{"日本", "語の", "テキ", "スト"},
		},
		{
			name:  "CJK after a word starts a new line when no cell is left",
			src:   "abc 日本語",
			width: 5,
			want:  []string{"abc", "日本", "語"},
		},
		{
			name:  "CJK fills the rest of a line",
			src:   "ab 日本語",
			width: 6,
			want:  []string{"ab 日", "本語"},
		},
		{
			name:  "emoji counted as two cells",
			src:   "👍🏽 ok 👨‍👩‍👧 family",
			width: 6,
			want:  []string{"👍🏽 ok", "👨‍👩‍👧", "family"},
		},
		{
			name:  "CJK code cut with marker",
			src:   "```\n日本語テキスト\n```",
			width: 5,
			plain: true,
			want:  []string{"日本›"},
		},
		{
			name:  "CJK heading wraps",
			src:   "# 漢字漢字",
			width: 4,
			want:  []string{"<b>漢字</b>", "<b>漢字</b>"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			width := tc.width
			if width == 0 {
				width = 80
			}
			opts := Options{Width: width, Pan: tc.pan, Style: markerStyle}
			if tc.plain {
				opts.Style = Style{}
			}
			got := Render(tc.src, opts).Lines
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Render(%q, width %d, pan %d)\n got: %q\nwant: %q", tc.src, width, tc.pan, got, tc.want)
			}
		})
	}
}

func TestRenderCodeWidth(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{name: "no code", src: "a long line of prose that is not code", want: 0},
		{name: "widest code line", src: "```\nab\n0123456789\n```", want: 10},
		{name: "tables count", src: "| a | b |", want: 9},
		{name: "wide runes count by cells", src: "```\n日本語\n```", want: 6},
		{name: "tabs expanded", src: "```\n\tx\n```", want: 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Render(tc.src, Options{Width: 4}).CodeWidth; got != tc.want {
				t.Errorf("CodeWidth = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRenderNeverExceedsWidth renders a mixed body at every width from 2 to
// 40 and every pan up to 12 and checks no line is wider than the width. A
// width of 1 cannot hold a wide rune, so it starts at 2.
func TestRenderNeverExceedsWidth(t *testing.T) {
	src := strings.Join([]string{
		"# 見出し heading with **bold** words",
		"Prose with `code`, [a link](https://example.com/a/very/long/path) and ![img](x.png).",
		"日本語のテキストと한국어 텍스트 mixed with English words and 👨‍👩‍👧 emoji 🎉🎉🎉.",
		"- a bullet whose text is long enough to wrap several times over",
		"    1. nested numbered item with 中文字符 inside",
		"> quoted text that also wraps around",
		"averyveryverylongwordwithoutanyspacesthatmustbebroken",
		"```",
		"func main() { fmt.Println(\"日本語 🎉 code line that is long\") }",
		"\tindented",
		"```",
		"| 列 | col | 👍 |",
		"---",
	}, "\n")
	for width := 2; width <= 40; width++ {
		for pan := 0; pan <= 12; pan++ {
			for _, line := range Render(src, Options{Width: width, Pan: pan}).Lines {
				if w := ansi.StringWidth(line); w > width {
					t.Fatalf("width %d pan %d: line %q is %d cells", width, pan, line, w)
				}
			}
		}
	}
}

// TestRenderMalformedInputTerminates feeds unterminated constructs at small
// widths; Render must return rather than loop.
func TestRenderMalformedInputTerminates(t *testing.T) {
	for _, src := range []string{
		"[", "![", "[a](", "<", "<img", "<!--", "`", "``", "**", "\\", "-", "1.", ">", "#",
		"[a]", "[a](b", "<a href=", "***", "|", "```", "~~~~",
	} {
		for width := -1; width <= 3; width++ {
			_ = Render(src, Options{Width: width, Pan: -5})
		}
	}
}
