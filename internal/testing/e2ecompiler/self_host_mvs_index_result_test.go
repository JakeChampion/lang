package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/mvs"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func indexResultContractSource() (string, string) {
	hash := "sha256:" + strings.Repeat("a", 64)
	cases := []struct{ name, text, pkg string }{
		{"empty", "", "foo"},
		{"empty-section", "[foo]\n", "foo"},
		{"one-entry", "[foo]\n\"1.0.0\"={path=\"first\"}\n", "foo"},
		{"duplicate-key", "[foo]\n\"1.0.0\"={path=\"first\"}\n\"1.0.0\"={path=\"last\"}\n", "foo"},
		{"repeated-section", "[foo]\n\"1.0.0\"={path=\"first\"}\n[bar]\n\"1.0.0\"={path=\"bar\"}\n[foo]\n\"1.0.0\"={path=\"last\"}\n", "foo"},
		{"independent-package", "[foo]\n\"1.0.0\"={path=\"foo\"}\n[bar]\n\"1.0.0\"={path=\"bar\"}\n", "bar"},
		{"replace-archive", "[foo]\n\"1.0.0\"={path=\"old\"}\n\"1.0.0\"={url=\"https://example.invalid/foo\",hash=\"" + hash + "\"}\n", "foo"},
		{"replace-path", "[foo]\n\"1.0.0\"={url=\"https://example.invalid/foo\",hash=\"" + hash + "\"}\n\"1.0.0\"={path=\"/last\"}\n", "foo"},
		{"other-version", "[foo]\n\"1.0.0\"={path=\"old\"}\n\"2.0.0\"={path=\"two\"}\n\"1.0.0\"={path=\"last\"}\n", "foo"},
		{"raw-keys", "[foo]\n\"01.0.0\"={path=\"zero\"}\n\"1.0.0\"={path=\"plain\"}\n\"01.0.0\"={path=\"last-zero\"}\n", "foo"},
		{"raw-only", "[foo]\n\"01.0.0\"={path=\"zero\"}\n", "foo"},
		{"crlf-comment", "# registry\r\n[foo] # section\r\n\"1.0.0\"={path=\"first\"} # old\r\n\"1.0.0\"={path=\"last\"}\r\n", "foo"},
		{"missing-package", "[bar]\n\"1.0.0\"={path=\"bar\"}\n", "foo"},
		{"bad-header", "[foo", "foo"},
		{"empty-header", "[ ]", "foo"},
		{"outside-section", "\"1.0.0\"={path=\"foo\"}", "foo"},
		{"missing-equals", "[foo]\n\"1.0.0\" {path=\"foo\"}", "foo"},
		{"unquoted-key", "[foo]\n1.0.0={path=\"foo\"}", "foo"},
		{"invalid-version", "[foo]\n\"bad\"={path=\"foo\"}", "foo"},
		{"overflow-version", "[foo]\n\"9223372036854775808.0.0\"={path=\"foo\"}", "foo"},
		{"invalid-after-valid", "[foo]\n\"1.0.0\"={path=\"first\"}\n\"2.0.0\"={}", "foo"},
		{"invalid-replacement", "[foo]\n\"1.0.0\"={path=\"first\"}\n\"1.0.0\"={hash=\"bad\"}", "foo"},
		{"bad-key-escape", "[foo]\n\"1.0.\\b\"={path=\"foo\"}", "foo"},
		{"bad-header-control", "[foo\x00", "foo"},
		{"bad-header-quote", "[fo\"o", "foo"},
		{"bad-header-backslash", "[fo\\o", "foo"},
		{"section-key-separator", "[foo\x00bar]\n\"1.0.0\"={path=\"pair\"}\n[foo]\n\"1.0.0\"={path=\"single\"}", "foo\x00bar"},
	}
	var source, want strings.Builder
	source.WriteString(`import "./mvs";
import "./util";

function record(text: string, pkg: string): string {
  match (mvs.parse_index(text)) {
    Err(message) => { return "ERR|" + message; },
    Ok(parsed) => {
      let ix = mvs.with_dir(parsed, "/registry");
      let versions: string = "";
      for version in mvs.index_versions(ix, pkg) {
        if (versions.len() > 0) { versions = versions + ";"; }
        versions = versions + version;
      }
      let present: string = if (mvs.index_has(ix,pkg,"1.0.0")) { "true" } else { "false" };
      let src = mvs.index_source(ix,pkg,"1.0.0");
      return "OK|"+util.i32_to_string(ix.entries.len())+"|"+versions+"|"+present+"|"+src.path+"|"+src.url+"|"+src.hash;
    }
  }
}

function main(): i32 {
`)
	for _, tc := range cases {
		fmt.Fprintf(&source, "print(%s+record(%s,%s));\n", versionFixtureQuote(tc.name+"|"), versionFixtureQuote(tc.text), versionFixtureQuote(tc.pkg))
		ix, err := mvs.ParseIndex(tc.text)
		if err != nil {
			fmt.Fprintf(&want, "%s|ERR|%s\n", tc.name, err)
			continue
		}
		ix.Dir = "/registry"
		count := 0
		for _, versions := range ix.Packages {
			count += len(versions)
		}
		var versions []string
		for _, version := range ix.Versions(tc.pkg) {
			versions = append(versions, version.String())
		}
		src, present := ix.SourceFor(tc.pkg, mvs.Version{Major: 1})
		fmt.Fprintf(&want, "%s|OK|%d|%s|%t|%s|%s|%s\n", tc.name, count, strings.Join(versions, ";"), present, src.Path, src.URL, src.Hash)
	}
	source.WriteString("return 0;\n}\n")
	return source.String(), want.String()
}

func TestSelfHostMvsIndexResult(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "mvs.fern")
	source, want := indexResultContractSource()
	writeResolveProject(t, dir, map[string]string{"index_contract.fern": source})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "index_contract.fern", "index_contract", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil || string(out) != want {
				t.Fatalf("index result contract: %v\ngot:\n%swant:\n%s", err, out, want)
			}
		})
	}
}
