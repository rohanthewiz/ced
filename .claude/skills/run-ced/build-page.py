#!/usr/bin/env python3
# =============================================================================
# File: .claude/skills/run-ced/build-page.py
# Author: Rohan Allison <rohanthewiz@gmail.com>
# Created: 2026-07-31
# Copyright: 2026 Rohan Allison. All rights reserved.
# =============================================================================
"""Capture every built-in theme and assemble one comparison page.

    python3 .claude/skills/run-ced/build-page.py [out.html]

Runs ced once per theme through capture/ (a real PTY each time), pulls
the resolved palettes out of the theme package itself, and emits a
self-contained page ready to publish as an artifact.

Nothing here is transcribed by hand: the swatch colors come from
`theme.Normalize`, the authored-vs-derived counts are parsed out of
builtin.go, and every frame is bytes ced actually painted.

Design notes for anyone editing the page: the chrome is a deliberately
cool-biased neutral so it never fights ten palettes — ALL saturated color
comes from the themes, each card borrowing its own accent as a local
--tint. Type is mono-first (the subject is a terminal editor) with system
sans for prose. Layout is a sticky chip nav, a short intro at reading
width, then one full-bleed card per theme.
"""
import json
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CAPTURE_SRC = os.path.join(HERE, "capture")

CORE_ORDER = ["bg", "fg", "muted", "line", "accent", "ok", "warn", "err"]
DERIVED_SHOWN = ["sidebar-bg", "selection", "accent-soft", "syn-keyword",
                 "syn-string", "syn-function", "syn-type", "syn-comment"]

# A tiny throwaway test is the honest way to read the registry: it runs
# inside the module, so the palettes come from Normalize rather than from
# a copy of the table that could drift.
PALETTE_PROBE = '''package theme

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestZZProbe(t *testing.T) {
	type row struct {
		Name, Label string
		Dark        bool
		Core        map[string]string
		Derived     map[string]string
	}
	var out []row
	for _, s := range Builtins() {
		p, err := Normalize(s.Colors)
		if err != nil {
			t.Fatal(err)
		}
		core := map[string]string{}
		for _, k := range CoreKeys() {
			core[k] = p[k]
		}
		der := map[string]string{}
		for _, k := range %s {
			der[k] = p[k]
		}
		out = append(out, row{s.Name, s.Label, s.Dark, core, der})
	}
	b, _ := json.Marshal(out)
	fmt.Println("PROBE" + string(b) + "EBORP")
}
'''


def sh(cmd, **kw):
    """Run a command, echoing it, and fail loudly."""
    print("+", " ".join(cmd), file=sys.stderr)
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kw)


def read_palettes():
    """Resolve every built-in palette by asking the theme package."""
    keys = "[]string{" + ", ".join('"%s"' % k for k in DERIVED_SHOWN) + "}"
    probe = os.path.join(ROOT, "internal", "theme", "zz_probe_test.go")
    with open(probe, "w") as f:
        f.write(PALETTE_PROBE % keys)
    try:
        r = sh(["go", "test", "./internal/theme/", "-run", "TestZZProbe", "-v"], cwd=ROOT)
    finally:
        os.remove(probe)
    m = re.search(r"PROBE(.*?)EBORP", r.stdout, re.S)
    if not m:
        sys.exit("could not read palettes from the theme package")
    return json.loads(m.group(1))


def read_authored():
    """Parse which keys each built-in actually STATED, from the source."""
    src = open(os.path.join(ROOT, "internal", "theme", "builtin.go")).read()
    out = {}
    pat = r'Name:\s*(?:DefaultName|"([a-z-]+)"), Label:.*?Colors: Palette\{(.*?)\n\t\t\t\},'
    for m in re.finditer(pat, src, re.S):
        out[m.group(1) or "tokyo-night"] = set(re.findall(r'"([a-z-]+)":\s*"#', m.group(2)))
    return out


def swatches(pal, keys, authored):
    """Render one swatch row; keys not in `authored` are marked derived."""
    out = []
    for k in keys:
        hexv = pal[k]
        cls, title = "sw", k
        if k not in authored:
            cls, title = "sw sw-derived", k + " (derived)"
        out.append(f'<div class="{cls}" title="{title} {hexv}">'
                   f'<span class="chip" style="background:{hexv}"></span>'
                   f'<span class="swk">{k}</span><span class="swv">{hexv}</span></div>')
    return "".join(out)


CSS = """
:root{
  --ink:#14151a; --paper:#f6f7f9;
  --bg:var(--paper); --fg:#191b21; --muted:#6d7280; --rule:#e0e2e9;
  --panel:#ffffff; --panel-2:#eef0f4; --fs:11px;
  --mono:ui-monospace,"SF Mono",SFMono-Regular,Menlo,Consolas,monospace;
  --sans:ui-sans-serif,system-ui,-apple-system,"Helvetica Neue",Arial,sans-serif;
}
@media (prefers-color-scheme:dark){
  :root{ --bg:var(--ink); --fg:#e6e8ef; --muted:#8b909e; --rule:#262932;
         --panel:#1b1d24; --panel-2:#22252e; }
}
:root[data-theme="dark"]{ --bg:var(--ink); --fg:#e6e8ef; --muted:#8b909e; --rule:#262932;
                          --panel:#1b1d24; --panel-2:#22252e; }
:root[data-theme="light"]{ --bg:var(--paper); --fg:#191b21; --muted:#6d7280; --rule:#e0e2e9;
                           --panel:#ffffff; --panel-2:#eef0f4; }
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font-family:var(--sans);
     -webkit-font-smoothing:antialiased}
.wrap{max-width:1180px;margin:0 auto;padding:0 20px}
.top{padding:56px 0 28px}
.eyebrow{font-family:var(--mono);font-size:11px;letter-spacing:.14em;
         text-transform:uppercase;color:var(--muted);margin:0 0 14px}
h1{font-family:var(--mono);font-size:clamp(26px,4.2vw,42px);font-weight:600;
   letter-spacing:-.02em;margin:0 0 16px;text-wrap:balance}
.lede{max-width:62ch;font-size:16px;line-height:1.62;margin:0 0 10px}
.lede + .lede{color:var(--muted);font-size:15px}
.lede code{font-family:var(--mono);font-size:.88em;background:var(--panel-2);
           padding:1px 5px;border-radius:4px}
.nav{position:sticky;top:0;z-index:10;background:color-mix(in srgb,var(--bg) 92%,transparent);
     backdrop-filter:blur(10px);border-bottom:1px solid var(--rule);padding:10px 0}
.nav-in{display:flex;gap:6px;overflow-x:auto;scrollbar-width:thin;padding:0 20px;
        max-width:1180px;margin:0 auto}
.chip-link{display:inline-flex;align-items:center;gap:7px;white-space:nowrap;
  font-family:var(--mono);font-size:11.5px;color:var(--muted);text-decoration:none;
  padding:6px 11px;border-radius:999px;border:1px solid transparent;transition:.15s}
.chip-link:hover,.chip-link:focus-visible{color:var(--fg);border-color:var(--tint);
  background:color-mix(in srgb,var(--tint) 12%,transparent);outline:none}
.dot{width:8px;height:8px;border-radius:50%;flex:none}
.tools{display:flex;gap:8px;align-items:center;margin:22px 0 8px;flex-wrap:wrap}
.btn{font-family:var(--mono);font-size:11.5px;color:var(--muted);background:var(--panel);
  border:1px solid var(--rule);border-radius:7px;padding:6px 12px;cursor:pointer;transition:.15s}
.btn:hover,.btn:focus-visible{color:var(--fg);border-color:var(--muted);outline:none}
.btn[aria-pressed="true"]{color:var(--fg);border-color:var(--fg)}
.hint{font-family:var(--mono);font-size:11px;color:var(--muted)}
.card{margin:30px 0 0;border:1px solid var(--rule);border-radius:12px;overflow:hidden;
      background:var(--panel);scroll-margin-top:60px}
.rail{padding:16px 18px 14px;border-bottom:1px solid var(--rule);
      background:linear-gradient(180deg,color-mix(in srgb,var(--tint) 8%,var(--panel)),var(--panel))}
.rail-id{display:flex;align-items:baseline;gap:11px;flex-wrap:wrap;margin-bottom:13px}
.rail-id h2{font-family:var(--mono);font-size:17px;font-weight:600;margin:0;letter-spacing:-.01em}
.tid{font-family:var(--mono);font-size:11.5px;color:var(--muted);
     background:var(--panel-2);padding:2px 7px;border-radius:5px}
.kind{font-family:var(--mono);font-size:10px;letter-spacing:.1em;text-transform:uppercase;
      padding:2px 8px;border-radius:999px;border:1px solid var(--tint);color:var(--tint)}
.stated{font-family:var(--mono);font-size:11px;color:var(--muted);margin-left:auto}
.sws{display:grid;grid-template-columns:repeat(auto-fill,minmax(132px,1fr));gap:2px 10px}
.sws-sub{margin-top:8px;padding-top:8px;border-top:1px dashed var(--rule);opacity:.82}
.sw{display:flex;align-items:center;gap:7px;padding:3px 0;font-family:var(--mono);font-size:10.5px}
.chip{width:13px;height:13px;border-radius:3px;flex:none;
      box-shadow:inset 0 0 0 1px rgba(128,128,128,.35)}
.swk{color:var(--fg);min-width:74px}
.swv{color:var(--muted);font-variant-numeric:tabular-nums}
.sw-derived .swk{color:var(--muted)}
.sw-derived .swk::after{content:"~";margin-left:3px;opacity:.6}
.well{overflow-x:auto;background:var(--card-bg)}
.scr{font-family:var(--mono);font-size:var(--fs);line-height:1.28;margin:0;
     padding:12px 14px;white-space:pre;display:inline-block;min-width:100%;
     font-variant-ligatures:none;tab-size:4}
footer{border-top:1px solid var(--rule);margin-top:44px;padding:26px 0 60px;
       font-size:13.5px;color:var(--muted);line-height:1.6}
footer code{font-family:var(--mono);font-size:.9em}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
"""

JS = """
const root=document.documentElement;
document.querySelectorAll('[data-fs]').forEach(b=>b.addEventListener('click',()=>{
  document.querySelectorAll('[data-fs]').forEach(o=>o.setAttribute('aria-pressed','false'));
  b.setAttribute('aria-pressed','true');
  root.style.setProperty('--fs',b.dataset.fs);
}));
"""


def main():
    out_path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(tempfile.gettempdir(), "themes.html")

    if not os.path.exists(os.path.join(ROOT, "bin", "ced")):
        sh(["make", "build"], cwd=ROOT)
    cap = os.path.join(tempfile.gettempdir(), "ced-capture-bin")
    env = dict(os.environ, GOFLAGS="-mod=mod", GOPROXY="off")
    subprocess.run(["go", "build", "-o", cap, "."], cwd=CAPTURE_SRC, env=env, check=True)

    palettes = read_palettes()
    authored = read_authored()

    demo = tempfile.mkdtemp(prefix="ced-demo-")
    with open(os.path.join(demo, "main.go"), "w") as f:
        f.write(SAMPLE_GO)
    os.makedirs(os.path.join(demo, "internal"), exist_ok=True)
    with open(os.path.join(demo, "README.md"), "w") as f:
        f.write("# Demo\n")

    frames, nav, cards = {}, [], []
    for p in palettes:
        name = p["Name"]
        html_path = os.path.join(tempfile.gettempdir(), "ced-shot-%s.html" % name)
        subprocess.run([cap, "-bin", os.path.join(ROOT, "bin", "ced"), "-dir", demo,
                        "-theme", name, "-out", html_path, "-fragment"], check=True)
        frames[name] = open(html_path).read()
        print("captured", name, file=sys.stderr)

    for p in palettes:
        name, label, core = p["Name"], p["Label"], p["Core"]
        auth = authored.get(name, set())
        kind = "dark" if p["Dark"] else "light"
        nav.append(f'<a class="chip-link" href="#{name}" style="--tint:{core["accent"]}">'
                   f'<span class="dot" style="background:{core["accent"]}"></span>{label}</a>')
        cards.append(f"""
<section class="card" id="{name}" style="--tint:{core['accent']};--card-bg:{core['bg']}">
  <header class="rail">
    <div class="rail-id">
      <h2>{label}</h2>
      <code class="tid">{name}</code>
      <span class="kind kind-{kind}">{kind}</span>
      <span class="stated">{len(auth)} of 35 keys authored &middot; {35 - len(auth)} derived</span>
    </div>
    <div class="sws">{swatches(core, CORE_ORDER, auth)}</div>
    <div class="sws sws-sub">{swatches(p['Derived'], DERIVED_SHOWN, auth)}</div>
  </header>
  <div class="well">{frames[name]}</div>
</section>""")

    page = f"""<title>ced &mdash; ten themes</title>
<style>{CSS}</style>
<nav class="nav"><div class="nav-in">{''.join(nav)}</div></nav>
<div class="wrap">
  <div class="top">
    <p class="eyebrow">ced &middot; real captures, not mock-ups</p>
    <h1>Ten themes, painted by the editor itself</h1>
    <p class="lede">Every frame below is ced running on a pseudo-terminal at 150&times;44,
      driven through <code>Esc-p</code> &rarr; open <code>main.go</code>, with the ANSI it
      emitted replayed into a grid. The colors are the bytes the editor actually wrote.</p>
    <p class="lede">Each theme states only the keys it needs; the rest come from the
      derivation table. Swatch names marked <code>~</code> were derived, not authored.</p>
    <div class="tools">
      <span class="hint">frame size</span>
      <button class="btn" data-fs="9px" aria-pressed="false">small</button>
      <button class="btn" data-fs="11px" aria-pressed="true">default</button>
      <button class="btn" data-fs="14px" aria-pressed="false">large</button>
      <span class="hint">&mdash; frames scroll sideways at 150 columns</span>
    </div>
  </div>
  {''.join(cards)}
  <footer>
    Switch with <code>&equiv; &rarr; Theme</code> (or type &ldquo;theme&rdquo; into the
    command palette, <code>Esc-a</code>) &mdash; instant, no restart, remembered in
    <code>~/.config/ced/config.json</code>. Roll your own with eight colors in
    <code>~/.config/ced/themes/*.json</code>.
  </footer>
</div>
<script>{JS}</script>
"""
    with open(out_path, "w") as f:
        f.write(page)
    print("wrote", out_path, len(page), "bytes", file=sys.stderr)
    print(out_path)


SAMPLE_GO = '''// Package main demonstrates ced's syntax highlighting.
package main

import (
\t"fmt"
\t"strings"
)

const Greeting = "hello, themes"

type Palette struct {
\tName  string
\tDark  bool
\tCount int
}

// Render prints the palette summary.
func (p *Palette) Render(w int) error {
\tif w <= 0 {
\t\treturn fmt.Errorf("bad width %d", w)
\t}
\tlabel := strings.ToUpper(p.Name)
\tfor i := 0; i < p.Count; i++ {
\t\tfmt.Printf("%-12s %v %3d\\n", label, p.Dark, i*2+1)
\t}
\treturn nil
}

func main() {
\tp := &Palette{Name: Greeting, Dark: true, Count: 10}
\t_ = p.Render(80) // draw it
}
'''

if __name__ == "__main__":
    main()
