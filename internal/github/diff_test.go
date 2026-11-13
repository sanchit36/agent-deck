package github_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
)

// diffLines is a compact spelling of a hunk's lines for the table: kind,
// old and new line numbers, text, and "⏎" when the line has no newline at
// the end of the file.
type diffLine struct {
	kind     github.LineKind
	old, new int
	text     string
	noEOL    bool
}

func linesOf(h github.Hunk) []diffLine {
	var out []diffLine
	for _, l := range h.Lines {
		out = append(out, diffLine{l.Kind, l.Old, l.New, l.Text, l.NoNewline})
	}
	return out
}

const (
	ctxL = github.LineContext
	addL = github.LineAdd
	delL = github.LineDel
)

// TestParseDiff: the unified diff parser, one case per shape gh's diff
// media type produces.
func TestParseDiff(t *testing.T) {
	type wantFile struct {
		oldPath, newPath string
		status           github.FileStatus
		binary           bool
		adds, dels       int
		hunks            [][]diffLine
		headers          []string
	}
	cases := []struct {
		name string
		in   string
		want []wantFile
	}{
		{
			name: "modified, two hunks with section headers",
			in: "diff --git a/main.go b/main.go\n" +
				"index 1111111..2222222 100644\n" +
				"--- a/main.go\n" +
				"+++ b/main.go\n" +
				"@@ -1,3 +1,3 @@ package main\n" +
				" a\n" +
				"-b\n" +
				"+B\n" +
				" c\n" +
				"@@ -10,2 +10,3 @@ func main() {\n" +
				" x\n" +
				"+y\n" +
				" z\n",
			want: []wantFile{{
				oldPath: "main.go", newPath: "main.go", status: github.FileModified, adds: 2, dels: 1,
				headers: []string{"@@ -1,3 +1,3 @@ package main", "@@ -10,2 +10,3 @@ func main() {"},
				hunks: [][]diffLine{
					{{ctxL, 1, 1, "a", false}, {delL, 2, 0, "b", false}, {addL, 0, 2, "B", false}, {ctxL, 3, 3, "c", false}},
					{{ctxL, 10, 10, "x", false}, {addL, 0, 11, "y", false}, {ctxL, 11, 12, "z", false}},
				},
			}},
		},
		{
			name: "added file, single-line hunk header without counts",
			in: "diff --git a/new.txt b/new.txt\n" +
				"new file mode 100644\n" +
				"index 0000000..e69de29\n" +
				"--- /dev/null\n" +
				"+++ b/new.txt\n" +
				"@@ -0,0 +1 @@\n" +
				"+hello\n",
			want: []wantFile{{oldPath: "new.txt", newPath: "new.txt", status: github.FileAdded, adds: 1,
				headers: []string{"@@ -0,0 +1 @@"}, hunks: [][]diffLine{{{addL, 0, 1, "hello", false}}}}},
		},
		{
			name: "deleted file",
			in: "diff --git a/old.txt b/old.txt\n" +
				"deleted file mode 100644\n" +
				"index e69de29..0000000\n" +
				"--- a/old.txt\n" +
				"+++ /dev/null\n" +
				"@@ -1,2 +0,0 @@\n" +
				"-one\n" +
				"-two\n",
			want: []wantFile{{oldPath: "old.txt", newPath: "old.txt", status: github.FileDeleted, dels: 2,
				headers: []string{"@@ -1,2 +0,0 @@"}, hunks: [][]diffLine{{{delL, 1, 0, "one", false}, {delL, 2, 0, "two", false}}}}},
		},
		{
			name: "pure rename, no hunks",
			in: "diff --git a/docs/old name.md b/docs/new name.md\n" +
				"similarity index 100%\n" +
				"rename from docs/old name.md\n" +
				"rename to docs/new name.md\n",
			want: []wantFile{{oldPath: "docs/old name.md", newPath: "docs/new name.md", status: github.FileRenamed}},
		},
		{
			name: "rename with changes",
			in: "diff --git a/a.go b/b.go\n" +
				"similarity index 90%\n" +
				"rename from a.go\n" +
				"rename to b.go\n" +
				"index 1111111..2222222 100644\n" +
				"--- a/a.go\n" +
				"+++ b/b.go\n" +
				"@@ -1 +1 @@\n" +
				"-x\n" +
				"+y\n",
			want: []wantFile{{oldPath: "a.go", newPath: "b.go", status: github.FileRenamed, adds: 1, dels: 1,
				headers: []string{"@@ -1 +1 @@"}, hunks: [][]diffLine{{{delL, 1, 0, "x", false}, {addL, 0, 1, "y", false}}}}},
		},
		{
			name: "copy",
			in: "diff --git a/a.go b/c.go\n" +
				"similarity index 100%\n" +
				"copy from a.go\n" +
				"copy to c.go\n",
			want: []wantFile{{oldPath: "a.go", newPath: "c.go", status: github.FileCopied}},
		},
		{
			name: "binary files, then a text file",
			in: "diff --git a/img/logo.png b/img/logo.png\n" +
				"index 1111111..2222222 100644\n" +
				"Binary files a/img/logo.png and b/img/logo.png differ\n" +
				"diff --git a/new.bin b/new.bin\n" +
				"new file mode 100644\n" +
				"index 0000000..3333333\n" +
				"GIT binary patch\n" +
				"literal 4\n" +
				"LcmZQzWMT#Y01f~L\n" +
				"\n" +
				"literal 0\n" +
				"HcmV?d00001\n" +
				"\n" +
				"diff --git a/t.txt b/t.txt\n" +
				"--- a/t.txt\n" +
				"+++ b/t.txt\n" +
				"@@ -1 +1 @@\n" +
				"-a\n" +
				"+b\n",
			want: []wantFile{
				{oldPath: "img/logo.png", newPath: "img/logo.png", status: github.FileModified, binary: true},
				{oldPath: "new.bin", newPath: "new.bin", status: github.FileAdded, binary: true},
				{oldPath: "t.txt", newPath: "t.txt", status: github.FileModified, adds: 1, dels: 1,
					headers: []string{"@@ -1 +1 @@"}, hunks: [][]diffLine{{{delL, 1, 0, "a", false}, {addL, 0, 1, "b", false}}}},
			},
		},
		{
			name: "no newline at end of file on both sides",
			in: "diff --git a/f b/f\n" +
				"--- a/f\n" +
				"+++ b/f\n" +
				"@@ -1,2 +1,2 @@\n" +
				" keep\n" +
				"-old\n" +
				"\\ No newline at end of file\n" +
				"+new\n" +
				"\\ No newline at end of file\n",
			want: []wantFile{{oldPath: "f", newPath: "f", status: github.FileModified, adds: 1, dels: 1,
				headers: []string{"@@ -1,2 +1,2 @@"},
				hunks:   [][]diffLine{{{ctxL, 1, 1, "keep", false}, {delL, 2, 0, "old", true}, {addL, 0, 2, "new", true}}}}},
		},
		{
			name: "content lines that look like headers, and a blank context line",
			in: "diff --git a/x.md b/x.md\n" +
				"--- a/x.md\n" +
				"+++ b/x.md\n" +
				"@@ -1,4 +1,4 @@\n" +
				"--- a rule\n" +
				"+++ a plus rule\n" +
				"\n" +
				" diff --git a/q b/q\n" +
				"-@@ not a hunk\n" +
				"+@@ still not\n",
			want: []wantFile{{oldPath: "x.md", newPath: "x.md", status: github.FileModified, adds: 2, dels: 2,
				headers: []string{"@@ -1,4 +1,4 @@"},
				hunks: [][]diffLine{{{delL, 1, 0, "-- a rule", false}, {addL, 0, 1, "++ a plus rule", false},
					{ctxL, 2, 2, "", false}, {ctxL, 3, 3, "diff --git a/q b/q", false},
					{delL, 4, 0, "@@ not a hunk", false}, {addL, 0, 4, "@@ still not", false}}}}},
		},
		{
			name: "mode change only",
			in: "diff --git a/run.sh b/run.sh\n" +
				"old mode 100644\n" +
				"new mode 100755\n",
			want: []wantFile{{oldPath: "run.sh", newPath: "run.sh", status: github.FileModified}},
		},
		{
			name: "quoted paths with escapes",
			in: "diff --git \"a/caf\\303\\251 \\\"q\\\".txt\" \"b/caf\\303\\251 \\\"q\\\".txt\"\n" +
				"--- \"a/caf\\303\\251 \\\"q\\\".txt\"\n" +
				"+++ \"b/caf\\303\\251 \\\"q\\\".txt\"\n" +
				"@@ -1 +1 @@\n" +
				"-a\n" +
				"+b\n",
			want: []wantFile{{oldPath: "café \"q\".txt", newPath: "café \"q\".txt", status: github.FileModified, adds: 1, dels: 1,
				headers: []string{"@@ -1 +1 @@"}, hunks: [][]diffLine{{{delL, 1, 0, "a", false}, {addL, 0, 1, "b", false}}}}},
		},
		{
			name: "CRLF content keeps its text",
			in: "diff --git a/w.txt b/w.txt\n" +
				"--- a/w.txt\n" +
				"+++ b/w.txt\n" +
				"@@ -1 +1 @@\n" +
				"-a\r\n" +
				"+b\r\n",
			want: []wantFile{{oldPath: "w.txt", newPath: "w.txt", status: github.FileModified, adds: 1, dels: 1,
				headers: []string{"@@ -1 +1 @@"}, hunks: [][]diffLine{{{delL, 1, 0, "a\r", false}, {addL, 0, 1, "b\r", false}}}}},
		},
		{name: "empty", in: "", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := github.ParseDiff(tc.in)
			if len(d.Files) != len(tc.want) {
				t.Fatalf("%d files, want %d: %+v", len(d.Files), len(tc.want), d.Files)
			}
			adds, dels := 0, 0
			for i, w := range tc.want {
				f := d.Files[i]
				if f.OldPath != w.oldPath || f.NewPath != w.newPath || f.Status != w.status || f.Binary != w.binary {
					t.Errorf("file %d: %q → %q %s binary=%v, want %q → %q %s binary=%v",
						i, f.OldPath, f.NewPath, f.Status, f.Binary, w.oldPath, w.newPath, w.status, w.binary)
				}
				if f.Additions != w.adds || f.Deletions != w.dels {
					t.Errorf("file %d: +%d −%d, want +%d −%d", i, f.Additions, f.Deletions, w.adds, w.dels)
				}
				var headers []string
				var hunks [][]diffLine
				for _, h := range f.Hunks {
					headers = append(headers, h.Header)
					hunks = append(hunks, linesOf(h))
				}
				if !slices.Equal(headers, w.headers) {
					t.Errorf("file %d: hunk headers %q, want %q", i, headers, w.headers)
				}
				if len(hunks) != len(w.hunks) {
					t.Fatalf("file %d: %d hunks, want %d", i, len(hunks), len(w.hunks))
				}
				for j := range hunks {
					if !slices.Equal(hunks[j], w.hunks[j]) {
						t.Errorf("file %d hunk %d:\n got %+v\nwant %+v", i, j, hunks[j], w.hunks[j])
					}
				}
				adds += w.adds
				dels += w.dels
				// Each file's raw text is its own slice of the diff, from
				// its diff --git line, for the pager (#31).
				if raw := d.FileText(i); !strings.HasPrefix(raw, "diff --git ") {
					t.Errorf("file %d: raw text starts %q", i, raw[:min(len(raw), 20)])
				}
			}
			if d.Additions != adds || d.Deletions != dels {
				t.Errorf("diff +%d −%d, want +%d −%d", d.Additions, d.Deletions, adds, dels)
			}
			var joined strings.Builder
			for i := range d.Files {
				joined.WriteString(d.FileText(i))
			}
			if joined.String() != tc.in {
				t.Error("the files' raw texts do not add up to the diff")
			}
		})
	}
}

// TestDiffHunkHeaderParts: a hunk keeps its ranges and section text.
func TestDiffHunkHeaderParts(t *testing.T) {
	d := github.ParseDiff("diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -7,3 +9,4 @@ func f() {\n a\n+b\n c\n d\n")
	h := d.Files[0].Hunks[0]
	if h.OldStart != 7 || h.OldLines != 3 || h.NewStart != 9 || h.NewLines != 4 || h.Section != "func f() {" {
		t.Fatalf("hunk %+v", h)
	}
}

// TestDiffCollapseRules: generated files and files over 400 changed lines
// start collapsed; a diff over 1500 changed lines or 50 files is large.
func TestDiffCollapseRules(t *testing.T) {
	for _, tc := range []struct {
		path      string
		generated bool
	}{
		{"go.sum", true},
		{"tools/go.sum", true},
		{"go.work.sum", true},
		{"package-lock.json", true},
		{"web/yarn.lock", true},
		{"pnpm-lock.yaml", true},
		{"Cargo.lock", true},
		{"Gemfile.lock", true},
		{"poetry.lock", true},
		{"composer.lock", true},
		{"internal/ui/testdata/frame.golden", true},
		{"cmd/agent-deck/testdata/goldens/list.txt", true},
		{"testdata/x.json", true},
		{"pkg/__snapshots__/a.snap", true},
		{"api/v1/service.pb.go", true},
		{"internal/thing_gen.go", true},
		{"static/app.min.js", true},
		{"static/app.min.css", true},
		{"go.mod", false},
		{"main.go", false},
		{"docs/testdata.md", false},
		{"lock.go", false},
		{"README.md", false},
		{"generator.go", false},
	} {
		if got := github.IsGenerated(tc.path); got != tc.generated {
			t.Errorf("IsGenerated(%q) = %v, want %v", tc.path, got, tc.generated)
		}
	}

	file := func(path string, adds int) github.DiffFile {
		return github.DiffFile{OldPath: path, NewPath: path, Status: github.FileModified, Additions: adds}
	}
	for _, tc := range []struct {
		f         github.DiffFile
		collapsed bool
	}{
		{file("main.go", 400), false},
		{file("main.go", 401), true},
		{file("go.sum", 2), true},
		{github.DiffFile{OldPath: "go.sum", NewPath: "go.sum", Status: github.FileDeleted, Deletions: 3}, true},
	} {
		if got := tc.f.StartsCollapsed(); got != tc.collapsed {
			t.Errorf("%s +%d −%d collapsed=%v, want %v", tc.f.Path(), tc.f.Additions, tc.f.Deletions, got, tc.collapsed)
		}
	}

	var d github.Diff
	d.Additions = 1500
	if d.Large() {
		t.Error("1500 changed lines is large")
	}
	d.Deletions = 1
	if !d.Large() {
		t.Error("1501 changed lines is not large")
	}
	d = github.Diff{Files: make([]github.DiffFile, 50)}
	if d.Large() {
		t.Error("50 files is large")
	}
	d.Files = append(d.Files, github.DiffFile{})
	if !d.Large() {
		t.Error("51 files is not large")
	}
}

// TestPRDiffParsesRecorded: the recorded diff of cli/cli PR #14927 (gh
// api with the diff media type) parses into its 21 files, the counts git
// apply --numstat gives for it, and the collapse rules.
func TestPRDiffParsesRecorded(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.PRDiff), github.PRDiffArgs(cliRepo, 14927)...)
	d, err := github.NewClient(fake).PRDiff(context.Background(), cliRepo, 14927)
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.Calls()[0].Args; !slices.Equal(got, []string{"api", "--hostname", "github.com",
		"repos/cli/cli/pulls/14927", "-H", "Accept: application/vnd.github.diff"}) {
		t.Fatalf("gh call %q", got)
	}
	if len(d.Files) != 21 || d.Additions != 4835 || d.Deletions != 1 || !d.Large() {
		t.Fatalf("%d files +%d −%d large=%v", len(d.Files), d.Additions, d.Deletions, d.Large())
	}
	// From git apply --numstat on the fixture.
	want := map[string][2]int{
		".github/skills/setting-up-identites/SKILL.md":          {79, 0},
		"acceptance/testdata/identity/identity-lifecycle.txtar": {153, 0},
		"pkg/cmd/version/version.go":                            {5, 1},
		"script/identity-e2e/run.sh":                            {894, 0},
		"script/multiaccount-review/08-reset.sh":                {75, 0},
	}
	var collapsed []string
	added := 0
	for _, f := range d.Files {
		if w, ok := want[f.Path()]; ok && (f.Additions != w[0] || f.Deletions != w[1]) {
			t.Errorf("%s +%d −%d, want +%d −%d", f.Path(), f.Additions, f.Deletions, w[0], w[1])
		}
		if f.StartsCollapsed() {
			collapsed = append(collapsed, f.Path())
		}
		if f.Status == github.FileAdded {
			added++
		}
	}
	if added != 15 {
		t.Errorf("%d new files, want 15", added)
	}
	wantCollapsed := []string{
		"acceptance/testdata/identity/identity-lifecycle.txtar",
		"script/identity-e2e/README.md", "script/identity-e2e/bootstrap.sh", "script/identity-e2e/fixture_test.go",
		"script/identity-e2e/offline_test.go", "script/identity-e2e/run.sh", "script/identity-e2e/runner_test.go",
	}
	if !slices.Equal(collapsed, wantCollapsed) {
		t.Errorf("collapsed %q, want %q", collapsed, wantCollapsed)
	}
	if d.Text != string(githubtest.MustLoad(t, githubtest.PRDiff).Result.Stdout) {
		t.Error("the diff's text is not gh's output")
	}
}

// TestPRDiffFailure: a failed diff fetch is the classified gh error.
func TestPRDiffFailure(t *testing.T) {
	fake := githubtest.NewFake().Serve(githubtest.MustLoad(t, githubtest.NotFound), github.PRDiffArgs(cliRepo, 1)...)
	_, err := github.NewClient(fake).PRDiff(context.Background(), cliRepo, 1)
	var ge *github.Error
	if !errors.As(err, &ge) || ge.Class != github.ClassNotFound {
		t.Fatalf("err %v, want a not-found *github.Error", err)
	}
}
