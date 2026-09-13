//go:build linux && !amd64 && !arm64

package interp

// No number recorded for this architecture: hostSyncfs answers ENOSYS
// rather than issuing whatever call 0 happens to be.
const sysSyncfs = 0
