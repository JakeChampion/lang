// Package bootstrap embeds the stage0 pin, the earlier compiler `make
// bootstrap` and `fern`'s own first build of the self-host start from
// (docs/BOOTSTRAP.md).
package bootstrap

import _ "embed"

// Lock is stage0.lock: the release URL and each host's sha256.
//
//go:embed stage0.lock
var Lock string
