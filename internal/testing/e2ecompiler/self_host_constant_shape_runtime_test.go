package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostConstantShapeRuntime(t *testing.T) {
	const source = `const LIMIT:i32=7;
function limit_ref():i32{return LIMIT+1;}
function vb_empty(k:i32):i32[]{let o:Option[i32]=Some(k);return match(o){Some(v)=>[v,v],None=>[]};}
function vb_bare(k:i32):i32{return match(Some(k)){Some(v)=>v+1,None=>0};}
function main():i32{
  let values=vb_empty(6);
  let held=values;
  let changed=values.with(0,99);
  if(limit_ref()!=8||vb_bare(9)!=10||held.len()!=2||held[0]!=6||held[1]!=6||changed[0]!=99||changed[1]!=6){return 1;}
  return 0;
}`
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, source, []string{"FERN_SANITIZE=1"})
			var cmd *exec.Cmd
			if target == e2eharness.TargetWasm32Wasi {
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", bin)
			} else {
				cmd = runX86_64Bin(x86_64Runner(t), bin)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("constant and unboxed option runtime: %v\n%s", err, out)
			}
			if target == e2eharness.TargetX86_64Linux && !bytes.Contains(out, []byte("live_bytes=0")) {
				t.Fatalf("missing clean sanitizer census: %s", out)
			}
		})
	}
}
