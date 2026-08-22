// Package tty owns everything the tree knows about terminals: whether a file
// descriptor is one, and how to make one for a test. Both are the same per-OS
// ioctl knowledge, so they live together rather than in two places that drift.
//
// Two callers need "is this a terminal": the interpreter, which answers the
// language's `isatty` builtin, and `cmd/fern`, which decides whether to colour
// its own diagnostics. The compiled backends emit the equivalent ioctl inline;
// this is the Go-side twin of what they emit.
package tty
