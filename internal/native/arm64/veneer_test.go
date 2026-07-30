package arm64

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// The veneer tests shrink the b/bl span (Assembler.veneerReach) so the
// trampoline machinery — anchor choice, dedupe, index remapping — can be
// exercised on programs of a few hundred instructions instead of the
// 128 MB one the architectural limit would demand. The real ceiling gets
// its own end-to-end test at the bottom of this file.
const shortReach = 64

// assembleShortReach parses gas source, shrinks the branch span, and
// lays it out at the W^X text address, returning the assembler (for
// label lookups) and the code words.
func assembleShortReach(t *testing.T, src string, reach int) (*Assembler, []uint32) {
	t.Helper()
	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	a.veneerReach = reach
	if _, _, err := a.BytesProgramWX(elf.TextVAddrWX); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	return a, a.insns
}

// nops returns n `nop` lines — filler to push a branch out of range.
func nops(n int) string {
	return strings.Repeat("\tnop\n", n)
}

// TestVeneerRetargetsFarBranch is the core contract: a `bl` past the
// branch span is redirected to a trampoline that materialises the real
// target with adrp+add and jumps to it, rather than being refused.
func TestVeneerRetargetsFarBranch(t *testing.T) {
	src := ".text\n_start:\n\tbl target\n\tret\n" + nops(300) + "target:\n\tret\n"
	a, insns := assembleShortReach(t, src, shortReach)

	blIdx := a.labels["_start"]
	if got := insns[blIdx] & 0xfc000000; got != 0x94000000 {
		t.Fatalf("instruction at _start is %#08x, want a bl", insns[blIdx])
	}
	// Sign-extend the 26-bit offset and check where the bl now lands.
	off := int32(insns[blIdx]<<6) >> 6
	landing := blIdx + int(off)
	if landing == a.labels["target"] {
		t.Fatal("bl still branches straight to target — no veneer was planted")
	}
	if off < -shortReach || off >= shortReach {
		t.Fatalf("bl offset %d is still outside the (shortened) ±%d span", off, shortReach)
	}

	// The landing site is adrp x17 / add x17, x17 / br x17, and the
	// address it builds is target's.
	if got, want := insns[landing]&0x9f00001f, uint32(0x90000000|veneerReg); got != want {
		t.Fatalf("veneer word 0 = %#08x, want adrp x%d", insns[landing], veneerReg)
	}
	if got := insns[landing+2]; got != BR(veneerReg) {
		t.Fatalf("veneer word 2 = %#08x, want br x%d (%#08x)", got, veneerReg, BR(veneerReg))
	}
	insnAddr := elf.TextVAddrWX + uint64(landing)*4
	immlo := uint64((insns[landing] >> 29) & 0x3)
	immhi := uint64(int32(insns[landing]<<8) >> 13) // sign-extended immhi
	page := (insnAddr &^ 0xfff) + ((immhi<<2 | immlo) << 12)
	lo12 := uint64((insns[landing+1] >> 10) & 0xfff)
	want := elf.TextVAddrWX + uint64(a.labels["target"])*4
	if page+lo12 != want {
		t.Fatalf("veneer builds %#x, want target at %#x", page+lo12, want)
	}
}

// TestVeneerDedupesPerIsland checks that repeated calls to the same far
// target share one trampoline: without dedupe the island grows with the
// call count rather than the callee count.
func TestVeneerDedupesPerIsland(t *testing.T) {
	calls := strings.Repeat("\tbl target\n", 8)
	src := ".text\n_start:\n" + calls + "\tret\n" + nops(300) + "target:\n\tret\n"
	a, insns := assembleShortReach(t, src, shortReach)

	// One island: hop-over `b` + one 3-word veneer, padded to an even
	// word count.
	const wantIsland = 4
	if got, want := len(insns), 8+1+300+1+wantIsland; got != want {
		t.Fatalf("text is %d instructions, want %d (one deduped %d-word island)", got, want, wantIsland)
	}
	var veneers int
	for name := range a.labels {
		if strings.HasPrefix(name, veneerLabelPrefix) {
			veneers++
		}
	}
	if veneers != 1 {
		t.Fatalf("planted %d veneers for 8 calls to one target, want 1", veneers)
	}
}

// TestVeneerNotPlantedInRange guards the common case: a program whose
// branches all encode must come out of the assembler untouched.
func TestVeneerNotPlantedInRange(t *testing.T) {
	t.Setenv("FERN_ARM64_VENEER_REACH", "") // this one asserts the real span
	src := ".text\n_start:\n\tbl target\n\tret\n" + nops(300) + "target:\n\tret\n"
	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	before := len(a.insns)
	if _, _, err := a.BytesProgramWX(elf.TextVAddrWX); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	if len(a.insns) != before {
		t.Fatalf("text grew from %d to %d instructions with every branch in range", before, len(a.insns))
	}
	if a.veneerSeq != 0 {
		t.Fatalf("planted %d veneers with every branch in range", a.veneerSeq)
	}
}

// TestVeneerBothEndsOfText checks the anchor choice. Splicing at the
// ends of .text is what keeps veneering safe (a prepended island shifts
// everything uniformly, an appended one shifts nothing), so a far branch
// near the start must use the leading island and one near the end the
// trailing island — never a single island that only half the program can
// reach.
func TestVeneerBothEndsOfText(t *testing.T) {
	// Long enough that neither end alone covers both branches.
	const pad = 100
	src := ".text\nhead:\n\tret\n" + nops(pad) +
		"early:\n\tb tail\n" + nops(pad) +
		"late:\n\tb head\n" + nops(pad) + "tail:\n\tret\n"
	a, insns := assembleShortReach(t, src, shortReach)

	leading, trailing := 0, 0
	for name, at := range a.labels {
		if !strings.HasPrefix(name, veneerLabelPrefix) {
			continue
		}
		if at < len(insns)/2 {
			leading++
		} else {
			trailing++
		}
	}
	if leading != 1 || trailing != 1 {
		t.Fatalf("veneers: %d leading, %d trailing; want one island at each end", leading, trailing)
	}
}

// TestVeneerRemapsLiteralsSymbolsAndLines pins the bookkeeping that
// splicing an island has to keep straight: every recorded instruction
// index — literal-pool loads, adrp/:lo12: data references, and DWARF
// line rows — has to move with the code it names.
func TestVeneerRemapsLiteralsSymbolsAndLines(t *testing.T) {
	src := ".text\n" +
		".loc 1 7\n" +
		"_start:\n\tbl target\n\tldr x0, =0x1122334455667788\n\tadrp x1, msg\n" +
		"\tadd x1, x1, #:lo12:msg\n\tret\n\t.ltorg\n" +
		nops(300) +
		".loc 1 99\n" +
		"target:\n\tret\n" +
		".rodata\nmsg:\n\t.asciz \"hi\"\n"
	a, insns := assembleShortReach(t, src, shortReach)

	// The literal load still reaches its pool, and the pool still holds
	// the value (the halves of the 8-byte literal must not have been
	// separated by an island).
	litIdx := a.labels["_start"] + 1
	if got := insns[litIdx] & 0xff000000; got != 0x58000000 {
		t.Fatalf("instruction after the bl is %#08x, want ldr x0, =literal", insns[litIdx])
	}
	poolIdx := litIdx + int(int32(insns[litIdx]<<8)>>13)
	if got := uint64(insns[poolIdx]) | uint64(insns[poolIdx+1])<<32; got != 0x1122334455667788 {
		t.Fatalf("literal pool holds %#x, want 0x1122334455667788", got)
	}

	// The DWARF rows still name the instructions they were attached to.
	if len(a.locRows) != 2 {
		t.Fatalf("locRows = %v, want 2 rows", a.locRows)
	}
	if got, want := a.locRows[0].Offset/4, a.labels["_start"]; got != want {
		t.Errorf(".loc 7 sits at instruction %d, want _start at %d", got, want)
	}
	if got, want := a.locRows[1].Offset/4, a.labels["target"]; got != want {
		t.Errorf(".loc 99 sits at instruction %d, want target at %d", got, want)
	}
}

// TestVeneerSecondPassKeepsIslandsIntact is the regression guard for a
// real miscompile: a second veneering pass computes its anchors over the
// stream the first one produced, so it can splice an island INSIDE an
// earlier island. The hop-over `b` was a hand-encoded offset — the one
// thing the index remap cannot correct — so the earlier island's hop
// landed on its own `add x17, x17` and control fell into `br x17` with a
// half-built address. Programs hung or died on a bogus pointer; found by
// running the arm64 corpus under FERN_ARM64_VENEER_REACH, where
// e2e's TestArm64VeneerForcedReach/float_to_string still reproduces the
// original hang against the pre-fix assembler.
//
// This is the unit-level half: it drives the assembler to a multi-pass,
// nested-island layout and runs the result, so the arrangement the fix
// makes legal — control flowing through an inner island's hop and back
// into the veneer it interrupted — is executed, not just assembled.
func TestVeneerSecondPassKeepsIslandsIntact(t *testing.T) {
	launcher := qemuOrNative(t)
	// Calls to six callees scattered across a long body under a span so
	// short that the hop-overs themselves need veneers, which is what
	// drives a second pass and the nesting with it. Every call's result
	// is accumulated, so a veneer that lands anywhere but its callee
	// shows up in the exit code (or, as the bug did, hangs).
	var b strings.Builder
	b.WriteString(".text\n_start:\n\tmov x19, #0\n")
	for i := 0; i < 8; i++ {
		b.WriteString(fmt.Sprintf("\tbl t%d\n\tadd x19, x19, x0\n", i%6))
		b.WriteString(nops(2))
	}
	b.WriteString("\tmov x0, x19\n\tmov x8, #93\n\tsvc #0\n" + nops(400))
	for i := 0; i < 6; i++ {
		b.WriteString(fmt.Sprintf("t%d:\n\tmov x0, #%d\n\tret\n", i, i+1))
	}

	a, err := ParseProgram(b.String())
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	a.veneerReach = 8
	text, data, err := a.BytesProgramWX(elf.TextVAddrWX)
	if err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	if a.veneerPasses < 2 {
		t.Fatalf("veneering converged in %d pass(es); this test needs at least 2 to reach the nesting case", a.veneerPasses)
	}
	// 1+2+3+4+5+6+1+2
	if got := runELF(t, launcher, elf.StaticExecutableDataWX(text, data)); got != 24 {
		t.Fatalf("multi-pass veneered program exited %d, want 24", got)
	}
}

// TestVeneerEnvReach covers FERN_ARM64_VENEER_REACH, the knob that makes
// ordinary-sized programs take the veneer path so the whole arm64 test
// corpus can exercise it. Without it, veneers are reachable only through
// one ~130 MB program.
func TestVeneerEnvReach(t *testing.T) {
	src := ".text\n_start:\n\tbl target\n\tret\n" + nops(300) + "target:\n\tret\n"

	t.Setenv("FERN_ARM64_VENEER_REACH", "64")
	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if _, _, err := a.BytesProgramWX(elf.TextVAddrWX); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	if a.veneerSeq == 0 {
		t.Fatal("FERN_ARM64_VENEER_REACH=64 planted no veneer for a 300-instruction call")
	}

	// A malformed value is ignored rather than fatal — it must never turn
	// a working build into a broken one.
	t.Setenv("FERN_ARM64_VENEER_REACH", "not-a-number")
	b, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	if _, _, err := b.BytesProgramWX(elf.TextVAddrWX); err != nil {
		t.Fatalf("BytesProgramWX with a malformed reach: %v", err)
	}
	if b.veneerSeq != 0 {
		t.Fatal("a malformed FERN_ARM64_VENEER_REACH changed the branch span")
	}
}

// qemuOrNative returns the qemu-aarch64 launcher, or "" when the host
// runs aarch64 binaries directly. Skips when neither is available.
func qemuOrNative(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return ""
	}
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("no qemu-aarch64 to run arm64 binaries")
	return ""
}

// runELF writes an ELF image to a temp file and runs it, returning the
// exit status.
func runELF(t *testing.T, launcher string, bin []byte) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	cmd := exec.Command(path)
	if launcher != "" {
		cmd = exec.Command(launcher, path)
	}
	err := cmd.Run()
	if err == nil {
		return 0
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run %s: %v", path, err)
	}
	return ee.ExitCode()
}

// TestVeneerExecutes is the test that a byte-level check cannot stand in
// for: a veneered call has to actually run — reach the callee, come back
// to the right place, and leave the surrounding code (a literal-pool
// load and a data-symbol reference either side of the splice) working.
func TestVeneerExecutes(t *testing.T) {
	launcher := qemuOrNative(t)
	// exit(callee() + 1) where callee lives past the branch span, with a
	// literal load and an adrp/:lo12: data reference in between so the
	// remapping is exercised by execution too.
	src := ".text\n_start:\n" +
		"\tldr x19, =0x2b\n" + // 43
		"\tadrp x20, val\n\tadd x20, x20, #:lo12:val\n" +
		"\tldrb w20, [x20]\n" + // 1
		"\tbl callee\n" + // far: x0 = 3
		"\tsub x0, x19, x0\n" + // 43 - 3 = 40
		"\tadd x0, x0, x20\n" + // + 1 = 41
		"\tmov x8, #93\n\tsvc #0\n\t.ltorg\n" +
		nops(300) +
		"callee:\n\tmov x0, #3\n\tret\n" +
		".rodata\nval:\n\t.byte 1\n"

	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	a.veneerReach = shortReach
	text, data, err := a.BytesProgramWX(elf.TextVAddrWX)
	if err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	if a.veneerSeq == 0 {
		t.Fatal("no veneer was planted — the test is not exercising the veneer path")
	}
	if got := runELF(t, launcher, elf.StaticExecutableDataWX(text, data)); got != 41 {
		t.Fatalf("veneered program exited %d, want 41", got)
	}
}

// TestVeneerInteriorIslandsExecute covers the case the two ends of
// .text cannot reach between them, where islands have to be spliced
// into the middle of the code — and so have to be hopped over rather
// than fallen through. It runs a program with a far forward call and a
// far backward one, either side of code that keeps executing across the
// splice points.
func TestVeneerInteriorIslandsExecute(t *testing.T) {
	launcher := qemuOrNative(t)
	const pad = 500 // ≫ shortReach, so both calls need a veneer
	src := ".text\n_start:\n\tbl outer\n\tmov x8, #93\n\tsvc #0\n" +
		nops(pad) +
		"inner:\n\tmov x0, #7\n\tret\n" +
		nops(pad) +
		"outer:\n\tstr x30, [sp, #-16]!\n\tbl inner\n\tldr x30, [sp], #16\n\tret\n" +
		nops(pad)

	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	a.veneerReach = shortReach
	text, data, err := a.BytesProgramWX(elf.TextVAddrWX)
	if err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	// A shortReach-sized span over ~1500 instructions puts anchors deep
	// inside the code, not just at its ends.
	var interior int
	for name, at := range a.labels {
		if strings.HasPrefix(name, veneerLabelPrefix) && at > 8 && at < len(a.insns)-8 {
			interior++
		}
	}
	if interior == 0 {
		t.Fatalf("no interior island was planted across %d instructions", len(a.insns))
	}
	if got := runELF(t, launcher, elf.StaticExecutableDataWX(text, data)); got != 7 {
		t.Fatalf("program exited %d, want 7", got)
	}
}

// TestVeneerRealImm26Ceiling is the ceiling itself, unshortened: a
// program whose .text spans more than the architectural ±128 MB between
// a call and its callee. It is the shape that broke the self-host arm64
// build — before veneers this program could not be assembled at all —
// and it is checked by running, since an image that merely links proves
// nothing about a trampoline.
//
// It builds a ~135 MB image, so it is skipped under -short.
func TestVeneerRealImm26Ceiling(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a ~135 MB image")
	}
	t.Setenv("FERN_ARM64_VENEER_REACH", "") // the point is the architectural span
	launcher := qemuOrNative(t)

	// bl callee / exit(x0), then enough filler to put callee out of
	// imm26 range, then callee: mov x0, #7; ret.
	a := NewAssembler()
	a.TextLabel("_start")
	a.BL("callee")
	a.Emit(MOVZ(8, 93, 0)) // exit_group
	a.Emit(SVC(0))
	pad := imm26Reach + 1<<20 // comfortably past ±2^25 instructions
	for i := 0; i < pad; i++ {
		a.Emit(nopInsn)
	}
	a.TextLabel("callee")
	a.Emit(MOVZ(0, 7, 0))
	a.Emit(RET(30))

	span := a.labels["callee"] - a.labels["_start"]
	if span <= imm26Reach {
		t.Fatalf("call spans %d instructions, want more than the imm26 reach of %d", span, imm26Reach)
	}
	text, data, err := a.BytesProgramWX(elf.TextVAddrWX)
	if err != nil {
		t.Fatalf("BytesProgramWX over the imm26 ceiling: %v", err)
	}
	t.Logf("text = %s across a %d-instruction call, %d veneer(s)", byteSize(len(text)), span, a.veneerSeq)
	if got := runELF(t, launcher, elf.StaticExecutableDataWX(text, data)); got != 7 {
		t.Fatalf("program exited %d, want 7", got)
	}
}

func byteSize(n int) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
