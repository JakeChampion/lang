// Package compiler embeds the self-host compiler's sources. `fern` compiles
// them with the pinned stage0 when no self-host compiler is installed
// (internal/launcher).
package compiler

import "embed"

// Sources is every .fern file of the compiler; fern.fern is its entry.
//
//go:embed *.fern
var Sources embed.FS
