package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/childenv"
)

// localGitTimeout bounds each local git call of ReadLocal.
const localGitTimeout = 5 * time.Second

// ReadLocal reads the checkout holding path with git: the main repository
// root (`rev-parse --git-common-dir`, so a linked worktree resolves to its
// main checkout) and the fetch URLs of `git remote -v` (after insteadOf
// rewrites). A path outside any git repository, or one that does not exist,
// is ErrNotGit. It runs only local git, never the network.
func ReadLocal(ctx context.Context, path string) (Local, error) {
	out, err := localGit(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Local{}, fmt.Errorf("reading the git repository of %s: %w", path, ErrNotGit)
		}
		return Local{}, fmt.Errorf("reading the git repository of %s: %w", path, err)
	}
	common := filepath.Clean(strings.TrimSpace(string(out)))
	root := common
	if base := filepath.Base(common); base == ".git" || base == ".bare" {
		root = filepath.Dir(common)
	}
	out, err = localGit(ctx, path, "remote", "-v")
	if err != nil {
		return Local{}, fmt.Errorf("listing the git remotes of %s: %w", path, err)
	}
	return Local{Root: root, Remotes: parseRemoteV(string(out))}, nil
}

// parseRemoteV reads `git remote -v` output: one remote per fetch line.
func parseRemoteV(out string) []Remote {
	var remotes []Remote
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "(fetch)" {
			continue
		}
		remotes = append(remotes, Remote{Name: fields[0], URL: fields[1]})
	}
	return remotes
}

func localGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, localGitTimeout)
	defer cancel()
	// #nosec G204 -- "git" is a fixed binary run with a literal argv (no
	// shell); dir is passed as the value of -C, never as an option.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(localGitEnv(childenv.ForLaunch("")), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("running git %s: %w", args[0], ctx.Err())
	}
	return out, err
}

// localGitEnv drops the variables that would point git at some other
// repository than the one holding -C's directory (set when agent-deck runs
// from a git hook, for example).
func localGitEnv(env []string) []string {
	kept := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_CEILING_DIRECTORIES":
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}
