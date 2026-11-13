package ui

import (
	"slices"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/github/freshness"
)

// forgeDiffCacheCap is how many PR diffs the Freshness cache keeps. A diff
// is immutable for its head SHA, so a cached one is never refetched; this
// bounds how many stay cached.
const forgeDiffCacheCap = 12

// boundForgeDiffs keeps the cached diffs bounded. It runs from
// forgeFilesFetch, which sees every diff key before it is fetched (the
// head of the overlay's PR, from its cached detail) and runs again after
// every fetch lands. The diff on screen and the head's diff are marked
// most recently used and never dropped; another head's diff of a PR is
// dropped once the diff of the PR's current head (from its cached detail)
// is cached; past forgeDiffCacheCap, the least recently used go. A diff
// whose fetch is in flight stays.
func (h *Home) boundForgeDiffs() {
	c := h.forge.fresh
	if c == nil {
		return
	}
	var keep []freshness.Key
	if _, ok := h.forgeFilesDetailKey(); ok {
		o := &h.forge.overlay
		if o.files.sha != "" {
			keep = append(keep, freshness.DiffKey(o.repo, o.row.number, o.files.sha))
		}
		if sha := h.forgeFilesHead(); sha != "" && sha != o.files.sha {
			keep = append(keep, freshness.DiffKey(o.repo, o.row.number, sha))
		}
	}
	for _, k := range keep {
		h.forge.diffs = append(slices.DeleteFunc(h.forge.diffs, func(d freshness.Key) bool { return d == k }), k)
	}

	entries := make(map[freshness.Key]freshness.Entry, len(h.forge.diffs))
	for _, k := range h.forge.diffs {
		if e, ok := c.Get(k); ok {
			entries[k] = e
		}
	}
	dropped := map[freshness.Key]bool{}
	droppable := func(k freshness.Key) bool {
		e, ok := entries[k]
		return ok && !e.Fetching && !dropped[k] && !slices.Contains(keep, k)
	}
	// The PR's current head (from its cached detail) supersedes the other
	// heads' diffs once its own diff is cached.
	for _, k := range h.forge.diffs {
		if !droppable(k) {
			continue
		}
		de, _ := c.Get(freshness.DetailKey(k.Repo, k.Number))
		d, ok := de.Value.(github.PRDetail)
		if !ok || d.HeadSHA == "" || d.HeadSHA == k.HeadSHA {
			continue
		}
		if he, ok := c.Get(freshness.DiffKey(k.Repo, k.Number, d.HeadSHA)); ok && he.HasData() {
			dropped[k] = true
		}
	}
	live := len(entries) - len(dropped)
	for _, k := range h.forge.diffs {
		if live <= forgeDiffCacheCap {
			break
		}
		if droppable(k) {
			dropped[k] = true
			live--
		}
	}
	for k := range dropped {
		c.Delete(k)
	}
	// Forget the keys no longer cached, unless just marked used (a head
	// whose fetch has not started yet).
	h.forge.diffs = slices.DeleteFunc(h.forge.diffs, func(k freshness.Key) bool {
		_, cached := entries[k]
		return dropped[k] || (!cached && !slices.Contains(keep, k))
	})
}
