# Data access: gh CLI or the GitHub API directly?

Research for sanchit36/agent-deck#2 (map: #1). Terms (Project, Issue, Pull Request) as in `GLOSSARY.md`.

## Recommendation

**Shell out to `gh`, behind one small Go package; do not call the GitHub API from Go and do not add a GitHub client module.**

- Reads: `gh issue list|view --json`, `gh pr view --json`, `gh pr diff`. For the PR *list* use
  `gh api graphql` with our own query (aggregate check state only), not `gh pr list --json statusCheckRollup`.
- Merge: `gh pr merge <n> --squash|--merge|--rebase --match-head-commit <sha>` (the sha the user saw).
- Env: `childenv.ForLaunch("")` plus `GH_PROMPT_DISABLED=1 NO_COLOR=1 GH_NO_UPDATE_NOTIFIER=1`,
  i.e. exactly what `internal/issuereport.GhEnv()` already builds. Reuse it (or move it to the new package).
- Always pass `-R [HOST/]OWNER/REPO` from the resolved Project; never rely on gh inferring the repo from cwd.
- Classify failures by exit code first (gh documents 0/1/2/4; 8 = pending for `gh pr checks`), then by stderr prefix.

Why: the GitHub server time dominates every call (0.65-2.6 s); gh's own overhead is about
18 ms process spawn plus ~40 ms auth/config, i.e. 2-6 %. Each `gh ... --json` read is one GraphQL
request costing 1 point, the same as a hand-written query. gh already handles keyring tokens, token
refresh, Enterprise hosts, SAML SSO prompts and the 401 recovery hint, which we would otherwise
re-implement. It adds zero go.mod entries, and `gh` is already an agent-deck dependency
(`internal/issuereport`, `internal/feedback`, `internal/update`, `cmd/agent-deck/feedback_*`).
A fake `gh` on `PATH` is also the simplest offline test seam (see "Test strategy" in #1).

## Measurements

Host: macOS (Darwin 25.6), gh 2.103.3, authenticated (OAuth token, keyring). Target: public repo
`asheshgoplani/agent-deck` (11 open issues, PR #3126 = 13 files, PR #3123 = 26 files / ~3.4k lines).
5 runs each, wall clock including process start; read-only calls only. Requests counted with `GH_DEBUG=api`.

| Call | median ms | range ms | stdout bytes | HTTP requests |
|---|---:|---:|---:|---|
| `gh --version` (spawn floor) | 18 | 17-75 | 81 | 0 |
| `gh issue list --limit 30 --json number,title,labels,author,updatedAt,state` | 684 | 621-706 | 4,877 | 1 GraphQL |
| same, `--limit 100` | 689 | 640-740 | 4,877 | 1 GraphQL |
| `gh issue view N --json number,title,body,comments,labels,author` | 678 | 665-731 | 3,609 | 1 GraphQL |
| `gh pr list --limit 30 --json ...,statusCheckRollup,...` | **5,027** | 4,138-6,284 | **248,117** | 1 GraphQL |
| `gh pr list --limit 30 --json ...` (no checks) | 1,890 | 1,445-2,310 | 7,378 | 1 GraphQL |
| `gh pr view N --json body,reviews,statusCheckRollup,mergeable,mergeStateStatus,reviewDecision,comments` | 1,296 | 1,191-1,606 | 26,444 | 1 GraphQL |
| `gh pr checks N` | 1,463 | 1,356-2,126 | 3,816 | 4 GraphQL |
| `gh pr diff N` (13 files) | 1,229 | 1,194-1,394 | 30,671 | 1 GraphQL + 1 REST |
| `gh pr diff N` (26 files) | 1,695 | 1,372-2,319 | 166,916 | 1 GraphQL + 1 REST |
| `gh api repos/O/R/pulls/N -H 'Accept: application/vnd.github.diff'` | 750 | 719-892 | 30,671 | 1 REST |
| `curl` same diff URL with the token (no gh process) | 708 | 644-871 | 30,671 | 1 REST |
| `gh api graphql`: open issues 30 + open PRs 30 (+rollup state), one round-trip | 2,854 | 1,932-2,941 | 10,484 | 1 GraphQL (cost 1) |
| `curl` same GraphQL query (no gh process) | 2,595 | 1,617-2,981 | 10,485 | 1 GraphQL |
| `gh api graphql`: PR detail (body, reviews, comments, mergeable, check contexts) | 1,166 | 1,126-1,950 | 16,877 | 1 GraphQL (cost 1) |
| `gh api graphql`: PRs 30 + `commits(last:1){statusCheckRollup{state}}` | 2,602 | 2,182-2,701 | 7,745 | 1 GraphQL |
| `gh api graphql`: PRs 30, no checks | 2,103 | 1,272-2,299 | 6,020 | 1 GraphQL |
| `gh api graphql`: issues 30 | 648 | 605-695 | 2,724 | 1 GraphQL |
| issues list (`gh issue list`) and PR list (`gh api graphql`) **in parallel** | 2,081 | 1,858-2,618 | 12,457 | 2 GraphQL |

What the numbers say:

1. **gh overhead is small.** Same request through `gh api` vs `curl`: 750 vs 708 ms (diff), 2,854 vs
   2,595 ms (GraphQL, noisy). Spawn floor is 18 ms. Server time is what the user waits for.
2. **Batching into one GraphQL round-trip does not help latency.** The combined issues+PRs query
   (2.85 s) is slower than running the two lists in parallel (2.08 s), because the PR connection is
   server-bound (~2 s even without checks) and a single query waits for its slowest field. Batch per
   view, parallelise across views.
3. **`statusCheckRollup` in `gh pr list --json` is the one trap**: it returns every check context of
   every PR (248 KB, 5 s). A custom `gh api graphql` query asking for `statusCheckRollup { state }`
   gives the aggregate badge in 7.7 KB / 2.6 s. Fetch full check contexts only in the PR detail view.
4. **Diff**: `gh pr diff` makes an extra GraphQL lookup before the REST diff (1.23 s vs 0.75 s for
   `gh api ... vnd.github.diff`). When the PR number is already known (it always is in the TUI),
   prefer `gh api repos/{owner}/{repo}/pulls/{n}` with `Accept: application/vnd.github.diff`.
   Note the REST diff endpoint refuses very large diffs (GitHub returns 406 above its diff limits); show
   "diff too large, open in browser" then.
5. **`gh pr checks` makes 4 GraphQL requests**; `gh pr view --json statusCheckRollup` (or our own
   GraphQL) gets the same data in 1. Don't use `gh pr checks` for the detail pane.

## Rate-limit cost

`gh api rate_limit` (authenticated user): `core` 5,000/h, `graphql` 5,000 points/h.

- Every `gh issue|pr list|view --json` call is a single GraphQL request (`GH_DEBUG=api`), and the
  hand-written queries above report `rateLimit { cost: 1 }`. List of 30 or 100: still 1 point.
- `gh pr diff` and `gh api .../pulls/N` (diff) cost 1 `core` request (+1 GraphQL point for `gh pr diff`).
- The whole investigation (about 90 gh calls) moved the counters by ~50 GraphQL points and ~25 core.
- Budget at 1 point per call: refreshing issues + PRs for 5 Projects every 60 s is
  5 x 2 x 60 = 600 points/h, 12 % of the hourly budget. GraphQL batching across Projects
  (aliases, `p1: repository(...) p2: repository(...)`) could cut that to 60 points/h if refresh volume ever
  matters, at the latency cost noted above; that is a freshness-policy question, not a transport one.
  Both `gh api graphql` and a direct client can send such a batched query, so it doesn't decide gh vs API.

## Error cases (observed, gh 2.103.3)

| Case | gh behaviour | Direct API behaviour |
|---|---|---|
| gh not installed | `exec.LookPath("gh")` fails (`exec: "gh": executable file not found in $PATH`) | needs a token from somewhere; without gh there is none unless `GH_TOKEN`/`GITHUB_TOKEN` is set |
| logged out (empty `GH_CONFIG_DIR`) | **exit 4**, stderr `To get started with GitHub CLI, please run:  gh auth login` | must detect "no token" ourselves |
| bad / revoked token | exit 1, `HTTP 401: Bad credentials (https://api.github.com/graphql)` + `Try authenticating with:  gh auth login -h github.com` | HTTP 401 JSON `{"message":"Bad credentials"}`; we write the hint |
| SAML SSO not authorised | gh reads the `X-GitHub-SSO` response header and prints `Authorize in your web browser:  <url>` ([cli/cli `pkg/cmd/factory/default.go`](https://github.com/cli/cli/blob/trunk/pkg/cmd/factory/default.go) `SSOURL`, printed in [`internal/ghcmd/cmd.go`](https://github.com/cli/cli/blob/trunk/internal/ghcmd/cmd.go)). Not reproduced here (needs an SSO org) | 403 + `X-GitHub-SSO` header; we parse it |
| Enterprise host not logged in | `-R ghe.example.invalid/o/r`: exit 1, `error connecting to ghe.example.invalid`; `gh auth token --hostname ghe...`: exit 1 `no oauth token found for ghe...` | must map host to `https://HOST/api/v3` and `/api/graphql` and find a per-host token (`GH_ENTERPRISE_TOKEN`) |
| repo not found / no access | exit 1, `GraphQL: Could not resolve to a Repository with the name 'o/r'. (repository)` | GraphQL `errors[]` with the same message |
| PR not found | exit 1, `GraphQL: Could not resolve to a PullRequest with the number of 999999. (repository.pullRequest)` | same |
| no `-R`, cwd not a git repo | exit 1, `failed to run git: fatal: not a git repository ...` | n/a (always explicit) |
| checks pending | `gh pr checks` exits 8 (`exitPending` in `internal/ghcmd/cmd.go`) | n/a |

Exit codes: `gh help exit-codes` documents 0 ok, 1 error, 2 cancelled, 4 auth required; the source adds 8 pending.
Proposed error classification for the TUI (one place, mapped to a status line + hint):
`LookPath` failure -> GhMissing ("install gh"); exit 4 -> NotAuthenticated ("run gh auth login [-h HOST]");
stderr `HTTP 401` -> BadCredentials; stderr `Authorize in your web browser:` -> SSORequired (show/copy URL);
stderr `error connecting to` -> Network/UnknownHost; stderr `Could not resolve to a` -> NotFound;
context deadline -> Timeout; anything else -> GhFailed with the first stderr line, scrubbed as
`issuereport.Scrub` does.

A caveat for the direct-API route: with an empty `GH_CONFIG_DIR`, `gh issue list` exits 4 but
`gh auth token` still exits 0 and prints a keyring token. Reading the token via `gh auth token` and
calling the API ourselves can therefore disagree with what `gh` itself considers "logged in". gh also
renews refreshable OAuth credentials in-process (`configWithTokenRefresher` in `default.go`); a token
read once and cached by agent-deck would not be renewed.

## go.mod cost

| Option | New modules |
|---|---|
| shell out to `gh` | none |
| `net/http` + hand-written JSON/GraphQL | none, but we own auth discovery, host mapping, SSO, pagination, error text |
| `github.com/google/go-github/v93` (REST) | go-github + `google/go-querystring` (`go-cmp`, `x/oauth2` already in our go.mod); REST only, so lists with review/check state need several calls per PR |
| `github.com/shurcooL/githubv4` (GraphQL) | githubv4 + `shurcooL/graphql` |
| `github.com/cli/go-gh/v2` (gh's own library) | ~25 direct requires (survey, glamour/v2, lipgloss/v2, sprig, chroma, gojq, gock, ...), and it still shells out to `gh` for auth on keyring setups |

AGENTS.md puts new modules under extra scrutiny; the gh route needs none.

## How agent-deck already runs gh and git

- `internal/issuereport/file.go`: `GhReady` (`LookPath` + `gh auth status --hostname github.com`, 15 s
  timeout) and `File` (`exec.CommandContext`, 60 s timeout, `cmd.Env = ghEnv()`, stderr first line
  scrubbed into a typed `*Error`). `ghEnv()` = `childenv.ForLaunch("")` + `GH_PROMPT_DISABLED=1`,
  `NO_COLOR=1`, `GH_NO_UPDATE_NOTIFIER=1`, exported as `GhEnv()`. This is the pattern to copy.
- `internal/update/update.go`: `resolveGitHubToken` (`GITHUB_TOKEN`, `GH_TOKEN`, then
  `gh auth token --hostname github.com`) and raw `net/http` against the REST API with ETag
  conditional GETs. That's the only direct-API caller, for unauthenticated-friendly release checks.
- Siblings that run gh **without** `ghEnv()` (plain `exec.Command`, inherited env, no timeout):
  `internal/ui/feedback_dialog.go:49`, `cmd/agent-deck/feedback_cmd.go:31`, `internal/feedback/sender.go:51`,
  `internal/update/update.go:262`. Not in scope here; list them in the first slice's PR if it touches gh plumbing.
- git: `internal/git/git.go` and friends shell out to `git`; `internal/git/` is exempt from the
  forbidigo `os.Environ` ban (`.golangci.yml`). A new GitHub package is **not** exempt, so it must build
  env through `internal/childenv` (`ForLaunch`), which `issuereport.GhEnv()` already does.

`childenv.ForLaunch("")` strips `TELEGRAM_*`, `CLAUDE_CONFIG_DIR` and `CLAUDE_JOB_DIR` and keeps
everything else, so `GH_TOKEN`, `GH_HOST`, `GH_ENTERPRISE_TOKEN`, `GH_CONFIG_DIR` and `XDG_CONFIG_HOME`
pass through to gh unchanged, which is what we want.

## Implementation notes for the spec

- One package (say `internal/forge`), the only place that runs `gh` for this feature, with a
  `Runner` interface (`Run(ctx, args, stdin) (stdout, stderr, exitCode)`) so tests inject fixtures;
  container e2e tests can put a fake `gh` script on `PATH`.
- Every call through `exec.CommandContext` with a timeout (reads ~15 s, merge ~60 s), run off the
  Bubble Tea update loop (`tea.Cmd`), never synchronously on a hot path.
- Pass `-R HOST/OWNER/REPO` for non-github.com Projects; gh then picks that host's stored credentials.
- Decode `--json` output into our own structs; ask only for fields the view shows.
- Merge with `--match-head-commit` so a push after the user looked at the PR makes the merge fail
  rather than merge unseen commits. Merge-queue repos: gh enables auto-merge / enqueues (see
  `gh pr merge --help`); the confirm dialog should say so.

## Caveats

- Single machine, single network, one public repo, small sample (5 runs); server latency varies by
  repo size and time of day. The relative numbers (gh overhead vs server time, batched vs parallel,
  rollup payload) are what matter.
- SSO and real Enterprise behaviour come from gh source, not from a live run.
- Not measured: Windows process-spawn cost (higher than macOS; still small next to server time).

## Sources

- Live runs: `gh` 2.103.3 against `asheshgoplani/agent-deck`, `gh api rate_limit`, `GH_DEBUG=api`.
- `gh help exit-codes`, `gh help environment`, `gh pr merge --help`.
- cli/cli source: `internal/ghcmd/cmd.go` (exit codes, SSO and 401 messages), `pkg/cmd/factory/default.go` (`X-GitHub-SSO`, token refresher).
- go.mod of google/go-github (v93), cli/go-gh (v2), shurcooL/githubv4.
- This repo: `internal/issuereport/file.go`, `internal/update/update.go`, `internal/childenv/childenv.go`, `.golangci.yml`, `go.mod`.
