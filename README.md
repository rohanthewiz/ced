<!--
  File: README.md
  Author: Rohan Allison <rohanthewiz@gmail.com>
  Created: 2026-04-29
  Copyright: 2026 Rohan Allison. All rights reserved.
  Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
-->

# ced — Cats Editor

> An opinionated, **mouse-first** terminal code editor for SSH workflows.

ced (short for **Cats Editor**) is a single-binary code editor that runs
inside your terminal but behaves like a tiny VS Code: a file tree on the
left, tabs across the top, syntax highlighting in the middle, a status bar
at the bottom — and it's all driven by the **mouse**, not arcane keystrokes.

It's built for the workflow most "modern" terminal editors ignore: SSHing
into a remote box from inside `tmux` / `zellij`, opening a project, clicking
around files like a normal human, copying and pasting through your local
clipboard, and getting back to work.

<img width="2510" height="1712" alt="CleanShot 2026-04-29 at 23 30 21@2x" src="https://github.com/user-attachments/assets/a42ff082-406c-48cf-b5ca-9ca978ada217" />

## Why does this exist?

Vim and friends are wonderful if you've spent years memorizing them. Most
terminal editors assume you have. ced doesn't.

The goals, in order:

1. **Mouse-first.** Click a file to open it. Click a tab to switch.
   Click-and-drag to select text. Scroll wheel actually scrolls.
   Drag the splitter to resize the sidebar. Right-click (or click the
   `≡` icon, or double-tap `Esc`) for the action menu.
2. **No hot-key archaeology.** Save, save & close, quit — they all live
   in a centered modal you open with one gesture. No `Ctrl+` shortcuts
   that fight `tmux`, your shell, or your terminal emulator.
3. **SSH-friendly.** Copy uses OSC 52 escape sequences with a tmux
   passthrough wrapper, so highlighting text on a remote box still
   ends up in your local Mac clipboard.
4. **One static binary.** No runtime, no plugin manager, no config
   directory full of YAML. Drop it on a server and run it.
5. **Looks reasonable.** Tokyo Night-inspired palette out of the box,
   syntax highlighting via [chroma](https://github.com/alecthomas/chroma)
   (no CGO, no tree-sitter setup).

## Features

- **VS Code-shaped layout** — file tree on the left, tab bar across the
  top, editor in the middle, status bar at the bottom.
- **Mouse-driven everything** — click to place cursor, drag to select,
  scroll wheel scrolls, double-click selects a word, drag past the edge
  to auto-scroll a selection.
- **Syntax highlighting** for dozens of languages via Chroma.
- **Action menu** opened with the `≡` icon, right-click, or double-tap
  `Esc`. Keyboard navigation works too — arrow keys + `Enter`.
- **Live file tree** — auto-refreshes every 10 seconds so files added
  or removed from disk show up without you doing anything.
- **External change detection** — if a file on disk changes underneath
  an open clean buffer, the editor reloads it; if your buffer is dirty,
  you get a heads-up; if the file is deleted, the tab is flagged once.
- **Toggleable, draggable sidebar** — show/hide the file tree from the
  menu, or drag the splitter to resize it.
- **Clipboard over SSH** — OSC 52, including a `tmux` passthrough so
  copy works from inside a tmux session on a remote host.
- **Format on save** — opt-in per-project via `.ced/format.json`
  with a first-run trust prompt so cloning a repo never silently
  executes its commands. See [Format on save](#format-on-save).
- **AI, optional** — inline ghost-text completions from GitHub Copilot,
  plus a chat panel that talks to Copilot, Claude Code, or Gemini over
  ACP: your code as context, and file edits you approve one at a time.
  Installing an agent's binary is the whole opt-in; without one the
  editor never mentions AI. See
  [AI features](#ai-features-chat-agents--copilot-completions).
- **MCP servers** — declare Model Context Protocol servers once in
  `~/.config/ced/mcp.json` (the same format Claude Desktop uses) and
  they're handed to whichever chat agent you run, plus browsable and
  runnable from the menu. See [MCP servers](#8-mcp-servers-more-tools-for-your-agent).
- **Single binary, no CGO** — cross-compiled for macOS and Linux on
  amd64 and arm64. POSIX only: the embedded terminal panel needs
  job-control syscalls Windows doesn't provide.

<img width="2504" height="1726" alt="CleanShot 2026-04-29 at 23 32 22@2x" src="https://github.com/user-attachments/assets/d0dca3da-5ba7-474d-852e-832acde90ca4" />

## Install

### macOS / Linux (Homebrew)

The Homebrew formula is published into this repo's `Formula/` directory.
Tap it by URL (no `homebrew-*` repo naming convention required), then
install:

```sh
brew tap rohanthewiz/ced https://github.com/rohanthewiz/ced
brew install rohanthewiz/ced/ced
```

### Updating

When a new release ships, refresh the tap and upgrade:

```sh
brew update
brew upgrade rohanthewiz/ced/ced
```

### Uninstalling

```sh
brew uninstall rohanthewiz/ced/ced
brew untap rohanthewiz/ced
```

### Linux (one-line install script)

The simplest way to drop ced onto a Linux box (or any macOS that
isn't using Homebrew) is the install script:

```sh
curl -fsSL https://raw.githubusercontent.com/rohanthewiz/ced/main/install.sh | sh
```

It detects your OS / arch, downloads the matching archive from the
latest [GitHub Release](https://github.com/rohanthewiz/ced/releases),
and drops the `ced` binary into `~/.local/bin` (or `/usr/local/bin`
when `~/.local/bin` isn't writable). **Re-run the same command to
upgrade** — it always fetches the latest tagged release.

Override behaviour with environment variables:

```sh
# Pin to a specific release.
curl -fsSL https://raw.githubusercontent.com/rohanthewiz/ced/main/install.sh \
  | VERSION=v0.0.18 sh

# Install to a custom directory.
curl -fsSL https://raw.githubusercontent.com/rohanthewiz/ced/main/install.sh \
  | INSTALL_DIR=/opt/bin sh
```

The script is plain POSIX `sh` — it works on Alpine / BusyBox / any
SSH target where you don't want to depend on bash. It only needs `tar`
plus one of `curl` or `wget`.

### Other platforms (manual binary install)

Pre-built binaries for Linux and macOS (amd64 + arm64) are
attached to every [GitHub Release](https://github.com/rohanthewiz/ced/releases).
Download the archive for your OS/arch, extract it, and drop the
`ced` binary somewhere on your `$PATH`.

### From source

```sh
git clone https://github.com/rohanthewiz/ced.git
cd ced
make install        # builds and installs to $GOPATH/bin
```

## Usage

```sh
ced              # opens the current directory
ced ~/code/app   # opens a specific project root
ced main.go      # opens a file (project root = its parent dir)
ced new-file.go  # creates the file on first save (vim-style)
ced --version    # print version and exit
ced --help       # print short usage
```

Then:

- Click a file in the tree to open it.
- Click a tab to switch, click the `×` to close it.
- Click `≡` (top-left), right-click anywhere, or double-tap `Esc`
  for the action menu — including New file, Rename, Delete.
- If your terminal forwards Button3, right-click on a file or folder
  in the tree opens a per-item context menu (New File on folders,
  Rename, Delete). macOS Terminal + tmux often swallows right-click,
  so all of those actions also live in the main `≡` menu.
- Drag the splitter between the sidebar and editor to resize.
- Click and drag in the editor to select; drag past the top or bottom
  edge to auto-scroll the selection.

### Hotkeys

ced deliberately avoids `Ctrl+`-style shortcuts (they fight `tmux`,
`zellij`, and the terminal itself — `Ctrl+S` is XOFF flow control on a
real terminal). Instead, **`Esc` is the leader key**: tap `Esc`, then
within half a second tap one of the letters below.

| Combo       | Action               |
| ----------- | -------------------- |
| `Esc Esc`   | Open ≡ menu          |
| `Esc s`     | Save                 |
| `Esc u`     | Undo                 |
| `Esc r`     | Redo                 |
| `Esc w`     | Close tab            |
| `Esc q`     | Quit                 |
| `Esc n`     | New file             |
| `Esc t`     | Toggle sidebar       |
| `Esc /`     | Toggle line comment  |
| `Esc f`     | Find in file         |
| `Esc p`     | Find file in project |

A lone `Esc` is harmless — if you don't follow it with a bound key
within the window, your next keystroke goes to the editor as normal,
so accidental `Esc` taps never swallow a real character.

Everything reachable by hotkey is also reachable from the `≡` menu —
the hotkeys are just a faster path for the actions you reach for most.

### Find in file

`Esc f` (or **Find in file** from the `≡` menu) opens a search bar
above the status bar:

```
 Find: foo█                       3 of 12   Enter: next · Shift+Enter: prev · Esc: close
```

- Type to search — matching is **case-insensitive substring**, results
  highlight live as you type.
- `Enter` jumps to the next match (wraps at the end), `Shift+Enter`
  jumps to the previous one.
- `Esc` closes the bar and clears the highlights — each `Esc f` opens
  a fresh search.
- The active match is painted a brighter color than the rest, so you
  can pick out where you are in the result set.

There's no regex, whole-word, or case-sensitive toggle in v1 — the
common case is "I know roughly what I'm looking for, take me there."

### Find file in project

`Esc p` (or **Find file in project** from the `≡` menu) opens a
fuzzy file finder over every non-ignored file in the project:

```
┌ Find file                                                    esc ┐
│  app.go                                              50/12345    │
│  internal/app/app.go                                             │
│  internal/app/app_test.go                                        │
│  internal/finder/score.go                                        │
│  ...                                                             │
└──────────────────────────────────────────────────────────────────┘
```

- Type to fuzzy-match. The matcher prefers basename hits, consecutive
  matches, and word boundaries — typing `tab` finds `tab.go` before
  `tabs/foo.go` before `notable.go`.
- `↑` / `↓` to move, `Enter` to open, `Esc` to dismiss. Mouse hover
  highlights, click opens.
- Honours `.gitignore` automatically. The fast path uses
  `git ls-files --cached --others --exclude-standard` (so a 50k-file
  repo indexes in ~150ms); non-git projects fall back to a Go
  walker that still respects the project root's `.gitignore`.
- Indexed in the background at startup so the modal opens with
  results already in hand. Refreshes on the same 10-second cadence
  as the file tree, plus immediately after any create/rename/delete
  inside the editor.
- Only files are listed — no directories, no symlinked duplicates.

## Custom actions (open remote files on your laptop)

[![Watch the walkthrough](https://img.youtube.com/vi/vDWZWEmIiZ8/maxresdefault.jpg)](https://www.youtube.com/watch?v=vDWZWEmIiZ8)

> 📺 [Custom actions walkthrough on YouTube](https://www.youtube.com/watch?v=vDWZWEmIiZ8)

ced can read user-defined shell-out actions from
`~/.config/ced/actions.json` and prepend them to the action menu.
Each action runs against the **currently open file** when you click it.

The use case this was built for: you SSH from your laptop into a remote
box, edit a file there, and want to *open it on your laptop* — but
neither Sixel nor the Kitty graphics protocol survive the trip through
zellij/tmux. The trick is to bypass the terminal entirely and pipe the
file back over a second SSH connection.

### File location

`~/.config/ced/actions.json` (or `$XDG_CONFIG_HOME/ced/actions.json`
when set). The file is optional — without it, the menu just shows the
built-in actions.

### Schema

```json
{
  "actions": [
    {
      "label": "Open on Rager",
      "command": "scp \"$FILE\" rager:~/Downloads/ && ssh rager open \"~/Downloads/$FILENAME\""
    },
    {
      "label": "Open on Cascade",
      "command": "scp \"$FILE\" cascade:~/Downloads/ && ssh cascade open \"~/Downloads/$FILENAME\""
    }
  ]
}
```

Each entry needs:

- **`label`** — the menu text (kept under ~30 chars; long labels clip
  inside the modal).
- **`command`** — handed to `sh -c` with two env variables exported:
  - `FILE` — absolute path of the active tab's file
  - `FILENAME` — basename of the same file

> **`$HOME` and `~` gotcha for two-hop SSH:** the command runs in a
> shell on the *ced host* (the remote box you SSH'd into). So
> `$HOME` and `~` outside of `ssh "..."` quotes expand to *that* box's
> home directory, not your laptop's. To run something on your laptop,
> wrap the remote command in quotes: `ssh rager "open ~/Downloads/$FILENAME"` —
> `$FILENAME` is expanded locally (you want that — it's a filename),
> but `~` is sent literally and rager's shell expands it on arrival.

The action only enables when there's a file open. Commands run in a
background goroutine, so a slow `scp` or hanging `ssh` won't freeze
the editor; success or failure flashes in the status bar when it
finishes.

### Debugging — every run is logged

Every custom-action invocation appends a record to
`~/.local/state/ced/actions.log` (or
`$XDG_STATE_HOME/ced/actions.log` when set). One entry per run,
human-readable, with the exact command, the env vars that were
exported, the duration, and the combined stdout / stderr:

```
[2026-04-30T13:26:32-07:00] Open on Rager (1.234s) → ok
  command: scp "$FILE" rager:~/Downloads/ && ssh rager open "$HOME/Downloads/$FILENAME"
  FILE:     /Users/spicer/dev/foo/bar.txt
  FILENAME: bar.txt
  --- output ---
  --- end ---

[2026-04-30T13:27:01-07:00] Open on Cascade (0.521s) → exit status 1
  command: scp "$FILE" cascade:~/Downloads/ && ssh cascade open "$HOME/Downloads/$FILENAME"
  FILE:     /Users/spicer/dev/foo/bar.txt
  FILENAME: bar.txt
  --- output ---
  ssh: connect to host cascade port 22: Connection refused
  lost connection
  --- end ---
```

`tail -f ~/.local/state/ced/actions.log` while you click around
to watch entries roll in. There's no rotation — the file is one-line
per run plus a few lines of output, so it grows slowly. Delete it
whenever you want to start fresh.

### The "open on my laptop" workflow

Both example actions assume `rager` and `cascade` are SSH host aliases
in the **remote** machine's `~/.ssh/config` that resolve back to your
laptop. The simplest way to set that up:

1. **On your laptop**, generate (or pick) an SSH key pair you'll
   dedicate to inbound connections from your remote work box.
2. **On your laptop**, make sure Remote Login is enabled (System
   Settings → General → Sharing → Remote Login on macOS) and add the
   public key to `~/.ssh/authorized_keys`.
3. **On the remote box**, drop the matching private key into
   `~/.ssh/id_<name>` and add a host alias:

   ```sshconfig
   Host rager
     HostName your-laptop.example.com   # or a Tailscale / mesh hostname
     User your-mac-username
     IdentityFile ~/.ssh/id_rager
   ```

4. Test it by hand from the remote: `ssh rager echo hi`. Once that
   works, ced can drive it the same way.

If your laptop sits behind NAT, point `HostName` at a Tailscale /
WireGuard / Cloudflare-tunnel address — anywhere the remote can reach
the laptop directly. The action itself is just `scp` + `ssh`; it
doesn't care how the network gets there.

### Anything else `sh` can do

The schema is deliberately small. If you can write it on one shell
line, you can put it in `actions.json`:

```json
{ "label": "Send to ChatGPT", "command": "cat \"$FILE\" | pbcopy && open https://chat.openai.com/" }
{ "label": "Lint with eslint", "command": "cd $(dirname \"$FILE\") && eslint \"$FILENAME\"" }
{ "label": "Run formatter",    "command": "gofmt -w \"$FILE\"" }
```

## Format on save

ced can run a formatter on every save — `gofmt`, `php-cs-fixer`,
`prettier`, anything you like — but the feature is **off by default**
and only kicks in for projects that opt in by checking in a config
file. Quick edits to a stranger's repo will never silently rewrite
their files.

### Setup

Create `.ced/format.json` in your project root:

```json
{
  "commands": {
    "go":  ["gofmt", "-w", "$FILE"],
    "php": ["php-cs-fixer", "fix", "$FILE", "--quiet"],
    "py":  ["ruff", "format", "$FILE"],
    "js":  ["prettier", "--write", "$FILE"],
    "ts":  ["prettier", "--write", "$FILE"]
  }
}
```

- Keys are file extensions, **without** the leading dot.
- Values are argv arrays — passed straight to `execve`, no shell, so
  there's no injection surface. (Use `["sh", "-c", "..."]` if you
  genuinely need a shell.)
- `$FILE` in any argument is replaced with the absolute path of the
  file being saved.

### First save: trust prompt

The first time ced would run a formatter from a new (or edited)
`.ced/format.json`, you get a Yes / No prompt:

> **Trust this project's formatter?**
> Allow .ced/format.json to run formatters on save?

Pick **Yes** once and ced will run the configured formatters
silently from then on. Pick **No** and it will never run them in this
project — until the config file changes, at which point you'll be
prompted again. The remembered answer (and the SHA-256 hash of the
config it applies to) lives in
`~/.config/ced/format-trust.json`.

The hash is the security trick: a teammate can't push a "v2" of the
config that runs `rm -rf` — your editor will re-prompt the next time
you save, because the file has changed since you trusted it.

### What happens on save

1. Save writes the file to disk first. A broken formatter never
   blocks the save.
2. ced looks up the file's extension in `format.json`. No
   match → done.
3. The configured command runs in a goroutine. Slow formatters don't
   freeze the UI; you can keep typing.
4. When the formatter finishes, ced reloads the buffer — but
   only if you haven't typed anything since saving. If you did, your
   in-flight edits win and a status flash tells you the on-disk file
   was reformatted.
5. If the configured binary isn't installed, it's a silent no-op.
   You don't have to install everyone's formatter to clone the repo.

### Sharing vs. ignoring

Two reasonable patterns:

- **Commit `.ced/format.json`** so everyone on the team gets
  the same format-on-save behavior automatically.
- **Add `.ced/` to `.gitignore`** if developers prefer their
  own setups — each person's local copy can configure whatever
  formatters they like.

Both work. ced doesn't care which you pick.

### Personal defaults — the install prompt

You can list your favorite formatters once globally in
`~/.config/ced/format-defaults.json` (same shape as the
project file):

```json
{
  "commands": {
    "go":  ["gofmt", "-w", "$FILE"],
    "php": ["php-cs-fixer", "fix", "$FILE", "--quiet"],
    "py":  ["ruff", "format", "$FILE"]
  }
}
```

These never run on their own. Instead, when you save a file in a
project where:

1. The project's `.ced/format.json` is missing or has no
   entry for that file's extension, **and**
2. Your global defaults *do* have an entry for that extension,

…ced asks once: **"Add `gofmt` for `.go` to `.ced/format.json`?"**

- **Yes** — merges the entry into the project's config (creating
  `.ced/format.json` if it didn't exist), auto-trusts the
  resulting file, and runs the formatter on the save you just made.
- **No / Esc** — remembered per-extension in the trust file. You
  won't be re-asked about that file type in this project until you
  manually edit the project config.

This keeps your personal preferences out of repos that don't want
them while still making it one click to opt a project in.

## AI features (chat agents + Copilot completions)

ced has three independent AI surfaces:

| Surface | What it is | Provided by |
| --- | --- | --- |
| **Inline suggestions** | Dimmed ghost text at your caret, `Tab` to accept | GitHub Copilot only |
| **Chat panel** | Full-height strip on the left: streaming answers, your code as context, and — with your approval — edits to your files | Any agent that speaks [ACP](https://agentclientprotocol.com) — Copilot, Claude Code, or Gemini |
| **MCP servers** | Extra tools — issue trackers, databases, docs, your own scripts — handed to whichever chat agent is running, and browsable from the menu | Any [MCP](https://modelcontextprotocol.io) server you declare |

Both follow the same philosophy as everything else here:

- **Installing the agent's binary is the opt-in.** ced never bundles
  one, downloads one, or nags you about it. Nothing on `$PATH` → the
  editor works exactly as before.
- **Silent degradation.** A missing binary, a crash, or a failed
  handshake never blocks the editor. The chat panel is the one
  exception to the silence: once it's open on screen, failures are
  written into the transcript, because a panel that answers nothing
  and explains nothing is worse than no panel.

### 1. Install an agent binary

| Agent | Binary on `$PATH` | Typical install |
| --- | --- | --- |
| **Copilot** (default) | `copilot-language-server` | `npm install -g @github/copilot-language-server` |
| **Claude Code** | `claude-code-acp` | `npm install -g @zed-industries/claude-code-acp` |
| **Gemini** | `gemini` | `npm install -g @google/gemini-cli` |

Copilot also ships a native binary if you'd rather not have Node —
grab it from the
[copilot-language-server releases](https://github.com/github/copilot-language-server-release/releases),
`chmod +x` it, and drop it in `~/.local/bin` (or anywhere on `$PATH`).
Using Copilot requires a Copilot subscription on the GitHub account
you sign in with (the free tier works).

Verify with `copilot-language-server --version` (or `claude-code-acp
--version`, `gemini --version`). If you installed the binary while ced
was already running, see the restart gestures in step 2 — ced
deliberately doesn't re-probe on its own.

### 2. Pick your chat backend

**≡ → Copilot → Chat agent: …** opens the list. Your pick persists as
`"chatagent"` in `~/.config/ced/config.json`.

> The ≡ menu opens with every section folded, so this is a click on the
> **Copilot** header first, then the row. Every AI setting lives in that
> one group.

Re-picking the agent you're already on — it's listed as
*"(current — restart)"* — is the deliberate **retry gesture**: it tears
the connection down, clears the "this agent is unavailable" verdict, and
starts fresh. That's how you recover from a crashed agent, and how you
pick up a binary you installed mid-session. For Copilot specifically,
toggling **≡ → Copilot → Disable/Enable Copilot** does the same thing.

Switching backends keeps your transcript and any pending attachments —
an info line marks where one agent's answers end and the next one's
begin.

### 3. Authenticate

Each agent authenticates its own way, entirely outside ced:

- **Copilot** — **≡ → Copilot → Sign in to GitHub** runs GitHub's
  device flow:
  1. A dialog shows a short device code and the URL
     `github.com/login/device`.
  2. Pick **Yes** — the code is copied to your clipboard and your
     browser opens the URL. (Over SSH the browser can't open on the
     remote box, of course — just visit the URL on your laptop; the
     code stays visible in the status bar the whole time.)
  3. Paste the code in the browser and approve. When GitHub finishes,
     the status bar shows `Copilot ✓` and the menu row flips to
     **Sign out**.

  Credentials are stored by `copilot-language-server` itself and shared
  with Copilot in other editors, so this is once per machine — not per
  project, not per session. The chat panel reads the same credentials.
- **Claude Code** — run `claude` in a terminal and sign in, or export
  `ANTHROPIC_API_KEY`.
- **Gemini** — run `gemini` in a terminal and sign in.

If a handshake fails, the panel writes the protocol error into the
transcript followed by that agent's auth hint — auth is the
overwhelmingly likely cause.

### 4. Inline suggestions (ghost text) — Copilot only

Ghost text comes from Copilot's completions sidecar, which is a
separate process from the chat panel. It's on by default once you're
signed in, regardless of which chat backend you picked. Type, pause for
a beat, and a dimmed suggestion appears at your cursor:

- **`Tab` accepts** the suggestion (with nothing showing, `Tab` is
  plain indentation as always).
- **`Esc` dismisses** it — as does moving the cursor or just typing
  through it.
- Multi-line suggestions show their first line inline plus a `⋯+N`
  marker for the rest; accepting inserts the whole thing (one undo
  step).
- Suggestions work in any text file, not just Go.

Don't want ghost text but still want sign-in and chat? **≡ → Copilot →
Disable inline suggestions** turns off just this feature, persisted
across restarts.

### 5. Chat panel

Open it with **≡ → Copilot → Show … chat** (the row names your current
agent). The panel docks as a full-height strip on the **left** edge, and
the file tree slides over to the right while it's open.

- Type in the composer at the bottom; **`Enter` sends**. Answers stream
  in live. `↑` / `↓` recall previous prompts.
- **Paste into the prompt** with your terminal's normal paste (`Cmd+V`,
  right-click, middle-click) or with `Cmd+V` for text you copied inside
  ced. Either way the text lands at the caret; since the composer is one
  line, a multi-line snippet is flattened — line breaks and tabs become
  single spaces — so the whole paste survives instead of just its first
  line.
- **⏹ stops** an answer mid-turn; **✕** (or the menu toggle) hides the
  panel — the conversation survives hide/show.
- **Model picker**: **≡ → Copilot → Chat model: …** lists the models
  the agent offers, with premium multipliers where it reports them.
  Your pick persists as `"chatmodel"` and is re-applied on every
  reconnect.
- **Copy anything**: click **⧉ copy** under a response to lift that
  answer, **⧉ copy conversation** at the end for the whole transcript
  (also at **≡ → Copilot → Copy chat transcript**), or drag across the
  text and hit `Cmd+C`. `Esc` drops a selection.
- Drag the panel's right-edge splitter to resize; scroll wheel and
  `PgUp` / `PgDn` move through the transcript.
- The chat panel and a **left-docked terminal** share the left edge:
  opening one tucks the other away (a bottom-docked terminal coexists
  fine).

### 6. Sending your code as context

Attachments are **pushed with your prompt**, not fetched — the agent
gets the bytes in the same turn, with no round trip and no permission
prompt interrupting your question.

- **Auto-attach is on by default.** Whatever tab is active rides along
  with your prompt; if you have text selected, the **selection wins**
  (a highlighted region is a narrower question). Toggle it at
  **≡ → Copilot → Enable/Disable auto-attach current file**, persisted
  as `"chatcontext"`.
- **Attach more**: **Attach current file / selection to chat**, or
  **Attach file to chat…** for the fuzzy file picker. Attaching opens
  the panel — context you can't see is context you can't trust.
- Attachments show as **`▤` chips** above the composer. Each chip's ✕
  removes it; the ✕ on an auto-attached chip flips the toggle off.
  **Clear attachments** drops them all.
- Content comes from the **open buffer**, so unsaved edits are what the
  agent sees. Payloads cap at 64 KB, cut on a line boundary, and the cut
  is announced.
- Attachments are **per-turn**: they're listed in a `▤` note in the
  transcript and then cleared. ACP sessions keep history server-side, so
  a sticky attachment would re-send the whole file on every prompt for
  the rest of the session.

### 7. Letting the agent read and write your files

The chat panel is a full ACP client: agents can ask to read and edit
your files, and **you approve each request**.

- A request opens a picker with **the agent's own options** (allow once,
  allow always, reject, …). Your decision is echoed into the transcript
  as `✓ allowed` / `⊘ rejected`, so later answers that reference it make
  sense.
- Dismissing the picker (`Esc` or a click outside) counts as a
  rejection. Requests that are still pending when you press ⏹, switch
  agents, or close the panel are answered as cancelled.
- Requests **queue politely** — a prompt never steals the modal slot
  from a dialog or the open menu; it resurfaces when the slot frees.
- **Reads serve your open buffer** (unsaved edits included) before
  falling back to disk.
- **Writes land on disk**, then the tree refreshes: a clean tab reloads
  itself, a dirty tab warns you. Each write leaves a `✎ wrote <path>`
  receipt in the transcript.
- **Everything is confined to the project root.** A path outside it gets
  an error the agent can read, not silence.

**Read-only chat.** If you'd rather ask questions with no possibility of
an edit landing, **≡ → Copilot → Block agent file changes (read-only
chat)** switches the whole capability off (persisted as `"chatwrite"`).
With it off ced tells the agent at connect time that it cannot write,
refuses its own write path outright, and auto-rejects any tool call the
agent labels as an edit, delete, move, or shell command — no prompt to
mis-click, with a `⊘ rejected (read-only chat)` line in the transcript so
you can see it happen. Reads and searches still ask normally. The
per-request prompt is the primary guard; this is the belt-and-braces
posture for reviewing an unfamiliar repo, or for letting an agent explain
code you don't want it touching.

Blocking takes effect immediately, including for a request already
queued. Re-allowing writes mid-session only updates what the agent was
*told* it can do after you restart it (**Chat agent → current —
restart**) — the transcript says so when it matters.

### 8. MCP servers (more tools for your agent)

[Model Context Protocol](https://modelcontextprotocol.io) servers give an
agent tools beyond your code: an issue tracker, a database, a docs index,
your own scripts. Declare them once and **whichever chat backend you're
running gets them** — this isn't a Copilot feature.

Create `~/.config/ced/mcp.json`. It's the same shape Claude Desktop and
friends use, so you can paste a block you already have:

```json
{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_…"}
    },
    "filesystem": {"command": "mcp-server-filesystem", "args": ["/srv/data"]},
    "parked":     {"command": "old-server", "disabled": true}
  }
}
```

VS Code's `"servers"` spelling of the top-level key works too. Remote
servers take `{"type": "http", "url": "…", "headers": {…}}` instead of a
command.

**What happens with them:**

- **Your chat agent gets them at connect time.** ced hands the enabled
  servers to the agent when the chat session opens; the agent runs its
  own copy of each and calls their tools during a turn. The transcript
  says which servers went out — and names any the agent can't reach
  (some agents don't support remote MCP transports), because a tool
  that's silently missing is the one you can't debug.
- **You can drive them yourself** from **≡ → MCP → MCP servers…**:

  | Row | What it does |
  | --- | --- |
  | `● github (12 tools)` | Status per server — `●` connected, `◌` connecting, `✕` failed with the reason, `○` idle, `·` disabled |
  | **Tools…** | Connects if needed, then lists the server's tools |
  | **Connect / Reconnect / Disconnect** | Manual lifecycle — a crashed server never restarts itself |
  | **Server info** | What was declared, what the server reports about itself, and why the last attempt failed |

  Picking a tool opens a JSON-arguments prompt pre-filled from the tool's
  own schema (a tool that needs no arguments just runs). The result opens
  in a dialog; **Copy last result** puts the untruncated text on your
  clipboard.

Notes:

- **Nothing is spawned until you ask.** Declaring a server doesn't launch
  it — ced starts one only when you connect from the menu, and the chat
  agent starts its own separately.
- ced's own connections are **stdio-only**. Remote (`http`/`sse`) servers
  are still declared to your agent; ced just doesn't dial them itself.
- One server failing is that server's problem: it gets a `✕` row with the
  reason and everything else carries on.
- Edited the file while ced was running? **≡ → MCP → Reload MCP config**.
- `env` values are secrets — ced shows only the **keys** in menu rows, so
  a screenshot of the picker never leaks a token.

### Turning it off

Every switch lives in the `≡ → Copilot` menu and persists to
`~/.config/ced/config.json`:

```json
{
  "copilot": "off",          // never spawn GitHub's binary at all
  "suggestions": "off",      // keep Copilot sign-in and chat, but no ghost text
  "chatcontext": "off",      // don't auto-attach the current file to prompts
  "chatwrite": "off",        // read-only chat: the agent may not change files
  "chatagent": "claude",     // which chat backend: "copilot", "claude", "gemini"
  "chatmodel": "<model-id>"  // preferred model for the chat session
}
```

MCP servers live in their own file (`~/.config/ced/mcp.json`) — delete an
entry, or give it `"disabled": true`, to take it out of play.

The four toggles default to `"on"` — which costs nothing until you
actually install a binary and sign in. Note that `"copilot": "off"` is a
kill switch for **GitHub's binary only**: it stops the completions
sidecar and the Copilot chat backend, but Claude Code and Gemini are
gated purely by their binary being on `$PATH`.

## Themes

ced ships **ten** color themes. Switch with **≡ → Theme** (or type
"theme" into the command palette, `Esc-a`) — the change is instant, no
restart, and it's remembered in `~/.config/ced/config.json`.

| Theme | | Theme | |
|---|---|---|---|
| `tokyo-night` | Tokyo Night — the default | `dark-game` | Neon / synthwave |
| `darcula` | JetBrains Darcula | `dark-city` | Desaturated noir |
| `gruvbox-dark` | Gruvbox Dark | `solarized-light` | Solarized Light ☀ |
| `solarized-dark` | Solarized Dark | `corporate` | Clean light blue/grey ☀ |
| `cool-blue` | Nord | | |
| `super-warm` | Warm amber / rust | | |

Or set it by hand:

```json
{ "theme": "gruvbox-dark" }
```

An unknown name falls back to the default with a one-line explanation —
a color preference can never stop the editor from opening.

### Rolling your own

Theme files live in `~/.config/ced/themes/*.json` (or
`$XDG_CONFIG_HOME/ced/themes/`). **Only eight colors are required** —
everything else is derived:

```json
{
  "colors": {
    "bg":     "#101018",   "fg":   "#e0e0f0",
    "muted":  "#707088",   "line": "#303040",
    "accent": "#78a8f0",
    "ok":     "#88cc70",   "warn": "#e0b060",   "err": "#f07080"
  }
}
```

That's a complete, working theme. The file is named `midnight.json`, so
the theme is called `midnight`. ced fills in the other twenty-seven
colors from those eight: the selection is a wash of `accent` over `bg`,
strings take `ok`, deleted-line marks take `err`, and so on. Any derived
color can be stated explicitly to override it (`"syn-keyword": "#ff5cf5"`,
`"selection": "#372b6b"`, …), and later derivations follow your value.

Optional top-level keys: `"name"` (defaults to the filename),
`"label"` (what the picker shows), and `"dark"` (inferred from the
background's brightness when absent).

### The editing loop

**≡ → Customize theme…** does the tedious part: it copies the theme
you're on — every derived color spelled out, so you can see the whole
board — into `themes/<name>-custom.json`, switches to it, and opens the
file in a tab. Change a hex, hit Save, and the editor repaints
immediately. That save-to-preview loop is deliberately the whole
customization UI; ced has no settings dialog and isn't getting one.

Naming your file after a built-in (`tokyo-night.json`) **replaces** it
in the picker, in place — which is how you tweak a shipped theme without
adding a near-duplicate row next to it. Editing themes from outside ced?
**≡ → Reload themes**. A broken theme file costs you that theme and
nothing else: you get one warning, and the rest of the registry loads.

## Project layout

```
.
├── main.go                   # Entry point — parses optional rootDir arg
├── internal/
│   ├── app/                  # Event loop, layout, menu modal, splitter
│   ├── editor/               # Buffer, tab, cursor, syntax highlighting
│   ├── filetree/             # Lazy directory tree with identity-preserving refresh
│   ├── lsp/                  # JSON-RPC client (gopls, Copilot sidecar, ACP chat, MCP)
│   ├── mcp/                  # MCP inventory (mcp.json) + stdio client
│   ├── clipboard/            # OSC 52 clipboard with tmux passthrough
│   ├── customactions/        # Loader for ~/.config/ced/actions.json
│   ├── format/               # Format-on-save config + trust store
│   ├── finder/               # Project file index + fuzzy matcher
│   ├── theme/                # Named themes: palette derivation + registry
│   └── version/              # Single-line version constant
├── .github/workflows/        # Auto-release pipeline
├── .goreleaser.yml           # Cross-compile + brew formula config
├── Formula/                  # Homebrew formula (written by CI)
└── Makefile
```

## Development

```sh
make run          # build and run against the current directory
make build        # build to ./bin/ced
make build-linux  # cross-compile a linux/amd64 binary
make test         # full suite with -race (same as CI)
make test-short   # quick iteration loop (-short, no race)
make coverage     # writes coverage.out + a browsable coverage.html
make tidy         # go mod tidy
make clean        # rm -rf bin + coverage artifacts
```

Every push and PR runs `go test ./...` on Linux + macOS via
[`.github/workflows/test.yml`](.github/workflows/test.yml). New code
needs a corresponding `_test.go` — see CLAUDE.md for the bar.

## Releases

Releases are fully automated. Every push to `main`:

1. Reads `internal/version/version.go`.
2. If that file was hand-edited in the pushed commit, the version is
   used as-is (this is how you bump major or minor: edit the constant
   manually). Otherwise the patch number is auto-bumped and committed
   back to `main` with `[skip ci]`.
3. Tags `v<x.y.z>` and pushes the tag.
4. [GoReleaser](https://goreleaser.com/) cross-compiles for
   linux/darwin × amd64/arm64, attaches archives to a GitHub
   Release, and pushes an updated formula into `Formula/ced.rb`
   on this same repo.

No PAT, no separate tap repo — the default workflow `GITHUB_TOKEN` is
enough since the formula lives in the source repo.

## License

MIT — see [LICENSE](LICENSE).

Copyright © 2026 Cloudmanic, LLC.
