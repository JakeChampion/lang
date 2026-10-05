package e2eharness

import (
	"os"
	"strings"
	"testing"
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
	return append(env, ProbeEnv(extra...)...)
}

// BoxedProbe compiles with the semantic inliner off, for a probe that pins the
// rc plan for a tuple, record or variant box the inliner would split into its
// parts, leaving nothing on the heap to count.
const BoxedProbe = "FERN_SEM_INLINE="

// boxed is set for the length of a BoxedProbes test.
var boxed bool

// BoxedProbes compiles the rest of the test's programs as BoxedProbe does,
// through ChildEnv and through children that inherit the test's environment.
// The setting is the test's own, so a FERN_SEM_INLINE in the developer's
// shell still reaches no ChildEnv child. A test calling it pins how the rc
// plan treats a box the inliner would remove; the default pipeline's
// correctness on the same shapes is the rc correctness corpus's and the
// fixture corpus's to gate.
func BoxedProbes(t testing.TB) {
	t.Helper()
	t.Setenv("FERN_SEM_INLINE", "")
	boxed = true
	t.Cleanup(func() { boxed = false })
}

// ProbeEnv is env with BoxedProbe added while a BoxedProbes test runs.
func ProbeEnv(env ...string) []string {
	if boxed {
		return append(env, BoxedProbe)
	}
	return env
}

// SelfHostVerify turns on the self-host compiler's re-checks of its own
// passes without the IR gate's coverage line. A plain compile skips them, so
// a test compiling a program with the self-host compiler sets this. A driver
// build does not: its cache admits no FERN_* knob (CompileWithSelfHost).
const SelfHostVerify = "FERN_IR_VERIFY=quiet"

// SelfHostChildEnv is ChildEnv with SelfHostVerify set. A FERN_IR_VERIFY in
// extra overrides it: exec keeps the last value of a duplicate key.
func SelfHostChildEnv(extra ...string) []string {
	return ChildEnv(append([]string{SelfHostVerify}, extra...)...)
}
