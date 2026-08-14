package testenv

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// PinCompileFlags holds the internal/ast diagnostic modes at OFF for the rest of
// the test, restoring them afterwards.
//
// These are package-level vars initialised from os.Getenv at init, so a test
// that asserts a property of the DEFAULT compile — a hostless artifact with no
// syscall in it, an op sequence, an asm shape — is asserting about whatever the
// shell exported unless it says otherwise. Clean covers the same hazard for a
// child process; this is the in-process half.
func PinCompileFlags(t *testing.T) {
	t.Helper()
	prevLeak, prevSan := ast.LeakCheckEnabled, ast.SanitizeEnabled
	prevTrace, prevDebug := ast.RcTrace, ast.RcFreeDebug
	prevTrap, prevGuided, prevSandbox := ast.RcUnderflowTrap, ast.RcReuseDropGuided, ast.SandboxEnabled
	t.Cleanup(func() {
		ast.LeakCheckEnabled, ast.SanitizeEnabled = prevLeak, prevSan
		ast.RcTrace, ast.RcFreeDebug = prevTrace, prevDebug
		ast.RcUnderflowTrap, ast.RcReuseDropGuided, ast.SandboxEnabled = prevTrap, prevGuided, prevSandbox
	})
	ast.LeakCheckEnabled, ast.SanitizeEnabled = false, false
	ast.RcTrace, ast.RcFreeDebug = false, false
	ast.RcUnderflowTrap, ast.RcReuseDropGuided, ast.SandboxEnabled = false, false, false
}
