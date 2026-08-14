package testenv

// Class says what an ambient value of a variable does to a test run.
type Class string

const (
	// Semantic: an ambient value changes what a compile emits, what an emitted
	// program does, or which number a gate compares against. Nothing in CI sets
	// one process-wide, so a value in the environment is a developer's shell
	// leaking into the result. CheckAmbient rejects these.
	Semantic Class = "semantic"

	// Lane: selects which tests run, how much they run, or where a tool and its
	// caches live. CI lanes set these deliberately, so an ambient value is
	// expected — but it still never reaches a child unless the test names it.
	Lane Class = "lane"
)

// Var is one behaviour-changing environment variable.
type Var struct {
	Name   string
	Class  Class
	Effect string // what an ambient value does, in one line
}

// Vars is the census. Every FERN_* / RUN_* / DIFF_ORACLE_* name that Go code
// reads via os.Getenv/os.LookupEnv, that a test sets via t.Setenv, that the
// self-hosted compiler or the stdlib reads via env(), or that anything under
// .github/ or scripts/ mentions, must appear here — TestCensusIsComplete pairs
// the list against those sites, so a new variable cannot skip classification.
//
// Names that exist only as data for an env-reading test program (FERN_E2E_VAR,
// FERN_NATIVELINK_PROBE, …) are deliberately absent: they change nothing, and
// Clean strips them along with everything else it was not asked to pass.
var Vars = []Var{
	{"DIFF_ORACLE_ARTIFACT_DIR", Lane, "where the differential oracle writes its per-seed artefacts"},
	{"DIFF_ORACLE_SHARD", Lane, "which slice of the fernsmith corpus this process runs"},
	{"FERN_ARM64_VENEER_REACH", Semantic, "the arm64 branch-span threshold, so which calls get a veneer"},
	{"FERN_BENCH", Lane, "runs the peak-RSS reclamation benchmark instead of skipping it"},
	{"FERN_BUILD_HEAVY_MB", Semantic, "the per-build memory reservation the self-host build gate compares against"},
	{"FERN_BUILD_MEM_BUDGET_MB", Semantic, "the total build memory budget, so how many self-host builds run at once"},
	{"FERN_CACHE_DIR", Semantic, "redirects the package cache, so a resolve or vendor assertion is about a different tree"},
	{"FERN_CI_JOBS_JSON", Semantic, "substitutes a saved jobs payload for the Actions API call the shard classifier makes"},
	{"FERN_CI_SIZE_GATE_STRICT", Lane, "promotes a driver-size finding from advisory to a failure"},
	{"FERN_CI_TOLERATE_VANISHED_SHARDS", Lane, "decides whether a vanished shard fails the run"},
	{"FERN_CI_WEIGHT_GATE_STRICT", Lane, "promotes a shard-weight finding from advisory to a failure"},
	{"FERN_CLIFF_REPORT", Semantic, "makes the self-host drivers print the quadratic-copy readout on stderr"},
	{"FERN_DRIVER_SIZE_REPORT", Lane, "where the warm jobs record the driver sizes they built"},
	{"FERN_DUMP_PROGRAMS", Lane, "harvests every program the interpret-the-driver shim compiles into a directory"},
	{"FERN_EMIT_MEMLIMIT_MB", Semantic, "the address-space cap a self-host emit runs under"},
	{"FERN_IR_VERIFY", Semantic, "turns a failed IR invariant from a silent pass into a hard error"},
	{"FERN_LEAKCHECK", Semantic, "compiles the leak census in; without it a leak reports nothing"},
	{"FERN_NATIVE_ASM", Lane, "routes the fixture corpus through the in-process assembler with no gcc fallback"},
	{"FERN_RC_FREE_DEBUG", Semantic, "poisons freed memory, so whether a use-after-free is observable at all"},
	{"FERN_RC_REUSE_DROP_GUIDED", Semantic, "selects drop-guided reuse over the default reuse analysis"},
	{"FERN_RC_TRACE", Semantic, "emits the per-heap-event tracer hook into every allocation site"},
	{"FERN_RC_UNDERFLOW_TRAP", Semantic, "traps an rc underflow instead of continuing past it"},
	{"FERN_SANDBOX", Semantic, "emits the seccomp filter, so which syscalls the program may make"},
	{"FERN_SANITIZE", Semantic, "switches the leak and double-free detectors on"},
	{"FERN_SELFHOST_BUILD_CACHE", Lane, "where warmed self-host driver binaries are cached between jobs"},
	{"FERN_SELFHOST_DIFF", Lane, "runs the fernsmith corpus through the self-host compiler"},
	{"FERN_SELFHOST_FIXTURES", Lane, "runs the 335-fixture corpus through the self-host compiler"},
	{"FERN_SELFHOST_INTERP", Semantic, "runs the self-host driver under the interpreter instead of natively"},
	{"FERN_SELFHOST_NO_REUSE", Semantic, "disables reuse in self-host lowering"},
	{"FERN_SIZE_TOLERANCE_PERCENT", Semantic, "the driver-size drift tolerance; a large value silences every finding"},
	{"FERN_SMOKE", Lane, "strict, advisory or off for the Netlify smoke lane"},
	{"FERN_SMOKE_BUDGET_S", Lane, "total seconds the smoke lane may spend"},
	{"FERN_SMOKE_CACHE", Lane, "where the smoke lane keeps its Go and driver caches"},
	{"FERN_SMOKE_STAGES", Lane, "which subset of smoke stages runs"},
	{"FERN_STAGE2_SELF", Lane, "adds the whole-compiler case to the stage-2 arm64 fixpoint"},
	{"FERN_STRICT_IMPORTS", Semantic, "makes the self-host parser exit on an unresolvable import instead of warning"},
	{"FERN_STRICT_IR", Semantic, "turns an IR bail from a silent fall-through into a hard error, or back"},
	{AmbientOKVar, Lane, "names the census variables an intentionally dirty run accepts and forwards"},
	{"FERN_WARM_DRIVER", Lane, "which self-host drivers this warm job builds"},
	{"FERN_WASI_ADAPTER", Lane, "the preview1-to-preview2 adapter path the component legs need"},
	{"FERN_WEIGHT_MIN_FLAG_SECONDS", Semantic, "the floor below which the shard-weight audit reports nothing"},
	{"FERN_WEIGHT_OWN_JOB_FRACTION", Semantic, "a shard-weight audit threshold"},
	{"FERN_WEIGHT_SAFETY", Semantic, "a shard-weight audit threshold"},
	{"FERN_WEIGHT_SHARD_BUDGET_SECONDS", Semantic, "the per-shard time budget the weight audit compares against"},
	{"FERN_WEIGHT_SHARD_LOAD_FRACTION", Semantic, "a shard-weight audit threshold"},
	{"FERN_WEIGHT_UNDER_FACTOR", Semantic, "a shard-weight audit threshold"},
	{"RUN_CONST_FUNC_GEN2", Lane, "runs the gen2 const-func case"},
	{"RUN_EMITALL_CHECK", Lane, "runs the per-module emit-all check"},
	{"RUN_EMITALL_FIXPOINT", Lane, "runs the per-module emit-all fixpoint"},
	{"RUN_ID", Lane, "the Actions run whose shard outcomes the classifier queries"},
	{"RUN_PERMODULE_FIXPOINT", Lane, "runs the per-module fixpoint"},
	{"RUN_SECCOMP_CORPUS", Lane, "runs the seccomp filter over the whole fixture corpus"},
	{"RUN_SHRINK_PROPERTY", Lane, "runs the fernsmith shrink property"},
}

// Lookup returns the census entry for name, or nil.
func Lookup(name string) *Var {
	for i := range Vars {
		if Vars[i].Name == name {
			return &Vars[i]
		}
	}
	return nil
}
