package e2ecompiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const lspSiteOptionProbe = `import "./lspquery";
import "./flatten";
import "./parser";
import "./lexer";
import "./util";
import "./drivers/rundriver";

function site_text(s:lspquery.Site):string {
 return s.name+"|"+s.qual+"|"+s.target+"|"+util.i32_to_string(s.line)+":"+util.i32_to_string(s.col)+":"+util.i32_to_string(s.len)+":"+util.i32_to_string(s.name_col)+":"+util.i32_to_string(s.dot_col)+"|"+s.prev;
}
function main():i32 {
 let source=rundriver.read_stdin("site_probe");
 let mod=parser.parse_module(lexer.tokenize(source));
 let p=lspquery.program(mod,mod,[],[source]);
 let q=flatten.Qualifiers{aliases:["math","type"],prefixes:["pkg","kind"],own:"",reexport_keys:["pkg__renamed"],reexport_targets:["dest__name"]};
 let av=args();let line=util.digits_to_i32(av[1]);let col=util.digits_to_i32(av[2]);
 print(match(lspquery.site_at(lspquery.module_toks(p,0),q,line,col)){Some(site)=>site_text(site),None=>"NONE"});
 return 0;
}
`

func TestSelfHostLSPMissingReceiverX86_64(t *testing.T) {
	s := buildLSPServers(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"missing_receiver.fern": ".foo(1);"})
	var probes []lspProbe
	for _, method := range []string{"hover", "definition", "signatureHelp", "references", "rename"} {
		params := at(0, 1)
		want := "null"
		if method == "signatureHelp" {
			params = at(0, 6)
		}
		if method == "references" {
			params["context"] = lspMsg{"includeDeclaration": true}
			want = "[]"
		}
		if method == "rename" {
			params["newName"] = "bar"
		}
		probes = append(probes, lspProbe{name: method, method: "textDocument/" + method, doc: "missing_receiver.fern", params: params, check: func(t *testing.T, result json.RawMessage) {
			if string(result) != want {
				t.Errorf("missing receiver: got %s, want %s", result, want)
			}
		}})
	}
	probeLSP(t, s, dir, []string{"missing_receiver.fern"}, probes)
}

const lspQueryOptionContract = `import "./lspquery";
import "./parser";
import "./lexer";

function main():i32 {
 let source="struct P{}\nfunction take(p:P):i32{return 0;}\nfunction main():i32{return take(P{});}";
 let mod=parser.parse_module(lexer.tokenize(source));
 let p=lspquery.viewed(lspquery.program(mod,mod,[],[source]));
 let type_q:lspquery.Query=match(lspquery.query(p,2,17)){Some(q)=>q,None=>{return 1;}};
 if(type_q.site.name!="P" || type_q.f.kind!=lspquery.K_NONE || type_q.v.mod.structs.len()==0){return 2;}
 match(lspquery.definition(p,2,17)){Some(d)=>{if(d.line!=1 || d.col!=8 || d.len!=1){return 3;}},None=>{return 4;}}
 let call_q:lspquery.Query=match(lspquery.query(p,3,28)){Some(q)=>q,None=>{return 5;}};
 if(call_q.site.name!="take" || call_q.f.kind!=lspquery.K_IDENT){return 6;}
 match(lspquery.query(p,99,1)){Some(_)=>{return 7;},None=>{}}
 match(lspquery.query(p,3,1)){Some(_)=>{return 8;},None=>{}}
 let broken=".foo";let broken_mod=parser.parse_module(lexer.tokenize(broken));
 let broken_p=lspquery.program(broken_mod,broken_mod,[],[broken]);
 match(lspquery.query(broken_p,1,2)){Some(_)=>{return 9;},None=>{}}
 match(lspquery.hover(broken_p,1,2)){Some(_)=>{return 10;},None=>{}}
 match(lspquery.definition(broken_p,1,2)){Some(_)=>{return 11;},None=>{}}
 return 0;
}
`

func TestSelfHostLSPSiteOptions(t *testing.T) {
	cases := []struct {
		name, source string
		args         []string
		want         string
	}{
		{name: "bare", source: "foo", args: []string{"1", "1"}, want: "foo|||1:1:3:1:0|\n"},
		{name: "bare-last-byte", source: "foo", args: []string{"1", "3"}, want: "foo|||1:1:3:1:0|\n"},
		{name: "end-exclusive", source: "foo", args: []string{"1", "4"}, want: "NONE\n"},
		{name: "empty", source: "", args: []string{"1", "1"}, want: "NONE\n"},
		{name: "quoted", source: "\"foo\"", args: []string{"1", "2"}, want: "NONE\n"},
		{name: "comment", source: "// foo", args: []string{"1", "4"}, want: "NONE\n"},
		{name: "number", source: "123", args: []string{"1", "2"}, want: "NONE\n"},
		{name: "wrong-line", source: "foo", args: []string{"2", "1"}, want: "NONE\n"},
		{name: "zero-column", source: "foo", args: []string{"1", "0"}, want: "NONE\n"},
		{name: "qualifier-left", source: "math.foo", args: []string{"1", "1"}, want: "foo|math|pkg__foo|1:1:8:6:5|\n"},
		{name: "qualifier-right", source: "math.foo", args: []string{"1", "6"}, want: "foo|math|pkg__foo|1:1:8:6:5|\n"},
		{name: "qualifier-spaces", source: "math . foo", args: []string{"1", "8"}, want: "foo|math|pkg__foo|1:1:10:8:6|\n"},
		{name: "reexport", source: "math.renamed", args: []string{"1", "6"}, want: "renamed|math|dest__name|1:1:12:6:5|\n"},
		{name: "keyword-qualifier", source: "type.foo", args: []string{"1", "1"}, want: "foo|type|kind__foo|1:1:8:6:5|\n"},
		{name: "member", source: "obj.foo", args: []string{"1", "5"}, want: "foo|||1:5:3:5:4|.\n"},
		{name: "chained-member", source: "a.math.foo", args: []string{"1", "8"}, want: "foo|||1:8:3:8:7|.\n"},
		{name: "chained-qualifier", source: "a.math.foo", args: []string{"1", "3"}, want: "math|||1:3:4:3:2|.\n"},
		{name: "line-break", source: "math.\nfoo", args: []string{"2", "1"}, want: "foo|||2:1:3:1:0|.\n"},
		{name: "dot-prior-line", source: ".\nfoo", args: []string{"2", "1"}, want: "foo|||2:1:3:1:0|.\n"},
		{name: "after-number", source: "1 foo", args: []string{"1", "3"}, want: "foo|||1:3:3:3:0|\n"},
		{name: "leading-dot", source: ".foo", args: []string{"1", "2"}, want: "NONE\n"},
		{name: "leading-dot-spaced", source: ". foo", args: []string{"1", "3"}, want: "NONE\n"},
		{name: "leading-dot-first-line", source: "\n.foo", args: []string{"2", "2"}, want: "NONE\n"},
		{name: "leading-dot-punctuation", source: ".foo", args: []string{"1", "1"}, want: "NONE\n"},
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lspquery.fern", "drivers/rundriver.fern")
	writeResolveProject(t, dir, map[string]string{"site_options.fern": lspSiteOptionProbe, "query_options.fern": lspQueryOptionContract})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "site_options.fern", "site_options", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, tc.args...)
					cmd.Stdin = strings.NewReader(tc.source)
					out, err := cmd.CombinedOutput()
					if err != nil || string(out) != tc.want {
						t.Fatalf("site options: %v\ngot:\n%swant:\n%s", err, out, tc.want)
					}
				})
			}
			t.Run("query-contract", func(t *testing.T) {
				contract := buildSelfHostBinFor(t, dir, "query_options.fern", "query_options", target)
				out, err := mvsDriverCommand(t, target, contract).CombinedOutput()
				if err != nil || len(out) != 0 {
					t.Fatalf("lexical sites and expression discovery: %v\n%s", err, out)
				}
			})
		})
	}
}
