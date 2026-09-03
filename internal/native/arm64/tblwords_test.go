package arm64

import (
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64tbl"
)

// TestEncoderFunctionsMatchTableWords pins the exported per-instruction
// encoders — the ones the code generator calls directly — to the base
// words in internal/native/arm64tbl that the assembler and the self-host
// assembler read. Each side pins its own copy against GNU as; this is what
// keeps the copies one number.
func TestEncoderFunctionsMatchTableWords(t *testing.T) {
	three := map[string]func(rd, rn, rm uint32) uint32{
		"adc": ADC, "adcs": ADCS, "sbc": SBC, "sbcs": SBCS,
		"umulh": UMULH, "smulh": SMULH,
		"fadd": FADD, "fsub": FSUB, "fmul": FMUL, "fdiv": FDIV, "fnmul": FNMUL,
		"fmin": FMIN, "fmax": FMAX, "fminnm": FMINNM, "fmaxnm": FMAXNM,
	}
	four := map[string]func(rd, rn, rm, ra uint32) uint32{
		"madd": MADD, "smaddl": SMADDL, "umaddl": UMADDL, "smsubl": SMSUBL, "umsubl": UMSUBL,
		"fmadd": FMADD, "fmsub": FMSUB, "fnmadd": FNMADD, "fnmsub": FNMSUB,
	}
	shifted := map[string]func(rd, rn, rm, st, amt uint32) uint32{
		"ands": ANDSregShift, "bic": BICregShift, "bics": BICSregShift, "orn": ORNregShift, "eon": EONregShift,
	}
	cond := map[string]func(rd, rn, rm, cond uint32) uint32{
		"csel": CSEL, "csinc": CSINC, "csinv": CSINV, "csneg": CSNEG,
	}
	two := map[string]func(rd, rn uint32) uint32{
		"fneg": FNEG, "fabs": FABS, "fsqrt": FSQRT, "frintm": FRINTM, "frintp": FRINTP,
		"frintz": FRINTZ, "frinta": FRINTA, "frintn": FRINTN,
	}
	check := func(mnem string, got uint32) {
		t.Helper()
		_, op, ok := arm64tbl.FamilyOf(mnem)
		if !ok {
			t.Fatalf("%s is not in arm64tbl.Scalar", mnem)
		}
		if got != op.Word {
			t.Errorf("%s: encoder function gives %08x, table word is %08x", mnem, got, op.Word)
		}
	}
	for m, f := range three {
		check(m, f(0, 0, 0))
	}
	for m, f := range four {
		check(m, f(0, 0, 0, 0))
	}
	for m, f := range shifted {
		check(m, f(0, 0, 0, 0, 0))
	}
	for m, f := range cond {
		check(m, f(0, 0, 0, 0))
	}
	for m, f := range two {
		check(m, f(0, 0))
	}
	check("ccmp", CCMPreg(0, 0, 0, 0))
	check("ccmn", CCMNreg(0, 0, 0, 0))
	// The aliases share their target's word: umulh's word already carries
	// Ra=XZR, so the widening aliases are checked through their full form.
	check("cset", CSINC(0, 0, 0, 0))
	check("csetm", CSINV(0, 0, 0, 0))
	check("cinc", CSINC(0, 0, 0, 0))
	check("cinv", CSINV(0, 0, 0, 0))
	check("cneg", CSNEG(0, 0, 0, 0))
	check("ngc", SBC(0, 31, 0)&^(31<<5))
	check("ngcs", SBCS(0, 31, 0)&^(31<<5))
	check("tst", ANDSregShift(0, 0, 0, 0, 0))
	check("mvn", ORNregShift(0, 0, 0, 0, 0))
	check("smull", SMADDL(0, 0, 0, 0))
	check("umull", UMADDL(0, 0, 0, 0))

	// Every row that carries a word is covered above, so a row added with a
	// word and no encoder function to pin it against fails here.
	pinned := map[string]bool{}
	for _, m := range []map[string]func(rd, rn, rm uint32) uint32{three} {
		for k := range m {
			pinned[k] = true
		}
	}
	for k := range four {
		pinned[k] = true
	}
	for k := range shifted {
		pinned[k] = true
	}
	for k := range cond {
		pinned[k] = true
	}
	for k := range two {
		pinned[k] = true
	}
	for _, k := range []string{"ccmp", "ccmn", "cset", "csetm", "cinc", "cinv", "cneg", "ngc", "ngcs", "tst", "mvn", "smull", "umull"} {
		pinned[k] = true
	}
	for _, f := range arm64tbl.Scalar {
		if f.Base == "" || f.Name == "shll" {
			continue
		}
		for _, o := range f.Ops {
			if !pinned[o.Mnemonic] {
				t.Errorf("%s carries a word in the table but no encoder function pins it", o.Mnemonic)
			}
		}
	}
}
