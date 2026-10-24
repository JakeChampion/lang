package e2ecompiler

import "testing"

func TestSelfHostTypeRef(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/typeref_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/typeref_run.fern", "typeref_run")

	const want = "ok   i32\n" +
		"ok   u32\n" +
		"ok   string\n" +
		"ok   bool\n" +
		"ok   f64\n" +
		"ok   Foo\n" +
		"ok   mod.Bar\n" +
		"ok   i32[]\n" +
		"ok   string[][]\n" +
		"ok   Foo[][][]\n" +
		"ok   Option[i32]\n" +
		"ok   Map[string, i32]\n" +
		"ok   Result[string, Err]\n" +
		"ok   Map[string, Option[i32]]\n" +
		"ok   Map[i32, Vec[string]][]\n" +
		"ok   Vec[T]\n" +
		"ok   (i32, string)\n" +
		"ok   (string, (i32, bool))\n" +
		"ok   Option[(string, string)]\n" +
		"ok   (i32, string)[]\n" +
		"ok   dyn Shape\n" +
		"ok   dyn Shape[]\n" +
		"ok   () => P\n" +
		"ok   (i32) => i32\n" +
		"ok   (T, T) => i32\n" +
		"ok   (T) => U[]\n" +
		"ok   (ast.Expr, T) => T\n" +
		"ok   (() => T) => U\n" +
		"ok   (i32) => (string, i32)\n" +
		"ok   Vec[(T) => boolean]\n" +
		"ok   ((T) => U)[]\n" +
		"ok   (T) => U[][]\n" +
		"ok   (T) => () => U[]\n" +
		"ok   ((((T) => U)))\n" +
		"ok   ((() => U)[])\n" +
		"ok   (((T) => U)[][])\n" +
		"ok   (() => T, () => U)\n" +
		"ok   () => (() => U[])[]\n" +
		"ok   [u8]\n" +
		"ok   [u8][]\n" +
		"ok   ([u8], i32)\n" +
		"ok   Option[[u8]]\n" +
		"ok   [[u8]]\n" +
		"ok   [(i32) => i32]\n" +
		"struct Map base=Map args=2 depth=0 tuple=0\n" +
		"struct Map.arg1 base=Option args=1\n" +
		"struct tuparr base= args=2 depth=1 tuple=1\n" +
		"struct arr3 base=Foo args=0 depth=3\n" +
		"struct viewarr view=1 depth=1 elemview=1 elem=u8\n" +
		"struct fn2 base= args=3 depth=0 tuple=0 fn=1 ret=i32\n" +
		"struct fn0 args=1 fn=1 ret=P\n" +
		"struct fnho args=2 fn=1 p0fn=1 ret=U\n" +
		"struct fnarrret fn=1 depth=0 retdepth=2\n" +
		"struct arrfn fn=0 depth=1 elemfn=1\n" +
		"struct groupedfn fn=1 params=T ret=U\n" +
		"tag fn=(T) => boolean\n" +
		"tag fn0=() => U\n" +
		"tag fn2=(T, Map[string, U]) => (string, i32)\n" +
		"tag fnarr=((T) => U)[]\n" +
		"tag fnview=[(T) => U]\n" +
		"tag opaque=fn\n" +
		"tag plain=i32[]\n" +
		"round_trip_failures=0\n"

	cmd := runX86_64Bin(runner, bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("typeref_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("typeref report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("typeref_run exit code = %d, want 0 (round-trip failures)", code)
	}
}
