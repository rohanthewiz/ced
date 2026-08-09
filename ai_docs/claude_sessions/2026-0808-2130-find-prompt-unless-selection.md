# Session: find asks, unless you selected it

Session ID: `3ff6e24a-0018-4d45-bad0-efa5cf3f8887`
Date: 2026-08-08
Branch: `main`
Parent commit: `84d9cce`

---

## The ask

One sentence, and it was a policy statement rather than a feature request:

> For every find feature, unless there is an active selection within the
> editor, give the user an opportunity to enter the search pattern.

So this session changed no geometry, no protocol, no panel — it changed what
a find verb is allowed to assume it already knows.

---

## What was there

`findAllSeedQuery` (findall.go) was a four-rung ladder, shared by the two
list-based find verbs:

```
find bar text → single-line selection → word under the cursor → prompt
```

The third rung is the one the ask is about. `Esc F` with a bare cursor
searched for whatever word the caret happened to be sitting in; `Esc P` did
the same across the whole tree. Neither asked. The prompt was only reached
when the ladder came up EMPTY — a cursor in whitespace.

---

## What changed

The ladder was split at the line between "the user said this" and "the editor
inferred this":

| Function | What it is |
|---|---|
| `findAllSelectionQuery()` | the ONE silent path — a single-line selection |
| `findAllPromptSeed()` | the prompt's PRE-FILL — find bar first, then the word under the cursor |

Both entry points now read: selection → run it; anything else → prompt,
pre-filled.

| File | What |
|---|---|
| `internal/app/findall.go` | the split; `openFindAll` prompts with a seed |
| `internal/app/projectsearch.go` | `menuFindInProject`, same shape |
| `internal/app/findall_test.go` | old ladder test replaced with three |
| `internal/app/projectsearch_test.go` | the project-side twin |
| `CLAUDE.md` | both seeding bullets rewritten |

`go test ./...` green.

---

## The argument that decided the shape

A guess and a selection are not the same kind of input, and the difference
shows up in what a wrong one COSTS.

A selection is the user pointing at the exact text. A prompt there could only
ever be answered "yes, that" — a keystroke charged for nothing. So it runs.

A word under a bare cursor is an implication. And a result list is
**indistinguishable from a correct answer to the wrong query**: the panel
looks identical either way, and the user reads it as the answer to the
question they meant to ask. That asymmetry is the whole justification —
nothing is lost by asking, and what's lost by not asking is silent.

The pre-fill is what makes this cost nothing: Enter accepts the guess, any
other key replaces it. The old behavior is still one keystroke away; it just
isn't the default any more.

---

## Three decisions inside that

**The find bar's text goes in the PRE-FILL, not the silent path.** Strictly,
what's in the bar is text the user typed, so an argument exists for running
it. But the rule as stated says "unless there is an active selection", and
holding to it literally costs one Enter while keeping the rule explainable in
one sentence. A rule with an exception nobody can predict is worse than a
rule that occasionally asks a question with the answer already filled in.

**The bar's ↓ gesture is untouched.** `openFindAllFromBar` still opens the
list straight from what's typed. That gesture IS the user having just typed
it, in the surface built for typing it — re-asking there would be the
keystroke-for-nothing case again.

**Find references (Esc-R) is not in scope, and shouldn't be.** It resolves a
symbol by POSITION, not by text — that's exactly what separates it from a
textual replace-all (the same argument rename's house rules make about the
old name never going on the wire). There is nothing to type there that would
mean the same thing, and a prompt would misrepresent the verb as a text
search.

---

## One implementation trap worth remembering

`openModal` → `closeAllModals` **wipes `findField`**. So the seed has to be
read BEFORE `openPrompt` is called, or the find bar's contribution vanishes
in the act of opening the box it was meant to fill.

Go's argument evaluation order already guarantees this for the inline call,
but both sites capture into a local with a comment anyway — the dependency is
invisible at the call site, and a later refactor that hoists the prompt would
break it silently and in a way no test shape naturally catches.

---

## Tests

The old `TestFindAll_SeedQueryPrefersBarThenSelectionThenWord` pinned the
ladder, so it had to go. Four in its place:

- `TestFindAll_SelectionQueryIsTheOnlySilentSeed` — bare cursor: nothing;
  single-line selection: the text; multi-line selection: nothing (FindAll
  matches within a line, so a blob with newlines isn't a search term)
- `TestFindAll_PromptSeedPrefersBarThenWord` — the pre-fill's own order
- `TestFindAll_AsksWhenNothingIsSelected` — the headline rule, asserting the
  prompt opens AND carries the word
- `TestFindInProject_AsksUnlessSomethingIsSelected` — the project twin, both
  directions in one test

`TestFindAll_SeedFallsBackToPrompt` survived, tightened to also assert the
prompt opens BLANK when the cursor is in whitespace.

---

## Not done

- The ≡ rows still read "Find all in file" / "Find in project" without an
  ellipsis. Menu convention ("Go to line…") uses `…` for a row that opens a
  dialog, and these now usually do — but not always, so the label would be
  lying in the selection case either way. Left alone deliberately.
- `Esc f` / `Esc e` still open the bar EMPTY even when there's a selection.
  Seeding the bar from a selection is a plausible next step and is the
  inverse of this session's rule (the bar is already the ask, so the
  question there is convenience, not correctness). Not requested.
