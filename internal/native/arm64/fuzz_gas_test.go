package arm64_test

// Generator-driven differential against GNU as (issue #7896), mirroring the
// x86-64 lane: a form inventory covers every shape the dispatch supports, a
// seeded PRNG instantiates cases per form (immediates and offsets weighted to
// straddle the imm7/imm9/imm12 and bitmask boundaries), and the program must
// assemble byte-for-byte identically to aarch64-linux-gnu-as. There are no
// known legitimate encoding differences on arm64, so every form is compared
// by bytes.
//
// Tiers: the default run is a small smoke tier; FERN_ASM_FUZZ=1 runs the deep
// tier. FERN_ASM_FUZZ_SEED overrides the fixed default seed.

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"math/bits"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/arm64tbl"
)

// fam is a family's mnemonics from the shared vocabulary table, minus any
// the form's operand shape does not fit. The inventory reads the table so it
// widens with the vocabulary instead of lagging it.
func fam(name string, drop ...string) []string {
	for _, f := range arm64tbl.Scalar {
		if f.Name != name {
			continue
		}
		var out []string
		for _, m := range f.Mnemonics() {
			keep := true
			for _, d := range drop {
				if d == m {
					keep = false
				}
			}
			if keep {
				out = append(out, m)
			}
		}
		return out
	}
	panic("no arm64tbl family " + name)
}

// a64Form is one row of the form inventory. multi marks label-bearing
// snippet forms (skipped by the llvm-mc lane: show-encoding leaves label
// operands as unresolved fixups).
type a64Form struct {
	name  string
	multi bool
	gen   func(r *rand.Rand, i int) string
}

func a64FuzzCases() int {
	if os.Getenv("FERN_ASM_FUZZ") != "" {
		return 2000
	}
	return 8
}

func a64FuzzSeed(t *testing.T) int64 {
	s := os.Getenv("FERN_ASM_FUZZ_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		t.Fatalf("bad FERN_ASM_FUZZ_SEED %q: %v", s, err)
	}
	return n
}

func a64FormRand(seed int64, name string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(name))
	return rand.New(rand.NewSource(seed ^ int64(h.Sum64())))
}

func a64FormUnits(f a64Form, r *rand.Rand, n int) []string {
	units := make([]string, n)
	for i := range units {
		units[i] = f.gen(r, i)
	}
	return units
}

// ---------------------------------------------------------------------------
// Value spaces.

func xr(r *rand.Rand) string                   { return fmt.Sprintf("x%d", r.Intn(31)) }
func wr(r *rand.Rand) string                   { return fmt.Sprintf("w%d", r.Intn(31)) }
func dr(r *rand.Rand) string                   { return fmt.Sprintf("d%d", r.Intn(32)) }
func sr(r *rand.Rand) string                   { return fmt.Sprintf("s%d", r.Intn(32)) }
func vr(r *rand.Rand) string                   { return fmt.Sprintf("v%d", r.Intn(32)) }
func a64pick(r *rand.Rand, xs []string) string { return xs[r.Intn(len(xs))] }

// A NEON arrangement: the element size and how many of them fill the 64- or
// 128-bit register. Which arrangements an instruction admits is per-mnemonic
// and is the whole point of sweeping them — an unsupported one has to be
// REFUSED, and a supported one has to pick the same Q and size bits gas does.
type a64Arr struct {
	spell string // "8b", "4h", "2s", "2d", …
	elem  int    // element width in bits
	lanes int
}

var (
	a64ArrB    = []a64Arr{{"8b", 8, 8}, {"16b", 8, 16}}
	a64ArrBHS  = append(append([]a64Arr{}, a64ArrB...), a64Arr{"4h", 16, 4}, a64Arr{"8h", 16, 8}, a64Arr{"2s", 32, 2}, a64Arr{"4s", 32, 4})
	a64ArrFull = append(append([]a64Arr{}, a64ArrBHS...), a64Arr{"2d", 64, 2})
	a64ArrFP   = []a64Arr{{"2s", 32, 2}, {"4s", 32, 4}, {"2d", 64, 2}}
	a64ArrH    = []a64Arr{{"4h", 16, 4}, {"8h", 16, 8}}
)

func a64arr(r *rand.Rand, set []a64Arr) a64Arr { return set[r.Intn(len(set))] }

// v3same renders `mnem Vd.<T>, Vn.<T>, Vm.<T>` — the three-register-same
// shape every integer and FP vector ALU op is spelled in.
func v3same(r *rand.Rand, mnem string, a a64Arr) string {
	return fmt.Sprintf("\t%s %s.%s, %s.%s, %s.%s\n", mnem, vr(r), a.spell, vr(r), a.spell, vr(r), a.spell)
}

// v2same renders `mnem Vd.<T>, Vn.<T>` — the two-register-misc shape.
func v2same(r *rand.Rand, mnem string, a a64Arr) string {
	return fmt.Sprintf("\t%s %s.%s, %s.%s\n", mnem, vr(r), a.spell, vr(r), a.spell)
}

// gp returns a same-width register pair maker: is64 selects x or w names.
func gp(r *rand.Rand, is64 bool) string {
	if is64 {
		return xr(r)
	}
	return wr(r)
}

// baseReg is a load/store base: Xn or, occasionally, sp.
func baseReg(r *rand.Rand) string {
	if r.Intn(4) == 0 {
		return "sp"
	}
	return fmt.Sprintf("x%d", r.Intn(29))
}

var a64Conds = []string{"eq", "ne", "hs", "cs", "lo", "cc", "mi", "pl",
	"vs", "vc", "hi", "ls", "ge", "lt", "gt", "le"}

// bitmaskImm draws an encodable logical (bitmask) immediate: a rotated run
// of ones inside a 2/4/8/16/32/64-bit element, replicated to the register
// width. The construction walks the whole encodable space.
func bitmaskImm(r *rand.Rand, is64 bool) uint64 {
	sizes := []uint{2, 4, 8, 16, 32}
	if is64 {
		sizes = append(sizes, 64)
	}
	e := sizes[r.Intn(len(sizes))]
	ones := uint(1 + r.Intn(int(e)-1))
	pattern := uint64(1)<<ones - 1
	rot := uint(r.Intn(int(e)))
	if rot != 0 {
		pattern = (pattern >> rot) | (pattern << (e - rot))
	}
	if e < 64 {
		pattern &= uint64(1)<<e - 1
		for f := e; f < 64; f *= 2 {
			pattern |= pattern << f
		}
	}
	return pattern
}

// imm12 draws an add/sub immediate: plain 0..4095, or a 4096-multiple the
// assembler must route to the `lsl #12` form, weighted to the edges.
func imm12(r *rand.Rand) (int64, bool) {
	edge := []int64{0, 1, 7, 255, 4094, 4095}
	var v int64
	if r.Intn(2) == 0 {
		v = edge[r.Intn(len(edge))]
	} else {
		v = int64(r.Intn(4096))
	}
	if r.Intn(3) == 0 {
		if v == 0 {
			v = 1 + int64(r.Intn(4095))
		}
		return v << 12, true
	}
	return v, false
}

// scaledOffset draws a load/store offset for the given access size: mostly
// in-range scaled multiples (imm12 edges included), sometimes a negative or
// unaligned value in the unscaled imm9 range, which both assemblers route
// to LDUR/STUR.
func scaledOffset(r *rand.Rand, size int) int64 {
	max := int64(4095 * size)
	switch r.Intn(4) {
	case 0: // scaled edges
		edges := []int64{0, int64(size), max, max - int64(size), 256, 504, 512}
		return edges[r.Intn(len(edges))]
	case 1: // random scaled
		return int64(r.Intn(4096)) * int64(size)
	default: // unscaled territory: imm9 edges and random
		edges := []int64{-256, -255, -1, 255}
		if size > 1 {
			edges = append(edges, 1, int64(size)-1, -int64(size))
		}
		if r.Intn(2) == 0 {
			return edges[r.Intn(len(edges))]
		}
		return int64(r.Intn(512)) - 256
	}
}

// imm9 draws a writeback/unscaled offset, weighted to the -256/255 edges.
func imm9(r *rand.Rand) int64 {
	edge := []int64{-256, -255, -8, -1, 0, 1, 8, 254, 255}
	if r.Intn(2) == 0 {
		return edge[r.Intn(len(edge))]
	}
	return int64(r.Intn(512)) - 256
}

// pairOffset draws an stp/ldp offset: a multiple of the register size in
// the scaled imm7 range, edges included.
func pairOffset(r *rand.Rand, size int) int64 {
	lo, hi := -64*int64(size), 63*int64(size)
	edge := []int64{lo, hi, 0, int64(size), -int64(size)}
	if r.Intn(2) == 0 {
		return edge[r.Intn(len(edge))]
	}
	return int64(r.Intn(128)-64) * int64(size)
}

// fpImmPool holds VFP-imm8-encodable literals, spelled as fmov accepts them.
var fpImmPool = []string{"1.0", "2.0", "-2.0", "0.5", "-0.5", "1.5", "2.5",
	"3.0", "4.0", "-4.5", "5.0", "8.0", "10.0", "16.0", "31.0", "0.125",
	"-0.125", "0.25", "-1.0", "17.0", "-31.0", "0.1328125"}

// movImm draws an immediate `mov Rd, #v` encodes in one instruction:
// single-lane movz or movn material, or a bitmask pattern. (Multi-lane
// movz+movk synthesis is an extension over GNU as — gas refuses those — so
// it is pinned by TestMovImmMovzMovk instead of fuzzed here.)
func movImm(r *rand.Rand, is64 bool) int64 {
	lane := int64(r.Intn(1 << 16))
	shifts := []uint{0, 16}
	if is64 {
		shifts = append(shifts, 32, 48)
	}
	sh := shifts[r.Intn(len(shifts))]
	switch r.Intn(3) {
	case 0:
		return lane << sh
	case 1: // movn material
		v := ^(lane << sh)
		if !is64 {
			v = int64(int32(uint32(v)))
		}
		return v
	default:
		v := bitmaskImm(r, is64)
		if !is64 {
			return int64(int32(uint32(v)))
		}
		return int64(v)
	}
}

// ---------------------------------------------------------------------------
// Form inventory.

func a64Forms() []a64Form {
	line := func(format string, args ...any) string {
		return "\t" + fmt.Sprintf(format, args...) + "\n"
	}
	sfPair := func(r *rand.Rand) (bool, string, string, string) {
		is64 := r.Intn(2) == 0
		return is64, gp(r, is64), gp(r, is64), gp(r, is64)
	}
	return []a64Form{
		{name: "addsub_imm", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"add", "sub", "adds", "subs"})
			is64 := r.Intn(2) == 0
			rd, rn := gp(r, is64), gp(r, is64)
			if is64 && (mnem == "add" || mnem == "sub") && r.Intn(5) == 0 {
				rd, rn = "sp", "sp"
			}
			v, shifted := imm12(r)
			if shifted && r.Intn(2) == 0 {
				return line("%s %s, %s, #%d, lsl #12", mnem, rd, rn, v>>12)
			}
			return line("%s %s, %s, #%d", mnem, rd, rn, v)
		}},
		{name: "addsub_shifted", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"add", "sub", "adds", "subs"})
			is64, rd, rn, rm := sfPair(r)
			if r.Intn(2) == 0 {
				return line("%s %s, %s, %s", mnem, rd, rn, rm)
			}
			width := 32
			if is64 {
				width = 64
			}
			return line("%s %s, %s, %s, %s #%d", mnem, rd, rn, rm,
				a64pick(r, []string{"lsl", "lsr", "asr"}), r.Intn(width))
		}},
		{name: "addsub_extended", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"add", "sub"})
			is64 := r.Intn(2) == 0
			exts := []string{"uxtb", "uxth", "uxtw", "sxtb", "sxth", "sxtw"}
			if is64 {
				exts = append(exts, "uxtx", "sxtx")
			}
			ext := a64pick(r, exts)
			rm := wr(r)
			if strings.HasSuffix(ext, "x") {
				rm = xr(r)
			}
			rd, rn := gp(r, is64), gp(r, is64)
			if is64 && r.Intn(4) == 0 {
				rn = "sp"
			}
			if r.Intn(2) == 0 {
				return line("%s %s, %s, %s, %s", mnem, rd, rn, rm, ext)
			}
			return line("%s %s, %s, %s, %s #%d", mnem, rd, rn, rm, ext, r.Intn(5))
		}},
		{name: "logical_imm", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"and", "orr", "eor", "ands", "tst"})
			is64 := r.Intn(2) == 0
			v := bitmaskImm(r, is64)
			imm := fmt.Sprintf("#0x%x", v)
			if !is64 {
				imm = fmt.Sprintf("#0x%x", uint32(v))
			}
			if mnem == "tst" {
				return line("tst %s, %s", gp(r, is64), imm)
			}
			return line("%s %s, %s, %s", mnem, gp(r, is64), gp(r, is64), imm)
		}},
		{name: "logical_shifted", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"and", "orr", "eor", "ands", "bic", "bics", "orn", "eon"})
			is64, rd, rn, rm := sfPair(r)
			width := 32
			if is64 {
				width = 64
			}
			if r.Intn(2) == 0 {
				return line("%s %s, %s, %s", mnem, rd, rn, rm)
			}
			return line("%s %s, %s, %s, %s #%d", mnem, rd, rn, rm,
				a64pick(r, []string{"lsl", "lsr", "asr", "ror"}), r.Intn(width))
		}},
		{name: "logical_aliases", gen: func(r *rand.Rand, _ int) string {
			is64, rd, rn, _ := sfPair(r)
			width := 32
			if is64 {
				width = 64
			}
			sh := fmt.Sprintf(", %s #%d", a64pick(r, []string{"lsl", "lsr", "asr"}), r.Intn(width))
			if r.Intn(2) == 0 {
				sh = ""
			}
			switch r.Intn(4) {
			case 0:
				return line("tst %s, %s%s", rd, rn, sh)
			case 1:
				return line("mvn %s, %s%s", rd, rn, sh)
			case 2:
				return line("neg %s, %s%s", rd, rn, sh)
			default:
				return line("negs %s, %s", rd, rn)
			}
		}},
		{name: "muldiv", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(4) {
			case 0:
				_, rd, rn, rm := sfPair(r)
				return line("%s %s, %s, %s", a64pick(r, []string{"mul", "udiv", "sdiv"}), rd, rn, rm)
			case 1:
				return line("%s %s, %s, %s", a64pick(r, []string{"umulh", "smulh"}), xr(r), xr(r), xr(r))
			case 2:
				is64, rd, rn, rm := sfPair(r)
				return line("%s %s, %s, %s, %s", a64pick(r, []string{"madd", "msub"}), rd, rn, rm, gp(r, is64))
			default:
				if r.Intn(3) == 0 {
					return line("%s %s, %s, %s", a64pick(r, []string{"smull", "umull"}), xr(r), wr(r), wr(r))
				}
				return line("%s %s, %s, %s, %s",
					a64pick(r, []string{"smaddl", "umaddl", "smsubl", "umsubl"}), xr(r), wr(r), wr(r), xr(r))
			}
		}},
		{name: "carry", gen: func(r *rand.Rand, _ int) string {
			_, rd, rn, rm := sfPair(r)
			if r.Intn(4) == 0 {
				return line("%s %s, %s", a64pick(r, []string{"ngc", "ngcs"}), rd, rn)
			}
			return line("%s %s, %s, %s", a64pick(r, fam("carry", "ngc", "ngcs")), rd, rn, rm)
		}},
		{name: "condsel", gen: func(r *rand.Rand, _ int) string {
			is64, rd, rn, rm := sfPair(r)
			cond := a64pick(r, a64Conds)
			switch r.Intn(4) {
			case 0:
				return line("%s %s, %s, %s, %s", a64pick(r, []string{"csel", "csinc", "csinv", "csneg"}), rd, rn, rm, cond)
			case 1:
				return line("%s %s, %s", a64pick(r, []string{"cset", "csetm"}), rd, cond)
			case 2:
				return line("%s %s, %s, %s", a64pick(r, []string{"cinc", "cinv", "cneg"}), rd, rn, cond)
			default:
				_ = is64
				return line("%s %s, %s, %s, %s", a64pick(r, []string{"csinc", "csinv", "csneg"}), rd, rn, rm, cond)
			}
		}},
		{name: "ccmp_ccmn", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"ccmp", "ccmn"})
			_, rn, rm, _ := sfPair(r)
			nzcv := r.Intn(16)
			cond := a64pick(r, a64Conds)
			if r.Intn(2) == 0 {
				return line("%s %s, #%d, #%d, %s", mnem, rn, r.Intn(32), nzcv, cond)
			}
			return line("%s %s, %s, #%d, %s", mnem, rn, rm, nzcv, cond)
		}},
		{name: "bitfield", gen: func(r *rand.Rand, _ int) string {
			is64, rd, rn, _ := sfPair(r)
			width := 32
			if is64 {
				width = 64
			}
			lsb := r.Intn(width)
			w := 1 + r.Intn(width-lsb)
			return line("%s %s, %s, #%d, #%d",
				a64pick(r, append(fam("bfx"), fam("bitfield")...)), rd, rn, lsb, w)
		}},
		{name: "extr_ror", gen: func(r *rand.Rand, _ int) string {
			is64, rd, rn, rm := sfPair(r)
			width := 32
			if is64 {
				width = 64
			}
			switch r.Intn(3) {
			case 0:
				return line("extr %s, %s, %s, #%d", rd, rn, rm, r.Intn(width))
			case 1:
				return line("ror %s, %s, #%d", rd, rn, r.Intn(width))
			default:
				return line("ror %s, %s, %s", rd, rn, rm)
			}
		}},
		{name: "shift_reg_imm", gen: func(r *rand.Rand, _ int) string {
			is64, rd, rn, rm := sfPair(r)
			mnem := a64pick(r, fam("shift"))
			width := 32
			if is64 {
				width = 64
			}
			if r.Intn(2) == 0 {
				return line("%s %s, %s, %s", mnem, rd, rn, rm)
			}
			return line("%s %s, %s, #%d", mnem, rd, rn, r.Intn(width))
		}},
		{name: "extend_bitops", gen: func(r *rand.Rand, _ int) string {
			_, rd, rn, _ := sfPair(r)
			switch r.Intn(4) {
			case 0:
				if r.Intn(3) == 0 {
					return line("sxtw %s, %s", xr(r), wr(r))
				}
				mnem := a64pick(r, []string{"sxtb", "sxth"})
				if r.Intn(2) == 0 {
					return line("%s %s, %s", mnem, xr(r), wr(r))
				}
				return line("%s %s, %s", mnem, wr(r), wr(r))
			case 1:
				return line("%s %s, %s", a64pick(r, []string{"uxtb", "uxth"}), wr(r), wr(r))
			case 2:
				if r.Intn(3) == 0 {
					return line("rev32 %s, %s", xr(r), xr(r))
				}
				return line("%s %s, %s", a64pick(r, []string{"rev", "rev16"}), rd, rn)
			default:
				return line("%s %s, %s", a64pick(r, []string{"clz", "cls", "rbit"}), rd, rn)
			}
		}},
		{name: "mov_imm", gen: func(r *rand.Rand, _ int) string {
			is64 := r.Intn(2) == 0
			return line("mov %s, #%d", gp(r, is64), movImm(r, is64))
		}},
		{name: "movz_movk_movn", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"movz", "movk", "movn"})
			is64 := r.Intn(2) == 0
			shifts := []int{0, 16}
			if is64 {
				shifts = []int{0, 16, 32, 48}
			}
			imm := r.Intn(1 << 16)
			if r.Intn(2) == 0 {
				return line("%s %s, #%d", mnem, gp(r, is64), imm)
			}
			return line("%s %s, #%d, lsl #%d", mnem, gp(r, is64), imm, shifts[r.Intn(len(shifts))])
		}},
		{name: "mov_reg_cmp", gen: func(r *rand.Rand, _ int) string {
			is64, rd, rn, rm := sfPair(r)
			switch r.Intn(5) {
			case 0:
				return line("mov %s, %s", rd, rn)
			case 1:
				if is64 {
					if r.Intn(2) == 0 {
						return line("mov sp, %s", xr(r))
					}
					return line("mov %s, sp", xr(r))
				}
				return line("mov %s, wzr", wr(r))
			case 2:
				v, shifted := imm12(r)
				_ = shifted
				return line("%s %s, #%d", a64pick(r, []string{"cmp", "cmn"}), rn, v)
			case 3:
				return line("cmp %s, %s", rn, rm)
			default:
				return line("cmn %s, %s", rn, rm)
			}
		}},
		{name: "ldst_scaled", gen: func(r *rand.Rand, _ int) string {
			type sh struct {
				ld, st string
				reg    func(*rand.Rand) string
				size   int
			}
			shapes := []sh{
				{"ldr", "str", xr, 8}, {"ldr", "str", wr, 4},
				{"ldrh", "strh", wr, 2}, {"ldrb", "strb", wr, 1},
			}
			s := shapes[r.Intn(len(shapes))]
			mnem := s.ld
			if r.Intn(2) == 0 {
				mnem = s.st
			}
			if r.Intn(4) == 0 {
				return line("%s %s, [%s]", mnem, s.reg(r), baseReg(r))
			}
			return line("%s %s, [%s, #%d]", mnem, s.reg(r), baseReg(r), scaledOffset(r, s.size))
		}},
		{name: "ldst_writeback", gen: func(r *rand.Rand, _ int) string {
			mnems := []string{"ldr", "str", "ldrb", "strb", "ldrh", "strh"}
			mnem := a64pick(r, mnems)
			// Writeback with Rt aliasing the base is architecturally
			// unpredictable (llvm-mc rejects it), so keep them distinct.
			rtn := r.Intn(31)
			base := baseReg(r)
			for fmt.Sprintf("x%d", rtn) == base {
				rtn = r.Intn(31)
			}
			rt := fmt.Sprintf("w%d", rtn)
			if (mnem == "ldr" || mnem == "str") && r.Intn(2) == 0 {
				rt = fmt.Sprintf("x%d", rtn)
			}
			if r.Intn(2) == 0 {
				return line("%s %s, [%s], #%d", mnem, rt, base, imm9(r))
			}
			return line("%s %s, [%s, #%d]!", mnem, rt, base, imm9(r))
		}},
		{name: "ldur_stur", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(3) {
			case 0:
				rt := xr(r)
				if r.Intn(2) == 0 {
					rt = wr(r)
				}
				return line("%s %s, [%s, #%d]", a64pick(r, []string{"ldur", "stur"}), rt, baseReg(r), imm9(r))
			case 1:
				return line("%s %s, [%s, #%d]", a64pick(r, []string{"ldurb", "sturb", "ldurh", "sturh"}), wr(r), baseReg(r), imm9(r))
			default:
				mnem := a64pick(r, []string{"ldursb", "ldursh"})
				rt := xr(r)
				if r.Intn(2) == 0 {
					rt = wr(r)
				}
				if r.Intn(3) == 0 {
					return line("ldursw %s, [%s, #%d]", xr(r), baseReg(r), imm9(r))
				}
				return line("%s %s, [%s, #%d]", mnem, rt, baseReg(r), imm9(r))
			}
		}},
		{name: "ldst_signed", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(3) {
			case 0:
				rt := xr(r)
				if r.Intn(2) == 0 {
					rt = wr(r)
				}
				return line("ldrsb %s, [%s, #%d]", rt, baseReg(r), scaledOffset(r, 1))
			case 1:
				rt := xr(r)
				if r.Intn(2) == 0 {
					rt = wr(r)
				}
				return line("ldrsh %s, [%s, #%d]", rt, baseReg(r), scaledOffset(r, 2))
			default:
				return line("ldrsw %s, [%s, #%d]", xr(r), baseReg(r), scaledOffset(r, 4))
			}
		}},
		{name: "ldst_regoff", gen: func(r *rand.Rand, _ int) string {
			type sh struct {
				ld, st string
				reg    func(*rand.Rand) string
				amt    int
			}
			shapes := []sh{
				{"ldr", "str", xr, 3}, {"ldr", "str", wr, 2},
				{"ldrh", "strh", wr, 1}, {"ldrb", "strb", wr, 0},
			}
			s := shapes[r.Intn(len(shapes))]
			mnem := s.ld
			if r.Intn(2) == 0 {
				mnem = s.st
			}
			base := baseReg(r)
			switch r.Intn(4) {
			case 0:
				return line("%s %s, [%s, %s]", mnem, s.reg(r), base, xr(r))
			case 1:
				if s.amt == 0 {
					return line("%s %s, [%s, %s]", mnem, s.reg(r), base, xr(r))
				}
				return line("%s %s, [%s, %s, lsl #%d]", mnem, s.reg(r), base, xr(r), s.amt)
			case 2:
				ext := a64pick(r, []string{"uxtw", "sxtw"})
				if s.amt > 0 && r.Intn(2) == 0 {
					return line("%s %s, [%s, %s, %s #%d]", mnem, s.reg(r), base, wr(r), ext, s.amt)
				}
				return line("%s %s, [%s, %s, %s]", mnem, s.reg(r), base, wr(r), ext)
			default:
				if s.amt > 0 && r.Intn(2) == 0 {
					return line("%s %s, [%s, %s, sxtx #%d]", mnem, s.reg(r), base, xr(r), s.amt)
				}
				return line("%s %s, [%s, %s, sxtx]", mnem, s.reg(r), base, xr(r))
			}
		}},
		{name: "pairs", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"stp", "ldp"})
			// Rt2==Rt is architecturally unpredictable for ldp, as is a
			// writeback base aliasing either transfer register (llvm-mc
			// rejects both), so all three numbers stay distinct.
			n1, n2, bn := r.Intn(31), r.Intn(31), r.Intn(29)
			for n2 == n1 {
				n2 = r.Intn(31)
			}
			for bn == n1 || bn == n2 {
				bn = r.Intn(29)
			}
			base := fmt.Sprintf("x%d", bn)
			if r.Intn(4) == 0 {
				base = "sp"
			}
			var r1, r2 string
			size := 8
			switch r.Intn(3) {
			case 0:
				r1, r2 = fmt.Sprintf("x%d", n1), fmt.Sprintf("x%d", n2)
			case 1:
				r1, r2, size = fmt.Sprintf("w%d", n1), fmt.Sprintf("w%d", n2), 4
			default:
				r1, r2 = fmt.Sprintf("d%d", n1), fmt.Sprintf("d%d", n2)
			}
			off := pairOffset(r, size)
			switch r.Intn(4) {
			case 0:
				return line("%s %s, %s, [%s]", mnem, r1, r2, base)
			case 1:
				return line("%s %s, %s, [%s, #%d]", mnem, r1, r2, base, off)
			case 2:
				return line("%s %s, %s, [%s, #%d]!", mnem, r1, r2, base, off)
			default:
				return line("%s %s, %s, [%s], #%d", mnem, r1, r2, base, off)
			}
		}},
		{name: "fp_ldst", gen: func(r *rand.Rand, _ int) string {
			mnem := a64pick(r, []string{"ldr", "str"})
			rt, size := dr(r), 8
			if r.Intn(2) == 0 {
				rt, size = sr(r), 4
			}
			base := baseReg(r)
			switch r.Intn(5) {
			case 0:
				return line("%s %s, [%s]", mnem, rt, base)
			case 1:
				return line("%s %s, [%s, #%d]", mnem, rt, base, scaledOffset(r, size))
			case 2:
				return line("%s %s, [%s], #%d", mnem, rt, base, imm9(r))
			case 3:
				return line("%s %s, [%s, #%d]!", mnem, rt, base, imm9(r))
			default:
				return line("%s %s, [%s, #%d]", a64pick(r, []string{"ldur", "stur"}), rt, base, imm9(r))
			}
		}},
		{name: "fp_arith", gen: func(r *rand.Rand, _ int) string {
			f := dr
			if r.Intn(2) == 0 {
				f = sr
			}
			switch r.Intn(4) {
			case 0:
				return line("%s %s, %s, %s", a64pick(r, fam("fp3")), f(r), f(r), f(r))
			case 1:
				return line("%s %s, %s, %s, %s", a64pick(r, fam("fp4")), f(r), f(r), f(r), f(r))
			case 2:
				return line("%s %s, %s", a64pick(r, fam("funary")), f(r), f(r))
			default:
				return line("fcsel %s, %s, %s, %s", f(r), f(r), f(r), a64pick(r, a64Conds))
			}
		}},
		{name: "fp_cmp", gen: func(r *rand.Rand, _ int) string {
			f := dr
			if r.Intn(2) == 0 {
				f = sr
			}
			mnem := a64pick(r, []string{"fcmp", "fcmpe"})
			switch r.Intn(3) {
			case 0:
				return line("%s %s, %s", mnem, f(r), f(r))
			case 1:
				return line("%s %s, #0.0", mnem, f(r))
			default:
				return line("fccmp %s, %s, #%d, %s", f(r), f(r), r.Intn(16), a64pick(r, a64Conds))
			}
		}},
		{name: "fp_mov_cvt", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(6) {
			case 0:
				if r.Intn(2) == 0 {
					return line("fmov %s, %s", dr(r), dr(r))
				}
				return line("fmov %s, %s", sr(r), sr(r))
			case 1:
				switch r.Intn(4) {
				case 0:
					return line("fmov %s, %s", dr(r), xr(r))
				case 1:
					return line("fmov %s, %s", xr(r), dr(r))
				case 2:
					return line("fmov %s, %s", sr(r), wr(r))
				default:
					return line("fmov %s, %s", wr(r), sr(r))
				}
			case 2:
				return line("fmov %s, #%s", dr(r), a64pick(r, fpImmPool))
			case 3:
				if r.Intn(2) == 0 {
					return line("fcvt %s, %s", dr(r), sr(r))
				}
				return line("fcvt %s, %s", sr(r), dr(r))
			case 4:
				fp, i := dr(r), xr(r)
				if r.Intn(2) == 0 {
					fp = sr(r)
				}
				if r.Intn(2) == 0 {
					i = wr(r)
				}
				return line("%s %s, %s", a64pick(r, []string{"scvtf", "ucvtf"}), fp, i)
			default:
				fp, i := dr(r), xr(r)
				if r.Intn(2) == 0 {
					fp = sr(r)
				}
				if r.Intn(2) == 0 {
					i = wr(r)
				}
				return line("%s %s, %s", a64pick(r, []string{"fcvtzs", "fcvtzu"}), i, fp)
			}
		}},
		{name: "atomics", gen: func(r *rand.Rand, _ int) string {
			base := fmt.Sprintf("x%d", r.Intn(29))
			switch r.Intn(5) {
			case 0:
				rt := xr(r)
				if r.Intn(2) == 0 {
					rt = wr(r)
				}
				return line("%s %s, [%s]", a64pick(r, []string{"ldxr", "ldaxr", "ldar", "stlr"}), rt, base)
			case 1:
				return line("%s %s, [%s]",
					a64pick(r, []string{"ldxrb", "ldaxrb", "ldxrh", "ldaxrh", "ldarb", "ldarh", "stlrb", "stlrh"}), wr(r), base)
			case 2, 3:
				// The status register must not alias the source or the
				// base: llvm-mc rejects that as unpredictable (gas only
				// warns), so keep all three distinct.
				ws, rtn, bn := r.Intn(29), r.Intn(29), r.Intn(29)
				for rtn == ws {
					rtn = r.Intn(29)
				}
				for bn == ws || bn == rtn {
					bn = r.Intn(29)
				}
				if r.Intn(2) == 0 {
					rt := fmt.Sprintf("x%d", rtn)
					if r.Intn(2) == 0 {
						rt = fmt.Sprintf("w%d", rtn)
					}
					return line("%s w%d, %s, [x%d]", a64pick(r, []string{"stxr", "stlxr"}), ws, rt, bn)
				}
				return line("%s w%d, w%d, [x%d]",
					a64pick(r, []string{"stxrb", "stlxrb", "stxrh", "stlxrh"}), ws, rtn, bn)
			default:
				return line("%s", a64pick(r, []string{"dmb ish", "dsb sy", "isb", "nop"}))
			}
		}},
		{name: "neon_bytes", gen: func(r *rand.Rand, _ int) string {
			arr := a64pick(r, []string{"8b", "16b"})
			switch r.Intn(7) {
			case 0:
				return line("dup v%d.%s, %s", r.Intn(32), arr, wr(r))
			case 1:
				return line("ld1 {v%d.%s}, [%s]", r.Intn(32), arr, fmt.Sprintf("x%d", r.Intn(29)))
			case 2:
				return line("cmeq v%d.%s, v%d.%s, v%d.%s", r.Intn(32), arr, r.Intn(32), arr, r.Intn(32), arr)
			case 3:
				return line("cmlt v%d.%s, v%d.%s, #0", r.Intn(32), arr, r.Intn(32), arr)
			case 4:
				return line("shrn v%d.8b, v%d.8h, #%d", r.Intn(32), r.Intn(32), 1+r.Intn(8))
			case 5:
				return line("umov %s, v%d.b[%d]", wr(r), r.Intn(32), r.Intn(16))
			default:
				if r.Intn(2) == 0 {
					return line("cnt v%d.%s, v%d.%s", r.Intn(32), arr, r.Intn(32), arr)
				}
				return line("addv b%d, v%d.%s", r.Intn(32), r.Intn(32), arr)
			}
		}},
		{name: "sysregs", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(3) {
			case 0:
				return line("mrs %s, %s", xr(r),
					a64pick(r, []string{"cntvct_el0", "cntfrq_el0", "fpcr", "fpsr", "dczid_el0", "tpidr_el0"}))
			case 1:
				return line("msr %s, %s", a64pick(r, []string{"tpidr_el0", "fpsr", "fpcr"}), xr(r))
			default:
				return line("%s", a64pick(r, []string{"svc #0", "brk #1", "ret", "br x3", "blr x9"}))
			}
		}},
		{name: "branches", multi: true, gen: func(r *rand.Rand, i int) string {
			back := fmt.Sprintf("af%d_a", i)
			fwd := fmt.Sprintf("af%d_b", i)
			pad := func() string { return strings.Repeat("\tnop\n", r.Intn(40)) }
			branch := func(target string) string {
				switch r.Intn(6) {
				case 0:
					return "\tb " + target + "\n"
				case 1:
					return "\tbl " + target + "\n"
				case 2:
					return fmt.Sprintf("\tb.%s %s\n", a64pick(r, a64Conds), target)
				case 3:
					rt := xr(r)
					if r.Intn(2) == 0 {
						rt = wr(r)
					}
					return fmt.Sprintf("\t%s %s, %s\n", a64pick(r, []string{"cbz", "cbnz"}), rt, target)
				default:
					bit := r.Intn(64)
					rt := xr(r)
					if r.Intn(2) == 0 {
						rt = wr(r)
						bit = r.Intn(32)
					}
					return fmt.Sprintf("\t%s %s, #%d, %s\n", a64pick(r, []string{"tbz", "tbnz"}), rt, bit, target)
				}
			}
			var b strings.Builder
			b.WriteString(back + ":\n")
			b.WriteString(pad())
			b.WriteString(branch(back))
			b.WriteString(branch(fwd))
			b.WriteString(pad())
			b.WriteString(fwd + ":\n\tret\n")
			return b.String()
		}},

		// ---- NEON / Advanced SIMD -------------------------------------
		// The arrangement is half the encoding (Q plus the size field), and
		// which arrangements a mnemonic admits varies per row, so each form
		// draws from exactly the set its mnemonics accept. A wrong set shows
		// up immediately as gas refusing the line rather than as a silent
		// byte difference.
		{name: "neon_3same_int", gen: func(r *rand.Rand, _ int) string {
			// Integer three-register-same over every arrangement including 2d.
			return v3same(r, a64pick(r, []string{
				"add", "sub", "cmeq", "cmgt", "cmge", "cmhi", "cmhs", "cmtst",
				"sshl", "ushl",
			}), a64arr(r, a64ArrFull))
		}},
		{name: "neon_3same_nod", gen: func(r *rand.Rand, _ int) string {
			// The same shape for the mnemonics with no 64-bit element form.
			return v3same(r, a64pick(r, []string{
				"mul", "smax", "smin", "umax", "umin",
			}), a64arr(r, a64ArrBHS))
		}},
		{name: "neon_3same_logical", gen: func(r *rand.Rand, _ int) string {
			// The bitwise row is byte-arrangement only: the operation is
			// element-size-independent, so gas admits 8b/16b and nothing else.
			return v3same(r, a64pick(r, []string{"and", "orr", "eor", "bic", "orn"}),
				a64arr(r, a64ArrB))
		}},
		{name: "neon_3same_fp", gen: func(r *rand.Rand, _ int) string {
			return v3same(r, a64pick(r, []string{
				"fadd", "fsub", "fmul", "fdiv", "fmax", "fmin",
				"fcmeq", "fcmge", "fcmgt",
			}), a64arr(r, a64ArrFP))
		}},
		{name: "neon_2misc_int", gen: func(r *rand.Rand, _ int) string {
			switch r.Intn(4) {
			case 0:
				return v2same(r, a64pick(r, []string{"abs", "neg"}), a64arr(r, a64ArrFull))
			case 1:
				return v2same(r, a64pick(r, []string{"cnt", "not", "mvn", "rev16"}), a64arr(r, a64ArrB))
			case 2:
				// rev32 reverses 32-bit containers, so its elements are
				// narrower than a word: bytes and halfwords only.
				return v2same(r, "rev32", a64arr(r, append(append([]a64Arr{}, a64ArrB...), a64ArrH...)))
			default:
				// rev64 likewise stops one element size short of the container.
				return v2same(r, "rev64", a64arr(r, a64ArrBHS))
			}
		}},
		{name: "neon_2misc_fp", gen: func(r *rand.Rand, _ int) string {
			return v2same(r, a64pick(r, []string{
				"fabs", "fneg", "fsqrt", "fcvtzs", "fcvtzu", "scvtf", "ucvtf",
			}), a64arr(r, a64ArrFP))
		}},
		{name: "neon_fcmp_zero", gen: func(r *rand.Rand, _ int) string {
			// The FP compares have no two-operand form: against zero they
			// take a literal #0.0, which is a distinct encoding from both the
			// register compare and the integer #0 form above.
			a := a64arr(r, a64ArrFP)
			return fmt.Sprintf("\t%s %s.%s, %s.%s, #0.0\n",
				a64pick(r, []string{"fcmeq", "fcmge", "fcmgt", "fcmle", "fcmlt"}),
				vr(r), a.spell, vr(r), a.spell)
		}},
		{name: "neon_cmp_zero", gen: func(r *rand.Rand, _ int) string {
			// The compare-against-zero forms take a literal #0 rather than a
			// second vector, and are a separate encoding from the register
			// compares above.
			mnem := a64pick(r, []string{"cmeq", "cmgt", "cmge", "cmle", "cmlt"})
			a := a64arr(r, a64ArrFull)
			return fmt.Sprintf("\t%s %s.%s, %s.%s, #0\n", mnem, vr(r), a.spell, vr(r), a.spell)
		}},
		{name: "neon_shift_imm", gen: func(r *rand.Rand, _ int) string {
			a := a64arr(r, a64ArrFull)
			// Left shifts take 0..esize-1; right shifts take 1..esize. Off by
			// one either way is a different (or invalid) encoding.
			if mnem := a64pick(r, []string{"shl", "sli"}); r.Intn(2) == 0 {
				return fmt.Sprintf("\t%s %s.%s, %s.%s, #%d\n", mnem, vr(r), a.spell, vr(r), a.spell, r.Intn(a.elem))
			}
			mnem := a64pick(r, []string{"sshr", "ushr", "sri"})
			return fmt.Sprintf("\t%s %s.%s, %s.%s, #%d\n", mnem, vr(r), a.spell, vr(r), a.spell, 1+r.Intn(a.elem))
		}},
		{name: "neon_permute", gen: func(r *rand.Rand, _ int) string {
			return v3same(r, a64pick(r, []string{"zip1", "zip2", "uzp1", "uzp2", "trn1", "trn2"}),
				a64arr(r, a64ArrFull))
		}},
		{name: "neon_across", gen: func(r *rand.Rand, _ int) string {
			// Across-lanes reduces a vector to a SCALAR whose width is the
			// element width (addv/…) or twice it (the widening saddlv/uaddlv).
			// .2s is excluded throughout: reducing two 32-bit lanes of a
			// 64-bit vector has no encoding, and gas refuses it too.
			a := a64arr(r, []a64Arr{{"8b", 8, 8}, {"16b", 8, 16}, {"4h", 16, 4}, {"8h", 16, 8}, {"4s", 32, 4}})
			if r.Intn(2) == 0 {
				scalar := map[int]string{8: "b", 16: "h", 32: "s"}[a.elem]
				return fmt.Sprintf("\t%s %s%d, %s.%s\n",
					a64pick(r, []string{"addv", "smaxv", "sminv", "umaxv", "uminv"}),
					scalar, r.Intn(32), vr(r), a.spell)
			}
			wide := map[int]string{8: "h", 16: "s", 32: "d"}[a.elem]
			return fmt.Sprintf("\t%s %s%d, %s.%s\n",
				a64pick(r, []string{"saddlv", "uaddlv"}), wide, r.Intn(32), vr(r), a.spell)
		}},
		{name: "neon_dup_ins_mov", gen: func(r *rand.Rand, _ int) string {
			a := a64arr(r, a64ArrFull)
			elemTag := map[int]string{8: "b", 16: "h", 32: "s", 64: "d"}[a.elem]
			switch r.Intn(4) {
			case 0: // dup from a general register — w for the narrow sizes.
				src := wr(r)
				if a.elem == 64 {
					src = xr(r)
				}
				return fmt.Sprintf("\tdup %s.%s, %s\n", vr(r), a.spell, src)
			case 1: // dup from a lane
				return fmt.Sprintf("\tdup %s.%s, %s.%s[%d]\n", vr(r), a.spell, vr(r), elemTag, r.Intn(a.lanes))
			case 2: // ins lane <- lane
				return fmt.Sprintf("\tins %s.%s[%d], %s.%s[%d]\n",
					vr(r), elemTag, r.Intn(a.lanes), vr(r), elemTag, r.Intn(a.lanes))
			default: // umov / smov out to a general register
				if a.elem == 64 {
					return fmt.Sprintf("\tumov %s, %s.d[%d]\n", xr(r), vr(r), r.Intn(2))
				}
				if r.Intn(2) == 0 {
					return fmt.Sprintf("\tumov %s, %s.%s[%d]\n", wr(r), vr(r), elemTag, r.Intn(a.lanes))
				}
				// smov sign-extends, so its destination must be STRICTLY
				// wider than the element: a .s lane goes only to an x, and
				// there is no smov out of a .d lane at all (nothing is wider).
				dst := xr(r)
				if a.elem < 32 && r.Intn(2) == 0 {
					dst = wr(r)
				}
				return fmt.Sprintf("\tsmov %s, %s.%s[%d]\n", dst, vr(r), elemTag, r.Intn(a.lanes))
			}
		}},
		{name: "neon_ext_tbl", gen: func(r *rand.Rand, _ int) string {
			a := a64arr(r, a64ArrB)
			if r.Intn(2) == 0 {
				return fmt.Sprintf("\text %s.%s, %s.%s, %s.%s, #%d\n",
					vr(r), a.spell, vr(r), a.spell, vr(r), a.spell, r.Intn(a.lanes))
			}
			return fmt.Sprintf("\ttbl %s.%s, {%s.16b}, %s.%s\n", vr(r), a.spell, vr(r), vr(r), a.spell)
		}},
		{name: "neon_ldst1", gen: func(r *rand.Rand, _ int) string {
			a := a64arr(r, a64ArrFull)
			if r.Intn(4) == 0 {
				elemTag := map[int]string{8: "b", 16: "h", 32: "s", 64: "d"}[a.elem]
				_ = elemTag
				return fmt.Sprintf("\tld1r {%s.%s}, [%s]\n", vr(r), a.spell, baseReg(r))
			}
			return fmt.Sprintf("\t%s {%s.%s}, [%s]\n",
				a64pick(r, []string{"ld1", "st1"}), vr(r), a.spell, baseReg(r))
		}},
		{name: "neon_narrow_widen", gen: func(r *rand.Rand, _ int) string {
			// The narrowing and widening pairs, whose two operands carry
			// DIFFERENT arrangements — the shape most likely to get one of
			// the two size fields wrong.
			narrow := []a64Arr{{"8b", 8, 8}, {"4h", 16, 4}, {"2s", 32, 2}}
			wide := map[string]string{"8b": "8h", "4h": "4s", "2s": "2d"}
			n := narrow[r.Intn(len(narrow))]
			switch r.Intn(3) {
			case 0:
				return fmt.Sprintf("\txtn %s.%s, %s.%s\n", vr(r), n.spell, vr(r), wide[n.spell])
			case 1:
				return fmt.Sprintf("\tshrn %s.%s, %s.%s, #%d\n",
					vr(r), n.spell, vr(r), wide[n.spell], 1+r.Intn(n.elem))
			default:
				if r.Intn(2) == 0 {
					return fmt.Sprintf("\t%s %s.%s, %s.%s, #%d\n",
						a64pick(r, []string{"sshll", "ushll"}), vr(r), wide[n.spell], vr(r), n.spell, r.Intn(n.elem))
				}
				return fmt.Sprintf("\t%s %s.%s, %s.%s\n",
					a64pick(r, []string{"sxtl", "uxtl"}), vr(r), wide[n.spell], vr(r), n.spell)
			}
		}},
	}
}

// TestFuzzEncodingsAgainstGNUAs is the arm64 generator-driven differential;
// see the package comment at the top of this file. On a mismatch the batch
// is minimized to a single unit and printed as a ready-to-pin row.
func TestFuzzEncodingsAgainstGNUAs(t *testing.T) {
	as, objcopy := findBinutils(t)
	seed := a64FuzzSeed(t)
	n := a64FuzzCases()
	for _, f := range a64Forms() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			units := a64FormUnits(f, a64FormRand(seed, f.name), n)
			src := strings.Join(units, "")
			got, err := arm64.Assemble(src)
			if err != nil {
				for _, u := range units {
					if _, uerr := arm64.Assemble(u); uerr != nil {
						t.Fatalf("unit fails to assemble:\n%s error: %v", u, uerr)
					}
				}
				t.Fatalf("Assemble: %v", err)
			}
			want := gnuAsText(t, as, objcopy, src)
			if !bytes.Equal(got, want) {
				a64Minimize(t, f, units, as, objcopy, seed)
			}
		})
	}
}

func a64Minimize(t *testing.T, f a64Form, units []string, as, objcopy string, seed int64) {
	t.Helper()
	for _, u := range units {
		got, err := arm64.Assemble(u)
		if err != nil {
			t.Fatalf("unit stopped assembling alone:\n%s error: %v", u, err)
		}
		want := gnuAsText(t, as, objcopy, u)
		if !bytes.Equal(got, want) {
			t.Fatalf("encoding differs from aarch64-linux-gnu-as (seed %d, form %s) — pin as:\n"+
				"source:\n%s ours: % x\n gas:  % x", seed, f.name, u, got, want)
		}
	}
	t.Fatalf("batch bytes differ but every unit matches alone (form %s, seed %d)", f.name, seed)
}

// bitmaskImm must only produce encodable immediates; pin the generator
// itself so a bad draw is a generator bug, not a confusing gas error.
func TestBitmaskImmGeneratorEncodable(t *testing.T) {
	r := a64FormRand(1, "bitmask-self")
	for i := 0; i < 4096; i++ {
		v := bitmaskImm(r, true)
		if v == 0 || v == ^uint64(0) {
			t.Fatalf("bitmaskImm produced unencodable %#x", v)
		}
		if got := bits.OnesCount64(v); got == 0 || got == 64 {
			t.Fatalf("bitmaskImm produced %#x with %d ones", v, got)
		}
	}
}
