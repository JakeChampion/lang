package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

func TestAndroidImmersiveRestoresBars(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	// These entries call the same private function as the focus callback.
	src += `
pub function focus(activity: usize): void { apply_system_bars(activity); }
pub function mode(ctx: usize, on: boolean): void { immersive(ctx, on); }
pub function context_size(): i32 { return Context.c_size() as i32; }
pub function wire(ctx: usize, activity: usize, read: i32, write: i32): void {
  unsafe {
    Context.c_store_activity(ctx, activity);
    Context.c_store_ui_read(ctx, read);
    Context.c_store_ui_write(ctx, write);
  }
}
pub function requests(ctx: usize, read: i32): i32 { return on_ui_request(read, 0, ctx); }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <fcntl.h>
#include <unistd.h>

static int locals, calls, ui = 77, flags = 1024 | 128;
static void require(int ok) { if (!ok) { fprintf(stderr, "bars: locals=%d calls=%d ui=%d flags=%d\n", locals, calls, ui, flags); exit(1); } }
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { locals++; return obj + 1000; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  if (!strcmp(name, "getWindow")) { require(cls == 1100 && !strcmp(sig, "()Landroid/view/Window;")); return 1; }
  if (!strcmp(name, "getDecorView")) { require(cls == 1200 && !strcmp(sig, "()Landroid/view/View;")); return 2; }
  if (!strcmp(name, "setFlags")) { require(cls == 1200 && !strcmp(sig, "(II)V")); return 3; }
  require(cls == 1300 && !strcmp(name, "setSystemUiVisibility") && !strcmp(sig, "(I)V"));
  return 4;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t args) {
  require((obj == 100 && id == 1) || (obj == 200 && id == 2));
  locals++;
  return obj + 100;
}
static void call(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (id == 3) { require(obj == 200 && args[1] == 1024); flags = (flags & ~(int)args[1]) | ((int)args[0] & (int)args[1]); }
  else { require(id == 4 && obj == 300); ui = (int)args[0]; }
  calls++;
}
static uintptr_t exception(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t obj) { require(obj == 200 || obj == 300 || obj == 1100 || obj == 1200 || obj == 1300); locals--; }
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  void (*focus)(uintptr_t) = dlsym(lib, "focus");
  void (*mode)(uintptr_t, int) = dlsym(lib, "mode");
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*wire)(uintptr_t, uintptr_t, int, int) = dlsym(lib, "wire");
  int (*requests)(uintptr_t, int) = dlsym(lib, "requests");
  require(focus && mode && context_size && wire && requests);
  uintptr_t table[64] = {0}, activity[8] = {0};
  uintptr_t *ctx = calloc(1, context_size());
  int fds[2];
  require(pipe(fds) == 0 && fcntl(fds[0], F_SETFL, O_NONBLOCK) == 0);
  wire((uintptr_t)ctx, (uintptr_t)activity, fds[0], fds[1]);
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[63] = (uintptr_t)call;
  table[15] = (uintptr_t)exception;
  table[23] = (uintptr_t)release;
  uintptr_t env = (uintptr_t)table;
  activity[2] = (uintptr_t)&env;
  activity[3] = 100;
  activity[7] = (uintptr_t)ctx;
  focus((uintptr_t)activity);
  require(calls == 0 && ui == 77 && flags == (1024 | 128));
  for (int i = 0; i < 4; i++) {
    int on = i % 2 == 0;
    mode((uintptr_t)ctx, on);
    // The request of the mode changes the bars with no focus change.
    require(requests((uintptr_t)ctx, fds[0]) == 1);
    require(ui == (on ? 5894 : 0) && flags == (128 | (on ? 1024 : 0)) && locals == 0);
    for (int again = 0; again < 2; again++) {
      focus((uintptr_t)activity);
      require(ui == (on ? 5894 : 0) && flags == (128 | (on ? 1024 : 0)) && locals == 0);
    }
  }
  require(calls == 24);
  return 0;
}
`
	androidJNIMockCheck(t, src, "focus,mode,context_size,wire,requests", check)
}
