// Package rexuikit is a Rex-native TUI toolkit: surfaces (modal, drawer,
// split, pre-warm pool) that open as Rex floating layers, a typed client for
// Rex's rex.control.v1 WebSocket protocol, a fast-path entry router that
// paints before any heavy initialization, and a content kit (card, fields,
// form host, list, info panel) whose every rendered cell carries an explicit
// background.
//
// rexuikit is its own Go module (github.com/dphaener/rexuikit) inside the forge
// repository so it can be extracted later. That is why it must never import
// forge's internal/ packages: an import of them would make the extraction
// impossible and would not even compile from outside the forge module.
//
// forge consumes rexuikit through a replace directive in its go.mod. Editors
// (gopls) need a local, gitignored go.work spanning both modules to resolve
// the two together:
//
//	go work init . ./rexuikit
package rexuikit
