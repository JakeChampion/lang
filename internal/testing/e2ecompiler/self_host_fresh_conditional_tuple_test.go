package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const freshConditionalTupleDecls = `struct State { values: i32[] }
enum Event { Finished, Output(i32) }
function (own state: State) step_event(): (State, Event) { return (state, Event.Finished); }
function (own state: State) next_event(): (State, Option[i32]) { return (state, None); }
function (own pair:(State,State)) step_pair():((State,State),i32) {return(pair,1);}
function make(): State { return State { values: [1] }; }
function (state: State) borrowed(): (State, i32) { return (state, 1); }
function (own state: State) step(): (State, i32) { return (state, 1); }
`

func freshConditionalTupleCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct {
		name, params, result, body string
		reject                     bool
	}{}
	for _, tail := range []string{`let (next,n)=state.step(); (next,n)`, `let (next,n)=state.step(); (next,1)`, `let next=make(); (next,1)`} {
		cases = append(cases, struct {
			name, params, result, body string
			reject                     bool
		}{"fresh-" + tail, "flag: boolean", "i32", `let state=make(); for i in [0,1] { let(next,n)=if(flag) {state.step()} else { ` + tail + ` }; state=next; } return state.values.len();`, false})
	}
	for _, tail := range []string{`(borrowed,1)`, `let next=borrowed; (next,1)`, `let next=make(); let alias=next; (next,1)`, `let next=make(); let captured=()=>next.values.len(); (next,1)`, `let next=make(); next=borrowed; (next,1)`, `let(next,n)=(borrowed,1); (next,n)`, `let(next,n)=borrowed.borrowed(); (next,n)`} {
		cases = append(cases, struct {
			name, params, result, body string
			reject                     bool
		}{"refuse-" + tail, "flag:boolean,borrowed:State", "i32", `let state=make(); for i in [0,1] { let(next,n)=if(flag) {state.step()} else { ` + tail + ` }; state=next; } return state.values.len();`, true})
	}

	cases = append(cases, struct {
		name, params, result, body string
		reject                     bool
	}{"mapped-event", "flag:boolean", "i32", `let state=make(); for i in [0,1] { let(next,event)=if(flag) {state.step_event()} else {let(next,event)=state.next_event();(next,match(event){Some(value)=>Event.Output(value),None=>Event.Finished})}; state=next; } return state.values.len();`, false})

	cases = append(cases, struct {
		name, params, result, body string
		reject                     bool
	}{"duplicate-payload", "flag:boolean", "i32", `let pair=(make(),make()); for i in [0,1] {let(next,n)=if(flag) {pair.step_pair()} else {let(next,n)=pair.step_pair();let(a,b)=next;((a,a),n)};pair=next;} return 0;`, true})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := freshConditionalTupleDecls + "function work(" + tc.params + "): " + tc.result + " { " + tc.body + " } function main(): i32 { return 0; }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if !tc.reject && len(ds) == 0 {
					return
				}
				if tc.reject {
					for _, d := range ds {
						if d.code == "E050" || d.code == "E051" {
							return
						}
					}
				}
				t.Fatalf("%s: reject=%v, diagnostics=%v", label, tc.reject, ds)
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostFreshConditionalTupleX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "fresh_conditional_tuple_checker")
	freshConditionalTupleCases(t, bin, runner, dir)
}

func TestSelfHostFreshConditionalTupleWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "fresh_conditional_tuple_checker.wasm", e2eharness.TargetWasm32Wasi)
	freshConditionalTupleCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostFreshConditionalTupleRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := freshConditionalTupleDecls + `
function work(flag:boolean, own state:State):i32 {
 let current=state;
 for i in [0,1,2] {
  let(next,n)=if(flag) {current.step()} else {let(next,n)=current.step();(next,n)};
  current=next;
 }
 return current.values.len();
}
function main():i32 {
 let a=work(true,State {values:[args().len(),2]});
 let b=work(false,State {values:[args().len(),2,3]});
 return if(a==2 && b==3) {0} else {1};
}`

	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var command *exec.Cmd
			if target == "wasm32-wasi" {
				command = exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, path, "FERN_STRICT_IR=1"))
			} else {
				command = runX86_64Bin(cli.runner, cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1"))
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", target, err, output)
			}
		})
	}
}
