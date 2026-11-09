package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// typeinfo.any_child reaches each type that a type holds directly, so a
// walker built on it visits every variant.
func TestSelfHostTypeinfoAnyChild(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	const driver = `import "./typeinfo";
function erased(t: typeinfo.Type): boolean {
  if let typeinfo.TypeErased(_) = t {
    return true;
  }
  return false;
}
function show(name: string, t: typeinfo.Type): void {
  print(name + ":" + typeinfo.any_child(t, erased).to_string());
}
function main(): i32 {
  let v: typeinfo.Type = typeinfo.TypeErased { name: "T" };
  let n: typeinfo.Type = typeinfo.TypeI32 { is_char: false, width: 32, unsigned: false, polymorphic: false };
  show("array", typeinfo.TypeArray { elem: v, view: false });
  show("tuple", typeinfo.TypeTuple { elements: [n, v] });
  show("struct", typeinfo.TypeStruct { name: "Box", args: [n, v] });
  show("union", typeinfo.TypeUnion { name: "Opt", args: [v] });
  show("map-key", typeinfo.TypeMap { key: v, value: n });
  show("map-value", typeinfo.TypeMap { key: n, value: v });
  show("func-param", typeinfo.TypeFunc { param_types: [n, v], param_own: [], ret_type: n, params_known: true });
  show("func-result", typeinfo.TypeFunc { param_types: [n], param_own: [], ret_type: v, params_known: true });
  show("no-child", typeinfo.TypeTuple { elements: [n, n] });
  show("leaf", v);
  return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "typeinfo_any_child.fern"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "typeinfo_any_child.fern", "typeinfo-any-child")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	const want = "array:true\ntuple:true\nstruct:true\nunion:true\nmap-key:true\nmap-value:true\nfunc-param:true\nfunc-result:true\nno-child:false\nleaf:false"
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("any_child: %v\ngot %s\nwant %s", err, out, want)
	}
}
