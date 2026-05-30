# Changelog

## Unreleased

### Added
- `vybe init` — idempotent one-step onboarding: creates config dir, initializes the database, installs hooks, and writes `default_agent: claude` so `--agent` is not required in single-agent setups
- `vybe doctor` — read-only health check: verifies binary on PATH, hooks installed in settings, DB reachable, config dir present; exits 0 always, reports `healthy` in JSON
- `vybe hook export` — emits the current hook manifest (`~/.config/vybe/hooks.json`) as JSON; use `--default` to print built-in defaults without reading the file

### Changed
- Hook registry externalized to `~/.config/vybe/hooks.json` (editable; written by `vybe init`; read by `vybe hook install`)
- Config dir resolution now respects `XDG_CONFIG_HOME` (falls back to `~/.config/vybe` when unset — no change on macOS)
- `CLAUDE_SETTINGS_PATH` environment variable overrides the Claude settings file path for hook install (useful in CI and multi-home-dir environments)
- `default_agent: claude` is now written as an active (uncommented) line in fresh config files; `vybe init` back-fills it for existing installs where the field is absent

### Removed
- Per-turn task reminder from the UserPromptSubmit hook — the hook now only logs the `user_prompt` event and emits a rich brief on explicit trigger words (`brief me`, `status`, etc.); the model receives the full brief at session start via SessionStart and on demand via trigger words

## v1.12.0 (2026-05-29)

### Features
- update task command behavior

### Fixes
- reap timed out child processes reliably

### Other
- centralize failure-reason construction and drain SIGKILL goroutine
- update tests
