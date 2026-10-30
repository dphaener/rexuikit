# rexuikit

rexuikit is a Go toolkit for terminal programs that run as floating layers in
Rex. It talks to Rex, opens a program as a modal, drawer or split, and includes
UI components for forms, lists and dialogs.

## What it includes

- **client**: a connection to the Rex server.
- **surface**: opens, sizes, places and closes a program's layer. Supports
  pre-warmed programs.
- **ui**: forms, lists, dialogs, info panels, cards, panes, tabs, hint bars and
  notices, with light and dark themes.
- **entry**: fast startup for quick actions, with time-to-first-paint
  measurement.

## Install

```sh
go get github.com/dphaener/rexuikit@latest
```

Requires Go 1.25 or later.

## Status

Rex is in beta and its API may change. Usage details are in
[AGENTS.md](AGENTS.md).

## License

See [LICENSE](LICENSE).
