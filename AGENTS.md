# AGENTS.md

Start at [llms.txt](llms.txt), which indexes the usage docs in `docs/usage/`.

## Rules

- Make every Rex call through `client`. Never shell out to the `rex` CLI.
- Never import application-private packages.
- Every rendered cell needs an explicit background. Use `paint.Holes` in tests.

## Gotchas

- Drain `client.Events()` or set `DisableEvents`.
- `surface` needs `session.attach`, not `attach_all`.
- A form field's `Value()` must be comparable. Read multi-selects with `Values.Strings`.
- Rex is beta. Its behaviors are in `docs/rex-behaviors.md`, and `make live` re-checks them.

## Commands

- `make test`, `make vet`, `gofmt -l .`
- `make live` needs a running Rex session, outside the sandbox.
