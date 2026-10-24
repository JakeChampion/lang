package e2ecompiler

import (
	"fmt"
	"testing"
)

func optBoxNonScalarScrutSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    let i: i32 = 0;
    while (i < %d) {
        match (g(i)) { Some(_) => { return 9; }, None => {} }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func optBoxNonScalarBoundSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    let i: i32 = 0;
    while (i < %d) {
        let e: Option[IoError] = g(i);
        match (e) { Some(_) => { return 9; }, None => {} }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func optBoxDeadScalarSrc(rounds int) string {
	return fmt.Sprintf(`function f(x: i32): Option[i32] { if (x > 0) { return Some(x); } return None; }
function main(): i32 {
    let i: i32 = 0;
    while (i < %d) {
        let a: Option[i32] = f(i);
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func optBoxDeadNonScalarSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    let i: i32 = 0;
    while (i < %d) {
        let a: Option[IoError] = g(i);
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func optBoxReturningArmSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function main(): i32 {
    let i: i32 = 0;
    while (i < 1000000) {
        let e: Option[IoError] = g(i);
        match (e) {
            Some(_) => { return 9; },
            None => { i = i + 1; if (i >= %d) { return 0; } }
        }
    }
    return 0;
}`, rounds)
}

func optBoxNestedMatchSrc(rounds int) string {
	return fmt.Sprintf(`function g(x: i32): Option[IoError] { return None; }
function step(i: i32): i32 {
    let e: Option[IoError] = g(i);
    if (i >= 0) { match (e) { Some(_) => { return 9; }, None => {} } }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < %d) {
        if (step(i) != 0) { return 9; }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func TestSelfHostOptBoxReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const (
		few  = 20
		many = 200
	)

	for _, tc := range []struct {
		name string
		src  func(rounds int) string
	}{
		{name: "nonscalar_payload_scrutinee", src: optBoxNonScalarScrutSrc},
		{name: "nonscalar_payload_bound", src: optBoxNonScalarBoundSrc},
		{name: "dead_binding_scalar", src: optBoxDeadScalarSrc},
		{name: "dead_binding_nonscalar", src: optBoxDeadNonScalarSrc},
		{name: "returning_arm", src: optBoxReturningArmSrc},
		{name: "nested_match_fn_level", src: optBoxNestedMatchSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure := func(label string, rounds int) (int64, int64, int64) {
				asm := hevCompile(t, runner, driverBin, tc.src(rounds), []string{"FERN_LEAKCHECK=1"})
				bin := buildBin(t, gcc, dir, tc.name+"_"+label, asm)
				stderr, exit := runWithStdin(t, runner, bin, nil)
				if exit != 0 {
					t.Fatalf("%s/%s exited %d (9 = the wrong arm ran; 99 = rc underflow), stderr:\n%s",
						tc.name, label, exit, stderr)
				}
				return leakSummaryOf(t, tc.name+"/"+label, stderr)
			}
			fa, ff, fl := measure("few", few)
			ma, mf, ml := measure("many", many)
			if fl != ml {
				t.Errorf("live_bytes %d rounds=%d vs %d rounds=%d (allocs %d/%d, frees %d/%d): "+
					"the box is left behind every round", fl, few, ml, many, fa, ma, ff, mf)
			}
			if ma != mf || ml != 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — every block the loop allocates must be freed",
					tc.name, ma, mf, ml)
			}
		})
	}
}

func ioOpenCloseSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let i: i32 = 0;
    while (i < %d) {
        match (open_reader("/dev/null")) {
            Ok(r) => { match (r.close()) { Err(e) => { return 9; }, Ok(_) => {} } },
            Err(e) => { return 8; }
        }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func ioOpenCloseBoundSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let i: i32 = 0;
    while (i < %d) {
        let r: Result[Reader, IoError] = open_reader("/dev/null");
        match (r) {
            Ok(rd) => { match (rd.close()) { Err(e) => { return 9; }, Ok(_) => {} } },
            Err(e) => { return 8; }
        }
        i = i + 1;
    }
    return 0;
}`, rounds)
}

func ioReadChunkSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let acc: i32 = 0;
    match (open_reader("/dev/zero")) {
        Ok(r) => {
            let i: i32 = 0;
            while (i < %d) {
                match (r.read_chunk(64)) {
                    Ok(chunk) => { acc = acc + chunk.len(); },
                    Err(e) => { return 9; }
                }
                i = i + 1;
            }
        },
        Err(e) => { return 8; }
    }
    if (acc < 0) { return 7; }
    return 0;
}`, rounds)
}

func TestSelfHostIoResultBoxReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const (
		few  = 20
		many = 200
	)
	measure := func(name, label string, src string) (int64, int64, int64) {
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		bin := buildBin(t, gcc, dir, name+"_"+label, asm)
		stderr, exit := runWithStdin(t, runner, bin, nil)
		if exit != 0 {
			t.Fatalf("%s/%s exited %d (8 = the open failed; 9 = the call reported an error; 99 = rc underflow), stderr:\n%s",
				name, label, exit, stderr)
		}
		return leakSummaryOf(t, name+"/"+label, stderr)
	}

	t.Run("open_close", func(t *testing.T) {
		fa, ff, fl := measure("open_close", "few", ioOpenCloseSrc(few))
		ma, mf, ml := measure("open_close", "many", ioOpenCloseSrc(many))
		if fl != ml || ma != mf || ml != 0 {
			t.Errorf("allocs %d/%d, frees %d/%d, live_bytes %d/%d at %d and %d rounds: "+
				"an open-and-close round must leave nothing — one box a round unreleased "+
				"shows here as %d unfreed blocks, both as %d",
				fa, ma, ff, mf, fl, ml, few, many, many, 2*many)
		}
	})

	t.Run("open_close_bound", func(t *testing.T) {
		fa, ff, fl := measure("open_close_bound", "few", ioOpenCloseBoundSrc(few))
		ma, mf, ml := measure("open_close_bound", "many", ioOpenCloseBoundSrc(many))
		if fl != ml || ma != mf || ml != 0 {
			t.Errorf("allocs %d/%d, frees %d/%d, live_bytes %d/%d at %d and %d rounds: "+
				"the bound open's Result box is not released",
				fa, ma, ff, mf, fl, ml, few, many)
		}
	})

	t.Run("read_chunk", func(t *testing.T) {
		_, _, fl := measure("read_chunk", "few", ioReadChunkSrc(few))
		ma, mf, ml := measure("read_chunk", "many", ioReadChunkSrc(many))
		if fl != ml {
			t.Errorf("live_bytes %d rounds=%d vs %d rounds=%d (allocs=%d frees=%d): "+
				"the per-call Result box or its payload is left behind every round",
				fl, few, ml, many, ma, mf)
		}
	})
}
