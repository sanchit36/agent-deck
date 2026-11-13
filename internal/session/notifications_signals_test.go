package session

import (
	"testing"
	"time"
)

// TestNotificationManagerSignalMarks: a session an Attention signal marked
// (its linked PR changed, spec #18 §15) joins the bar even when it is not
// waiting, with the signal's glyph before its title, newest first; the
// session being looked at is left out like any other.
func TestNotificationManagerSignalMarks(t *testing.T) {
	nm := NewNotificationManager(6, false, false)
	t0 := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	nm.SetMarks(map[string]NotificationMark{
		"pr":  {Glyph: "✗", At: t0.Add(time.Minute)},
		"cur": {Glyph: "✔", At: t0},
	})
	instances := []*Instance{
		{ID: "wait", Title: "waiting one", Status: StatusWaiting},
		{ID: "pr", Title: "fix login", Status: StatusIdle},
		{ID: "cur", Title: "attached", Status: StatusRunning},
		{ID: "idle", Title: "idle one", Status: StatusIdle},
	}
	nm.SyncFromInstances(instances, "cur")
	if !nm.Has("pr") || nm.Has("cur") || nm.Has("idle") {
		t.Fatalf("entries = %+v, want the marked and the waiting session only", nm.GetEntries())
	}
	if got, want := nm.FormatBar(), "⚡ [1] ✗ fix login [2] waiting one"; got != want {
		t.Fatalf("FormatBar() = %q, want %q", got, want)
	}

	// The mark is taken away: the session leaves the bar.
	nm.SetMarks(nil)
	nm.SyncFromInstances(instances, "cur")
	if nm.Has("pr") {
		t.Fatal("an unmarked idle session stays on the bar")
	}
	if got, want := nm.FormatBar(), "⚡ [1] waiting one"; got != want {
		t.Fatalf("FormatBar() = %q, want %q", got, want)
	}
}

// TestNotificationManagerSignalMarkOnWaitingSession: a waiting session that
// is also marked shows the glyph too.
func TestNotificationManagerSignalMarkOnWaitingSession(t *testing.T) {
	nm := NewNotificationManager(6, false, false)
	nm.SetMarks(map[string]NotificationMark{"w": {Glyph: "◆", At: time.Now()}})
	nm.SyncFromInstances([]*Instance{{ID: "w", Title: "shipit", Status: StatusWaiting}}, "")
	if got, want := nm.FormatBar(), "⚡ [1] ◆ shipit"; got != want {
		t.Fatalf("FormatBar() = %q, want %q", got, want)
	}
}
