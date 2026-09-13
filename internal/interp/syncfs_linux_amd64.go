//go:build linux && amd64

package interp

// syncfs(2) on x86-64, from arch/x86/entry/syscalls/syscall_64.tbl.
const sysSyncfs = 306
