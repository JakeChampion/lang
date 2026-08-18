package e2eharness

import (
	"os"
	"strings"
)

// ChildEnv builds the environment for a compiler or driver child process:
// everything the test process inherited, minus every FERN_* variable, plus
// exactly what the caller sets.
//
// A test that spawns a child on a bare os.Environ() lets the ambient
// environment decide what it asserts, which is invisible — a vacuous test and
// a passing test are byte-identical in the log (#6833). Measured: under
// FERN_SELFHOST_NO_REUSE=1, TestSelfHostReuseDifferentialX86_64 failed all 89
// of its rows, because its "reuse on" arm was then reuse-off too and the
// on-vs-off comparison it exists to make was comparing a run with itself.
//
// What this does NOT reach is a variable read in the TEST process rather than
// the child: FERN_SANITIZE and FERN_LEAKCHECK are read at init by internal/ast
// and so instrument the driver as it is EMITTED, whatever the child env says.
// Those surface as loud, self-describing failures, which is the diagnostic
// mode working.
//
// Stripping ALL of FERN_* is safe here, which is not obvious: the variables CI
// sets most (FERN_SELFHOST_BUILD_CACHE, FERN_WASI_ADAPTER) are read by this
// harness in the TEST process to find a warm driver and the WASI adapter, not
// by the child. What a child does read — FERN_STRICT_IR and FERN_IR_VERIFY in
// the self-host drivers, FERN_CACHE_DIR in pkgcache, the rc and sanitize knobs
// in the compiler — is exactly what a test must set for itself rather than
// inherit.
//
// The polarity is deliberate: a FERN_* variable added later is stripped by
// default, so a new knob cannot silently start deciding an old test.
func ChildEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FERN_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}
