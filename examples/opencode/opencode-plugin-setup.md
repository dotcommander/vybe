# OpenCode Vibe Bridge Setup

Goal: install the minimal OpenCode bridge with the least number of steps.

## Fast Path (Recommended)

```bash
vibe hook install --opencode
```

This writes:

- `~/.config/opencode/plugins/vibe-bridge.js`

This enables:

- `session.created` -> `vibe resume` (project-scoped)
- `todo.updated` -> `todo_snapshot` event
- system prompt injection with cached `Vibe Resume Context`

## Manual Setup (If Needed)

### Option A: Project-local plugin

```bash
mkdir -p .opencode/plugins
cp examples/opencode/opencode-vibe-plugin.ts .opencode/plugins/vibe-bridge.ts
```

Create `.opencode/package.json` if missing:

```json
{
  "dependencies": {
    "@opencode-ai/plugin": "latest"
  }
}
```

### Option B: Global plugin

```bash
mkdir -p ~/.config/opencode/plugins
cp examples/opencode/opencode-vibe-plugin.ts ~/.config/opencode/plugins/vibe-bridge.ts
```

Create `~/.config/opencode/package.json` if missing:

```json
{
  "dependencies": {
    "@opencode-ai/plugin": "latest"
  }
}
```

## Environment

Ensure `vibe` is available:

```bash
command -v vibe
```

Optional stable identity:

```bash
export VIBE_AGENT=opencode-main
```

## Reload

Restart OpenCode so plugins load and dependencies install.

## Verify

1. Start a new OpenCode session (`session.created` fires).
2. Update todos in-session (`todo.updated` fires).
3. Check events:

```bash
vibe events list --agent "${VIBE_AGENT:-opencode-main}" --limit 30
```

Expected:

- `todo_snapshot` events exist
- next OpenCode session can reference prior context
