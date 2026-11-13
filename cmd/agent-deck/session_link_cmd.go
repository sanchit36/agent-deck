package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/github"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// handleSessionLink records a Link from a session to an Issue or PR
// (spec #18 §11): `session link <id> issue|pr <n> [--repo [HOST/]OWNER/NAME]`.
// No gh call: the link is a stored repo + number, drawn as a static badge.
func handleSessionLink(profile string, args []string) {
	fs := flag.NewFlagSet("session link", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("quiet", false, "Minimal output")
	quietShort := fs.Bool("q", false, "Minimal output (short)")
	repoFlag := fs.String("repo", "", "Repo as OWNER/NAME or HOST/OWNER/NAME (default: the session checkout's origin, else its first GitHub remote by name)")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session link <session> issue|pr <number> [--repo [HOST/]OWNER/NAME]")
		fmt.Println()
		fmt.Println("Link a session to a GitHub issue or pull request. A session holds at most")
		fmt.Println("one issue link and one PR link; linking again replaces the old one.")
		fmt.Println("The TUI shows the link as a #12 / PR#34 badge on the session row.")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
	}

	if err := parseCLIFlags(fs, normalizeArgs(fs, args)); err != nil {
		exitCLI(1)
	}
	if fs.NArg() != 3 {
		fs.Usage()
		exitCLI(1)
	}
	out := NewCLIOutput(*jsonOutput, *quiet || *quietShort)

	kind, err := session.ParseLinkKind(fs.Arg(1))
	if err != nil {
		out.Error(err.Error(), ErrCodeInvalidOperation)
		exitCLI(1)
	}
	number, err := parseLinkNumber(fs.Arg(2))
	if err != nil {
		out.Error(err.Error(), ErrCodeInvalidOperation)
		exitCLI(1)
	}

	storage, instances, groupsData, profile := loadLinkSessionData(profile, out)
	defer storage.Close()
	inst, errMsg, errCode := ResolveSession(fs.Arg(0), instances)
	if inst == nil {
		out.Error(errMsg, errCode)
		exitCLI(2)
		return // unreachable, satisfies staticcheck SA5011
	}

	var link session.Link
	if *repoFlag != "" {
		link, err = session.ParseLinkRepo(*repoFlag)
	} else {
		link, err = linkRepoFromCheckout(inst.ProjectPath)
		if err != nil {
			err = fmt.Errorf("finding the repo of session '%s' (pass --repo): %w", inst.Title, err)
		}
	}
	if err != nil {
		out.Error(err.Error(), ErrCodeInvalidOperation)
		exitCLI(1)
	}
	link.Number = number
	inst.SetLink(kind, link)

	if err := saveSessionData(storage, instances, groupsData); err != nil {
		out.Error(fmt.Sprintf("failed to save: %v", err), ErrCodeInvalidOperation)
		exitCLI(1)
	}
	out.Success(
		fmt.Sprintf("Linked '%s' to %s %s", inst.Title, link.Repo(), link.Label(kind)),
		linkJSON(profile, inst, kind, &link),
	)
}

// handleSessionUnlink removes a session's Issue or PR link:
// `session unlink <id> issue|pr`.
func handleSessionUnlink(profile string, args []string) {
	fs := flag.NewFlagSet("session unlink", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("quiet", false, "Minimal output")
	quietShort := fs.Bool("q", false, "Minimal output (short)")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session unlink <session> issue|pr")
		fmt.Println()
		fmt.Println("Remove a session's issue or pull request link.")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
	}

	if err := parseCLIFlags(fs, normalizeArgs(fs, args)); err != nil {
		exitCLI(1)
	}
	if fs.NArg() != 2 {
		fs.Usage()
		exitCLI(1)
	}
	out := NewCLIOutput(*jsonOutput, *quiet || *quietShort)

	kind, err := session.ParseLinkKind(fs.Arg(1))
	if err != nil {
		out.Error(err.Error(), ErrCodeInvalidOperation)
		exitCLI(1)
	}

	storage, instances, groupsData, profile := loadLinkSessionData(profile, out)
	defer storage.Close()
	inst, errMsg, errCode := ResolveSession(fs.Arg(0), instances)
	if inst == nil {
		out.Error(errMsg, errCode)
		exitCLI(2)
		return // unreachable, satisfies staticcheck SA5011
	}

	old := inst.LinkOf(kind)
	if !inst.ClearLink(kind) {
		out.Error(fmt.Sprintf("session '%s' has no %s link", inst.Title, kind), ErrCodeNotFound)
		exitCLI(1)
	}
	if err := saveSessionData(storage, instances, groupsData); err != nil {
		out.Error(fmt.Sprintf("failed to save: %v", err), ErrCodeInvalidOperation)
		exitCLI(1)
	}
	out.Success(
		fmt.Sprintf("Unlinked '%s' from %s %s", inst.Title, old.Repo(), old.Label(kind)),
		linkJSON(profile, inst, kind, nil),
	)
}

// loadLinkSessionData resolves the profile before opening any state (AGENTS.md
// review rule 2) and returns the storage, the sessions and the profile used.
func loadLinkSessionData(profile string, out *CLIOutput) (*session.Storage, []*session.Instance, []*session.GroupData, string) {
	resolved, err := session.ResolveProfileForStorage(profile)
	if err != nil {
		out.Error(fmt.Sprintf("resolving profile: %v", err), ErrCodeNotFound)
		exitCLI(1)
	}
	storage, instances, groupsData, err := loadSessionData(resolved)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		exitCLI(1)
	}
	return storage, instances, groupsData, resolved
}

// parseLinkNumber accepts "12" or "#12".
func parseLinkNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid number %q: want a positive issue or PR number", s)
	}
	return n, nil
}

func linkJSON(profile string, inst *session.Instance, kind session.LinkKind, link *session.Link) map[string]any {
	data := map[string]any{
		"success":    true,
		"profile":    profile,
		"session_id": inst.ID,
		"title":      inst.Title,
		"kind":       string(kind),
		"linked":     link != nil,
	}
	if link != nil {
		data["repo"] = link.Repo()
		data["number"] = link.Number
		data["badge"] = link.Label(kind)
	}
	return data
}

// linkRepoFromCheckout reads the repo from a checkout's git remotes, locally,
// with the Project rule the forge tabs use (spec §2, github.PickRemote):
// origin (even when it is a fork); with no origin, the only GitHub remote,
// else the first alphabetically; upstream never. git runs through
// github.ReadLocal (bounded, childenv). No gh call: any host that is not a
// well-known other forge counts as GitHub, so a GHE origin links as is.
func linkRepoFromCheckout(dir string) (session.Link, error) {
	if strings.TrimSpace(dir) == "" {
		return session.Link{}, errors.New("session has no project path")
	}
	loc, err := github.ReadLocal(context.Background(), dir)
	if err != nil {
		return session.Link{}, err
	}
	res := github.PickRemote(loc.Remotes, linkHostKind)
	if !res.Found() {
		return session.Link{}, fmt.Errorf("resolving the GitHub repo of %s: %s", dir, res.Message())
	}
	return session.Link{Host: res.Repo.Host, Owner: res.Repo.Owner, Name: res.Repo.Name}, nil
}

// linkHostKind classifies a remote host without gh: other forges are not
// GitHub, every other host is.
func linkHostKind(host string) github.HostKind {
	if github.IsOtherForge(host) {
		return github.HostOtherForge
	}
	return github.HostGitHub
}
