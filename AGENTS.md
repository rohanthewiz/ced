# Repository Guidelines

## Project Structure & Module Organization

ced ("Cats Editor") is a Go terminal editor module at `github.com/rohanthewiz/ced`; the CLI entry point is `main.go` and the binary is `ced`. Core packages live under `internal/`: `app` owns the event loop and rendering, `editor` owns buffers/tabs/editing behavior, `filetree` manages the sidebar tree, and supporting packages cover clipboard, formatting, icons, config, theme, finder, versioning, and the JSON-RPC clients (`lsp` for gopls/ACP, `mcp` for Model Context Protocol servers). Tests sit beside source files as `*_test.go`. Release packaging includes `Formula/ced.rb`, `install.sh`, and samples under `samples/`. `website/` holds the inherited SpiceEdit Hugo site — dormant since the ced rebrand and no longer built or deployed; leave it alone.

## Build, Test, and Development Commands

- `make run`: run the editor in the current directory with `go run .`.
- `make build`: compile `./bin/ced`.
- `make build-linux`: cross-compile a static `linux/amd64` binary.
- `make test`: run `go test -race ./...`; use before PRs.
- `make test-short`: quick `go test -short ./...` loop while iterating.
- `make coverage`: write `coverage.out` and `coverage.html`.
- `make tidy`: sync `go.mod` and `go.sum`.

## Coding Style & Naming Conventions

Use `gofmt`/`go test` defaults and idiomatic Go names: exported identifiers in `CamelCase`, unexported in `camelCase`, package names short and lowercase. New Go source files should follow the existing header block style. Keep short doc comments above functions, including private helpers, explaining intent. Avoid adding `Ctrl+` shortcuts; editor actions must stay reachable from the main `≡` menu because SSH/tmux workflows may swallow shortcuts or right-click events.

## Testing Guidelines

Every non-trivial source file should have a same-package test file, for example `internal/editor/buffer.go` and `internal/editor/buffer_test.go`. Add regression tests for bug fixes and cover happy paths and obvious failures. Use `t.TempDir()` for filesystem state. For drawing tests, use `tcell.NewSimulationScreen("UTF-8")` and assert screen contents.

## Commit & Pull Request Guidelines

Recent commits use concise, imperative summaries, often with PR numbers, such as `Mute dotfiles in tree + per-tab Nerd Font icons (#32)`. Release automation uses `[skip ci]`; preserve that marker when editing generated release commits or workflows. PRs should describe behavior changes, mention tests run, link issues, and include screenshots or terminal captures for UI changes.

## Security & Configuration Tips

Format-on-save commands are project config and require trust prompts; do not bypass that flow. `~/.config/ced/mcp.json` holds MCP server commands and their credentials: never echo an entry's `env` values into the UI, logs, or tests — surface the keys only. Keep generated artifacts (`bin/`, `coverage.out`, `coverage.html`) out of normal feature commits unless the release workflow explicitly requires them.
