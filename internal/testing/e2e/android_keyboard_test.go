package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// show_keyboard asks InputMethodManager for the keyboard on behalf of the
// decor view of the window, which the system serves, and hides it by the
// token of that window. It releases each local reference it makes.
func TestAndroidKeyboardThroughDecorView(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	src += `
pub function keyboard(ctx: usize, on: boolean): void { show_keyboard(ctx, on); }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static int locals, shown = -1, calls;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "keyboard: %s (locals=%d shown=%d calls=%d)\n", what, locals, shown, calls); exit(1); } }
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { locals++; return obj + 1000; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  if (!strcmp(name, "getWindow")) { require(cls == 1100 && !strcmp(sig, "()Landroid/view/Window;"), "getWindow"); return 1; }
  if (!strcmp(name, "getDecorView")) { require(cls == 1200 && !strcmp(sig, "()Landroid/view/View;"), "getDecorView"); return 2; }
  if (!strcmp(name, "getSystemService")) { require(cls == 1100 && !strcmp(sig, "(Ljava/lang/String;)Ljava/lang/Object;"), "getSystemService"); return 3; }
  if (!strcmp(name, "showSoftInput")) { require(cls == 1400 && !strcmp(sig, "(Landroid/view/View;I)Z"), "showSoftInput"); return 4; }
  if (!strcmp(name, "getWindowToken")) { require(cls == 1300 && !strcmp(sig, "()Landroid/os/IBinder;"), "getWindowToken"); return 5; }
  require(cls == 1400 && !strcmp(name, "hideSoftInputFromWindow") && !strcmp(sig, "(Landroid/os/IBinder;I)Z"), "hideSoftInputFromWindow");
  return 6;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  locals++;
  if (id == 1) { require(obj == 100, "the window of the activity"); return 200; }
  if (id == 2) { require(obj == 200, "the decor view of the window"); return 300; }
  if (id == 3) { require(obj == 100 && args[0] == 600, "the input method service"); return 400; }
  require(id == 5 && obj == 300, "the token of the decor view");
  return 500;
}
static uint8_t boolean(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  calls++;
  require(obj == 400 && args[1] == 0, "a call on InputMethodManager");
  if (id == 4) { require(args[0] == 300, "showSoftInput on the decor view"); shown = 1; }
  else { require(id == 6 && args[0] == 500, "hideSoftInputFromWindow with the window token"); shown = 0; }
  return 1;
}
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  const char *want = "input_method";
  require(n == strlen(want), "the length of the service name");
  for (uintptr_t i = 0; i < n; i++) require(units[i] == (uint16_t)want[i], "the service name");
  locals++;
  return 600;
}
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t ref) { if (ref) locals--; }
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  void (*keyboard)(uintptr_t, int) = dlsym(lib, "keyboard");
  require(keyboard != 0, "the keyboard entry");
  uintptr_t table[240] = {0}, activity[10] = {0}, ctx[9] = {0};
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[23] = (uintptr_t)release;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[39] = (uintptr_t)boolean;
  table[163] = (uintptr_t)new_string;
  uintptr_t env = (uintptr_t)table;
  activity[2] = (uintptr_t)&env;
  activity[3] = 100;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&env;
  for (int i = 0; i < 4; i++) {
    int on = i % 2 == 0;
    keyboard((uintptr_t)ctx, on);
    require(shown == on && locals == 0, "the keyboard state after a call");
  }
  require(calls == 4, "one call to InputMethodManager each time");
  return 0;
}
`
	androidJNIMockCheck(t, src, "keyboard", check)
}
