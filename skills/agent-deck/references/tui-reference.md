# TUI Reference

Complete reference for agent-deck Terminal UI features.

## Keyboard Shortcuts

Reconciled against the in-app help overlay (`?`), which is the source of truth
(`internal/ui/help.go`). Keys marked **rebindable** can be remapped under
`[hotkeys]` in `config.toml`; the rest are fixed.

Letter hotkeys also fire on the key's Russian ЙЦУКЕН twin, so the overview
keeps working with a non-Latin layout selected: `т` opens New Session like
`n`, `А` forks like `F`, `Г` unarchives like `Shift+u`. Twins are derived from
the resolved bindings (rebinding an action moves its twin with it), only the
Russian layout is covered for now, and an explicit `[hotkeys]` value always
wins over a derived twin (`quick_fork = "т"` keeps `т` for quick_fork). The
fixed navigation keys (`j`/`k`/`h`/`l`/`G`, jump-mode hints) stay Latin only,
and in jump mode a Cyrillic letter falls through to its twin's action. Attached
panes are untouched: they receive the raw bytes exactly as typed.

### First run

A fresh install (no `config.toml`, no sessions) opens a **Welcome** instead of
the setup wizard. It shows once; `Esc` skips it and it does not come back.

- It lists Claude Code, Codex, Gemini CLI and Pi as installed or not, with the
  install command for each missing one, and offers a plain shell when none is
  installed.
- For Claude and Codex it shows whether you are signed in, from the tool's own
  status command (`claude auth status`, `codex login status`; only the exit
  code is read). `l` on a tool that is not signed in starts a session running
  the tool's own login (`claude auth login`, `codex login`) and attaches you to it;
  come back with `Ctrl+Q` and the Welcome confirms the sign-in. agent-deck never
  reads or stores a token. The check uses the account a new session would use
  (`[claude] default_account`).
- `↑`/`↓` pick an agent, `Enter` opens the New Session dialog with it and this
  folder filled in, and `Enter` again starts the session: two keys from the
  Welcome to a running session.
- After that first session, **What you can do** opens once. `Alt+W` ("Show me
  around", `show_around`) opens it any time; `Tab` there goes to the agents and
  sign-in page.

### Navigation

| Key | Action |
|-----|--------|
| `j` / `↓` | Move down |
| `k` / `↑` | Move up |
| `Ctrl+u` / `Ctrl+d` | Half page up / down |
| `PgUp` / `PgDn` | Half page up / down |
| `Ctrl+f` / `Ctrl+b` | Full page up / down |
| `Home` / `End` | Jump to first / last item |
| `gg` | Jump to top |
| `G` | Recall search (see [Search & Filter](#search--filter)) |
| `h` / `←` | Collapse group, fold a parent session's child list, or go to parent |
| `l` / `→` / `Tab` | Toggle expand/collapse group, or a parent session's child list |
| `1-9` | Jump to Nth root group |
| `Space` | Jump mode |
| `Enter` | Attach to session OR toggle group |
| `Shift+Enter` | Open session in new iTerm window (macOS) |
| `` ` `` | Alternate-session toggle: swap with the previous session, vim `Ctrl-^` style (**rebindable** as `alt_session`) |
| `Alt+←` / `Alt+→` | Walk back / forward through recently used sessions, MRU-ordered via `last_accessed` (**rebindable** as `mru_back` / `mru_forward`) |

### Group Navigation

| Key | Action |
|-----|--------|
| `Alt+j` / `Alt+k` | Next / previous session in group |
| `Alt+1` - `Alt+9` | Jump to Nth session in group |
| `Alt+g` / `Alt+G` | First / last session in group |
| `Alt+/` | Filter search within group |

### Session Actions

| Key | Action |
|-----|--------|
| `Enter` | Attach to session OR toggle group |
| `n` / `N` | New session / quick create (**rebindable**) |
| `r` | Rename session or group (**rebindable**) |
| `R` | Restart session, reloads MCPs (**rebindable**) |
| `T` | Restart with a new session ID (**rebindable**) |
| `d` | Delete session or group (**rebindable**) |
| `D` | Close session process (**rebindable**) |
| `Ctrl+Z` | Undo delete (**rebindable**) |
| `A` | Archive session — stops tmux, hides from default list; conversations/metadata untouched (**rebindable**) |
| `Shift+U` | Unarchive session; does NOT auto-start tmux (**rebindable**) |
| `^` | Toggle archived view (**rebindable**) |
| `M` | Move session to a different group (**rebindable**) |
| `m` | MCP Manager (Claude/Gemini/Cursor) (**rebindable**) |
| `L` | Plugin Manager (Claude) (**rebindable**) |
| `s` | Skills Manager (**rebindable**) |
| `$` | Cost Dashboard |
| `v` | Cycle preview mode: output / stats / both (**rebindable**) |
| `O` | Toggle preview orientation (right / below — portrait monitors) |
| `<` / `>` | Shrink / grow preview pane by 5% (or drag the divider with the mouse) |
| `u` | Mark unread, idle -> waiting (**rebindable**) |
| `a` | Quick approve — sends `1` to Claude (**rebindable**) |
| `o` | Prompt session — send a one-line prompt without attaching (**rebindable**) |
| `Q` | Queue a message for after the current turn (`queue_message`, **rebindable**) |
| `C` | Inspect full context — everything the agent is being sent, ranked by cost (`context_inspector`, **rebindable**) |
| `I` | Insert mode: keystrokes go straight to the selected session's pane until `Esc`; `Enter` submits. Insert mode is raw by design: it can submit unsent composer text, whatever is in the composer, including an operator's unsent draft |
| `y` | Toggle YOLO mode (**rebindable**) |
| `+` / `K` / `Shift+↑` | Move item up (auto-promotes a sub-session to top-level at the parent's first child) |
| `-` / `J` / `Shift+↓` | Move item down (auto-promotes a sub-session to top-level at the parent's last child) |
| `Shift+→` / `Shift+←` | Indent / outdent within current group (single-level nesting) |
| `,` | Pin (cycles off -> top -> bottom -> off) |
| `f` / `F` | Quick fork / fork with options (Claude/OpenCode/Pi/Codex/Oh My Pi) (**rebindable**) |
| `x` | Send output to another session (**rebindable**) |
| `alt+l` | Hand a reference to another session: pick this one, move to the target, `Enter` (or `alt+l`) drops, `Esc` cancels. Types a one-line pointer to the picked session (title, id, path, status, `agent-deck session output <id> -q`) into the target's composer and never presses Enter. Group rows, the same session and remote sessions are refused in place; nothing moves (**rebindable**: `[hotkeys].session_ref`) |
| `E` | Exec shell in sandbox container (**rebindable**) |
| `H` | Open shell in session's worktree, split pane / window (**rebindable**) |
| `p` | Edit multi-repo paths (**rebindable**) |
| `P` | Edit session settings — title / color / ... (**rebindable**) |
| `e` | Edit notes; hidden when notes are disabled (**rebindable**) |
| `b` | Re-run worktree setup script `.agent-deck/worktree-setup.sh` (**rebindable**) |
| `W` | Finish worktree — merge + cleanup (**rebindable**) |
| `w` | Watcher panel (**rebindable**) |

For remote group headers, `Enter`/`Tab` toggles collapse and `h`/Left collapses or moves to the parent. A remote host header shows `v1.15.0 ↑` after its count when the remote runs an older agent-deck than this controller (the version is asked once per hour per remote on the session poll); `u` on that header opens "Update remote <name> from v<old> to v<new>?" and runs the same verified deploy as `agent-deck remote update <name>`. A remote session whose parent (for example its conductor) is in the same remote group is shown one level under it, as local sub-sessions are; with the parent absent it is shown flat. Remote-session reorder keys move only within the current remote group (a child only among its parent's children); the order is saved on the viewing machine, while remote group headers remain name-sorted.

Local parent rows with sub-sessions (for example a conductor and the sessions it spawned) carry a `▾N`/`▸N` child-fold badge after the row title. `Tab`/`l`/`→` toggles just that child list; `h`/`←` folds it shut, and on an already-folded parent continues to the window fold or the group collapse. Unfolding a parent also reopens its window rows, and a search jump or focus request to a hidden child unfolds its parent. Folded parents keep their own row, mirroring a collapsed group header, and the fold state persists per machine alongside the other view preferences.

A session linked to a GitHub issue or pull request (behind `[github] enabled`) carries a static `#12` / `PR#34` badge after its worktree branch, both when it has both (`#12 PR#34`); the embedded sidebar card shows the same text on its metadata line. The badge is drawn from the stored link only, with no network or `git` call on the render path, and yields to the title in a narrow column: it keeps only `PR#34`, then drops. Links are made with `agent-deck session link` / `unlink`, or by the PRs tab's head-branch auto-detect (see GitHub tabs); there is no TUI key for them.

Attention signals (behind `[github] enabled`, off with `[github] signals = false`) add the linked item's live state after each label: for a PR `✗` checks failing, `±` changes requested or `✔` approved, `◆` merged, `✕` closed; for an Issue `✓` closed (`#12 ✓ PR#34 ✗±`). A badge shows while its condition holds; nothing is acknowledged or saved. A host `gh` is not logged in to shows `⚠` on its own badges only. In a narrow column the badge falls back to the PR with its glyphs, then to the bare label. The header shows `⇠ n reviews` for open PRs that request your review by name (team requests do not count), and the `PRS` chip shows the on-screen Project's share (`PRS 12 ⇠1`); no key jumps from them. The state comes from one batched `gh api graphql` request per host every `[github] refresh_interval` (one request covers every linked item on the host and the review search), made by a poller that runs while the TUI runs, attached or not; the session list only reads its last result.

Only a change raises a notification, never the first load or a restart: checks going to failing, a review going to changes requested or to approved, the PR merged or closed (a closed Issue does not notify), and a new review request. Each one marks the linked session on the tmux notification bar with the signal's glyph before its title (`[1] ✗ fix login`), even when the session is not waiting, until you attach to it or jump to it from the bar; with `[notifications] desktop = true` it also raises a desktop notification naming the session and what changed.

### Copy & Text Selection

Dragging with the mouse does **not** select text: the TUI puts the terminal in
mouse reporting mode (`tea.WithMouseCellMotion`) so that click-to-select,
wheel scrolling and the divider drag work, which means the terminal never sees
your drag as a selection gesture.

| Key | Action |
|-----|--------|
| `c` | Copy last AI response (**rebindable**) |
| `B` | Copy session info — repo / path / branch (**rebindable**, `copy_info`) |
| `V` | Copy visible terminal text, links included (**rebindable**) |
| `Y` | Copy a fenced code block from output; opens a picker when there are several |
| `Shift+drag` | Native terminal selection — bypasses mouse reporting |
| `Option+drag` | Native terminal selection in iTerm2 |

`c`, `B` and `V` are `copy_output`, `copy_info` and `copy_pane` under
`[hotkeys]`; `Y` is fixed. Copy info moved from `C` to `B`: `C` now opens the
context inspector (set `copy_info = "C"` and rebind `context_inspector` to get
the old key back).

All four copy paths use the same clipboard chain, falling back to OSC 52 so they
work over SSH. If your terminal offers no selection bypass at all, you can turn
off tmux mouse mode for attached sessions — at the cost of tmux scrolling, pane
resize and mouse copy mode:

```toml
[tmux]
mouse = false
```

That setting affects **attached sessions only**; the agent-deck list view keeps
its own mouse capture regardless.

### Group Actions

| Key | Action |
|-----|--------|
| `g` | Create group (subgroup if on group) (**rebindable**) |
| `r` | Rename group (**rebindable**) |
| `Tab` | Toggle expand |

### Search & Filter

| Key | Action |
|-----|--------|
| `/` | Local search, fuzzy (**rebindable**) |
| `G` | Recall search over every indexed conversation (Claude, Codex, pi, Gemini, OpenCode, Hermes); footer notice and local search when `[recall] enabled = false` |
| `Tab` | Switch between local/global search |
| `0` | Clear filter (show all) |
| `!` / `Shift+1` | Filter: running only (toggle) |
| `@` / `Shift+2` | Filter: waiting only (toggle) |
| `#` / `Shift+3` | Filter: idle only (toggle) |
| `&` | Filter: errors only (toggle) |
| `%` | Filter: Open only, hides stopped and error sessions by default (toggle). With custom exclusions that keep stopped visible, cycles All → Open → Open with stopped hidden → All. The selected step survives restart, including an empty Open view. |
| `^` | Filter: view archived sessions (toggle) |
| `t` | Cycle group view: active-on-top / populated-on-top (**rebindable**). Active-on-top puts running/waiting/starting sessions above an `idle / done` divider; set `[ui] active_includes_idle = true` to keep idle sessions with a live pane on top as well (divider becomes `stopped / done`) |
| `*` | Cycle time filter: today / 3 days / 7 days / 30 days / all (**rebindable**) |

Inside the search prompt, `/waiting`, `/running` and `/idle` filter by status.

### Global

| Key | Action |
|-----|--------|
| `?` | Help overlay (**rebindable**) |
| `S` | Settings (**rebindable**) |
| `i` | Import existing tmux sessions (**rebindable**) |
| `Ctrl+R` | Manual refresh / reload from disk (**rebindable**) |
| `Ctrl+Q` | Detach, keeps tmux running (**rebindable**) |
| `Ctrl+S` | Switch session, here or attached — unbound by default (**rebindable**) |
| `PageUp` | Scrollback pager, while attached |
| `Alt+A` | Agents panel; appears once an agent is adopted (**rebindable**) |
| `$` | Cost Dashboard |
| `Ctrl+Y` | Install the available update now (`install_update`; runs `agent-deck update` on the terminal, see [Updates](#updates)) |
| `Ctrl+T` | Restart agent-deck in place now (`restart_deck`; the new build starts with the same args, env and selection) |
| `Alt+D` | Dead-letter events (list, inspect, retry, confirmed selected purge) |
| `Alt+T` | Triggers panel — every launchd job, systemd unit, watcher and bridge that wakes a session (`triggers`, **rebindable**) |
| `Ctrl+E` | Open feedback dialog |
| `Z` | Report a problem or idea: a short form, then the full GitHub issue text, filed only after you press `y` (`report_problem`, **rebindable**) |
| `Alt+W` | Show me around: "What you can do" (sessions, switching, send, conductor, remotes, each with its key), then `Tab` for your agents and sign-in (`show_around`, **rebindable**) |
| `Alt+N` | What's new: this version's highlights and any of your reports it fixed, each with "Is it fixed for you?" (`y`/`n`, optional comment, then the exact public comment and the GitHub account that posts it; posted only after you press `y`, never while gh is not logged in). Also opens by itself on the first start after an update (`whats_new`, **rebindable**) |
| `q` / `Ctrl+C` | Quit (**rebindable**) |

### GitHub tabs

(behind `[github] enabled`)

With the gate on, the left panel's title row is a tab strip,
`SESSIONS │ ISSUES n │ PRS n` (`AGENTS` in the embedded layout, where the
`FLAT`/`GROUPED` label moves to the right end of the row). The active tab is
the inverted chip; the counts are the open Issues and PRs once a list has
loaded. The strip takes no extra row, and a click on a chip switches to it.
Each tab keeps its own cursor and scroll position.

The first row of the Issues and PRs tabs is the Project line: `owner/repo ▾`
and the filter, or one line saying why the group has no GitHub Project (`not a
git repository`, `no GitHub remote`, `remote is on gitlab.com, not GitHub`, ...).
The Project is resolved the first time a forge tab shows the group, never while
the list is drawn. Click the `owner/repo ▾` chip or press `p` to choose among a
group's Projects; it starts on the selected session's Project.

The Issues tab lists the 100 most recently updated open Issues, fetched in one
GitHub GraphQL request. Each Issue is two lines and a blank: the state glyph
(`○` open, `✓` closed) and the title, then `#n · author · age` and the labels,
dim. The selected row has a `▌` bar; long titles and labels are cut at the
pane's edge with `…`. A repository with Issues turned off says `Issues are
disabled for owner/repo`; an empty list says `No open issues`.

The PRs tab lists the 100 most recently updated open pull requests the same
way, in one GraphQL request that asks for each PR's overall check state only
(never the individual checks). Line 1 is the state glyph (`●` open, `◌` draft,
`◆` merged, `✕` closed) and the title; line 2 is `#n · author · age`, then the
checks (`✓ ci` passing, `✗ ci` failing, `◷ ci` running, `· ci` none) and the
review (`✔ review` approved, `± review` changes requested, `? review` review
required, `· review` none). An empty list says `No open pull requests`.

A row linked to a session ends with `◉ <session title>` (`+n` when more
sessions are linked), on both tabs. When the PRs list arrives, a session in a
git worktree whose branch is a PR's head branch, in the repository the
session's Project resolves to, is linked to that PR, the most recently updated
one if several match. A session that already has a PR link keeps it; SSH
sessions are never linked this way. This runs in the background, never while
the list is drawn.

On the Issues and PRs tabs the right pane (the lower one in the stacked
layout) is the selected row's detail, titled `ISSUE #n` or `PR #n`: the
title, then a key/value block and a short timeline, newest first. An Issue
shows its state, author, age, labels, assignee, comment count and linked
sessions; a PR shows its state, head branch (`owner:branch` from a fork),
base branch (`into`), checks (`✗ 1 failed · 11 passed`), review, merge state
(`⇢` clean, `≠` conflict, `↓` behind, `⊘` blocked), diff size
(`+120 −30 · 7 files`), linked sessions and `local`. The timeline lists the last
comments, reviews, pushes, check results and state changes. The body and
comments are in the overlay (`Enter`). The detail is one GraphQL request: its cached
copy shows at once, it is fetched fresh each time you open it (once the
cursor rests on the row), and it refetches while on screen when the list
reports the item changed. In the single column the list has the screen;
`Enter` opens the selected row's detail full screen and `Esc` returns to the
list.

A PR's `local` row is your local copy of its head, for example
`local  feature/42-x · ↑2 unpushed · dirty  +1 more`. The checkouts are the
worktrees of the sessions linked to the PR, then any worktree or branch of the
Project's local repo that tracks the PR head (its head branch, or GitHub's
`pull/N/head` ref; for a fork PR only a remote of the fork's owner). The first
one is shown, a linked session's first, then `+n more`. Its commit is compared
with the PR head without fetching: `in sync`, `↑n unpushed`, `↓n behind`,
`↑a ↓b` when both sides have commits, or `↓ PR has newer commits` when the head
commit is not in your repo yet; `dirty` marks uncommitted changes. The row
reads `none` with no checkout, `unavailable` when git could not be read (or
for a jj checkout), and `checking…` before the first read. Agent Deck reads it
in the background (each git call has a 5 s limit) when the PR opens, when its
head commit changes, and on `r`; it is never polled.

`Enter` on a row (in the single column, from the full-screen detail) opens the
item full screen in an overlay: a header from the list row (state glyph,
`#n`, title, state), a tab row (`Conversation`, plus `Files` for a PR), the
tab's body and a footer with the position and the keys. It opens at once
with a loading line and fetches the Conversation in one GraphQL request; the
cached copy shows at once on a later open. The Conversation tab is the body
(`No description provided.` when empty), then the comments and PR review
summaries (verdict and body) oldest first, each under `author · age` with
`[bot]` and `(edited)` marks and a dim rule between them. Up to 100 are
shown, then `N more comments: open in browser`; inline review comments are
only counted (`N inline review comments: open in browser`), and timeline
events are not shown. A comment GitHub minimized is one line
(`▸ author · age · hidden as spam`) until you expand it. Markdown is drawn
by a light renderer: bold headings, kept bullets, styled `**bold**` and
`` `code` ``, links as `text (url)`, images as `[image: alt]`, prose wrapped
at 100 columns, code blocks and tables unwrapped, cut with `›` and pannable.
While the overlay is open the list keeps polling: when the list reports the
item changed, the Conversation refetches without moving the scroll, and the
footer says `N new comments` until you scroll to the end. Each tab keeps its
own scroll. The Files tab arrives in a later slice.

| Overlay key | Action |
|-----|--------|
| `c` / `d`, `1` / `2` | Conversation / Files tab (Files: PRs only; **rebindable**: `forge_conversation`, `forge_files`) |
| `j`/`k`, `↑`/`↓`, `Ctrl+D`/`Ctrl+U`, `Space`/`PgDn`, `PgUp`, mouse wheel | Scroll |
| `g`/`G`, `Home`/`End` | Top / bottom |
| `h`/`l`, `←`/`→` | Pan code blocks and tables |
| `Enter` | Expand or collapse the minimized comment on screen |
| `e` | Expand every minimized comment, or collapse them all (**rebindable**: `forge_expand`) |
| `o` `y` `n` `a` `r` | As on the list, for the open item (`n` closes the overlay for the chooser or the dialog) |
| `Esc` / `q` | Close the overlay; only quit (`Ctrl+C`) still reaches the global keys |

The right end of the Project line says how fresh the list is: `updated 2m ago`
(`N open · 2m ago` once the count is known, or `100 of 342 · 2m ago` when
GitHub has more open items than the list fetched), a spinner while it loads, or
`⚠ stale` when the last refetch failed and the previous list is still shown.
The visible list refetches every `[github] refresh_interval` (default 60 s)
while the TUI is in the foreground and no session is attached. With nothing
cached yet, a failure fills the pane with what to do: an install hint when
`gh` is missing, `gh auth login -h <host>` for a missing login or bad token,
the authorize URL for SAML SSO, `gh has no login for <host>`, `offline ·
retrying`, or `rate-limited until HH:MM` (polling stops until then).

| Key | Action |
|-----|--------|
| `[` / `]` | Previous / next tab, wrapping; from every tab (**rebindable**: `forge_prev_tab`, `forge_next_tab`) |
| `Alt+S` / `Alt+I` / `Alt+P` | Sessions / Issues / PRs tab; from every tab (**rebindable**: `forge_tab_sessions`, `forge_tab_issues`, `forge_tab_prs`) |
| `p` | Project selector: `j`/`k` move, `Enter` selects, `Esc` closes (Issues, PRs; **rebindable**: `forge_project`) |
| `P` | Toggle between the Project and its Parent Repository (from GitHub's fork metadata; a Project that is not a fork says so) (Issues, PRs; **rebindable**: `forge_parent`) |
| `r` | Refresh: refetch the list and the open detail now (and re-read a PR's `local` row), joining a fetch already running, and restart the poll timer; nothing while GitHub's rate limit lasts (Issues, PRs; **rebindable**: `forge_refresh`; `Ctrl+R` stays the global reload) |
| `o` / `y` | Open the selected Issue or PR in the browser / copy its URL; on the SAML message, the authorize URL. Both run in the background, never blocking the TUI (Issues, PRs; **rebindable**: `forge_open`, `forge_copy`) |
| `n` | Start a session on the selected Issue or PR: the chooser first when sessions are linked to it (`Enter` attaches, `n` starts a new one anyway, `Esc` cancels), then the new-session dialog, prefilled (see below) (Issues, PRs; **rebindable**: `forge_new`) |
| `a` | Attach to the session linked to the selected Issue or PR; with several, a picker (`j`/`k` move, `Enter` attaches, `Esc` closes); with none, a message says so (Issues, PRs; **rebindable**: `forge_attach`) |
| `Enter` | Open the selected row in the overlay; in the single column the first `Enter` opens its detail full screen and `Esc` returns to the list (Issues, PRs; **rebindable**: `forge_view`) |
| `j`/`k`, `↑`/`↓`, `Ctrl+U/D`, `PgUp`/`PgDn`, `Ctrl+B/F`, `Home`/`End` | Move in the tab's list (Issues, PRs) |

`n` on an Issue or PR opens the new-session dialog prefilled: the tab's
group, the local checkout of the Project (its main worktree), Worktree ticked
whatever `[worktree] default_enabled` says, the tool (the group's newest
session's, then `default_tool`, then `claude`) and the title `#123 <title>` /
`PR#34 <title>`. An Issue's branch is `<branch_prefix><number>-<slug>` (the
title lowercased, other characters turned into `-`, cut to 40 characters on a
word boundary); an existing branch or worktree is reused and the field is
editable. A PR gets a worktree detached at its base, then `gh pr checkout <n>`
inside it; its branch field shows the PR's head branch and is read-only.
With Worktree unticked, `gh pr checkout` runs in the checkout itself, and only
on a clean tree: otherwise the launch is refused with the reason.

The dialog's Template field holds the first message, from the
`[prompt_templates]` that match the item (see the configuration reference).
`←`/`→` on it cycle the label matches, the other templates, then `none` (no
first message; the session is still linked); when several match or a
template file is missing it opens with the list. Under it are the first lines
of the filled message and its size. `Ctrl+E` opens the message in `$VISUAL` /
`$EDITOR` (`vi` by default); after an edit the field reads `<name> (edited)`,
and changing the template asks first (press `←`/`→` again to discard the
edit). The body, and the comments and review threads a template uses, load
in the background (`loading comments…`); `Enter` during that waits for them,
and if they fail the dialog asks before launching with them empty. `Enter`
launches: the worktree, the checkout and the start run in the background
while the dialog waits. A failure there leaves no session and removes a
worktree made for the launch; the error shows in the dialog, which stays
open. Once the session runs it is linked to the Issue or PR; if only the
first message fails, the session and link are kept and a warning says so.

On the Issues and PRs tabs every other session key is ignored except these,
which work as on Sessions: quit (`q`, `Ctrl+C`), help (`?`), `Esc`, group
navigation (`Alt+J`/`Alt+K`, `1`-`9`; the tab follows the selected group),
layout (`<` `>` `O`), settings (`S`), report a problem (`Z`), what's new
(`Alt+N`), show me around (`Alt+W`) and reload (`Ctrl+R`). There, `?` opens a
GitHub help section with the forge keys and these global keys, and the bottom
bar shows the forge keys.

### Worktree Shortcuts

| Key | Action |
|-----|--------|
| `n` -> `w` | Create session in a worktree |
| `F` -> `w` | Fork session into a worktree |

### Startup Flags

| Flag | Action |
|------|--------|
| `--group <name>` | Launch scoped to a group |
| `--profile <name>` | Use a specific profile |

## Local Status Indicators

| Symbol | Status | Color | Meaning |
|--------|--------|-------|---------|
| `●` | Running | Green | Active, content changed in last 2s |
| `◐` | Waiting | Yellow | Stopped, unacknowledged |
| `○` | Idle | Gray | Stopped, acknowledged |
| `✕` | Error | Red | tmux session doesn't exist |
| `⟳` | Starting | Yellow | Session launching |

A Claude session whose foreground turn is over but which still has a Workflow, background agents, shells or a Monitor in flight is `●` running (substate `background-work`); the preview shows one line under the status, `background: <task> n/m · <elapsed>` (for example `background: comms-followon-round3 3/5 · 18m32s`). When the work reports back the session turns `◐` waiting, then `○` idle once acknowledged.

Federated remote rows currently carry coarse running/waiting/idle/error status; local Honest Status substates are not included in the remote payload.

## Dialogs

### Dead-letter events (`Alt+D`)

The panel mirrors `agent-deck inbox dead-letter`: `j`/`k` selects a record,
`Enter` shows bounded metadata, `r` retries delivery, and `d` starts a selected
record purge that must be confirmed with `y`. A retry that cannot resolve a live
target reports the reason and retains the record. Raw prompt, output, and
completion content are never rendered.

### New Session (`n`)

**Fields (order: Name → Tool → Model → Reasoning effort → Path):**
- Session name (optional: left empty, it is the folder name, made unique and cut to 50 characters; the empty field previews that exact name). With a worktree on, an empty name's branch is taken from that name
- Command (claude/gemini/opencode/codex/custom) — the dialog remembers the last-used tool (persisted per profile, never written to config.toml; an explicit `default_tool` in config wins)
- Model ID (claude/codex/gemini/opencode): empty means the tool default; `↓` or `Space` opens the list of known IDs, or type any ID (CLI: `--model`)
- Reasoning effort (claude/codex): `←`/`→` or `Space` cycles the levels (CLI: `--effort`)
- Project path (required, supports `~/`)
- Parent group (auto-selected)
- Claude options (when Claude is selected): permission mode, Chrome, teammate mode, extra args, start query, and the account row (CLI: `--account`; hidden when no named accounts are configured; starts on `[claude].default_account`, labelled `(default)`, when one is set)
- `[ Create session ]` button (last row)

**Controls:** `Enter` creates from any row with what is filled in | `Tab`/`Shift+Tab` and `↓`/`↑` move between rows (inside the Claude options row by row) | `↓`/`Space` open the model list, `Space`/`→` browse paths; while a list is open `Enter` picks | `Ctrl+S` creates too | `Esc` back out of a list, then cancel

The common case is two keys: `n`, then `Enter` (this folder, the remembered tool, the selected group, the folder name as title). `N` on the home screen creates with the group's defaults without opening the dialog. If a required field is wrong (empty path, worktree without a valid branch, multi-repo with fewer than 2 paths), `Enter` moves to that field and shows why. A path that does not exist asks whether to create the folder; Cancel returns to the dialog with focus on Path, so a typo can be fixed. Set `[ui].new_session_enter_advances = true` to walk the form instead: Enter moves to the next row and only the `[ Create session ]` button or `Ctrl+S` creates.

Pressing `n` on a remote group/session opens a remote-aware dialog (remote paths and group pre-filled); the session is created over SSH on the remote, never on localhost.

Claude New Session defaults are remembered in `$XDG_CONFIG_HOME/agent-deck/config.toml` (default `~/.config/agent-deck/config.toml`) under `[claude]`, except start query and resume IDs, which are per-launch values.

### Edit Session (`Shift+P`)

Edits the fields a session iterates on at runtime: Title, Harness (tool), Pin position, the account row (shown when `[profiles.<name>.*].config_dir` slots are configured for a supported harness), and for claude sessions Skip permissions, Auto mode, Extra args, Wait timer (take the recommended option after this long: `5m`, `off`, or empty for `[sessions] wait_timeout`; applies live), Plugins.

**Controls:** `Tab`/`↓` `Shift+Tab`/`↑` move rows | `←`/`→` choose (pills) | `Space` toggle (checkboxes) | `Enter` save | `Esc` cancel. The footer says what the keys do on the focused row; on a changed harness or account row it reads "Enter switch (asks first)".

A harness or account change is saved on its own (not together with other edits) and always asks first: a same-harness account change shows "Switch Account?" (from → to, what happens), a different harness shows the transfer disclosure. `y`/Switch runs it, `n`/Esc returns to the row with nothing written; the outcome is reported in a notice. CLI equivalents: `agent-deck session switch-account <session> <account>` (same flow), `agent-deck session set <session> account <name>`, `agent-deck session switch <session> --to-harness <tool> [--to-account <account>]` (preview with `session switch-preview`); `agent-deck accounts` lists the slots.

### MCP Manager (`m`)

**Layout:**
- Two columns: Attached | Available
- Two scopes: LOCAL | GLOBAL

**Controls:**
- `Tab` - Switch scope
- `←/→` - Switch columns
- `↑/↓` - Navigate
- `Type letters/digits` - Jump to MCP name prefix
- `Space` - Toggle MCP
- `Enter` - Apply changes
- `Esc` - Cancel

**Indicators:**
- `(l)` LOCAL scope
- `(g)` GLOBAL scope
- `(p)` PROJECT scope
- `🔌` MCP is pooled
- `⟳` Pending restart

### Skills Manager (`s`)

**Layout:**
- Two columns: Attached | Available
- Available is pool-only (`source=pool`)
- Column headers include counts (for example: `Attached (3)`, `Available (28)`)

**Controls:**
- `←/→` - Switch columns
- `↑/↓` - Navigate (scrolls long lists)
- `Type letters/digits` - Jump to skill name prefix
- `Space` - Move skill between columns
- `Enter` - Apply changes
- `Esc` - Cancel

**Persistence:**
- Writes attachment state to `<project>/.agent-deck/skills.toml`
- Claude-compatible sessions materialize selected entries in `<project>/.claude/skills`
- Gemini, Codex, and Pi sessions materialize selected entries in `<project>/.agents/skills`
- If no pool entries exist, dialog shows guidance for `~/.agent-deck/skills/pool`

**Runtime notes:**
- Skills Manager is available for Claude, Gemini, Codex, and Pi sessions
- Pressing `Enter` reconciles managed attachments to the active runtime root even if the attached list did not change
- Auto-restart after apply is supported for Claude, Gemini, and Codex; Pi requires manual reload/restart

### Fork Dialog (`F`)

**Fields:**
- Session title (pre-filled)
- Group (auto-selected)

**Controls:** `Enter` fork | `Esc` cancel

### Delete Confirmation (`d`)

**For sessions:** Warning about tmux kill, process termination

**For groups:** Sessions move to default (not deleted)

**Controls:** `y` confirm | `n`/`Esc` cancel

## Search

### Local Search (`/`)

- Fuzzy search session titles and groups
- Max 10 results
- `↑/↓` or `Ctrl+K/J` navigate
- `Enter` select | `Tab` switch to global | `Esc` close

### Global Search (`G`)

- Full-text search of the Recall index (`recall.db`): Claude transcripts plus the other harnesses Recall reads (see `docs/recall.md`)
- Same query as `agent-deck recall search`
- Recency ranking
- Split view: results + preview
- `[/]` scroll preview
- `Enter` create/jump to session

**Config:** `G` is off unless Recall is on (with Recall off it falls back to the local title search; see `docs/recall.md`):
```toml
[recall]
enabled = true
```

## Preview Pane

- Shows last ~500 lines of session's tmux pane
- Auto-updates every 2 seconds
- Launch animation: 6-15s for Claude/Gemini

### Git section (session detail)

Every session whose path is in a git repository shows a **Git** section in the preview (worktree sessions always do):

```
Branch:  feature/x
Repo:    /work/repo
Path:    /work/repo/.worktrees/feature-x
Status:  dirty (uncommitted changes)
Upstream: origin/feature/x · ↑2 ↓1
Setup:   b re-run setup script
Finish:  W merge + cleanup
```

- **Status**: `clean`, `dirty (uncommitted changes)` (untracked files count as dirty), `checking...` before the first read, `unavailable` when git failed or took longer than 5 s.
- **Upstream**: the tracked branch with commits ahead (`↑`) and behind (`↓`) as of the last `git fetch` (agent-deck never fetches), the bare branch name when the remote branch is gone, or `no upstream`.
- **Setup** / **Finish** hints show for worktree sessions only.
- A Jujutsu workspace shows `jj workspace: not shown in v1`.
- The state comes from one `git status --porcelain=v2 --branch` per checkout, run in the background when the selection settles and cached for 10 s per path. Rendering never runs git. Opening the Finish dialog (`W`) always reads it fresh.

## Updates

The TUI checks for a new release on startup and every 5 minutes, and watches its own binary on disk once per tick (one `stat`; a `version` probe only when the file changed). What happens next depends on `[updates]` in config.toml (both default to `true`, both also in the Settings panel under UPDATES):

| Setting | On (default) | Off |
|---------|--------------|-----|
| `auto_install` | When a release is installable the TUI runs `agent-deck update --unattended --trigger tui` in the background (no prompt, the deck stays usable). One attempt per version per hour; a failure shows one footer line ("auto-update to vX failed: ...; run agent-deck update"). Skipped for Homebrew-managed installs, while a release is still publishing, and under `AGENTDECK_SKIP_UPDATE_CHECK`. | Banner: `⬆ Update available: vA → vB (... press ctrl+y to install (agent-deck update) · Esc to dismiss)` for 6+ releases behind; `Ctrl+Y` installs interactively. |
| `auto_restart` | Once a newer build is on disk the banner reads `⬆ vX installed, restarting when idle (ctrl+t now)` and the TUI restarts itself at the first tick with no dialog open, no insert mode and no session action in flight (attached sessions are never interrupted: the restart only happens from the home screen). After the restart the footer says `restarted into vNEW (was vOLD)` and the cursor is back on the session it was on. | Banner: `⬆ vX installed, press ctrl+t to restart agent-deck`; `Ctrl+T` restarts when you choose. |

Before the restart is armed (by the key or on its own) the TUI checks the file it is about to exec: it must be a regular, non-empty, executable file that answers `agent-deck version`; otherwise the restart is refused with a footer message (`restart blocked: new binary ... ; still running vOLD`) and the old build keeps running. The restart replaces the process in place (same executable path, args and environment), so tmux sessions, MCP pools and the web server are untouched. `web --no-tui` restarts itself the same way when no request is in flight; a remote agent exits cleanly instead and the controller reconnects. None of this happens on its own when the TUI is driven by a test, CI or a script: under `go test`, with `AGENTDECK_SKIP_UPDATE_CHECK` set, with `CI` truthy, with an `AGENTDECK_TEST_*` marker, or without a terminal on stdin and stdout, the banner reverts to `press ctrl+t to restart agent-deck` and only the keys act (issue #2251).

## Layout

- **< 50 cols:** List only
- **50-79 cols:** Stacked (list above preview)
- **80+ cols:** Side-by-side (default)

## Tool Icons

| Tool | Icon | Color |
|------|------|-------|
| Claude | 🤖 | Orange |
| Gemini | ✨ | Purple |
| OpenCode | 🌐 | Cyan |
| Codex | 💻 | Cyan |
| Cursor | 📝 | Blue |
| Shell | 🐚 | Default |

## Color Scheme (Tokyo Night)

| Element | Color |
|---------|-------|
| Accent (selection) | #7aa2f7 |
| Running | #9ece6a |
| Waiting | #e0af68 |
| Error | #f7768e |
| Groups | #7dcfff |
| Background | #1a1b26 |
| Surface | #24283b |

## Hidden Features

- **`Ctrl+K/J`:** Vim-style navigation in search
- **Numbers 1-9:** Jump to root groups instantly
- **Status filters are toggles:** Press again to turn off
