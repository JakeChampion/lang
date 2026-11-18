package manifest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The [android] keys of this parser and of compiler/fern_toml.fern are one
// list: a key that one of them takes and the other refuses breaks fern -apk
// or the pre-push hook.
func TestAndroidKeysMatchCompiler(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "fern_toml.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]string{"ANDROID_TEXT": androidText, "ANDROID_SWITCHES": androidSwitches, "ANDROID_LISTS": androidLists, "ANDROID_NUMBERS": androidNumbers} {
		m := regexp.MustCompile(`const ` + name + `: string\[\] = \[([^\]]*)\];`).FindSubmatch(raw)
		if m == nil {
			t.Fatalf("compiler/fern_toml.fern has no %s", name)
		}
		var got []string
		for _, item := range strings.Split(string(m[1]), ",") {
			got = append(got, strings.Trim(strings.TrimSpace(item), `"`))
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: the compiler lists %v, this parser %v", name, got, want)
		}
	}
	_, err = androidValue("colour", `"red"`)
	if err == nil || !strings.Contains(err.Error(), "package, label, icon, banner, key, version_name, min_sdk, version_code, home, tv, pip, compress, cleartext, debuggable, permissions, assets, dex, widget") {
		t.Errorf("the refusal of an unknown key does not list the keys: %v", err)
	}
}
