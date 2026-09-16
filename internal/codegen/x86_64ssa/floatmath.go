package x86_64ssa

import (
	"fmt"

	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
)

// The f64 math helpers, in the SSA GP convention: the argument's bits arrive
// in rdi (pow's exponent in rsi) and the result's leave in rax. The rounding
// family is one SSE4.1 instruction each, inside the x86-64-v3 baseline. The
// transcendentals call the flat backend's polynomial kernels, which take and
// return xmm0 (pow: y in xmm1) and are emitted once per module by
// emitTranscendentals.

// emitF64UnaryHelper writes name(x) -> f(x) for a body that maps xmm0 to xmm0
// and touches nothing else that has to survive.
func emitF64UnaryHelper(name string, body ...string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmovq xmm0, rdi")
		for _, b := range body {
			w("\t%s", b)
		}
		w("\tmovq rax, xmm0")
		w("\tret")
	}
}

// emitAbsF64Helper writes __abs_f64(x) -> |x|: the sign bit cleared.
func emitAbsF64Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__abs_f64"))
	w("\tmovabs rax, 0x7fffffffffffffff")
	w("\tand rax, rdi")
	w("\tret")
}

// emitRoundF64Helper writes __round_f64(x): half away from zero, as the flat
// backend and arm64's frinta round. r = trunc(x); if |x - r| >= 0.5 then r
// moves one further from zero. The difference is exact, so the tie is read
// without error.
func emitRoundF64Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__round_f64"))
	w("\tmovq xmm0, rdi")
	w("\troundsd xmm1, xmm0, 3")
	w("\tmovapd xmm2, xmm0")
	w("\tsubsd xmm2, xmm1")
	w("\tmovq rcx, xmm2")
	w("\tmovabs rdx, 0x7fffffffffffffff")
	w("\tand rcx, rdx")
	w("\tmovq xmm3, rcx")
	w("\tmovabs rcx, 0x3fe0000000000000")
	w("\tmovq xmm4, rcx")
	w("\tcomisd xmm3, xmm4")
	w("\tjb .Lssa_round_done")
	w("\tmovq rcx, xmm0")
	w("\tmovabs rdx, 0x8000000000000000")
	w("\tand rcx, rdx")
	w("\tmovabs rdx, 0x3ff0000000000000")
	w("\tor rcx, rdx")
	w("\tmovq xmm5, rcx")
	w("\taddsd xmm1, xmm5")
	w(".Lssa_round_done:")
	w("\tmovq rax, xmm1")
	w("\tret")
}

// emitTranscendentalHelper writes name(x) -> kernel(x) through the flat
// backend's __fern_<name> routine. A helper is entered with rsp 8 mod 16, so
// one slot of padding puts the kernel's entry where System V has it.
func emitTranscendentalHelper(name string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmovq xmm0, rdi")
		if name == "__pow_f64" {
			w("\tmovq xmm1, rsi")
		}
		w("\tsub rsp, 8")
		w("\tcall __fern_%s", name[2:])
		w("\tadd rsp, 8")
		w("\tmovq rax, xmm0")
		w("\tret")
	}
}

// transcendentalHelpers are the helpers that call into the shared kernel
// bundle, which is emitted once whenever any of them is referenced. The
// bundle is not in runtimeHelperEmitters (the IR cannot name its routines), so
// this gate is what puts it in the module.
var transcendentalHelpers = map[string]bool{
	"__exp_f64": true,
	"__log_f64": true,
	"__pow_f64": true,
	"__sin_f64": true,
	"__cos_f64": true,
}

// usesTranscendentals reports whether any referenced helper needs the kernel
// bundle.
func usesTranscendentals(helpers []string) bool {
	for _, h := range helpers {
		if transcendentalHelpers[h] {
			return true
		}
	}
	return false
}

// emitTranscendentals writes the kernel bundle and its coefficient table,
// with labels the module's own emission cannot collide with.
func emitTranscendentals(w func(string, ...any)) {
	n := 0
	fresh := func(prefix string) string {
		n++
		return fmt.Sprintf(".Lssa_fk_%s_%d", prefix, n)
	}
	nativex86_64.EmitFloatTranscendentals(w, fresh)
}
