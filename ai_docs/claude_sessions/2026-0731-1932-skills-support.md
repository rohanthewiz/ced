# Session: Skill support — the SKILL.md folders you already have

Session ID: 32f5c26b-db23-4fcb-8166-09ade9167408
Date: 2026-07-31

### Ask

> "Add skill support to CEd. Look in the standard places for skills
> particularly the global `.claude/skills` folder"

### The shape of it

A skill is a directory holding a `SKILL.md` whose YAML frontmatter names
and describes it — the format Claude Code and friends already use. The
question wasn't "what is a skill", it was **what can ced do with one**.

ced is not a model. It cannot read a description and decide a skill
applies, which is exactly what makes skills work in an agent harness. So
auto-selection was off the table from the start, and what's left is the
useful half: **ced discovers skills and pushes a chosen one to the chat
agent.** That maps onto machinery that already exists — the per-turn
context attachments — rather than needing anything new.

Read as-is from three directories, in increasing precedence:

```
~/.config/ced/skills          ced's own (new, via userconfig.SkillsDir)
~/.claude/skills              personal — the one the ask named
<project>/.claude/skills      checked in beside the code
```

Nobody should have to duplicate a folder to use it here, so ced reads the
`.claude` directories it doesn't own. Same-named skills **shadow** (later
wins, one row) — the theme registry's rule, and the whole reason a
project directory is worth scanning.

### What shipped

**`internal/skills/skills.go`** — the inventory. `Registry([]Dir)` scans,
merges, shadows, sorts by name. `LoadFile` reads one `SKILL.md`;
`parseFrontmatter` is a hand-rolled subset (quoted scalars, `|`/`>`
block scalars, wrapped continuations, nested structures skipped) because
pulling in a YAML dependency to read two string keys would be a bad trade
for a project whose whole shape is "one static binary, few deps". A file
with no frontmatter still loads, named by its directory; only an
unreadable file is an error, and it costs that one skill.

**`internal/app/skills.go`** — the editor half: state, the three ≡ rows,
the pickers, and `attachSkill`.

**A skill IS a `chatAttach`.** Two fields added to the existing struct
(`skill`, `skillDir`) and nothing else changes: same chips, same ✕, same
per-turn consumption, same 64 KB cap, same embedded-`resource` /
fenced-block wire formats. The two behaviors that ride on the fields:

- **Label by name** (`skill: run-ced`). A personal skill lives outside
  the project, so `relativePathFor` would render a chip full of `../../`
  for the entry whose identity is one word.
- **`chatSkillDirective` leads the TEXT block.** Without it a `SKILL.md`
  reads as "here is a document"; with it the agent knows the text is a
  procedure to follow. It goes in the text block because that's the one
  part of the payload the agent reads in BOTH wire shapes. It also names
  the skill's **directory** — ced ships only the markdown (a skill folder
  can be megabytes), so naming the directory keeps the scripts and
  references beside it reachable by an agent with fs access.

**≡ → Skills** (its own group next to MCP — both are inventories handed
to whichever chat backend is running, not Copilot features):

| Row | Behavior |
| --- | --- |
| `Use skill in chat… (6)` | Picker, name + scope + description; the pick attaches to the next message |
| `Open skill…` | Opens a `SKILL.md` in a tab |
| `Reload skills` | Rescans, reports the count — the one surface that says why a file failed |

Both pickers rescan first, so a skill written moments ago in ced is
already in the list (the theme feature's save-to-preview loop, minus the
save hook). An empty inventory opens the setup help instead of a picker
with no rows — "where do skills go?" is the question a user with none is
actually asking, so the help names all three directories that were
searched.

### Verified in the real binary

Via the `run-ced` PTY-capture skill from the previous session — which
also made it the first skill this feature was pointed at:

- Command palette filtered to `skill` shows all three rows, with
  **`Use skill in chat… (6)`** — five personal skills plus this repo's
  `run-ced`.
- The picker lists them with scope and description:
  `run-ced (project) — Launch and drive the real ced binary…` beside
  `btypedb-embedded-kv-store (user) — …`.
- Picking `run-ced` with a chat backend enabled opens the panel and
  paints **`▤ skill: run-ced ✕`** above the composer, with
  *"Attached skill: run-ced to the next chat message"* in the status bar.
- The ≡ menu shows the `▸ Skills` fold header between MCP and File.

### Tests

12 in `internal/skills` (frontmatter shapes, precedence shadowing, silent
missing dirs, clipping), 12 in `internal/app` (dir order, label counts,
setup help, picker → attach, rescan, open, reload + error flash, the
directive in both wire shapes, per-turn consumption, duplicate refusal),
plus a `SkillsDir` pin in `userconfig`. `newTestApp` points
`skillsUserDirFn` / `skillsCedDirFn` at temp dirs so no test can read the
developer's real skills — a picker assertion must not pass or fail
depending on whose machine ran it.

Menu geometry pins moved to 86 rows / height 92 / dividers `[2, 5, 89]`.
CLAUDE.md's copy of those numbers was **already stale by 3 rows** before
this change; corrected to the current truth.

### Not done (deliberate)

- **No auto-selection, and no skill index on every prompt.** ced can't
  judge relevance, and a per-turn index would be a cost with nothing on
  screen to explain it. The chip is the cost, and it's visible before
  Enter.
- **Nothing is executed.** A `SKILL.md` is markdown handed to the agent.
  That's what keeps these directories on the right side of the
  no-plugin-system line — noted in CLAUDE.md next to the themes-are-data
  argument.
- **No supporting-file bundling.** The directive names the directory; the
  agent's own fs access reads the rest.
- **No config toggle.** An empty skills folder is an empty list.
- **No declaration at `session/new`.** Unlike MCP there's no ACP slot for
  it, and the Claude Code backend already reads `~/.claude/skills`
  itself — declaring would duplicate.
