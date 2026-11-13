# Agent Deck

A terminal session manager for AI coding agents. Sessions are organised into groups; this glossary names the concepts users meet in the TUI, CLI and web UI.

## Sessions and groups

**Session**:
One agent (Claude Code, Codex, Gemini, ...) running in its own tmux pane, rooted at a working directory.
_Avoid_: Agent, instance, tab

**Group**:
A named, nestable folder of sessions in the sidebar. A group is not itself a directory on disk.
_Avoid_: Folder, workspace

## Git forge integration

**Project**:
The GitHub repository a group resolves to: taken from the group's default path, otherwise from the repositories of the sessions directly in it. Worktrees of one repository resolve to the same project; a group whose sessions span several repositories has several projects. A group never shows its child groups' projects. When the repository is a fork, the project is the fork, not its parent repository.
_Avoid_: Repo (when meaning the group's resolved repository), workspace

**Parent Repository**:
The repository a fork project was forked from. It can be viewed in place of the project but is never the project itself.
_Avoid_: Upstream (that is a git remote name, which may point anywhere)

**Issue**:
A GitHub issue of a project.
_Avoid_: Ticket, task

**Pull Request**:
A GitHub pull request of a project. Shortened to PR in the UI.
_Avoid_: Merge request, MR

**Merge method**:
How a pull request's commits land on its base branch: squash, merge commit, or rebase. A project allows a subset of the three.
_Avoid_: Merge strategy (git uses that for something else)

**Link**:
A recorded tie between a session and an issue or pull request. A session has at most one linked issue and one linked pull request; an issue or pull request may be linked to many sessions.
_Avoid_: Association, binding

**Attention signal**:
A live condition of a session's linked issue or pull request (failing checks, changes requested, approved, merged, closed), or a pending review request addressed to the user, shown without opening the issues or pull requests of a project. A signal lasts exactly as long as its condition holds; it is never marked as seen.
_Avoid_: Alert, badge (a badge is how a signal is drawn)

**Filter**:
A query that narrows the issues or pull requests of a project shown in a list, written in GitHub's search syntax. Every list has one; the default shows open items. A filter belongs to one project and one kind (issues or pull requests).
_Avoid_: Search (that finds sessions), view

**Prompt Template**:
A named text, chosen per kind of issue or pull request (bug, implementation, review feedback, ...), that is filled with that issue's or pull request's number and content to become the first message of a session launched from it. Agent Deck ships built-in templates; a user's template of the same name replaces the built-in one.
_Avoid_: Preset, prompt preset
