// Package markdown renders GitHub-flavoured markdown (issue, pull request and
// comment bodies) as terminal lines with a light, in-house pass:
//
//   - headings bold, without their # markers
//   - list bullets, quote markers and indentation kept; wrapped lines hang
//     under the item text
//   - fenced code dim, never wrapped, cut with › and pannable
//   - **bold** (and __bold__) and `code` styled
//   - links as "text (url)", images as "[image: alt]"
//   - HTML comments stripped (outside code), other HTML tags reduced to their text
//     (<img> becomes "[image: alt]")
//   - tables left as raw text, cut and panned like code
//   - prose wrapped at min(width, 100) columns; a newline in the source is a
//     line break, as GitHub renders comments
//
// Every width is measured in terminal cells by grapheme cluster, so wide runes
// (CJK, emoji) never overflow a line and are never cut in half. Control and
// bidi formatting characters are dropped (termtext.Clean), since bodies are
// written by other people.
//
// The package depends only on the standard library, internal/termtext and the
// charmbracelet/x/ansi width functions already in go.mod.
package markdown

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/termtext"
)

// MaxProseWidth caps the column at which prose wraps, however wide the view.
const MaxProseWidth = 100

// tabWidth is how many spaces a tab expands to.
const tabWidth = 4

// cutMarker ends a code or table line that continues past the view.
const cutMarker = "›"

// Style holds the styling functions. A nil function leaves its text as is.
type Style struct {
	Bold func(string) string // headings and **bold**
	Code func(string) string // `inline code`
	Dim  func(string) string // fenced code lines and horizontal rules
}

// Options control one Render call.
type Options struct {
	// Width is the view width in cells. Prose wraps at min(Width, 100);
	// code and table lines are cut at Width. Values below 1 count as 1; a
	// width of 1 cannot hold a wide rune.
	Width int
	// Pan is how many cells are cut from the left of code and table lines.
	Pan int
	// Style styles the output. The zero Style renders plain text.
	Style Style
}

// Result is a rendered body.
type Result struct {
	// Lines are the output lines, none wider than Options.Width (for
	// Width >= 2), with no trailing newline.
	Lines []string
	// CodeWidth is the widest code or table line in cells, so a caller can
	// clamp Pan to CodeWidth - Width.
	CodeWidth int
}

// Render renders src at opts.
func Render(src string, opts Options) Result {
	r := renderer{
		width: max(opts.Width, 1),
		pan:   max(opts.Pan, 0),
		style: opts.Style,
	}
	r.proseWidth = min(r.width, MaxProseWidth)
	r.run(src)
	for len(r.out) > 0 && r.out[len(r.out)-1] == "" && !r.lastCode {
		r.out = r.out[:len(r.out)-1]
	}
	return Result{Lines: r.out, CodeWidth: r.codeWidth}
}

var (
	headingRe = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	ruleRe    = regexp.MustCompile(`^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	listRe    = regexp.MustCompile(`^([ \t]*)([-*+]|[0-9]{1,9}[.)])(?:[ \t]+|$)`)
	quoteRe   = regexp.MustCompile(`^[ \t]*(?:>[ \t]?)+`)
	fenceRe   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
)

type renderer struct {
	width, proseWidth, pan int
	style                  Style

	out       []string
	codeWidth int
	// lastCode is true when the last emitted line came from a code block,
	// whose blank lines are content and are neither collapsed nor trimmed.
	lastCode bool

	fence     string // open fence marker ("```", "~~~~", ...); empty outside code
	inComment bool   // inside an HTML comment that spans lines
}

func (r *renderer) run(src string) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	for _, raw := range strings.Split(src, "\n") {
		line := termtext.Clean(expandTabs(raw))
		if r.fence != "" {
			if isFenceClose(line, r.fence) {
				r.fence = ""
				continue
			}
			r.emitCut(line, true)
			continue
		}
		if !r.inComment {
			if m := fenceRe.FindStringSubmatch(line); m != nil && (m[1][0] != '`' || !strings.Contains(m[2], "`")) {
				r.fence = m[1]
				continue
			}
		}
		hadText := strings.TrimSpace(line) != ""
		line = r.stripComments(line)
		r.block(line, hadText)
	}
}

// block renders one source line outside code. hadText says whether the line
// had text before HTML comments were stripped, so a line that held only a
// comment or only tags disappears instead of leaving a blank.
func (r *renderer) block(line string, hadText bool) {
	if strings.TrimSpace(line) == "" {
		if !hadText {
			r.blank()
		}
		return
	}
	if strings.HasPrefix(strings.TrimLeft(line, " "), "|") {
		r.emitCut(line, false)
		return
	}
	if ruleRe.MatchString(line) {
		r.emit(r.styled(r.style.Dim, strings.Repeat("─", r.proseWidth)))
		return
	}
	if m := headingRe.FindStringSubmatch(line); m != nil {
		spans := parseInline(m[2])
		if !hasText(spans) {
			return
		}
		for i := range spans {
			if spans[i].kind == kindPlain {
				spans[i].kind = kindBold
			}
		}
		r.wrap(spans, "", "")
		return
	}
	var first, cont, rest string
	if m := quoteRe.FindString(line); m != "" {
		first, cont, rest = m, m, line[len(m):]
	} else if m := listRe.FindStringSubmatch(line); m != nil {
		marker := m[1] + m[2] + " "
		first, cont, rest = marker, strings.Repeat(" ", ansi.StringWidth(marker)), line[len(m[0]):]
	} else {
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		first, cont, rest = indent, indent, line[len(indent):]
	}
	spans := parseInline(rest)
	if !hasText(spans) && strings.TrimSpace(first) == "" {
		// Only tags: nothing to show.
		return
	}
	r.wrap(spans, first, cont)
}

// blank emits one empty line, collapsing runs and dropping leading blanks.
func (r *renderer) blank() {
	if len(r.out) == 0 || (r.out[len(r.out)-1] == "" && !r.lastCode) {
		return
	}
	r.emit("")
}

func (r *renderer) emit(s string) {
	r.out = append(r.out, s)
	r.lastCode = false
}

// emitCut emits a code (dim) or table (raw) line: never wrapped, panned by
// r.pan and cut at r.width with a › marker when more follows.
func (r *renderer) emitCut(line string, code bool) {
	line = strings.TrimRight(line, " ")
	w := ansi.StringWidth(line)
	r.codeWidth = max(r.codeWidth, w)
	var vis string
	switch {
	case r.pan >= w:
		vis = ""
	case w-r.pan > r.width:
		limit := r.width - ansi.StringWidth(cutMarker)
		vis = fitCells(ansi.Cut(line, r.pan, r.pan+limit), limit) + cutMarker
	default:
		vis = fitCells(ansi.Cut(line, r.pan, w), r.width)
	}
	if code && vis != "" {
		vis = r.styled(r.style.Dim, vis)
	}
	r.emit(vis)
	r.lastCode = code
}

// fitCells trims s to at most n cells. ansi.Cut can keep a wide rune that
// straddles its left edge; this drops it from the end instead of overflowing.
func fitCells(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "")
}

func (r *renderer) styled(f func(string) string, s string) string {
	if f == nil || s == "" {
		return s
	}
	return f(s)
}

// stripComments removes HTML comments, which may span lines. A comment
// opener inside a code span is code, not a comment, so code spans (and
// escaped backticks, which open none) are copied as they are.
func (r *renderer) stripComments(line string) string {
	var b strings.Builder
	for line != "" {
		if r.inComment {
			end := strings.Index(line, "-->")
			if end < 0 {
				return b.String()
			}
			line = line[end+len("-->"):]
			r.inComment = false
			continue
		}
		i := commentStart(line)
		if i < 0 {
			b.WriteString(line)
			break
		}
		b.WriteString(line[:i])
		line = line[i+len("<!--"):]
		r.inComment = true
	}
	return b.String()
}

// commentStart is the index of the first HTML comment opener in line
// outside a code span, or -1. An escaped backtick opens no code span.
func commentStart(line string) int {
	for i := 0; i < len(line); {
		switch {
		case line[i] == '\\' && i+1 < len(line) && strings.IndexByte(escapable, line[i+1]) >= 0:
			i += 2
		case line[i] == '`':
			if _, n, ok := codeSpan(line[i:]); ok {
				i += n
			} else {
				i += len(line[i:]) - len(strings.TrimLeft(line[i:], "`"))
			}
		case strings.HasPrefix(line[i:], "<!--"):
			return i
		default:
			i++
		}
	}
	return -1
}

func isFenceClose(line, fence string) bool {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return false
	}
	run := len(t) - len(strings.TrimLeft(t, fence[:1]))
	return run >= len(fence) && strings.TrimSpace(t[run:]) == ""
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabWidth))
}

func hasText(spans []span) bool {
	for _, s := range spans {
		if strings.TrimSpace(s.text) != "" {
			return true
		}
	}
	return false
}

// spanKind is how a run of inline text is styled.
type spanKind int

const (
	kindPlain spanKind = iota
	kindBold
	kindCode
)

type span struct {
	text string
	kind spanKind
}

// piece is a styled run of a line under construction.
type piece = span

// word is a run of non-space text, possibly crossing styles, plus the style
// of the space that followed it in the source.
type word struct {
	parts     []piece
	width     int
	wide      bool // holds a wide grapheme: may break between graphemes
	spaceKind spanKind
}

// words splits spans at spaces. Runs of spaces collapse to one.
func words(spans []span) []word {
	var out []word
	var cur word
	flush := func(k spanKind) {
		if len(cur.parts) > 0 {
			cur.spaceKind = k
			out = append(out, cur)
		}
		cur = word{}
	}
	for _, s := range spans {
		text := s.text
		for text != "" {
			i := strings.IndexByte(text, ' ')
			if i == 0 {
				flush(s.kind)
				text = text[1:]
				continue
			}
			chunk := text
			if i > 0 {
				chunk = text[:i]
			}
			text = text[len(chunk):]
			w := ansi.StringWidth(chunk)
			cur.parts = append(cur.parts, piece{text: chunk, kind: s.kind})
			cur.width += w
			cur.wide = cur.wide || hasWideGrapheme(chunk)
		}
	}
	flush(kindPlain)
	return out
}

func hasWideGrapheme(s string) bool {
	for s != "" {
		g, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		if w > 1 {
			return true
		}
		s = s[len(g):]
	}
	return false
}

// line is a wrapped output line under construction.
type line struct {
	parts []piece
	width int
}

func (l *line) add(p piece, w int) {
	if n := len(l.parts); n > 0 && l.parts[n-1].kind == p.kind {
		l.parts[n-1].text += p.text
	} else {
		l.parts = append(l.parts, p)
	}
	l.width += w
}

// wrap word-wraps spans at r.proseWidth, starting the first line with first
// and every later line with cont.
func (r *renderer) wrap(spans []span, first, cont string) {
	// Keep at least two cells for text so a wide rune always fits.
	room := max(r.proseWidth-2, 0)
	first = fitCells(first, room)
	cont = fitCells(cont, room)

	prefix := first
	avail := r.proseWidth - ansi.StringWidth(prefix)
	var cur line
	var prevSpace spanKind
	flush := func() {
		trimTrailingSpace(&cur)
		var b strings.Builder
		b.WriteString(prefix)
		for _, p := range cur.parts {
			switch p.kind {
			case kindBold:
				b.WriteString(r.styled(r.style.Bold, p.text))
			case kindCode:
				b.WriteString(r.styled(r.style.Code, p.text))
			default:
				b.WriteString(p.text)
			}
		}
		r.emit(strings.TrimRight(b.String(), " "))
		cur = line{}
		prefix = cont
		avail = r.proseWidth - ansi.StringWidth(prefix)
	}
	for _, w := range words(spans) {
		gap := 0
		if cur.width > 0 {
			gap = 1
		}
		switch {
		case cur.width+gap+w.width <= avail:
			// Fits on this line.
		case !w.wide && w.width <= avail:
			flush()
			gap = 0
		case !w.wide && cur.width > 0:
			// Longer than a line: start it on a fresh one, then break it.
			flush()
			gap = 0
		}
		if gap == 1 {
			cur.add(piece{text: " ", kind: prevSpace}, 1)
		}
		r.place(&cur, w.parts, &avail, flush)
		prevSpace = w.spaceKind
	}
	flush()
}

// place adds parts to cur grapheme by grapheme, starting a new line whenever
// the next grapheme does not fit.
func (r *renderer) place(cur *line, parts []piece, avail *int, flush func()) {
	for _, p := range parts {
		text := p.text
		if cur.width+ansi.StringWidth(text) <= *avail {
			cur.add(p, ansi.StringWidth(text))
			continue
		}
		for text != "" {
			g, w := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
			if cur.width+w > *avail && cur.width > 0 {
				// A space added before this word must not end the line.
				trimTrailingSpace(cur)
				flush()
			}
			cur.add(piece{text: g, kind: p.kind}, w)
			text = text[len(g):]
		}
	}
}

func trimTrailingSpace(l *line) {
	n := len(l.parts)
	if n == 0 {
		return
	}
	last := &l.parts[n-1]
	if trimmed := strings.TrimRight(last.text, " "); trimmed != last.text {
		l.width -= len(last.text) - len(trimmed)
		last.text = trimmed
		if trimmed == "" {
			l.parts = l.parts[:n-1]
		}
	}
}

// Inline parsing.

var (
	imgTagRe  = regexp.MustCompile(`(?i)^<img\b[^>]*>`)
	altAttrRe = regexp.MustCompile(`(?i)\balt\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	tagRe     = regexp.MustCompile(`^</?[A-Za-z][A-Za-z0-9-]*(?:\s[^>]*)?/?>`)
	autoRe    = regexp.MustCompile(`^<((?:https?|mailto):[^\s<>]+)>`)
)

const escapable = "\\`*_{}[]()#+-.!|<>~\""

// parseInline turns one line of markdown into styled spans.
func parseInline(s string) []span {
	var out []span
	var plain strings.Builder
	push := func(sp span) {
		if sp.text == "" {
			return
		}
		if sp.kind == kindPlain {
			plain.WriteString(sp.text)
			return
		}
		if plain.Len() > 0 {
			out = append(out, span{text: plain.String()})
			plain.Reset()
		}
		out = append(out, sp)
	}
	pushAll := func(spans []span) {
		for _, sp := range spans {
			push(sp)
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte(escapable, s[i+1]) >= 0:
			plain.WriteByte(s[i+1])
			i += 2
			continue
		case c == '`':
			if text, n, ok := codeSpan(s[i:]); ok {
				push(span{text: text, kind: kindCode})
				i += n
				continue
			}
			run := len(s[i:]) - len(strings.TrimLeft(s[i:], "`"))
			plain.WriteString(s[i : i+run])
			i += run
			continue
		case (c == '*' || c == '_') && strings.HasPrefix(s[i+1:], string(c)):
			marker := s[i : i+2]
			if end := strings.Index(s[i+2:], marker); end > 0 {
				for _, sp := range parseInline(s[i+2 : i+2+end]) {
					if sp.kind == kindPlain {
						sp.kind = kindBold
					}
					push(sp)
				}
				i += 2 + end + 2
				continue
			}
		case c == '!' && strings.HasPrefix(s[i+1:], "["):
			if text, _, n, ok := link(s[i+1:]); ok {
				push(span{text: imageText(plainText(parseInline(text)))})
				i += 1 + n
				continue
			}
		case c == '[':
			if text, url, n, ok := link(s[i:]); ok {
				spans := parseInline(text)
				pushAll(spans)
				if plainText(spans) != url && url != "" {
					push(span{text: " (" + url + ")"})
				}
				i += n
				continue
			}
		case c == '<':
			rest := s[i:]
			if m := autoRe.FindStringSubmatch(rest); m != nil {
				push(span{text: m[1]})
				i += len(m[0])
				continue
			}
			if m := imgTagRe.FindString(rest); m != "" {
				alt := ""
				if a := altAttrRe.FindStringSubmatch(m); a != nil {
					alt = a[1] + a[2] + a[3]
				}
				push(span{text: imageText(alt)})
				i += len(m)
				continue
			}
			if m := tagRe.FindString(rest); m != "" {
				i += len(m)
				continue
			}
		}
		plain.WriteByte(c)
		i++
	}
	if plain.Len() > 0 {
		out = append(out, span{text: plain.String()})
	}
	return out
}

// codeSpan parses a code span at the start of s: a run of n backticks closed
// by a run of exactly n. It returns the content and the bytes consumed.
func codeSpan(s string) (string, int, bool) {
	n := len(s) - len(strings.TrimLeft(s, "`"))
	for j := n; j < len(s); {
		k := strings.IndexByte(s[j:], '`')
		if k < 0 {
			return "", 0, false
		}
		start := j + k
		run := len(s[start:]) - len(strings.TrimLeft(s[start:], "`"))
		if run == n {
			text := s[n:start]
			if len(text) >= 2 && text[0] == ' ' && text[len(text)-1] == ' ' && strings.TrimSpace(text) != "" {
				text = text[1 : len(text)-1]
			}
			return text, start + run, true
		}
		j = start + run
	}
	return "", 0, false
}

// link parses [text](url "title") at the start of s, with nested brackets in
// the text. It returns the text, the url and the bytes consumed.
func link(s string) (text, url string, n int, ok bool) {
	depth := 0
	closeText := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				closeText = i
			}
		}
		if closeText >= 0 {
			break
		}
	}
	if closeText < 0 || !strings.HasPrefix(s[closeText+1:], "(") {
		return "", "", 0, false
	}
	rest := s[closeText+2:]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return "", "", 0, false
	}
	dest := strings.TrimSpace(rest[:end])
	if f := strings.Fields(dest); len(f) > 0 {
		dest = strings.Trim(f[0], "<>")
	}
	return s[1:closeText], dest, closeText + 2 + end + 1, true
}

func imageText(alt string) string {
	alt = strings.TrimSpace(alt)
	if alt == "" {
		return "[image]"
	}
	return "[image: " + alt + "]"
}

func plainText(spans []span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.text)
	}
	return b.String()
}
