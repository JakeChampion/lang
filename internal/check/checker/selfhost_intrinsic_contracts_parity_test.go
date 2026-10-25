package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestSelfHostTypesEveryIntrinsicFamily(t *testing.T) {
	native := nativeIntrinsicSigs(t)
	lowerable := selfHostLoweredIntrinsics(t)
	checkerTyped := selfHostTypedIntrinsics(t)
	contracted := selfHostIntrinsicContracts(t)

	for _, family := range intrinsicFamilies {
		names := family.members(native)
		names = withLowering(names, lowerable)
		if len(names) == 0 {
			t.Errorf("family %q matched no native intrinsic the self-host lowers — either the "+
				"predicate has drifted from what internal/check/checker registers, or the lowering "+
				"went away, so this family is no longer gating anything", family.name)
			continue
		}
		var untyped, uncontracted []string
		for _, n := range names {
			if !checkerTyped[n] {
				untyped = append(untyped, n)
			}
			if !contracted[n] {
				uncontracted = append(uncontracted, n)
			}
		}
		sort.Strings(untyped)
		sort.Strings(uncontracted)
		if len(untyped) > 0 {
			t.Errorf("family %q: %d intrinsic(s) native types and compiler/checker.fern's "+
				"intrinsic_result does not: %s\nA call to one of these types as unknown, which "+
				"collapses the literal, array or operator holding it.",
				family.name, len(untyped), strings.Join(untyped, ", "))
		}
		if len(uncontracted) > 0 {
			t.Errorf("family %q: %d intrinsic(s) with no contract in compiler/semsource.fern's "+
				"intrinsic_contracts: %s\nThe semantic boundary refuses every function that calls one.",
				family.name, len(uncontracted), strings.Join(uncontracted, ", "))
		}
	}
}

type intrinsicFamily struct {
	name    string
	members func(map[string]*sigShape) []string
}

type sigShape struct {
	params int
	result string
}

var intrinsicFamilies = []intrinsicFamily{
	{"f64 primitive", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, s *sigShape) bool {
			return strings.HasSuffix(n, "_f64") && !strings.HasPrefix(n, "__c_call") &&
				s.params == 1 && s.result == "ast.FloatType"
		})
	}},
	{"bit count", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, s *sigShape) bool {
			base := strings.HasPrefix(n, "__clz") || strings.HasPrefix(n, "__ctz") ||
				strings.HasPrefix(n, "__popcount")
			return base && s.params == 1
		})
	}},
	{"raw memory", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, _ *sigShape) bool {
			for _, p := range []string{"__alloc", "__free", "__load_", "__store_", "__memcpy", "__memset", "__heap_", "__ptr_width"} {
				if strings.HasPrefix(n, p) {
					return true
				}
			}
			return false
		})
	}},
	{"rc probe", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, _ *sigShape) bool {
			return strings.HasPrefix(n, "__rc_") || strings.HasPrefix(n, "__arr_push_shared_")
		})
	}},
	{"lane", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, _ *sigShape) bool {
			return strings.HasPrefix(n, "__f64x2_") || strings.HasPrefix(n, "__f32x4_")
		})
	}},
	{"byte scan", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, _ *sigShape) bool {
			for _, s := range []string{"__sum_bytes", "__ascii_run", "__count_byte", "__count_byte_bytes", "__crc32_cksum_array", "__crc32_ieee_array", "__sum_bytes_array", "__bsd_sum_bytes", "__memchr", "__mismatch_bytes", "__mismatch_bytes_str", "__memchr_bytes", "__rmemchr_bytes", "__scan_set_bytes", "__count_runs_bytes", "__rmemchr", "__mismatch"} {
				if n == s {
					return true
				}
			}
			return false
		})
	}},
}

func withLowering(names []string, lowerable map[string]bool) []string {
	var out []string
	for _, n := range names {
		if lowerable[n] {
			out = append(out, n)
		}
	}
	return out
}

func selfHostLoweredIntrinsics(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range []string{"semsource.fern", "ssarc.fern", "irtables.fern", "lift.fern", "ir.fern"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range regexp.MustCompile(`"(__[A-Za-z0-9_]+)"`).FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	return out
}

func matching(m map[string]*sigShape, pred func(string, *sigShape) bool) []string {
	var out []string
	for n, s := range m {
		if pred(n, s) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func nativeIntrinsicSigs(t *testing.T) map[string]*sigShape {
	t.Helper()
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	out := map[string]*sigShape{}
	for name, sig := range info.FuncSigs {
		if !strings.HasPrefix(name, "__") || strings.HasPrefix(name, "__method_") {
			continue
		}
		out[name] = &sigShape{params: len(sig.Params), result: fmt.Sprintf("%T", sig.Result)}
	}
	if len(out) == 0 {
		t.Fatal("no `__` intrinsics in a checked probe — this test would pass on anything")
	}
	return out
}

var selfHostTypedRE = regexp.MustCompile(`"(__[A-Za-z0-9_]+)"`)

func selfHostTypedIntrinsics(t *testing.T) map[string]bool {
	t.Helper()
	body := selfHostSection(t, "checker.fern",
		regexp.MustCompile(`(?s)function is_f64_primitive\(.*?\nfunction builtin_result_type\(`))
	out := map[string]bool{}
	for _, m := range selfHostTypedRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	if strings.Contains(body, `"__c_call"`) {
		for n := 0; n <= 4; n++ {
			for _, suffix := range []string{"", "_f32", "_f64"} {
				out["__c_call"+string(rune('0'+n))+suffix] = true
			}
		}
	}
	return out
}

func selfHostIntrinsicContracts(t *testing.T) map[string]bool {
	t.Helper()
	body := selfHostSection(t, "semsource.fern",
		regexp.MustCompile(`(?s)function intrinsic_contracts\(.*?\nfunction union_result_contracts\(`))
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`ssasem\.Contract \{\s*name: "(__[A-Za-z0-9_]+)"`).FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\[\s*((?:"__[A-Za-z0-9_]+",?\s*)+)\]`).FindAllStringSubmatch(body, -1) {
		for _, n := range regexp.MustCompile(`"(__[A-Za-z0-9_]+)"`).FindAllStringSubmatch(m[1], -1) {
			out[n[1]] = true
		}
	}
	if strings.Contains(body, `"__c_call" + util.i32_to_string(n)`) {
		for n := 0; n <= 4; n++ {
			for _, suffix := range []string{"", "_f32", "_f64"} {
				out["__c_call"+string(rune('0'+n))+suffix] = true
			}
		}
	}
	return out
}

func selfHostSection(t *testing.T, file string, re *regexp.Regexp) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := re.FindString(string(b))
	if body == "" {
		t.Fatalf("%s: the section this gate reads has been renamed or reshaped; "+
			"point the pattern at its new form rather than deleting the gate", file)
	}
	return body
}

func TestSelfHostParameterisesEveryTypedBuiltin(t *testing.T) {
	typed := selfHostTypedBuiltins(t, `(?s)function is_f64_primitive\(.*?\nfunction builtin_result_type\(`, false)
	parameterised := selfHostTypedBuiltins(t, `(?s)function intrinsic_params\(.*?\npub function type_debug\(`, true)
	if len(typed) == 0 || len(parameterised) == 0 {
		t.Fatal("one of the two builtin tables read empty — this test would pass on anything")
	}
	var unchecked []string
	for n := range typed {
		if !parameterised[n] {
			unchecked = append(unchecked, n)
		}
	}
	sort.Strings(unchecked)
	if len(unchecked) > 0 {
		t.Errorf("%d builtin(s) whose result compiler/checker.fern types and whose parameters it "+
			"does not: %s\nA call to one of these infers, so nothing refuses a wrong argument and the "+
			"self-host accepts what native rejects.", len(unchecked), strings.Join(unchecked, ", "))
	}
}

func selfHostTypedBuiltins(t *testing.T, section string, members bool) map[string]bool {
	t.Helper()
	body := selfHostSection(t, "checker.fern", regexp.MustCompile(section))
	if members {
		var claiming []string
		for _, m := range regexp.MustCompile(`(?s)if \(([^{]*)\) \{\s*return [^;]*, true\);`).FindAllStringSubmatch(body, -1) {
			claiming = append(claiming, m[1])
		}
		body = strings.Join(claiming, "\n")
	}
	out := map[string]bool{}
	harvest := func(src string) {
		for _, m := range regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`).FindAllStringSubmatch(src, -1) {
			out[m[1]] = true
		}
	}
	harvest(body)
	for _, m := range regexp.MustCompile(`(is_[a-z0-9_]+)\(name\)`).FindAllStringSubmatch(body, -1) {
		harvest(selfHostSection(t, "checker.fern",
			regexp.MustCompile(`(?s)function `+m[1]+`\(name: string\): boolean \{.*?\n\}`)))
	}
	return out
}

func TestSelfHostRawFloorIsTypedWhole(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	called := map[string]bool{}
	for _, m := range regexp.MustCompile(`\b(__raw_[a-z0-9_]+|__syscall[0-9])\(`).FindAllStringSubmatch(read("asmcore.fern"), -1) {
		called[m[1]] = true
	}
	if len(called) == 0 {
		t.Fatal("asmcore.fern's helper sources call no raw-floor name — the pattern has drifted from its spelling")
	}
	typed := rawFloorTableNames(t, read("checker.fern"), `function raw_floor_sigs\(\): RawFloorSig\[\] \{`)
	arms := rawFloorTableNames(t, read("ssarc.fern"), `function raw_floor_ops\(\): RawOps\[\] \{`)
	var untyped, unlowered []string
	for n := range called {
		if !typed[n] {
			untyped = append(untyped, n)
		}
		if !arms[n] {
			unlowered = append(unlowered, n)
		}
	}
	sort.Strings(untyped)
	sort.Strings(unlowered)
	if len(untyped) > 0 {
		t.Errorf("the runtime helpers call %s, which checker.fern's raw_floor_sigs does not type", strings.Join(untyped, ", "))
	}
	if len(unlowered) > 0 {
		t.Errorf("the runtime helpers call %s, which ssarc.fern's raw_floor_ops has no row for", strings.Join(unlowered, ", "))
	}
}

func TestSelfHostRawFloorLookupReachesEveryRow(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "checker.fern"))
	if err != nil {
		t.Fatalf("read checker.fern: %v", err)
	}
	src := string(b)
	rows := rawFloorTableNames(t, src, `function raw_floor_sigs\(\): RawFloorSig\[\] \{`)
	lookup := regexp.MustCompile(`(?s)function raw_floor_at\(.*?\n\}`).FindString(src)
	if lookup == "" {
		t.Fatal("checker.fern has no raw_floor_at — point this test at the lookup that replaced it")
	}
	var prefixes []string
	for _, m := range regexp.MustCompile(`str_has_prefix\(name, "([^"]+)"\)`).FindAllStringSubmatch(lookup, -1) {
		prefixes = append(prefixes, m[1])
	}
	if len(rows) == 0 || len(prefixes) == 0 {
		t.Fatalf("read %d rows and %d prefixes — the patterns have drifted, so this test proves nothing", len(rows), len(prefixes))
	}
	var unreached []string
	for n := range rows {
		reached := false
		for _, p := range prefixes {
			if strings.HasPrefix(n, p) {
				reached = true
			}
		}
		if !reached {
			unreached = append(unreached, n)
		}
	}
	sort.Strings(unreached)
	if len(unreached) > 0 {
		t.Errorf("raw_floor_at's prefixes %v reach none of %s, so the checker leaves those rows untyped",
			prefixes, strings.Join(unreached, ", "))
	}
}

func TestSelfHostIntrinsicNamesAreInternal(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "checker.fern"))
	if err != nil {
		t.Fatalf("read checker.fern: %v", err)
	}
	src := string(b)
	compared := regexp.MustCompile(`name == "([^"]*)"`)
	listed := regexp.MustCompile(`(?s)let names: string\[\] = \[(.*?)\];`)
	quoted := regexp.MustCompile(`"([^"]*)"`)
	names := map[string]bool{}
	for _, fn := range []string{"intrinsic_result", "intrinsic_params", "is_f64_primitive", "is_bit_count", "byte_kernel_arity", "is_sha_hw_builtin"} {
		body := regexp.MustCompile(`(?s)\nfunction ` + fn + `\(.*?\n\}`).FindString(src)
		if body == "" {
			t.Fatalf("checker.fern has no function %s — point this test at what replaced it", fn)
		}
		for _, m := range compared.FindAllStringSubmatch(body, -1) {
			names[m[1]] = true
		}
		for _, l := range listed.FindAllStringSubmatch(body, -1) {
			for _, m := range quoted.FindAllStringSubmatch(l[1], -1) {
				names[m[1]] = true
			}
		}
	}
	rows := regexp.MustCompile(`(?s)function raw_floor_sigs\(\): RawFloorSig\[\] \{(.*?)\n\}`).FindStringSubmatch(src)
	if rows == nil {
		t.Fatal("checker.fern has no raw_floor_sigs table")
	}
	for _, m := range regexp.MustCompile(`name: "([^"]*)"`).FindAllStringSubmatch(rows[1], -1) {
		names[m[1]] = true
	}
	if len(names) < 100 {
		t.Fatalf("read only %d intrinsic names — the patterns have drifted, so this test proves nothing", len(names))
	}
	var plain []string
	for n := range names {
		if !strings.HasPrefix(n, "__") {
			plain = append(plain, n)
		}
	}
	sort.Strings(plain)
	if len(plain) > 0 {
		t.Errorf("the intrinsic lookups answer %s, which internal_name refuses before any table is read", strings.Join(plain, ", "))
	}
}

func TestSelfHostBuiltinRowsAreSorted(t *testing.T) {
	body := selfHostSection(t, "checker.fern",
		regexp.MustCompile(`(?s)function builtin_sigs\(\): string\[\] \{.*?\n\}`))
	var rows []string
	for _, m := range regexp.MustCompile(`(?m)^\s*"([^"]*)",$`).FindAllStringSubmatch(body, -1) {
		rows = append(rows, m[1])
	}
	if len(rows) < 100 {
		t.Fatalf("read only %d rows — the pattern has drifted, so this test proves nothing", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1] >= rows[i] {
			t.Errorf("builtin_sigs() row %d %q does not sort after %q", i, rows[i], rows[i-1])
		}
	}
}

func rawFloorTableNames(t *testing.T, src, header string) map[string]bool {
	t.Helper()
	body := regexp.MustCompile(`(?s)` + header + `(.*?)\n\}`).FindStringSubmatch(src)
	if body == nil {
		t.Fatalf("no table matching %s", header)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`name: "(__[a-z0-9_]+)"`).FindAllStringSubmatch(body[1], -1) {
		out[m[1]] = true
	}
	return out
}
