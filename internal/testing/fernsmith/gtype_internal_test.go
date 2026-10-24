package fernsmith

import "testing"

func TestGtypeNamesCoversEveryBuiltin(t *testing.T) {
	for i := gtype(0); i < numTypes; i++ {
		if gtypeNames[i] == "" {
			t.Errorf("gtypeNames[%d] is empty; missing entry for builtin gtype #%d", int(i), int(i))
		}
	}
	if len(allTypes) != len(gtypeNames) {
		t.Errorf("len(allTypes)=%d != len(gtypeNames)=%d", len(allTypes), len(gtypeNames))
	}
	if got, want := tI32.String(), "i32"; got != want {
		t.Errorf("tI32.String() = %q, want %q", got, want)
	}
	if got, want := tResI32I32.String(), "Result[i32, i32]"; got != want {
		t.Errorf("tResI32I32.String() = %q, want %q", got, want)
	}
}

func TestGtypeStringOnDynamicReturnsPlaceholder(t *testing.T) {
	dyn := numTypes + 3
	got := dyn.String()
	want := "?dyn" + itoa(int(dyn))
	if got != want {
		t.Errorf("gtype(%d).String() = %q, want %q", int(dyn), got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
