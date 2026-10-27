//go:build !linux

package interp

// A host without prctl(2) answers ENOSYS in the runtime's errno numbering.
func prctlQuery(option int) int64 { return -38 }
