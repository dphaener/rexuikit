// Package rexuikit is a Rex-native TUI toolkit: surfaces (modal, drawer,
// split, pre-warm pool) that open as Rex floating layers, a typed client for
// Rex's rex.control.v1 WebSocket protocol, a fast-path entry router that
// paints before any heavy initialization, and a content kit (card, fields,
// form host, list, info panel) whose every rendered cell carries an explicit
// background.
//
// rexuikit is a standalone Go module (github.com/dphaener/rexuikit). It must
// never import application-private packages from the programs that use it:
// the module would stop building outside them.
package rexuikit
