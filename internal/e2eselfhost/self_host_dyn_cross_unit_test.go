package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeConcatDynFixture is writeConcatFixture with a trait object crossing
// the unit boundary: `speak` declares the trait and the function that
// dispatches on `dyn Speak`, and the entry declares the one implementing
// type. In the per-module concat the dispatch chain is emitted in speak's
// unit, so its arm is a method another unit declares. main exits 0 when the
// cross-unit sum and the dispatch both answer correctly, 1 and 2 otherwise;
// a chain with no arm aborts with 134.
func writeConcatDynFixture(t *testing.T, dir string) string {
	t.Helper()
	proj := filepath.Join(dir, "concatdynproj")
	imports, calls, want := writeConcatLibs(t, proj)
	speak := "pub trait Speak {\n  function speak(self: Self): i32;\n}\n\npub function talk(d: dyn Speak): i32 { return d.speak(); }\n"
	if err := os.WriteFile(filepath.Join(proj, "speak.fern"), []byte(speak), 0o644); err != nil {
		t.Fatalf("write speak: %v", err)
	}
	entry := fmt.Sprintf(`%simport "./speak";

struct Dog { n: i32 }

impl speak.Speak for Dog {
  function speak(self: Self): i32 { return self.n * 3; }
}

function main(): i32 {
  var t: i32 = %s;
  if (t != %d) { return 1; }
  if (speak.talk(Dog { n: 7 }) != 21) { return 2; }
  return 0;
}
`, imports, calls, want)
	entryPath := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entryPath, []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	return entryPath
}

// TestSelfHostDynArmAcrossUnitsX86_64 pins that a dyn dispatch emitted in one
// unit of the per-module concat reaches an implementation declared in another
// (#10891). The chain is built from the emit state's function list, which the
// unit emit used to fill with the unit's own declarations: a program whose
// trait object was implemented in the entry and dispatched on in a library
// module then compiled to a chain with no arms, and the dispatch aborted. The
// merged emit never saw it, as its one unit is the whole program, and neither
// did the CLI, which tree-shakes under the per-module budget.
func TestSelfHostDynArmAcrossUnitsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir, mmr := buildConcatDriver(t, gcc)
	entryPath := writeConcatDynFixture(t, dir)

	asm, err := exec.Command(mmr, entryPath).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)

	bin := buildBin(t, gcc, dir, "concat_dyn_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("per-module concat program exited %d, want 0 (2: the cross-unit dyn dispatch answered wrong; 134: it had no arm)", code)
	}
}

// TestSelfHostDynArmAcrossUnitsArm64 is the arm64 leg of
// TestSelfHostDynArmAcrossUnitsX86_64: the x86-64 driver cross-emits the
// program, which the aarch64 toolchain links and qemu runs.
func TestSelfHostDynArmAcrossUnitsArm64(t *testing.T) {
	hostGcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host driver is built for x86-64-linux, so it must run on a native x86-64 host")
	}
	armGcc, qemu := arm64Tooling(t)
	dir, mmr := buildConcatDriver(t, hostGcc)
	entryPath := writeConcatDynFixture(t, dir)

	asm, err := exec.Command(mmr, entryPath, "-target", "arm64-linux").Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("arm64 over-budget concat emit failed: %v (len=%d)", err, len(asm))
	}
	assertConcatProduced(t, asm)

	bin := buildBin(t, armGcc, dir, "concat_dyn_prog_arm64", string(asm))
	rc := runArm64Bin(qemu, bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("arm64 per-module concat program exited %d, want 0 (2: the cross-unit dyn dispatch answered wrong; 134: it had no arm)", code)
	}
}
