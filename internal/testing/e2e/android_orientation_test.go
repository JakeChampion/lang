package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// orientation sends its request to the UI thread, which gives the value to
// setRequestedOrientation of the activity: SENSOR_LANDSCAPE (6),
// SENSOR_PORTRAIT (7) or UNSPECIFIED (-1). The loop thread calls no Java.
// Each request releases the local references that it makes.
func TestAndroidOrientation(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	src += `
pub function context_size(): i32 { return Context.c_size() as i32; }
pub function wire(ctx: usize, activity: usize, read: i32, write: i32, e: usize): void {
  unsafe {
    Context.c_store_activity(ctx, activity);
    Context.c_store_ui_read(ctx, read);
    Context.c_store_ui_write(ctx, write);
    Context.c_store_env(ctx, e);
    Context.c_store_sdk(ctx, 31);
  }
}
pub function turn(ctx: usize, which: i32): void {
  if (which == 0) {
    orientation(ctx, ORIENTATION_ANY);
  } else if (which == 1) {
    orientation(ctx, ORIENTATION_LANDSCAPE);
  } else {
    orientation(ctx, ORIENTATION_PORTRAIT);
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

static int locals, sets, value = 99;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "%s: locals=%d sets=%d value=%d\n", what, locals, sets, value); exit(1); } }

static uintptr_t get_class(uintptr_t e, uintptr_t obj) { require(obj == 100, "class of the activity"); locals++; return 200; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  require(cls == 200 && !strcmp(name, "setRequestedOrientation") && !strcmp(sig, "(I)V"), name);
  return 1;
}
static void call(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(id == 1 && obj == 100, "setRequestedOrientation of the activity");
  value = (int32_t)args[0];
  sets++;
}
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t obj) { require(obj != 0, "release of null"); locals--; }

int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*wire)(uintptr_t, uintptr_t, int, int, uintptr_t) = dlsym(lib, "wire");
  void (*turn)(uintptr_t, int) = dlsym(lib, "turn");
  int (*requests)(uintptr_t, int) = dlsym(lib, "requests");
  require(context_size && wire && turn && requests, "exports");
  static uintptr_t table[240];
  uintptr_t activity[8] = {0};
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[23] = (uintptr_t)release;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[63] = (uintptr_t)call;
  uintptr_t env = (uintptr_t)table;
  uintptr_t *ctx = calloc(1, context_size());
  int fds[2];
  require(pipe(fds) == 0 && fcntl(fds[0], F_SETFL, O_NONBLOCK) == 0, "pipe");
  wire((uintptr_t)ctx, (uintptr_t)activity, fds[0], fds[1], (uintptr_t)&env);
  activity[2] = (uintptr_t)&env;
  activity[3] = 100;
  activity[7] = (uintptr_t)ctx;

  turn((uintptr_t)ctx, 1);
  require(sets == 0, "orientation ran on the loop thread");
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(sets == 1 && value == 6 && locals == 0, "landscape");
  turn((uintptr_t)ctx, 2);
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(sets == 2 && value == 7 && locals == 0, "portrait");
  turn((uintptr_t)ctx, 0);
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(sets == 3 && value == -1 && locals == 0, "back to the device");
  return 0;
}
`
	androidJNIMockCheck(t, src, "context_size,wire,turn,requests", check)
}
