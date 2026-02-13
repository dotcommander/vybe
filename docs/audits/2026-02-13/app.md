# Audit: internal/app/

**Date:** 2026-02-13
**Scope:** internal/app/*.go (6 files, ~238 lines production + ~196 lines test)
**Mode:** Comprehensive (direct analysis — package too small for scout deployment)

---

## Executive Summary

**Critical Issues:** 0
**High Issues:** 0
**Total Findings:** 0 (above threshold)

This package is clean. Three small, focused files handling config resolution, DB path precedence, and settings loading. Good test coverage, proper synchronization, reasonable error handling.

---

## What Was Checked

| Area | Result |
|------|--------|
| **Flow** | Config resolution follows documented 4-level precedence. Entry points are clear (`GetDBPath`, `LoadSettings`, `ConfigDir`). |
| **Query** | No database operations — pure filesystem/config reads. |
| **Concurrency** | `sync.Once` for settings cache, `sync.RWMutex` for DB path override. Both correct. |
| **Performance** | Single-read config loading, no hot paths. |
| **Security** | File permissions: config.yaml created with 0600, directories with 0755. No untrusted input paths (all user-controlled via CLI/env/config). |
| **Test coverage** | 11 tests cover: precedence order, fallback chain, invalid YAML, directory creation, idempotent config creation. |

---

## Observations (Below Threshold — Informational Only)

### 1. Duplicated Config Lookup Order (Score: 6)

`GetDBPath()` (db.go:17) uses cached `LoadSettings()`, while `ResolveDBPathDetailed()` (db.go:43) manually iterates config paths via `loadSettingsFile()`. Comment at db.go:59 says "Config file order must match LoadSettings" but no structural enforcement. Low-risk since both are in the same file and one is debug-only.

### 2. sync.Once Caches Errors Permanently (Score: 4)

`LoadSettings()` uses `sync.Once` — if first call fails (e.g., malformed YAML), all subsequent calls return the cached error. Appropriate for a CLI tool (short-lived process) but would be a bug in a long-lived daemon.

### 3. Tests Use os.Chdir (Score: 3)

`settings_test.go:19-22` changes working directory to test local config lookup. This mutates global state. Tests use `t.Cleanup()` correctly but could cause flakes if tests run in parallel. Currently safe since tests reset via `resetSettingsStateForTest()`.

---

## No Tasks Created

No findings met the Critical (15+) or High (10-14) threshold.
