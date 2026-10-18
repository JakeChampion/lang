package envflag

import (
	"os"
	"testing"
)

func TestValueOn(t *testing.T) {
	for _, tc := range []struct {
		v  string
		on bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"False", false},
		{"off", false},
		{"OFF", false},
		{"Off", false},
		{"1", true},
		{"true", true},
		{"on", true},
		{"yes", true},
		{"quiet", true},
		{"00", true},
		{" 0", true},
		{"no", true},
		{"falſe", true},
	} {
		if got := ValueOn(tc.v); got != tc.on {
			t.Errorf("ValueOn(%q) = %v, want %v", tc.v, got, tc.on)
		}
	}
}

func TestOn(t *testing.T) {
	const name = "FERN_ENVFLAG_TEST"
	os.Unsetenv(name)
	if On(name) {
		t.Error("an unset variable reads as on")
	}
	for v, want := range map[string]bool{"": false, "0": false, "Off": false, "1": true, "cpu": true} {
		t.Setenv(name, v)
		if got := On(name); got != want {
			t.Errorf("%s=%q: On = %v, want %v", name, v, got, want)
		}
	}
}
