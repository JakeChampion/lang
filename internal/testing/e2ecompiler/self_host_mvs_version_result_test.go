package e2ecompiler

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/pkg/mvs"
)

func versionFixtureQuote(text string) string {
	quoted := strconv.Quote(text)
	var out strings.Builder
	for i := 0; i < len(quoted); i++ {
		if quoted[i] == '\\' && i+1 < len(quoted) {
			if quoted[i+1] == 'u' && i+6 <= len(quoted) {
				out.WriteString("\\u{" + quoted[i+2:i+6] + "}")
				i += 5
				continue
			}
			if quoted[i+1] == 'U' && i+10 <= len(quoted) {
				out.WriteString("\\u{" + quoted[i+2:i+10] + "}")
				i += 9
				continue
			}
			out.WriteByte(quoted[i])
			i++
			out.WriteByte(quoted[i])
			continue
		}
		out.WriteByte(quoted[i])
	}
	return out.String()
}

func versionContractSource() (string, string) {
	cases := []string{
		"0.0.0", "1.2.3", "01.002.0003", "0000000000000000000000000000000000001.2.3",
		"2147483647.0.0", "2147483648.0.0", "1.2147483648.0", "1.2.2147483648",
		"9223372036854775807.0.0", "0.9223372036854775807.0", "0.0.9223372036854775807",
		"9223372036854775808.0.0", "0.9223372036854775808.0", "0.0.9223372036854775808",
		"999999999999999999999999999999999999999999.0.0", "0009223372036854775807.0.0", "0009223372036854775808.0.0",
		"", "1", "1.2", "1.2.3.4", "1..3", ".2.3", "1.2.", "1.+2.3", "1.-0.3", "-1.2.3",
		" 1.2.3", "1.2.3 ", "1.2.3\n", "1.2.3\t", "1.2.3-rc1", "1.2.3+build", "１.2.3", "1.².3", "1.2.\x00", "1.2.\u200d",
	}
	pairs := [][2]string{{"2147483647.0.0", "2147483648.0.0"}, {"9223372036854775807.0.0", "2147483648.0.0"}, {"0.9223372036854775807.0", "1.0.0"}, {"1.0.9223372036854775807", "1.1.0"}, {"0001.2.3", "1.2.3"}, {"0.0.0", "0.0.0"}, {"9223372036854775807.9223372036854775807.9223372036854775807", "9223372036854775807.9223372036854775807.9223372036854775806"}}
	registry := "[foo]\n"
	for _, v := range []string{"2147483648.0.0", "1.0.0", "9223372036854775807.0.0", "2147483647.0.0", "0000000000000002.0.0", "0.9223372036854775807.0", "1.0.9223372036854775807", "0.0.0"} {
		registry += strconv.Quote(v) + " = { path = \"/pkg\" }\n"
	}
	ix, err := mvs.ParseIndex(registry)
	if err != nil {
		panic(err)
	}
	var source, want strings.Builder
	source.WriteString("import \"./mvs\";\nimport \"./util\";\n")
	row := `"OK|"+mvs.version_string(value)+"|"+util.i64_to_string(value.major as i64)+"|"+util.i64_to_string(value.minor as i64)+"|"+util.i64_to_string(value.patch as i64)`
	source.WriteString("function parsed(text:string):string {return match(mvs.parse_version(text)){Ok(value)=>" + row + ",Err(message)=>\"ERR|\"+message};}\n")
	source.WriteString("function compared(a:string,b:string):string {let av=match(mvs.parse_version(a)){Ok(value)=>value,Err(message)=>{return message;}};let bv=match(mvs.parse_version(b)){Ok(value)=>value,Err(message)=>{return message;}};return util.i32_to_string(mvs.version_cmp(av,bv));}\n")
	source.WriteString(`function joined(values:string[]):string {let out="";for value in values {if(out.len()>0){out=out+";";}out=out+value;}return out;}
function resolved(ix:mvs.Index,min:string,bans:mvs.Ban[]):string {
 let st=mvs.begin([mvs.Req{pkg:"foo",min:min}],bans,ix);
 while(st.err.len()==0 && !mvs.done(st)){st=mvs.supply(st,[]);}
 if(st.err.len()>0){return "ERR|"+st.err;}
 let out="OK|";for item in mvs.selected(st){out=out+item.name+"@"+item.ver+"|"+item.src.path;}return out;
}
function main():i32 {
`)
	for i, text := range cases {
		fmt.Fprintf(&source, "print(\"PARSE|%d|\"+parsed(%s));\n", i, versionFixtureQuote(text))
		v, err := mvs.ParseVersion(text)
		if err != nil {
			fmt.Fprintf(&want, "PARSE|%d|ERR|%s\n", i, err)
		} else {
			fmt.Fprintf(&want, "PARSE|%d|OK|%s|%d|%d|%d\n", i, v, v.Major, v.Minor, v.Patch)
		}
	}
	for i, pair := range pairs {
		a, e := mvs.ParseVersion(pair[0])
		if e != nil {
			panic(e)
		}
		b, e := mvs.ParseVersion(pair[1])
		if e != nil {
			panic(e)
		}
		fmt.Fprintf(&source, "print(\"CMP|%d|\"+compared(%s,%s));\n", i, strconv.Quote(pair[0]), strconv.Quote(pair[1]))
		fmt.Fprintf(&want, "CMP|%d|%d\n", i, a.Compare(b))
	}
	fmt.Fprintf(&source, "let ix=mvs.parse_index(%s);\nprint(\"INDEX|\"+joined(mvs.index_versions(ix,\"foo\")));\n", strconv.Quote(registry))
	var versions []string
	for _, v := range ix.Versions("foo") {
		versions = append(versions, v.String())
	}
	fmt.Fprintf(&want, "INDEX|%s\n", strings.Join(versions, ";"))
	bad := "[foo]\n\"9223372036854775808.0.0\" = { path = \"/bad\" }\n"
	_, err = mvs.ParseIndex(bad)
	if err == nil {
		panic("overflow registry accepted")
	}
	fmt.Fprintf(&source, "print(\"INDEXERR|\"+mvs.parse_index(%s).err);\n", strconv.Quote(bad))
	fmt.Fprintf(&want, "INDEXERR|%s\n", err)
	source.WriteString(`let manual=mvs.Index{dir:"",err:"",entries:[mvs.Entry{pkg:"foo",ver:"broken",src:mvs.Source{path:"/bad",url:"",hash:""}},mvs.Entry{pkg:"foo",ver:"1.0.0",src:mvs.Source{path:"/pkg",url:"",hash:""}}]};print("MANUAL|"+joined(mvs.index_versions(manual,"foo")));
`)
	want.WriteString("MANUAL|1.0.0\n")
	requests := []struct {
		min  string
		bans []string
	}{{"2147483648.0.0", nil}, {"2147483648.0.0", []string{"2147483648.0.0"}}, {"9223372036854775807.0.0", nil}, {"9223372036854775808.0.0", nil}, {"0000.0.0", nil}, {"0002.0.0", nil}, {"1.0.0", []string{"9223372036854775808.0.0"}}, {"2147483648.0.0", []string{"2147483648.0.0", "9223372036854775807.0.0"}}}
	for i, req := range requests {
		var bans []string
		for _, ban := range req.bans {
			bans = append(bans, `mvs.Ban{pkg:"foo",ver:`+strconv.Quote(ban)+`}`)
		}
		fmt.Fprintf(&source, "print(\"RESOLVE|%d|\"+resolved(ix,%s,[%s]));\n", i, strconv.Quote(req.min), strings.Join(bans, ","))
		selected, err := mvs.Resolve(map[string]string{"foo": req.min}, map[string][]string{"foo": req.bans}, ix, func(string, mvs.Version, mvs.Source) (map[string]string, error) { return nil, nil })
		if err != nil {
			fmt.Fprintf(&want, "RESOLVE|%d|ERR|%s\n", i, err)
		} else {
			fmt.Fprintf(&want, "RESOLVE|%d|OK|", i)
			for _, item := range selected {
				fmt.Fprintf(&want, "%s@%s|%s", item.Name, item.Version, item.Source.Path)
			}
			want.WriteByte('\n')
		}
	}
	source.WriteString("return 0;\n}\n")
	return source.String(), want.String()
}

func TestSelfHostMvsVersionResult(t *testing.T) {
	source, want := versionContractSource()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "mvs.fern")
	writeResolveProject(t, dir, map[string]string{"version_contract.fern": source})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "version_contract.fern", "version_contract", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil || string(out) != want {
				t.Fatalf("version contract: %v\ngot:\n%swant:\n%s", err, out, want)
			}
		})
	}
}
