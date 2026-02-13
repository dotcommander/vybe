# Vybe Documentation

Documentation for integrating autonomous coding agents with vybe.

## Quick Start

**New to vybe?** Start here:

1. **[Usage Examples](usage-examples.md)** - Copy-paste playbook for common operations
2. **[Agent Install](agent-install.md)** - Wire your coding agent to vybe with minimal friction

## Integration Guides

| Guide | Audience | Purpose |
|-------|----------|---------|
| [Agent Install](agent-install.md) | Agent operators | Quick-start bootstrap and core loop examples |
| [Integration Contract](integration-custom-assistant.md) | Integration developers | Assistant-agnostic lifecycle mapping and contracts |

## Developer Guides

| Guide | Audience | Purpose |
|-------|----------|---------|
| [Idempotent Action Pattern](idempotent-action-pattern.md) | Vybe contributors | How to add new features without breaking consistency |
| [Important Features Matrix](testing/important-features-matrix.md) | Vybe contributors | Test coverage tracking for continuity-critical behavior |

## Document Structure

- `docs/` - Active documentation
- `docs/testing/` - Test coverage matrices and verification guides

## Related Documentation

- **[CLAUDE.md](../CLAUDE.md)** - Full architecture, schema, coding guidelines, operational context
- **[README.md](../README.md)** - Product overview, installation, and hook setup
- **[examples/](../examples/)** - Working code examples (OpenCode bridge, research loop demo)
