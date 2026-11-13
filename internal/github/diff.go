package github

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Pull Request diffs (spec §10): `gh api repos/O/R/pulls/N` with the diff
// media type returns the PR's unified diff (git's format); ParseDiff turns
// it into files and hunks for the overlay's Files tab. The parser is pure,
// so the diff is parsed once, off the render path, where it is fetched.

// DiffMediaType is the media type that makes the pulls endpoint answer
// with the unified diff.
const DiffMediaType = "application/vnd.github.diff"

// Collapse thresholds (spec §10).
const (
	// LargeFileLines is how many changed lines a file may have before it
	// starts collapsed.
	LargeFileLines = 400
	// LargeDiffLines and LargeDiffFiles are where a diff starts to warn.
	LargeDiffLines = 1500
	LargeDiffFiles = 50
)

// FileStatus is what a diff did to a file.
type FileStatus string

const (
	FileModified FileStatus = "modified"
	FileAdded    FileStatus = "added"
	FileDeleted  FileStatus = "deleted"
	FileRenamed  FileStatus = "renamed"
	FileCopied   FileStatus = "copied"
)

// LineKind is a diff line's kind, spelled as its unified diff prefix.
type LineKind byte

const (
	LineContext LineKind = ' '
	LineAdd     LineKind = '+'
	LineDel     LineKind = '-'
)

// DiffLine is one line of a hunk. Old and New are its line numbers in the
// old and new file; 0 on the side it is not on (an added line has no Old).
// Text is the line without its prefix, as the diff has it (tabs and a
// CRLF's CR included): the viewer sanitises it for the terminal.
type DiffLine struct {
	Kind     LineKind
	Old, New int
	Text     string
	// NoNewline marks the last line of a side that has no newline at the
	// end of the file (git's "\ No newline at end of file").
	NoNewline bool
}

// Hunk is one @@ block: its header line, ranges, the section text git
// prints after the ranges (often the enclosing function) and its lines.
type Hunk struct {
	Header             string
	OldStart, OldLines int
	NewStart, NewLines int
	Section            string
	Lines              []DiffLine
}

// DiffFile is one file of a diff. A binary file has no hunks; neither has
// a pure rename or a mode change.
type DiffFile struct {
	OldPath, NewPath string
	Status           FileStatus
	Binary           bool
	// Additions and Deletions count the added and removed lines.
	Additions, Deletions int
	Hunks                []Hunk
	// start and end bound the file's text in Diff.Text (FileText).
	start, end int
}

// Path is the file's path: the new one, or the old one for a deleted file.
func (f DiffFile) Path() string {
	if f.Status == FileDeleted || f.NewPath == "" {
		return f.OldPath
	}
	return f.NewPath
}

// Changed is how many lines the file adds and removes.
func (f DiffFile) Changed() int { return f.Additions + f.Deletions }

// Generated reports whether the file is generated (IsGenerated).
func (f DiffFile) Generated() bool { return IsGenerated(f.Path()) }

// StartsCollapsed reports whether the viewer shows the file as one line
// until it is expanded: a generated file, or one with more than
// LargeFileLines changed lines.
func (f DiffFile) StartsCollapsed() bool {
	return f.Generated() || f.Changed() > LargeFileLines
}

// Diff is a parsed unified diff.
type Diff struct {
	Files                []DiffFile
	Additions, Deletions int
	// Text is the diff as gh printed it (the pager gets it whole).
	Text string
}

// Changed is how many lines the diff adds and removes.
func (d Diff) Changed() int { return d.Additions + d.Deletions }

// Large reports whether the diff is over LargeDiffLines changed lines or
// LargeDiffFiles files: the viewer warns above it.
func (d Diff) Large() bool {
	return d.Changed() > LargeDiffLines || len(d.Files) > LargeDiffFiles
}

// FileText is file i's part of the diff text, from its `diff --git` line.
func (d Diff) FileText(i int) string {
	if i < 0 || i >= len(d.Files) {
		return ""
	}
	f := d.Files[i]
	return d.Text[f.start:f.end]
}

// lockfiles are generated dependency lock files, by base name.
var lockfiles = map[string]bool{
	"go.sum": true, "go.work.sum": true, "package-lock.json": true, "npm-shrinkwrap.json": true,
	"yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true, "Cargo.lock": true,
	"Gemfile.lock": true, "poetry.lock": true, "Pipfile.lock": true, "composer.lock": true,
	"uv.lock": true, "mix.lock": true, "flake.lock": true, "Podfile.lock": true, "pubspec.lock": true,
}

// IsGenerated reports whether path is a generated file the viewer starts
// collapsed (spec §10): go.sum and lockfiles, anything under testdata/,
// goldens and snapshots, protobuf and *_gen.go output, minified assets.
func IsGenerated(p string) bool {
	base := path.Base(p)
	if lockfiles[base] {
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		switch seg {
		case "testdata", "goldens", "__snapshots__":
			return true
		}
	}
	for _, suffix := range []string{".golden", ".snap", ".pb.go", "_gen.go", ".min.js", ".min.css"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// PRDiffArgs is the gh call (after `gh`) that fetches PR number's diff.
func PRDiffArgs(repo Repo, number int) []string {
	return []string{"api", "--hostname", repo.HostOrDefault(),
		fmt.Sprintf("repos/%s/%s/pulls/%d", repo.Owner, repo.Name, number), "-H", "Accept: " + DiffMediaType}
}

// PRDiff fetches and parses Pull Request number's diff at its current
// head. Failures are *Error.
func (c *Client) PRDiff(ctx context.Context, repo Repo, number int) (Diff, error) {
	if repo.Owner == "" || repo.Name == "" {
		return Diff{}, fmt.Errorf("fetching the diff of #%d in %q: owner and name are required", number, repo.String())
	}
	args := PRDiffArgs(repo, number)
	out, err := c.API(ctx, repo.HostOrDefault(), Call{Args: args[3:]})
	if err != nil {
		return Diff{}, err
	}
	return ParseDiff(string(out)), nil
}

// ParseDiff parses a git unified diff. It never fails: what it does not
// recognise it skips, so a diff it cannot read shows as fewer files rather
// than none.
func ParseDiff(text string) Diff {
	d := Diff{Text: text}
	var f *DiffFile
	var h *Hunk
	// left counts the old and new lines the hunk still holds.
	leftOld, leftNew := 0, 0
	oldNo, newNo := 0, 0
	closeFile := func(at int) {
		if f != nil {
			f.end = at
			d.Additions += f.Additions
			d.Deletions += f.Deletions
			d.Files = append(d.Files, *f)
		}
		f, h = nil, nil
	}
	for pos := 0; pos < len(text); {
		end := strings.IndexByte(text[pos:], '\n')
		next := len(text)
		if end >= 0 {
			next = pos + end + 1
			end = pos + end
		} else {
			end = len(text)
		}
		ln := text[pos:end]
		start := pos
		pos = next

		inHunk := h != nil && (leftOld > 0 || leftNew > 0)
		if inHunk {
			kind := byte(' ')
			body := ""
			if ln != "" {
				kind, body = ln[0], ln[1:]
			}
			switch kind {
			case ' ':
				h.Lines = append(h.Lines, DiffLine{Kind: LineContext, Old: oldNo, New: newNo, Text: body})
				oldNo++
				newNo++
				leftOld--
				leftNew--
				continue
			case '-':
				h.Lines = append(h.Lines, DiffLine{Kind: LineDel, Old: oldNo, Text: body})
				f.Deletions++
				oldNo++
				leftOld--
				continue
			case '+':
				h.Lines = append(h.Lines, DiffLine{Kind: LineAdd, New: newNo, Text: body})
				f.Additions++
				newNo++
				leftNew--
				continue
			case '\\':
				markNoNewline(h)
				continue
			}
			// Anything else ends a hunk whose counts lied.
		}
		if h != nil && strings.HasPrefix(ln, `\`) {
			markNoNewline(h)
			continue
		}
		if strings.HasPrefix(ln, "diff --git ") {
			closeFile(start)
			oldP, newP := splitGitHeader(strings.TrimPrefix(ln, "diff --git "))
			f = &DiffFile{OldPath: oldP, NewPath: newP, Status: FileModified, start: start}
			continue
		}
		if f == nil {
			continue
		}
		switch {
		case strings.HasPrefix(ln, "@@ "):
			nh, ok := parseHunkHeader(ln)
			if !ok {
				h = nil
				continue
			}
			f.Hunks = append(f.Hunks, nh)
			h = &f.Hunks[len(f.Hunks)-1]
			leftOld, leftNew = nh.OldLines, nh.NewLines
			oldNo, newNo = nh.OldStart, nh.NewStart
		case h != nil:
			// Past the last hunk's counts: nothing but the next file.
		case strings.HasPrefix(ln, "new file mode"):
			f.Status = FileAdded
		case strings.HasPrefix(ln, "deleted file mode"):
			f.Status = FileDeleted
		case strings.HasPrefix(ln, "rename from "):
			f.Status, f.OldPath = FileRenamed, unquotePath(strings.TrimPrefix(ln, "rename from "), "")
		case strings.HasPrefix(ln, "rename to "):
			f.Status, f.NewPath = FileRenamed, unquotePath(strings.TrimPrefix(ln, "rename to "), "")
		case strings.HasPrefix(ln, "copy from "):
			f.Status, f.OldPath = FileCopied, unquotePath(strings.TrimPrefix(ln, "copy from "), "")
		case strings.HasPrefix(ln, "copy to "):
			f.Status, f.NewPath = FileCopied, unquotePath(strings.TrimPrefix(ln, "copy to "), "")
		case strings.HasPrefix(ln, "--- "):
			if p := strings.TrimPrefix(ln, "--- "); p != "/dev/null" {
				f.OldPath = unquotePath(p, "a/")
			}
		case strings.HasPrefix(ln, "+++ "):
			if p := strings.TrimPrefix(ln, "+++ "); p != "/dev/null" {
				f.NewPath = unquotePath(p, "b/")
			}
		case strings.HasPrefix(ln, "Binary files "), ln == "GIT binary patch":
			f.Binary = true
		}
	}
	closeFile(len(text))
	return d
}

// markNoNewline marks the hunk's last line as lacking a final newline.
func markNoNewline(h *Hunk) {
	if n := len(h.Lines); n > 0 {
		h.Lines[n-1].NoNewline = true
	}
}

// parseHunkHeader parses `@@ -a[,b] +c[,d] @@ section`.
func parseHunkHeader(ln string) (Hunk, bool) {
	rest := strings.TrimPrefix(ln, "@@ ")
	end := strings.Index(rest, " @@")
	if end < 0 {
		return Hunk{}, false
	}
	ranges := strings.Fields(rest[:end])
	if len(ranges) != 2 || !strings.HasPrefix(ranges[0], "-") || !strings.HasPrefix(ranges[1], "+") {
		return Hunk{}, false
	}
	os, ol, ok1 := parseRange(ranges[0][1:])
	ns, nl, ok2 := parseRange(ranges[1][1:])
	if !ok1 || !ok2 {
		return Hunk{}, false
	}
	return Hunk{Header: ln, OldStart: os, OldLines: ol, NewStart: ns, NewLines: nl,
		Section: strings.TrimSpace(rest[end+3:])}, true
}

// parseRange parses `start[,count]`; a missing count is 1.
func parseRange(s string) (int, int, bool) {
	start, count, has := strings.Cut(s, ",")
	a, err := strconv.Atoi(start)
	if err != nil {
		return 0, 0, false
	}
	if !has {
		return a, 1, true
	}
	b, err := strconv.Atoi(count)
	if err != nil {
		return 0, 0, false
	}
	return a, b, true
}

// splitGitHeader splits the paths of a `diff --git a/X b/Y` line. Quoted
// paths are unquoted. Unquoted paths may hold spaces, so the split is only
// certain when both are the same file (the line is "a/P b/P"); otherwise
// the rename, copy and ---/+++ lines that follow set the paths.
func splitGitHeader(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		if q, err := strconv.QuotedPrefix(s); err == nil {
			rest := strings.TrimPrefix(s[len(q):], " ")
			return unquotePath(q, "a/"), unquotePath(rest, "b/")
		}
	}
	if n := len(s); n%2 == 1 {
		half := (n - 1) / 2
		a, b := s[:half], s[half+1:]
		if s[half] == ' ' && strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") && a[2:] == b[2:] {
			return a[2:], b[2:]
		}
	}
	if i := strings.Index(s, " b/"); i >= 0 {
		return strings.TrimPrefix(s[:i], "a/"), s[i+3:]
	}
	return s, s
}

// unquotePath unquotes a git path (C-style quoted when it holds unusual
// bytes) and drops its a/ or b/ prefix.
func unquotePath(p, prefix string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			p = u
		}
	}
	// A path ending in a tab is git's marker for a path with spaces in
	// the ---/+++ lines of some diff producers.
	p = strings.TrimSuffix(p, "\t")
	return strings.TrimPrefix(p, prefix)
}
