package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostSSAKindRegistry(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ssa_kind_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ssa_kind_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ssa_kind_run.fern", "ssa_kind_run")

	const want = "kind_count=37\n" +
		"bijection_ok=34\n" +
		"bijection_failures=0\n" +
		"retired=3\n" +
		"unknown_id=0\n" +
		"id0=invalid\n" +
		"oob=invalid\n" +
		"const_int=1\n" +
		"binary=9\n" +
		"phi=8\n" +
		"store_elem=16\n" +
		"write_file=27\n" +
		"select=36\n" +
		"carry=37\n" +
		"term_count=3\n" +
		"term_bijection_ok=3\n" +
		"term_bijection_failures=0\n" +
		"term_none_id=0\n" +
		"term_none_name_len=0\n" +
		"ret=1\n" +
		"brif=3\n" +
		"semantic1=array_new\n" +
		"semantic2=tuple_new\n" +
		"semantic3=array_get\n" +
		"semantic4=tuple_get\n" +
		"semantic5=record_new\n" +
		"semantic6=record_get\n" +
		"semantic7=call\n" +
		"semantic8=variant_new\n" +
		"semantic9=variant_is\n" +
		"semantic10=variant_get\n" +
		"semantic11=variant_up\n" +
		"semantic12=variant_as\n" +
		"semantic13=length\n" +
		"semantic14=append\n" +
		"semantic15=slice\n" +
		"semantic16=str_index\n" +
		"semantic17=cast\n" +
		"semantic18=str_as\n" +
		"semantic19=with\n" +
		"semantic20=closure_new\n" +
		"semantic21=call_value\n" +
		"semantic22=cell_new\n" +
		"semantic23=cell_get\n" +
		"semantic24=cell_set\n" +
		"semantic25=map_new\n" +
		"semantic26=map_insert\n" +
		"semantic27=map_has\n" +
		"semantic28=map_get_or\n" +
		"semantic29=map_len\n" +
		"semantic30=map_get\n" +
		"semantic31=map_keys\n" +
		"semantic32=map_values\n" +
		"semantic33=map_delete\n" +
		"semantic34=map_cleared\n" +
		"semantic35=zero\n" +
		"semantic36=str_bytes\n" +
		"semantic37=arr_join\n" +
		"semantic38=arr_slice\n" +
		"semantic39=map_iter\n" +
		"semantic40=mapiter_has_next\n" +
		"semantic41=mapiter_key\n" +
		"semantic42=mapiter_value\n" +
		"semantic43=mapiter_advance\n" +
		"semantic44=dyn_up\n" +
		"semantic45=dyn_box\n" +
		"semantic46=dyn_is\n" +
		"semantic47=dyn_as\n" +
		"semantic48=hole_at\n" +
		"semantic49=hole_fill\n" +
		"semantic50=array_lend\n" +
		"semantic51=str_byte_view\n" +
		"semantic52=append_range\n" +
		"semantic_count=52\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ssa_kind_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("SSA kind registry report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ssa_kind_run exit code = %d, want 0 (bijection failures)", code)
	}
}
