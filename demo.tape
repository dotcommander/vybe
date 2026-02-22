# vybe — install, wire, check, remove
# https://github.com/dotcommander/vybe

Output demo.gif

Require echo
Require printf

# --- Display settings ---
Set Theme "Catppuccin Mocha"
Set FontSize 22
Set Width 1000
Set Height 500
Set TypingSpeed 35ms
Set Padding 25
Set CursorBlink false
Set Shell "bash"

# --- Hidden setup: fake go and vybe commands ---
Hide

Type@0ms "function go() {"
Enter
Type@0ms `  if [[ "$1 $2" == "install github.com/dotcommander/vybe/cmd/vybe@latest" ]]; then`
Enter
Type@0ms "    return 0"
Enter
Type@0ms "  fi"
Enter
Type@0ms `  command go "$@"`
Enter
Type@0ms "}"
Enter

Type@0ms "function vybe() {"
Enter
Type@0ms `  local cmd="$1" sub="$2"`
Enter
Type@0ms `  if [[ "$cmd" == "hook" && "$sub" == "install" ]]; then`
Enter
Type@0ms `    printf 'Installed 6 hooks into Claude Code:\n'`
Enter
Type@0ms `    printf '  \e[32m✓\e[0m SessionStart     → session context injection\n'`
Enter
Type@0ms `    printf '  \e[32m✓\e[0m SessionEnd       → session cleanup\n'`
Enter
Type@0ms `    printf '  \e[32m✓\e[0m UserPromptSubmit → prompt logging\n'`
Enter
Type@0ms `    printf '  \e[32m✓\e[0m PreToolUse       → tool failure tracking\n'`
Enter
Type@0ms `    printf '  \e[32m✓\e[0m PreCompact       → memory checkpoint\n'`
Enter
Type@0ms `    printf '  \e[32m✓\e[0m TaskCompleted    → task lifecycle\n'`
Enter
Type@0ms `  elif [[ "$cmd" == "status" ]]; then`
Enter
Type@0ms `    printf '{\n  "success": true,\n  "data": {\n    "db_ok": true,\n    "query_ok": true,\n    "version": "1.1.0"\n  }\n}\n'`
Enter
Type@0ms `  elif [[ "$cmd" == "hook" && "$sub" == "uninstall" ]]; then`
Enter
Type@0ms `    printf 'Removed 6 hooks from Claude Code \e[32m✓\e[0m\n'`
Enter
Type@0ms "  fi"
Enter
Type@0ms "}"
Enter

Type@0ms "clear"
Enter
Sleep 100ms

Show

# --- Install ---
Type `go install github.com/dotcommander/vybe/cmd/vybe@latest`
Enter
Sleep 2000ms

# --- Wire into Claude Code ---
Type "vybe hook install --claude"
Enter
Sleep 2200ms

# --- Health check ---
Type "vybe status --check"
Enter
Sleep 1800ms

# --- Uninstall ---
Type "vybe hook uninstall --claude"
Enter
Sleep 1500ms

# --- Tagline ---
Type `printf '\n\e[2m# github.com/dotcommander/vybe\e[0m\n'`
Enter
Sleep 2000ms
