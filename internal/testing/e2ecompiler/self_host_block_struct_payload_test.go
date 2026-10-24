package e2ecompiler

import (
	"strings"
	"testing"
)

const blkStructNestedSrc = `import "core/int";
struct P { xs: i32[], n: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let o: Option[P] = Some(P { xs: [k, k + 1], n: k });
            if (k >= 0) {
                match (o) { Some(p) => { acc = acc + p.n + p.xs.len(); }, None => { acc = acc + 1; } }
            }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const blkStructFlatSrc = `import "core/int";
struct P { xs: i32[], n: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let o: Option[P] = Some(P { xs: [k, k + 1], n: k });
            match (o) { Some(p) => { acc = acc + p.n + p.xs.len(); }, None => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

const blkStructFieldMovedSrc = `struct P { xs: i32[], n: i32 }
function main(): i32 {
    let held: i32[] = [0, 0];
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let k: i32 = 0;
        while (k < 4) {
            let o: Option[P] = Some(P { xs: [k, k + 1], n: k });
            if (k >= 0) {
                match (o) { Some(p) => { held = p.xs; acc = acc + p.n; }, None => { acc = acc + 1; } }
            }
            acc = acc + held[1];
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 83;
}
`

func TestSelfHostBlockStructPayloadNestedMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s: self-host exited %d, fern -interp exited %d — the deep field "+
				"drop reached a live buffer", name, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	for _, tc := range []struct{ name, src string }{
		{"struct_payload_nested", blkStructNestedSrc},
		{"struct_payload_flat_control", blkStructFlatSrc},
		{"field_moved_out_of_the_arm", blkStructFieldMovedSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance",
					tc.name, allocs, frees, live)
			}
		})
	}
}
