//go:build linux && arm64

package interp

// syncfs(2) on arm64, from asm-generic/unistd.h.
const sysSyncfs = 267
