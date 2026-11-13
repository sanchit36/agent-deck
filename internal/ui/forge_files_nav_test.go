package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/github/githubtest"
	tea "github.com/charmbracelet/bubbletea"
)

// navDiff is a hand-written diff with one file of each shape: a.go with
// two hunks, a tab and a long line; go.sum (generated); a binary image;
// big.txt (450 lines); b.go with no newline at the end; a pure rename.
var navDiff = func() string {
	var b strings.Builder
	b.WriteString("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n" +
		"@@ -1,3 +1,3 @@ package a\n" +
		" package a\n" +
		"-\tvar x = 1\n" +
		"+\tvar x = 2\n" +
		" \n" +
		"@@ -50,3 +50,4 @@ func f() {\n" +
		" \treturn\n" +
		"+\t// " + strings.Repeat("0123456789", 20) + "END\n" +
		" }\n" +
		" \n")
	b.WriteString("diff --git a/go.sum b/go.sum\n--- a/go.sum\n+++ b/go.sum\n@@ -1,1 +1,2 @@\n x v1 h1:a\n+y v1 h1:b\n")
	b.WriteString("diff --git a/img/logo.png b/img/logo.png\nindex 1..2 100644\nBinary files a/img/logo.png and b/img/logo.png differ\n")
	b.WriteString("diff --git a/big.txt b/big.txt\nnew file mode 100644\n--- /dev/null\n+++ b/big.txt\n@@ -0,0 +1,450 @@\n")
	for i := 1; i <= 450; i++ {
		b.WriteString("+line\n")
	}
	b.WriteString("diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1,1 +1,1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n")
	b.WriteString("diff --git a/old.md b/new.md\nsimilarity index 100%\nrename from old.md\nrename to new.md\n")
	return b.String()
}()

// navHome opens navDiff's Files tab at w×h.
func navHome(t *testing.T, w, h int) *Home {
	t.Helper()
	home, _ := diffHome(t, w, h, navDiff)
	return openFiles(t, home)
}

// TestForgeFilesMoveByFile: ] and [ step through the files, stopping at
// the ends; each file starts at the top.
func TestForgeFilesMoveByFile(t *testing.T) {
	h := navHome(t, 120, 30)
	h = drive(t, h, runeKey('['))
	if f := h.forge.overlay.files.file; f != 0 {
		t.Fatalf("[ on the first file moved to %d", f)
	}
	h = drive(t, h, runeKey(']'))
	if frame := filesFrame(h); !strings.Contains(frame, "M go.sum") || !strings.Contains(frame, "file 2/6") {
		t.Fatalf("] did not show go.sum as file 2/6:\n%s", frame)
	}
	for i := 0; i < 10; i++ {
		h = drive(t, h, runeKey(']'))
	}
	if frame := filesFrame(h); !strings.Contains(frame, "R old.md → new.md") || !strings.Contains(frame, "file 6/6") {
		t.Fatalf("] past the end did not stop on the last file:\n%s", frame)
	}
	h = drive(t, h, runeKey('['))
	if !strings.Contains(filesFrame(h), "file 5/6") {
		t.Fatal("[ did not go back a file")
	}
}

// TestForgeFilesCollapseRules: generated and large files start collapsed
// to one line with their size that enter expands (and collapses again);
// a binary file shows a note, as do a pure rename and a missing final
// newline.
func TestForgeFilesCollapseRules(t *testing.T) {
	h := navHome(t, 120, 30)
	for _, s := range []struct {
		file int
		want string
	}{
		{1, "▸ generated file · 1 changed line · enter expands"},
		{2, "binary file · not shown"},
		{3, "▸ large file · 450 changed lines · enter expands"},
		{4, "\\ no newline at end of file"},
		{5, "renamed from old.md · no content changes"},
	} {
		h.selectForgeFile(s.file)
		if frame := filesFrame(h); !strings.Contains(frame, s.want) {
			t.Fatalf("file %d lacks %q:\n%s", s.file+1, s.want, frame)
		}
	}
	h.selectForgeFile(1)
	h = drive(t, h, overlayEnter())
	frame := filesFrame(h)
	if strings.Contains(frame, "enter expands") || !strings.Contains(frame, "+ y v1 h1:b") {
		t.Fatalf("enter did not expand go.sum:\n%s", frame)
	}
	h = drive(t, h, overlayEnter())
	if !strings.Contains(filesFrame(h), "▸ generated file") {
		t.Fatal("enter did not collapse go.sum again")
	}
	if strings.Contains(filesFrame(h), "large PR") {
		t.Fatal("a small diff warned it is large")
	}
}

// TestForgeFilesHunks: n moves the next hunk to the top, then on to the
// next file that shows hunks (skipping collapsed and binary ones); N goes
// back, into the previous file's last hunk.
func TestForgeFilesHunks(t *testing.T) {
	h := navHome(t, 120, 12)
	o := &h.forge.overlay
	h = drive(t, h, runeKey('n'))
	if o.files.file != 0 || o.scroll[forgeOverlayFiles].offset != 4 {
		t.Fatalf("n: file %d offset %d, want a.go's second hunk (row 4)", o.files.file, o.scroll[forgeOverlayFiles].offset)
	}
	if body := filesFrame(h); !strings.Contains(body, "@@ -50,3 +50,4 @@ func f() {") {
		t.Fatalf("second hunk not on screen:\n%s", body)
	}
	h = drive(t, h, runeKey('n'))
	if o.files.file != 4 || o.scroll[forgeOverlayFiles].offset != 0 {
		t.Fatalf("n past a.go's last hunk: file %d offset %d, want b.go (5) at its top", o.files.file, o.scroll[forgeOverlayFiles].offset)
	}
	if h.newDialog.IsVisible() {
		t.Fatal("n on Files started a session")
	}
	h = drive(t, h, runeKey('N'))
	if o.files.file != 0 || o.scroll[forgeOverlayFiles].offset != 4 {
		t.Fatalf("N: file %d offset %d, want a.go's last hunk", o.files.file, o.scroll[forgeOverlayFiles].offset)
	}
	_ = drive(t, h, runeKey('N'))
	if o.scroll[forgeOverlayFiles].offset != 0 {
		t.Fatalf("N: offset %d, want a.go's first hunk", o.scroll[forgeOverlayFiles].offset)
	}
}

// TestForgeFilesPan: long lines are cut with ›, never wrapped; l pans
// them and h pans back; tabs are expanded.
func TestForgeFilesPan(t *testing.T) {
	h := navHome(t, 120, 30)
	frame := filesFrame(h)
	if !strings.Contains(frame, "+     var x = 2") {
		t.Fatalf("tab not expanded:\n%s", frame)
	}
	long := func(frame string) string {
		for _, l := range strings.Split(frame, "\n") {
			if strings.Contains(l, "0123456789") || strings.Contains(l, "END") {
				return l
			}
		}
		return ""
	}
	before := long(frame)
	if !strings.HasSuffix(strings.TrimRight(before, " "), "›") || strings.Contains(before, "END") {
		t.Fatalf("long line not cut with ›: %q", before)
	}
	for i := 0; i < 40; i++ {
		h = drive(t, h, runeKey('l'))
	}
	after := long(filesFrame(h))
	if !strings.Contains(after, "END") || strings.HasSuffix(strings.TrimRight(after, " "), "›") {
		t.Fatalf("l did not pan to the line's end: %q", after)
	}
	for i := 0; i < 40; i++ {
		h = drive(t, h, runeKey('h'))
	}
	if got := long(filesFrame(h)); got != before {
		t.Fatalf("h did not pan back:\n got %q\nwant %q", got, before)
	}
}

// TestForgeFilesFocus: tab gives the list the keys (j/k move the file
// cursor, enter hands the keys back to the diff); otherwise j scrolls the
// diff.
func TestForgeFilesFocus(t *testing.T) {
	h := navHome(t, 120, 12)
	o := &h.forge.overlay
	h = drive(t, h, runeKey('j'))
	if o.files.file != 0 || o.scroll[forgeOverlayFiles].offset != 1 {
		t.Fatalf("j on the diff: file %d offset %d", o.files.file, o.scroll[forgeOverlayFiles].offset)
	}
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyTab})
	h = drive(t, h, runeKey('j'))
	h = drive(t, h, runeKey('j'))
	if o.files.file != 2 || !strings.Contains(filesFrame(h), "file 3/6") {
		t.Fatalf("j on the list: file %d", o.files.file)
	}
	h = drive(t, h, runeKey('G'))
	if o.files.file != 5 {
		t.Fatalf("G on the list: file %d", o.files.file)
	}
	h = drive(t, h, overlayEnter())
	_ = drive(t, h, runeKey('k'))
	if o.files.file != 5 || o.files.listFocus {
		t.Fatalf("enter did not hand the keys back to the diff: file %d focus %v", o.files.file, o.files.listFocus)
	}
}

// TestForgeFilesNarrow: under 90 columns the list hides and the header
// carries the position; f opens the list full screen, enter picks a file,
// esc closes the list before the overlay.
func TestForgeFilesNarrow(t *testing.T) {
	h := navHome(t, 70, 24)
	frame := filesFrame(h)
	if strings.Contains(frame, "Files 6") || !strings.Contains(frame, "file 1/6 · f list") {
		t.Fatalf("narrow Files tab:\n%s", frame)
	}
	h = drive(t, h, runeKey('f'))
	if frame := filesFrame(h); !strings.Contains(frame, "Files 6") || strings.Contains(frame, "file 1/6") {
		t.Fatalf("f did not open the list full screen:\n%s", frame)
	}
	h = drive(t, h, runeKey('j'))
	h = drive(t, h, overlayEnter())
	if frame := filesFrame(h); !strings.Contains(frame, "file 2/6") {
		t.Fatalf("enter did not show the picked file:\n%s", frame)
	}
	h = drive(t, h, runeKey('f'))
	h = drive(t, h, overlayEsc())
	if !h.forgeOverlayVisible() || h.forge.overlay.files.listFull {
		t.Fatal("esc did not close the list first")
	}
	h = drive(t, h, overlayEsc())
	if h.forgeOverlayVisible() {
		t.Fatal("esc did not close the overlay")
	}
}

// TestForgeFilesNewSession: S starts a session on the PR from the Files
// tab (n moves by hunk there).
func TestForgeFilesNewSession(t *testing.T) {
	h, fake := diffHome(t, 120, 30, navDiff)
	serveItemText(fake, githubtest.MustLoad(t, githubtest.ItemTextPR))
	h = openFiles(t, h)
	h = launchDrive(t, h, runeKey('S'))
	if !h.newDialog.IsVisible() || h.newDialog.forge == nil || h.newDialog.forge.number != 14927 {
		t.Fatal("S on Files did not open the launch dialog for #14927")
	}
	if h.forgeOverlayVisible() {
		t.Fatal("the overlay stayed open under the dialog")
	}
}

// TestForgeFilesKeySpace is the duplicate-key test for the Files tab
// (spec #18 §4), Cyrillic twins included: every Files key is a named
// forge_* action with a default, and its keys come first, so they may only
// shadow the overlay's launch key (n is next hunk there, so
// forge_files_new's S starts the session); no other overlay key, and no
// two Files keys, may share a key.
func TestForgeFilesKeySpace(t *testing.T) {
	seen := map[string]string{}
	claim := func(key, owner string) {
		for _, alias := range hotkeyAliases(key) {
			if prev, ok := seen[alias]; ok && prev != owner {
				t.Errorf("on Files %q means both %q and %q", alias, prev, owner)
				continue
			}
			seen[alias] = owner
		}
	}
	for _, action := range forgeFilesActionOrder {
		key := defaultForgeHotkeyBindings[action]
		if key == "" {
			t.Errorf("Files action %q has no default key", action)
		}
		if !strings.HasPrefix(action, "forge_") {
			t.Errorf("Files action %q is not prefixed forge_", action)
		}
		if _, clash := defaultHotkeyBindings[action]; clash {
			t.Errorf("Files action %q is also a global action", action)
		}
		claim(key, action)
	}
	for _, action := range forgeOverlayActionOrder {
		if action == hotkeyForgeNew {
			continue
		}
		claim(defaultForgeHotkeyBindings[action], action)
	}
	for key, owner := range forgeOverlayFixedKeys {
		claim(key, "fixed:"+owner)
	}
	claim("ctrl+c", "quit")
	lookup := buildForgeFilesLookup(defaultForgeHotkeyBindings)
	if lookup[defaultForgeHotkeyBindings[hotkeyForgeNew]] != hotkeyForgeNextHunk {
		t.Error("the overlay's launch key is not the Files tab's next hunk")
	}
	if lookup["S"] != hotkeyForgeFilesNew || lookup["Ы"] != hotkeyForgeFilesNew {
		t.Errorf("S / Ы on Files = %q / %q, want %s", lookup["S"], lookup["Ы"], hotkeyForgeFilesNew)
	}
	if lookup["т"] != hotkeyForgeNextHunk {
		t.Errorf("т (the twin of n) on Files = %q, want %s", lookup["т"], hotkeyForgeNextHunk)
	}
}

// filesHomeWith opens navDiff's Files tab at w×ht with the forge_*
// [hotkeys] overrides applied.
func filesHomeWith(t *testing.T, w, ht int, overrides map[string]string) *Home {
	t.Helper()
	h, fake := diffHome(t, w, ht, navDiff)
	serveItemText(fake, githubtest.MustLoad(t, githubtest.ItemTextPR))
	h.setForgeEnabled(true, overrides)
	return openFiles(t, h)
}

// TestForgeFilesKeysRebind: the Files keys are [hotkeys] actions: a
// rebound key does the action and the old one stops, "" unbinds, and the
// hint footer and the help show the bound keys.
func TestForgeFilesKeysRebind(t *testing.T) {
	h := filesHomeWith(t, 120, 12, map[string]string{
		"forge_next_file": "}", "forge_prev_file": "{",
		"forge_next_hunk": "x", "forge_split": "v", "forge_focus": "",
		"forge_pager_file": "ctrl+g",
	})
	o := &h.forge.overlay
	h = drive(t, h, runeKey(']'))
	if o.files.file != 0 {
		t.Fatalf("] still moves by file after forge_next_file = }: file %d", o.files.file)
	}
	h = drive(t, h, runeKey('}'))
	if o.files.file != 1 {
		t.Fatalf("} did not move to the next file: file %d", o.files.file)
	}
	h = drive(t, h, runeKey('{'))
	if o.files.file != 0 {
		t.Fatalf("{ did not move back a file: file %d", o.files.file)
	}
	h = drive(t, h, runeKey('x'))
	if o.files.file != 0 || o.scroll[forgeOverlayFiles].offset != 4 {
		t.Fatalf("x did not move to the next hunk: file %d offset %d", o.files.file, o.scroll[forgeOverlayFiles].offset)
	}
	h = drive(t, h, runeKey('v'))
	if !o.files.split {
		t.Fatal("v did not toggle the split view")
	}
	h = drive(t, h, runeKey('s'))
	if !o.files.split {
		t.Fatal("s still toggles the split view after forge_split = v")
	}
	h = drive(t, h, tea.KeyMsg{Type: tea.KeyTab})
	if o.files.listFocus {
		t.Fatal("tab still moves the focus after forge_focus = \"\"")
	}
	hints := stripAnsi(h.forgeOverlayHints(400))
	for _, want := range []string{"x/N hunk", "}/{ file", "v unified", "ctrl+g/\\ pager"} {
		if !strings.Contains(hints, want) {
			t.Errorf("hint footer %q lacks %q", hints, want)
		}
	}
	if strings.Contains(hints, "focus") {
		t.Errorf("hint footer %q shows the unbound focus key", hints)
	}
	help := map[string]string{}
	for _, r := range h.forgeHelpRows() {
		help[r[1]] = r[0]
	}
	for desc, want := range map[string]string{
		"Files: next / previous hunk":                    "x / N",
		"Files: next / previous file":                    "} / {",
		"Files: split view, old | new (120+ columns)":    "v",
		"Files: the file's / the PR's diff in the pager": "ctrl+g / \\",
		"Files: focus the file list / the diff":          "",
	} {
		if help[desc] != want {
			t.Errorf("help row %q = %q, want %q", desc, help[desc], want)
		}
	}
}

// TestForgeFilesNewFollowsForgeNew: on Files the new-session key is
// forge_new's own key unless a Files action has it (by default n is next
// hunk); then it is forge_files_new (default S).
func TestForgeFilesNewFollowsForgeNew(t *testing.T) {
	newKey := func(h *Home) string {
		for _, r := range h.forgeHelpRows() {
			if r[1] == "Files: start a session on the PR" {
				return r[0]
			}
		}
		return ""
	}
	hints := func(h *Home) string { return stripAnsi(h.forgeOverlayHints(400)) }

	t.Run("default: S", func(t *testing.T) {
		h := filesHomeWith(t, 120, 30, nil)
		if got := newKey(h); got != "S" || !strings.Contains(hints(h), "S new session") {
			t.Fatalf("help %q, hints %q", got, hints(h))
		}
	})

	t.Run("forge_new rebound to a free key", func(t *testing.T) {
		h := filesHomeWith(t, 120, 30, map[string]string{"forge_new": "x"})
		if got := newKey(h); got != "x" || !strings.Contains(hints(h), "x new session") {
			t.Fatalf("help %q, hints %q", got, hints(h))
		}
		h = launchDrive(t, h, runeKey('S'))
		if h.newDialog.IsVisible() || !h.forgeOverlayVisible() {
			t.Fatal("S still starts a session while forge_new = x reaches Files")
		}
		h = launchDrive(t, h, runeKey('x'))
		if !h.newDialog.IsVisible() || h.newDialog.forge == nil || h.newDialog.forge.number != 14927 {
			t.Fatal("x (forge_new) on Files did not open the launch dialog for #14927")
		}
	})

	t.Run("forge_new on a Files key keeps forge_files_new", func(t *testing.T) {
		h := filesHomeWith(t, 120, 30, map[string]string{"forge_new": "N", "forge_files_new": "ctrl+o"})
		if got := newKey(h); got != "ctrl+o" {
			t.Fatalf("help %q, want ctrl+o", got)
		}
		h = launchDrive(t, h, runeKey('N'))
		if h.newDialog.IsVisible() {
			t.Fatal("N started a session on Files instead of moving by hunk")
		}
		h = launchDrive(t, h, tea.KeyMsg{Type: tea.KeyCtrlO})
		if !h.newDialog.IsVisible() {
			t.Fatal("ctrl+o (forge_files_new) on Files did not open the launch dialog")
		}
	})
}
