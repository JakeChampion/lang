package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

const mvsSelectionContract = `import "./mvs";

function index(text: string): mvs.Index {
  return match (mvs.parse_index(text)) { Ok(value) => value, Err(message) => { print(message); exit(90); } };
}

function selection(state: mvs.State): mvs.Sel[] {
  return match (mvs.selected(state)) { Ok(value) => value, Err(message) => { print(message); exit(91); } };
}

function reason(state: mvs.State): string {
  return match (mvs.selected(state)) { Ok(_) => "unexpected success", Err(message) => message };
}

function main(): i32 {
  let ix=mvs.with_dir(index("[foo]\n\"1.0.0\"={path=\"foo\"}\n[bar]\n\"2.0.0\"={path=\"/bar\"}\n"),"/registry");
  match(mvs.index_source(ix,"foo","1.0.0")) {
    Some(src)=>{if(src.path!="/registry/foo" || src.url!="" || src.hash!=""){return 1;}},
    None=>{return 2;}
  }
  match(mvs.index_source(ix,"missing","1.0.0")){Some(_)=>{return 3;},None=>{}}
  match(mvs.index_source(ix,"foo","2.0.0")){Some(_)=>{return 4;},None=>{}}
  let empty=mvs.begin([],[],index(""));
  if(!mvs.done(empty) || selection(empty).len()!=0){return 5;}
  let waiting=mvs.begin([mvs.Req{pkg:"foo",min:"1.0.0"}],[],ix);
  if(reason(waiting)!="resolution needs dependencies of foo@1.0.0"){return 6;}
  let failed=mvs.fail(waiting,"saved refusal");
  if(reason(failed)!=failed.err){return 7;}
  let complete=mvs.supply(waiting,[]);
  let ready=selection(complete);
  if(ready.len()!=1 || ready[0].name!="foo" || ready[0].ver!="1.0.0" || ready[0].src.path!="/registry/foo"){return 8;}
  let repeated=selection(complete);
  if(repeated.len()!=1 || repeated[0].src.path!="/registry/foo" || complete.names[0]!="foo"){return 9;}
  let malformed=mvs.State{...empty,names:["foo"]};
  if(reason(malformed)!="resolution has unequal package and version counts"){return 10;}
  if(reason(mvs.State{...malformed,err:"first error"})!="first error"){return 11;}
  let absent=mvs.State{...complete,ix:index("")};
  if(reason(absent)!="registry has no source for foo@1.0.0"){return 12;}
  let partial=mvs.State{...empty,ix:ix,names:["bar","missing"],vers:["2.0.0","1.0.0"]};
  if(reason(partial)!="registry has no source for missing@1.0.0"){return 13;}
  let zero=mvs.Index{dir:"",serial:"",entries:[mvs.Entry{pkg:"zero",ver:"1.0.0",src:mvs.Source{path:"",url:"",hash:""}}]};
  match(mvs.index_source(zero,"zero","1.0.0")){
    Some(src)=>{if(src.path!="" || src.url!="" || src.hash!=""){return 14;}},
    None=>{return 15;}
  }
  let zero_state=mvs.supply(mvs.begin([mvs.Req{pkg:"zero",min:"1.0.0"}],[],zero),[]);
  if(selection(zero_state).len()!=1){return 16;}
  let many=mvs.begin([mvs.Req{pkg:"foo",min:"1.0.0"},mvs.Req{pkg:"bar",min:"2.0.0"}],[],ix);
  while(!mvs.done(many) && many.err.len()==0){many=mvs.supply(many,[]);}
  let sorted=selection(many);
  if(sorted.len()!=2 || sorted[0].name!="bar" || sorted[1].name!="foo" || sorted[0].src.path!="/bar"){return 17;}
  return 0;
}
`

func TestSelfHostMvsSourceSelection(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "mvs.fern")
	writeResolveProject(t, dir, map[string]string{"selection_contract.fern": mvsSelectionContract})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "selection_contract.fern", "selection_contract", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil || len(out) != 0 {
				t.Fatalf("source and selection contract: %v\n%s", err, out)
			}
		})
	}
}
