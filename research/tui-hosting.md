# TUI hosting: where tabs, detail views and new keys fit in `internal/ui`

Research for sanchit36/agent-deck#3 (map: #1). Source surveyed: `internal/ui/` and
`internal/session/userconfig.go` at `f7017710` (origin/main). Every claim below cites the
file and line it was read from; line numbers are for that commit.

## TL;DR

- The whole home screen is one Bubble Tea model, `*Home` (`internal/ui/home.go`, 9k lines).
  `View()` (`home_view_frame.go:472`) calls `renderFrame()` (`:505`), which either returns a
  full-screen overlay early or composes header, filter bar, main content and help bar.
- The left pane is two calls: `h.sidebarTitle()` + `h.renderSessionList(w, h)`. They appear
  in three layout functions (dual, stacked, single). That pair is the seam for a
  `Sessions | Issues | PRs` tab switcher.
- The right pane is `h.renderPreviewPane(w, h)` (`home_view_preview.go:735`), a big
  dispatch on the selected `flatItems[cursor]` type. A tab-aware branch at its top is the
  seam for Issue/PR detail.
- Full-screen overlays are an `if X.IsVisible() { return X.View() }` ladder in
  `renderFrame` (`home_view_frame.go:532-657`) mirrored by an equal key-routing ladder in
  `updateInner` (`home.go:4957-5233`) and a list in `hasModalVisible`
  (`home_keys_main.go:452`). A new overlay (diff viewer, merge confirm) must be added to
  all three.
- Keys: every letter a-z and A-Z is taken on the home screen (the code says so itself,
  `hotkeys.go:85-88`). Free: most `alt+<letter>`, a few `ctrl+<letter>` and unused
  punctuation (`[ ] { } ; : ' " . = _ | \ ~ ( )`). Table below.
- Hot path: `View()` must do no I/O. Slow work goes in a `tea.Cmd` closure that returns a
  typed `...Msg`, with a debounce (150 ms preview), a per-key cache with TTL and a stale
  guard when the message lands. `gh` is already run from the TUI this way
  (`report_dialog.go` + `internal/issuereport/file.go`).

## 1. Frame composition (`renderFrame`)

`home_view_frame.go:505-1077`, in order:

1. Size guard: below `minTerminalWidth=40` x `minTerminalHeight=12` (`home.go:156-157`)
   print "Terminal too small" (`home_view_frame.go:520-530`).
2. Early-return full-screen overlays, `:532-657` (section 3).
3. Header bar (`:659-~930`): brand mark, profile, group scope, status counts, optional
   segments driven by `[ui.header]` (`session.LoadUserConfig()` at `:698`).
4. Filter bar (`renderFilterBar`, `:221`): status filter chips (`0 ! @ # $ % & ^`).
5. Main content (`:942-971`), height
   `h.height - 1 - helpBar(2) - banners - sandbox - filterBar - debug - promptInput - hintBar`
   (`:951`). Dispatch:
   - `welcome` -> `renderWelcome` (`:954`; precedent for replacing the content area and
     owning keys, see `welcome.go:62,190,260`).
   - `embeddedMode && embeddedSidebarHidden` -> `renderEmbeddedOnlyLayout` (`:956`, `:1720`).
   - else `getLayoutMode()` (`:24`): `LayoutModeSingle` -> `renderSingleColumnLayout`
     (`:1701`), `LayoutModeStacked` -> `renderStackedLayout` (`:1663`), `LayoutModeDual` ->
     `renderDualColumnLayout` (`:1546`).
6. Prompt-input bar, onboarding hint bar, help bar (`renderHelpBar`, `:1748`; replaced by
   insert-mode bar, health warning, session-ref pick bar or toast, `:988-1009`), debug bar,
   error/storage/watcher warning lines.
7. Session switcher (Ctrl+S) is the one overlay composited ON TOP of the frame instead of
   replacing it: `renderSessionSwitcherOverlay` (`home.go:8168`) using `overlayAtCells`
   (`home.go:8200`), applied at `home_view_frame.go:1059-1075`.
8. Final `clampViewToViewport` (`:1410`) forces exactly `h.width x h.height`.

Rule enforced throughout: every block is padded with `ensureExactHeight` (`:1384`) and
`ensureExactWidth` (`:1513`) before joining; display text is cut with `truncateDisplay`
(e.g. `renderPanelTitle`, `:1080-1098`), never byte slices (AGENTS.md review rule 3).

## 2. Left pane: seam for a `Sessions | Issues | PRs` tab switcher

Today the left pane in each layout is:

| Layout | Title | Body |
|---|---|---|
| Dual | `sessionsTitle := h.sidebarTitle()` `home_view_frame.go:1562` (or `SESSIONS nn%` during a `<`/`>` resize, `:1565-1568`) | `h.renderSessionList(leftWidth, panelContentHeight)` `:1570` |
| Stacked | `h.renderPanelTitle(h.sidebarTitle(), h.width)` `:1676` | `h.renderSessionList(h.width, listHeight-2)` `:1677` |
| Single | `h.renderPanelTitle(h.sidebarTitle(), h.width)` `:1707` | `h.renderSessionList(h.width, listHeight)` `:1708` |

- `sidebarTitle()` (`embedded_sidebar.go:214-222`) returns `SESSIONS` (classic) or
  `AGENTS  FLAT|GROUPED` (embedded). It is a plain string that `renderPanelTitle`
  (`home_view_frame.go:1080`) draws as a 2-line block (title + `─` underline). A tab
  strip (`Sessions · Issues · PRs`) fits in that same title row with no height change.
- `renderSessionList` (`home_view_list.go:15`) renders `h.flatItems` (`home.go:251`) with
  `h.cursor` (`home.go:370`) and the viewport kept by `syncViewport`
  (`home_flatitems.go:925`). All of `handleMainKey`'s navigation (`home.go:5500-5610`)
  mutates that same `cursor`/`flatItems`, so an Issues or PRs list needs its own
  items/cursor/scroll state rather than reusing `flatItems`.

Recommended seam: introduce a `leftTab` field on `Home` and two helpers,
`h.leftPaneTitle()` and `h.renderLeftPane(w, h)`, that return today's
`sidebarTitle()`/`renderSessionList()` for the Sessions tab and the new list renderers
otherwise; replace the three call-site pairs above with them. Things that must move with
it:

- Mouse hit-testing assumes the list starts after header + filter bar + 2 title lines:
  `getListContentStartY` (`home_keys_main.go:646-659`), `mouseYToItemIndex` (`:662`),
  `contentChromeTop` (`home_view_frame.go:50`) and `stackedPreviewTopY` (`:72`). Keeping
  the tab strip inside the existing title row avoids touching these; a separate extra row
  would need all four updated.
- Click handling (`handleMouse`, `home.go:5311`) only selects `flatItems`; a non-session
  tab needs its own branch.
- Key routing: the precedent for a mode that owns keys before the main switch is the
  pre-dispatch block in `handleMainKey`: `welcomeActive()`/`handleWelcomeKey`
  (`home.go:5471-5475`) and `sessionRefPicking()` (`:5479-5483`). A non-Sessions tab
  would intercept movement, Enter and its own action keys there, and fall through to the
  global switch for everything else.
- The empty-state code for the list (`home_view_list.go:18-80`) and `filteredEmptyState`
  (`home_view_frame.go:1330`) are Sessions-specific.
- `tab` is currently taken (expand/collapse group, `home.go:5778`); switching tabs needs a
  different key (section 5).

## 3. Right pane / preview: seam for Issue and PR detail

`renderPreviewPane(width, height)` (`home_view_preview.go:735`) is called from dual
(`home_view_frame.go:1579` embedded / `:1590` classic), stacked (`:1691`) and
embedded-only (`:1723`) layouts. Single-column has no preview, and
`fetchSelectedPreview` returns nil there (`home_preview_fetch.go:514-520`, issue #1366).
Its dispatch order:

1. Embedded focused terminal -> `renderEmbeddedTerminalContent` (`:737-739`).
2. Empty list / no selection -> responsive empty state + system stats (`:741-787`).
3. Group row -> `renderGroupPreview` (`:792`, `:2292`).
4. Remote rows -> embedded/detached terminal or `renderRemotePreview` (`:796-826`, `:241`).
5. Window rows resolve to the parent session (`:829-839`); creating placeholders animate
   (`:842-847`).
6. Session: embedded live terminal (`:877-890`), embedded-layout detached content
   (`:892-897`), else the classic info card + optional Analytics + Notes + captured
   output. `previewMode` (`home.go:203-209`, `v` key) picks Both / Output / Analytics.

"Analytics" is not an overlay: it is a section inside this pane
(`home_view_preview.go:1640-1662`), filled from `h.currentAnalytics`, which
`fetchAnalytics` (`home_preview_fetch.go:575`) loads asynchronously and
`analyticsFetchedMsg` (`home.go:4498`) installs, with a TTL cache
(`getAnalyticsForSession`, `home_preview_fetch.go:556-571`; pruned at `:30`).

Seam: a branch at the very top of `renderPreviewPane` (after the embedded check) that,
when the left tab is Issues/PRs, renders the detail of the selected Issue/PR from a cache
filled by a `tea.Cmd`. The pane title `PREVIEW` is a literal at `home_view_frame.go:1563`
and `:1690`; it would become `ISSUE #123` / `PR #45`. Width is set by `splitPaneWidths`
(`split_config.go:113`, `[ui] preview_pct`), so the detail view gets the same resizable
column for free.

## 4. Full-screen overlays and panels today

Three parallel lists must stay in sync for any overlay:

| Concern | Where |
|---|---|
| Render (early return, first match wins) | `home_view_frame.go:532-657` |
| Key routing (same order, first match wins) | `home.go:4957-5233` inside `updateInner` (`home.go:2196`) |
| "Is a modal up?" (blocks mouse, background nudges) | `hasModalVisible`, `home_keys_main.go:452-473` |
| Resize | `updateSizes` (`home_view_frame.go:433`) and the `tea.WindowSizeMsg` case (`home.go:2224-2261`) |

Patterns in use:

- **Struct panel with `Show/Hide/IsVisible/SetSize/Update/View`**, held as a pointer on
  `Home`. Examples: `HelpOverlay` (`help.go:80-242`), `AgentsPanel`
  (`agents_panel.go:19-213`, list + detail inside one panel), `ContextPager`
  (`context_pager.go:35-1523`), watcher, triggers, dead-letter, settings panels.
  `Update` returns `(*Panel, tea.Cmd)`; the parent assigns it back
  (`home.go:5022-5041`).
- **Context pager** (`C`, the context inspector) is the best precedent for an Issue/PR
  detail or diff viewer: full screen, its own tab row (`Overview | Breakdown | Verify`,
  `context_pager.go:84`, `NextTab/PrevTab` `:309-312`, tab row rendered in `View` from
  `:1557`), per-tab navigation stacks (`:51`), and async load: `openContextInspector`
  shows the pager in a loading state immediately and returns `inspectContextCmd`
  (`context_inspector.go:69-143`); `contextReportMsg` is cached first, then installed only
  if the pager is still open on the same session (`home.go:4285-4297`).
- **Help overlay** (`?`, `help.go`): Tab toggles "5 keys to start" vs all keys, j/k/PgUp
  scroll, any other key closes (`help.go:185-239`). Sections are a static table
  (`help.go:356-492`: QUICK START, NAVIGATION, GROUP NAVIGATION, SESSIONS, COPY & TEXT
  SELECTION, WORKTREES, WATCHERS, GROUPS, SEARCH & FILTER, OTHER, STARTUP FLAGS); rebindable
  rows read the user's binding through `h.key(action, fallback)` (`help.go:160`).
- **Cost dashboard** (`$`): a bool + value struct, not a panel object
  (`home.go:985-986`). Opened at `home.go:7041-7049`, closed by `q`/`$`/`esc`, all other
  keys swallowed (`home.go:5221-5227`), drawn at `home_view_frame.go:655`. Two
  shortcuts worth not copying: `newCostDashboard` runs six `costs.Store` queries
  synchronously in the key handler (`cost_dashboard.go:24-40`), and `showCostDashboard`
  is missing from `hasModalVisible`, so mouse clicks still reach the list underneath.
- **Dialogs** (new, fork, confirm, report, ...) use the same ladder; `dialog_fit.go`
  provides width/row fitting helpers (`fitDialogWidth`, `dialogWindow` `:223`).
- **Composited overlay**: only the session switcher (section 1, step 7) and the new-session
  dropdown (`overlayDropdown`, `newdialog.go:28`) draw over the live frame.

## 5. Keys on the home screen

### How dispatch works

- `Update` (`home_update.go:12`) splits coalesced rune bursts into single keys, then
  `updateInner` routes `tea.KeyMsg` through the overlay ladder (section 4), then notes
  editor, then `handleMainKey` (`home.go:5431`).
- `handleMainKey`: insert mode short-circuit (`:5434`), then
  `key := h.normalizeMainKey(raw)` (`home_keys_main.go:80`) which maps the pressed key to
  the action's canonical default key via `h.hotkeyLookup`, or blanks it if the default was
  rebound away (`blockedHotkeys`). The big `switch key` (`home.go:5485-7062`) cases on
  canonical default keys (literals or `defaultHotkeyBindings[...]`).
- User-configurable: `[hotkeys]` in `config.toml` -> `UserConfig.Hotkeys map[string]string`
  (`internal/session/userconfig.go:86-89`), resolved by `resolveHotkeys`
  (`hotkeys.go:284`), with action names in `hotkeys.go:10-135` and defaults in
  `defaultHotkeyBindings` (`hotkeys.go:197-258`). Empty string unbinds. `switch_session`
  ships disabled (`defaultDisabledHotkeys`, `:280`). Russian ЙЦУКЕН twins of letter
  bindings are added automatically (`buildHotkeyLookup`, `:345-384`; `jcukenLetters`,
  `:457`), so a new letter binding also claims its Cyrillic twin.
- Docs for keys: `skills/agent-deck/references/tui-reference.md` (says which keys are
  rebindable) and the help overlay table.
- Guard test: `TestAdvertisedOverviewBindingsHaveSingleMeaning`
  (`hotkeys_test.go:21`) parses the package AST and fails if two dispatcher cases share
  a key alias; `TestHomeNavigationKeysMatchHelpOverlay` (`help_nav_keys_test.go:34`)
  ties navigation keys to the help overlay.

### Taken keys (home view, normal mode)

R = rebindable via `[hotkeys]` (action name in brackets); F = fixed literal in the switch.

| Key | Action | Kind / source |
|---|---|---|
| `q`, `ctrl+c` | quit | R `quit`, `home.go:5485` |
| `esc` | dismiss banner; double-Esc quits | F `:5488` |
| `j`/`down`/`ctrl+n`, `k`/`up`/`ctrl+p` | move | F `:5504-5530` |
| `ctrl+u`/`pgup`, `ctrl+d`/`pgdown` | half page | F `:5533-5560` |
| `ctrl+b`, `ctrl+f` | full page | F `:5563-5575` |
| `home`, `end` | first / last | F `:5590-5598` |
| `G` | Recall (global) search | F `:5609` |
| `alt+j`/`alt+k`, `alt+1..9`, `alt+g`/`alt+G`, `alt+/` | group navigation | F `:5614-5651` |
| `H` | open shell here | R `open_shell_here`, `:5656` |
| `shift+enter` | open in new iTerm window | F `:5671` |
| `alt+enter` | full-screen attach (embedded layout) | F `:5715` |
| `enter` | attach (classic) / focus embedded / toggle group | F `:5721` |
| `tab`, `l`, `right` | expand / toggle | F `:5778` |
| `h`, `left` | collapse / parent | F `:5822` |
| `shift+up`/`ctrl+up`/`+`/`K`, `shift+down`/`ctrl+down`/`-`/`J` | reorder | F `:5827-5866` |
| `shift+left`, `shift+right` | move between groups | F `:5897-5916` |
| `,` | cycle pin | F `:5936` |
| `p`, `P` | edit paths, edit session | R `edit_paths`, `edit_session`, `:5957-5968` |
| `m`, `L`, `s` | MCP, plugin, skills managers | R, `:5983-6081` |
| `f`, `F` | quick fork, fork with options | R, `:6017-6055` |
| `M` | move to group | R, `:6094` |
| `b`, `W` | worktree setup, finish | R, `:6115-6136` |
| `g` | create group | R, `:6164` |
| `r` | rename | R, `:6230` |
| `/` | search | R, `:6248` |
| `?` | help | R, `:6256` |
| `<`, `>` | shrink / grow preview | F `:6262-6272` |
| `O` | preview orientation | F `:6279` |
| `S` | settings | R, `:6288` |
| `w` | watcher panel | R, `:6294` |
| `alt+d`, `alt+l`, `alt+t`, `alt+a` | dead letters, session ref, triggers, agents | R, `:6301-6323` |
| `E` | exec shell | R, `:6335` |
| `n`, `N` | new session, quick create | R, `:6359-6362` |
| `z` | zoxide picker | F `:6373` |
| `d`, `D` | delete, close session | R, `:6379-6429` |
| `A`, `shift+u` (`U`) | archive, unarchive | R, `:6441-6452` |
| `X`, `ctrl+x` | remove stopped / bulk remove errored | F `:6466-6482` |
| `i`, `I` | import, insert mode | R / F, `:6499-6502` |
| `u` | mark unread | R, `:6511` |
| `a` | quick approve | R, `:6554` |
| `o`, `Q` | prompt session, queue message | R, `:6583` |
| `space` | jump mode | F `:6600` |
| `v`, `alt+v` | preview mode, flat/grouped sidebar | R / F, `:6607-6612` |
| `t`, `*` | group view mode, time filter | R, `:6627-6638` |
| `y`, `Y` | toggle yolo, copy (fixed) | R / F, `:6650`, `:6825` |
| `R`, `T` | restart, restart fresh | R, `:6724-6755` |
| `c`, `C`, `B`, `V` | copy output, context inspector, copy info, copy pane | R, `:6772-6814` |
| `x` | send output | R, `:6838` |
| `e` | edit notes | R, `:6854` |
| `ctrl+g` | Gemini model | F `:6870` |
| `ctrl+z` | undo delete | R, `:6878` |
| `ctrl+r`, `ctrl+t`, `ctrl+y` | reload, restart deck, install update | R, `:6903-6922` |
| `ctrl+s` | session switcher (opt-in) | R, `:6927` |
| `` ` ``, `alt+left`, `alt+right` | alternate / MRU back / forward | R, `:6939-6948` |
| `alt+w`, `alt+n` | show around, what's new | R, `:6952-6958` |
| `Z` | report a problem (files a GitHub issue via `gh`) | R, `:6970` |
| `ctrl+e` | feedback dialog | F `:6985` |
| `1`-`9`, `0` | jump to root group, clear filter | F `:7019-7025` |
| `!` `@` `#` `%` `&` `^` | status filters, `^` view archived | F `:7029-7059` (constants `home.go:162-175`) |
| `$` | cost dashboard | F `:7041` |
| `ctrl+q` | detach (attach loop, not this switch) | R `detach` |
| `pageup` | scrollback trigger while attached (attach loop) | R `scrollback` |

Letters: all 26 lowercase and all 26 uppercase are bound (U via `shift+u`). The code
itself records this: "every lowercase letter and A-Y are taken" (`hotkeys.go:85-88`),
written before `Z` went to report_problem.

### Free keys (no case in `handleMainKey`, no default binding)

| Class | Free | Notes |
|---|---|---|
| Alt + letter | `alt+b alt+c alt+e alt+f alt+h alt+i alt+m alt+o alt+p alt+q alt+r alt+s alt+u alt+x alt+y alt+z` | Precedent: agents/triggers/dead letters all chose `alt+<mnemonic>` because letters were full (`hotkeys.go:64-84`). `alt+i` (Issues) and `alt+p` (PRs) are free. |
| Alt + digit | `alt+0` | `alt+1..9` taken. |
| Ctrl + letter | `ctrl+a ctrl+k ctrl+l ctrl+o ctrl+v ctrl+w` | `ctrl+a` is used only inside the session switcher (`home.go:8919`). Avoid `ctrl+h` (Backspace), `ctrl+i` (Tab), `ctrl+j`/`ctrl+m` (Enter), `ctrl+q` (detach), `ctrl+s` (XOFF, switcher). |
| Punctuation | `[ ] { } ; : ' " . = _ \| \ ~ ( )` | `[`/`]` already mean page up/down inside global search (`global_search.go:369-372`), a consistent "previous/next" reading for tab cycling. |
| Shift + arrows | none free | all four used. |

In embedded mode with a focused session, almost nothing reaches the dashboard: the input
router forwards bytes to the PTY except Ctrl+Q (`session_input_router.go:578`), the
switch byte, and Ctrl+Alt+B (sidebar toggle, `:713-727`). New keys only matter in normal
(unfocused) mode.

## 6. Classic vs embedded layouts

Two separate flags on `Home`:

- `embeddedLayout` (`home.go:386`): the layout preference, `[ui] embedded_terminal`
  (`userconfig.go:573`, default false via `GetEmbeddedTerminal`, `:833`), loaded at
  `home_new.go:289`, toggled in Settings (`settings_panel.go:890`).
- `embeddedMode` (`home.go:1011`): a session is currently focused inside the dashboard
  (`enterEmbeddedMode`, `insert_mode.go:126-151`; sets `insertMode` too).

| | Classic (`embeddedLayout=false`) | Embedded (`embeddedLayout=true`) |
|---|---|---|
| Enter on a session | `attachSelectedLegacy` (`home.go:7066`): tmux attach via `tea.Exec`, TUI suspended; `View()` returns the last frame while `isAttaching` (`home_view_frame.go:487-489`) | `enterEmbeddedMode` + `startEmbeddedModeCmd`: live PTY in the right pane; `alt+enter` still does the classic attach (`home.go:5715`) |
| Left pane title | `SESSIONS` | `AGENTS  FLAT/GROUPED` (`alt+v`) |
| Session rows | `renderSessionItem` one line | card rows by `[ui] sidebar_density` (`renderEmbeddedSessionItem`, `embedded_sidebar.go:224`; card branch `home.go:8237`; classic `renderSessionItem` `home.go:8229`) |
| Right pane | `PREVIEW` title + `renderPreviewPane` | rounded accent border, no title (`home_view_frame.go:1576-1587`); live/detached terminal content for running sessions (`home_view_preview.go:892-897`) |
| Layout modes | single <50, stacked <80, dual (or stacked by `preview_orientation="below"`) (`home_view_frame.go:24-43`, breakpoints `home.go:191-192`) | stacked <80 else dual, never single (`:25-30`); sidebar can be hidden entirely (`renderEmbeddedOnlyLayout`) |

Both layouts call the same `sidebarTitle`/`renderSessionList`/`renderPreviewPane`, so a
tab seam placed in those functions serves both. The embedded right pane is a live
terminal only for the focused or running session; an Issues/PRs tab would render its own
detail there instead.

## 7. Hot-path constraints

### Budgets and tests

- Published budgets (`docs/performance.md:17-40`): `tui.first_frame` 500 ms; list render
  `BenchmarkOp_TUIListRender/{100,500}` 16 / 33 ms; `tui.key_frame` 16 ms;
  `tui.preview` 150 ms after the 150 ms debounce; `tui.status_pass` 250 ms; search 16 ms.
  Current medians are around 1 ms per frame (`:121-125`).
- Benchmarks: `opbench_tui_test.go:143-182` (full `View()` with N sessions),
  `fleet_bench_test.go:26` (tools/bench only). Perf helpers in
  `internal/testutil/perfbudget.go` (warm 3x / cold 5x, trimmed mean of 11, scaled by
  `PERF_BUDGET_MULTIPLIER`, CI 2.0).
- Idle CPU: `internal/tuitest/idle_cpu_test.go:58` holds an idle TUI with 1000 hook
  sessions under 5% of a core (`:25`). A new background poller (e.g. `gh` refresh) counts
  against this.
- Source-level guards: `TestIssue1753_AttachReturnHandlersHaveNoInlineTmuxCalls`
  (`issue1753_detach_return_test.go:327`) bans inline tmux calls in attach-return
  handlers; `TestIssue1753_AttachReturnUpdateStaysUnderBudget` (`:264`, 150 ms).
- Debug-mode render timing: `renderFrame` logs `slow_view_render` above 50 ms
  (`home_view_frame.go:1043-1053`); `optiming.Since(h.pendingFrameOp, ...)` measures
  key-to-frame ops (`:494-497`).
- AGENTS.md: hot paths (list, status, session output, startup, tmux layer) need
  before/after timing in the PR.

### Patterns for slow work

- **Never block in `View()` or in a key handler.** `fetchAnalytics` states it:
  "keeps View() pure (no blocking I/O)" (`home_preview_fetch.go:573-574`). The one
  exception tolerated in render is `session.LoadUserConfig()` (`home_view_frame.go:698`,
  `:823`), which is an mtime `os.Stat` plus a cached struct (`userconfig.go:4623-4640`).
- **Cmd closure + typed Msg**: `fetchPreview` (`home_preview_fetch.go:237-260`) returns
  `func() tea.Msg { ... return previewFetchedMsg{...} }`; handled at `home.go:4478`.
- **Debounce with a pending key**: `fetchPreviewDebounced` (`home_preview_fetch.go:264-276`)
  records `pendingPreviewKey`, sleeps 150 ms inside the Cmd, and `previewDebounceMsg`
  (`home.go:4338-4350`) drops the fetch if the user moved on. Cursor moves call it
  (`home.go:5509-5512`).
- **In-flight dedupe**: `previewFetchingID` (`home.go:4365-4369`).
- **Cache with TTL + prune**: analytics (`home_preview_fetch.go:30-42`, `:556-571`);
  context reports cached even if the overlay closed (`home.go:4286-4289`).
- **Stale guard on arrival**: install only if still visible and still the same target
  (`home.go:4290`).
- **Show immediately in a loading state**, then fill: `ContextPager.Show` +
  `SetLoading` (`context_inspector.go:74`, `:103`), "Loading analytics..."
  (`home_view_preview.go:1656-1661`).
- **Periodic work** rides `tea.Tick`: the main tick is 2 s (`home.go:88`,
  `home_watchers.go:261`); status runs on a worker goroutine (`home_status.go:650-670`).
  Any new goroutine/ticker must be joined before shutdown (AGENTS.md rule 4;
  `performFinalShutdown`, `home.go:7157`).
- **`gh` precedent**: the report dialog runs `gh` in a Cmd with a context timeout
  (`report_dialog.go:166`), `issuereport.GhReady` (`internal/issuereport/file.go:112`,
  `gh auth status`) and `issuereport.File` (`:131-148`), env from `childenv.ForLaunch`
  plus `GH_PROMPT_DISABLED=1 NO_COLOR=1 GH_NO_UPDATE_NOTIFIER=1` (`file.go:103`). A GitHub
  data layer can reuse that env and readiness probe.

## 8. Implications for the layout prototype and keybinding tickets

1. Put the tab strip in the existing left panel title row (`renderPanelTitle`), behind
   two helpers replacing the `sidebarTitle()`/`renderSessionList()` pairs at
   `home_view_frame.go:1562-1570`, `:1676-1677`, `:1707-1708`. No chrome-height change,
   so mouse Y math is untouched.
2. Give each tab its own items/cursor/viewport; intercept keys for non-Sessions tabs in
   `handleMainKey` next to `welcomeActive`/`sessionRefPicking` (`home.go:5471-5483`).
3. Detail goes in `renderPreviewPane` (top-level tab branch); the diff viewer and merge
   confirm are full-screen overlays modelled on `ContextPager` (tabs, loading state, async
   Cmd, stale guard), added to the render ladder, key ladder, `hasModalVisible` and the
   resize hooks.
4. Candidate keys: `alt+i` / `alt+p` (jump to Issues / PRs tab, matching the `alt+<mnemonic>`
   precedent), `[` / `]` to cycle tabs, all as new `[hotkeys]` actions in
   `hotkeyActionOrder` + `defaultHotkeyBindings`, listed in the help overlay and
   `tui-reference.md`. `tab` is not available without rebinding expand/collapse.
5. All `gh` calls as debounced, cached `tea.Cmd`s; any periodic refresh must stay inside
   the idle-CPU budget and be joined on shutdown.
