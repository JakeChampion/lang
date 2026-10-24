package e2e

import (
	"testing"
)

const deadAliasAppendSrc = `@noinline function observed_length(xs: i32[]): i32 { return xs.len(); }
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        let keep: i32[] = acc;
        if (observed_length(keep) != i) { return 999; }
        acc = acc.append(i);
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`

const liveAliasAppendSrc = `@noinline function observed_length(xs: i32[]): i32 { return xs.len(); }
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        let keep: i32[] = acc;
        acc = acc.append(i);
        if (observed_length(keep) != i) { return 999; }
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    return __arr_push_shared_count();
}`

func TestX86_64DeadAliasAppendNoCopy(t *testing.T) {
	if _, got := compileAndRunX86_64FreeOn(t, lengthOnlyAliasAppendSrc); got != 0 {
		t.Errorf("x86-64 length-only alias: got %d, want 0 copies with the original length preserved", got)
	}

	if _, got := compileAndRunX86_64FreeOn(t, deadAliasAppendSrc); got != 0 {
		t.Errorf("x86-64 dead alias: __arr_push_shared_count() = %d, want 0 — "+
			"a loop-body binding nothing reads again is still forcing a full buffer copy per append", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, liveAliasAppendSrc); got != 193 {
		t.Errorf("x86-64 live alias: __arr_push_shared_count() = %d, want 193 — "+
			"the alias is read after the append, so every copy is mandatory", got)
	}
}

const lengthOnlyAliasAppendSrc = `function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        let keep: i32[] = acc;
        acc = acc.append(i);
        if (keep.len() != i) { return 999; }
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    return __arr_push_shared_count();
}`
