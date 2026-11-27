package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// laneSavedRegisterProg has two functions that each keep 20 lane values live
// at one time. wide makes no call. across calls wide while its 20 values are
// live, and wide writes each lane register that it uses.
func laneSavedRegisterProg() string {
	var loads, sum strings.Builder
	for k := 0; k < 20; k++ {
		fmt.Fprintf(&loads, "    let a%d = __u8x16_load_ptr(p + %d as usize);\n", k, k*16)
	}
	for k := 19; k >= 0; k-- {
		fmt.Fprintf(&sum, "    s = s + a%d;\n", k)
	}
	return `@noinline function wide(p: usize): u8x16 {
  let s = __u8x16_splat(0 as u8);
  unsafe {
` + loads.String() + sum.String() + `  }
  return s;
}

@noinline function across(p: usize): u8x16 {
  let s = __u8x16_splat(0 as u8);
  unsafe {
` + loads.String() + `    s = wide(p);
` + sum.String() + `  }
  return s;
}

function main(): i32 {
  unsafe {
    let p = __alloc(320);
    for j in 0..320 {
      __store_u8(p + j as usize, j * 7 + 3 & 255);
    }
    let q = __alloc(32);
    __u8x16_store(q, wide(p));
    __u8x16_store(q + 16 as usize, across(p));
    for i in 0..16 {
      let want = 0;
      for k in 0..20 {
        want += (k * 16 + i) * 7 + 3 & 255;
      }
      if (__load_u8(q + i as usize) != (want & 255)) {
        return 1 + i;
      }
      if (__load_u8(q + (16 + i) as usize) != (want * 2 & 255)) {
        return 20 + i;
      }
    }
    __free(q, 32);
    __free(p, 320);
  }
  return 0;
}
`
}

var (
	arm64LaneSlotMove  = regexp.MustCompile(`(?:ldr|str) q\d+, \[(?:sp|x29)`)
	arm64LaneSlotStore = regexp.MustCompile(`str q\d+, \[(?:sp|x29)`)
	arm64SavedLaneReg  = regexp.MustCompile(`\bv(?:8|9|1[0-5])\.16b`)
)

// A call keeps only the low half of d8 to d15 on arm64. A lane value that no
// call crosses can stay in one of them, so 20 lane values that are live at
// one time need no frame slot. A lane value that a call crosses goes to a
// frame slot.
func TestSelfHostLaneValueKeepsASavedRegister(t *testing.T) {
	cli := buildSelfHostCLI(t)
	prog := laneSavedRegisterProg()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	wide := tailCallBody(t, string(asm), "wide")
	if moves := arm64LaneSlotMove.FindAllString(wide, -1); len(moves) != 0 {
		t.Errorf("wide moves a lane value through its frame %d time(s), want none:\n%s", len(moves), wide)
	}
	if !arm64SavedLaneReg.MatchString(wide) {
		t.Errorf("wide keeps no lane value in v8 to v15:\n%s", wide)
	}
	across := tailCallBody(t, string(asm), "across")
	if stores := arm64LaneSlotStore.FindAllString(across, -1); len(stores) < 20 {
		t.Errorf("across stores %d lane value(s) in its frame, want the 20 that the call crosses:\n%s", len(stores), across)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, prog, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
