package e2ecompiler

import "testing"

func TestSelfHostMapIteratorChecker(t *testing.T) {
	_, runner, driver := buildCheckerModloadDriverX86(t)
	cases := []struct {
		name, src string
		want      []string
	}{
		{"map-iter-cursor-method-is-deleted", `import "core/map"; function f(m: Map[string, i32]): i32 { let it = m.iter(); if (it.has_next()) { return 1; } return 0; } function main(): i32 { return 0; }`, []string{"E043"}},
		{"map-iter-cursor-type-is-deleted", `import "core/map"; function f(it: MapIter[string, i32]): i32 { return 0; } function main(): i32 { return 0; }`, []string{"E064"}},
		{"map-iter-redeclares-source-method", `import "core/map"; function (m: Map[string, i32]) iter(): i32 { return 1; } function main(): i32 { return 0; }`, []string{"E006"}},
		{"map-iter-methods", "import \"core/map\"; import \"core/iter\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(4); let it: iter.ArrayIter[(string, i32)] = m.iter(); let n: i32 = 0; for (k, v) in it { n = n + v + k.len();  } return n; }\n", nil},
		{"map-entries-arity-e004", `import "core/map"; function main(): i32 { let m: Map[string, i32] = map_new(4); let xs = m.entries(1); return 0; }`, []string{"E004"}},
		{"map-user-method-arg-e038", `import "core/map"; function (m: Map[K, V]) add_extra(value: i32): i32 { return value; } function main(): i32 { let m: Map[string, i32] = map_new(4); return m.add_extra("wrong"); }`, []string{"E038"}},
		{"map-iter-arity-e004", "import \"core/map\";\nfunction main(): i32 { let m: Map[string, i32] = map_new(4); let z = m.iter(1); return 0; }\n", []string{"E004"}},
		{"mapnew-iter-needs-map-type", "import \"core/map\"; import \"core/iter\";\nfunction main(): i32 { let it: iter.ArrayIter[(string, i32)] = map_new(8).iter(); return 0; }\n", []string{"E043"}},
		{"map-iterator-snapshot-local-map", "import \"core/map\"; import \"core/iter\";\nfunction f(): iter.ArrayIter[(i32, i32)] { let m: Map[i32, i32] = map_new(4); m = m.insert(1, 2); return m.iter(); }\nfunction main(): i32 { return 0; }\n", nil},
		{"map-iterator-snapshot-through-local", "import \"core/map\"; import \"core/iter\";\nfunction f(): iter.ArrayIter[(i32, i32)] { let m: Map[i32, i32] = map_new(4); let it = m.iter(); return it; }\nfunction main(): i32 { return 0; }\n", nil},
		{"map-iterator-snapshot-in-array-literal", "import \"core/map\"; import \"core/iter\";\nfunction f(): iter.ArrayIter[(i32, i32)][] { let m: Map[i32, i32] = map_new(4); return [m.iter()]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"map-iterator-snapshot-shadowed", "import \"core/map\"; import \"core/iter\";\nfunction f(m: Map[i32, i32]): iter.ArrayIter[(i32, i32)] { let mm: Map[i32, i32] = map_new(4); let it: iter.ArrayIter[(i32, i32)] = mm.iter(); if (m.len() > 0) { let it2: iter.ArrayIter[(i32, i32)] = m.iter(); let it: iter.ArrayIter[(i32, i32)] = it2; } return it; }\nfunction main(): i32 { return 0; }\n", nil},
		{"map-iterator-snapshot-reassigned", "import \"core/map\"; import \"core/iter\";\nfunction f(m: Map[i32, i32]): iter.ArrayIter[(i32, i32)] { let mm: Map[i32, i32] = map_new(4); let it: iter.ArrayIter[(i32, i32)] = m.iter(); if (m.len() > 0) { it = mm.iter(); } return it; }\nfunction main(): i32 { return 0; }\n", nil},
		{"map-iterator-snapshot-param-ok", "import \"core/map\"; import \"core/iter\";\nfunction f(m: Map[i32, i32]): iter.ArrayIter[(i32, i32)] { return m.iter(); }\nfunction main(): i32 { return 0; }\n", nil},
		{"map-iterator-snapshot-param-field-ok", "import \"core/map\"; import \"core/iter\";\nstruct B { m: Map[i32, i32] }\nfunction f(b: B): iter.ArrayIter[(i32, i32)] { let it = b.m.iter(); return it; }\nfunction main(): i32 { return 0; }\n", nil},
	}
	for _, tc := range cases {
		for _, wrapped := range []bool{false, true} {
			src, name := tc.src, tc.name
			if wrapped {
				src = wrapMainBodyInLambda(src)
				name += "/lambda"
				if src == "" {
					continue
				}
			}
			t.Run(name, func(t *testing.T) {
				got := driverCodes(checkSourceModload(t, runner, driver, src))
				if !equalStrings(got, uniqueSortedCodes(tc.want)) {
					t.Errorf("self-host codes %v, want %v", got, tc.want)
				}
				oracle := goCheckerCodes(t, t.TempDir(), src)
				if !equalStrings(got, oracle) {
					t.Errorf("self-host codes %v, Go codes %v", got, oracle)
				}
			})
		}
	}
}
