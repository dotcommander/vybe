set shell := ["bash", "-cu"]

default:
    @just --list

# Create/refresh the dedicated AI Python environment.
ensure-ai:
    mkdir -p "$HOME/.venvs"
    if [ ! -x "$HOME/.venvs/ai/bin/python" ]; then python3 -m venv "$HOME/.venvs/ai"; fi
    "$HOME/.venvs/ai/bin/python" -m pip install --upgrade pip setuptools wheel

# Show interpreter + pip versions in the AI environment.
ai-version: ensure-ai
    "$HOME/.venvs/ai/bin/python" --version
    "$HOME/.venvs/ai/bin/python" -m pip --version

# Run pip inside the AI environment (example: just ai-pip list).
ai-pip *args: ensure-ai
    "$HOME/.venvs/ai/bin/python" -m pip {{args}}

# Install packages into the AI environment.
ai-install *packages: ensure-ai
    "$HOME/.venvs/ai/bin/python" -m pip install {{packages}}

# Upgrade packages in the AI environment.
ai-upgrade *packages: ensure-ai
    "$HOME/.venvs/ai/bin/python" -m pip install --upgrade {{packages}}

# Verify dependency consistency in the AI environment.
ai-check: ensure-ai
    "$HOME/.venvs/ai/bin/python" -m pip check

# Save package list for backup/share.
ai-freeze: ensure-ai
    "$HOME/.venvs/ai/bin/python" -m pip freeze

# Enter an interactive shell with AI env activated.
ai-shell: ensure-ai
    bash -lc 'source "$HOME/.venvs/ai/bin/activate" && exec "$SHELL" -i'
