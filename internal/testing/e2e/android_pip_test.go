package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// pip_auto sends the shape of the video to the UI thread, which gives it to
// setPictureInPictureParams. The request holds no buffer, so the UI thread
// frees none. Each call releases the local references that it makes.
func TestAndroidPipAuto(t *testing.T) {
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
pub function shape(ctx: usize, on: i32, w: i32, h: i32): void { pip_auto(ctx, on != 0, w, h); }
pub function requests(ctx: usize, read: i32): i32 { return on_ui_request(read, 0, ctx); }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <fcntl.h>
#include <unistd.h>

static int locals, sets, width, height, entered = -1;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "%s: locals=%d sets=%d %dx%d entered=%d\n", what, locals, sets, width, height, entered); exit(1); } }

enum { BUILDER_CLASS = 300, RATIONAL_CLASS = 400, BUILDER = 500, RATIO = 600, PARAMS = 700 };
static const char *names[] = { "", "<init>", "setAspectRatio", "setAutoEnterEnabled", "build", "setPictureInPictureParams" };

static uintptr_t get_class(uintptr_t e, uintptr_t obj) { require(obj != 0, "class of null"); locals++; return obj + 1000; }
static uintptr_t find_class(uintptr_t e, const char *name) {
  locals++;
  if (!strcmp(name, "android/app/PictureInPictureParams$Builder")) return BUILDER_CLASS;
  require(!strcmp(name, "android/util/Rational"), "class name");
  return RATIONAL_CLASS;
}
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  for (uintptr_t i = 1; i < sizeof names / sizeof names[0]; i++) {
    if (!strcmp(name, names[i])) return i;
  }
  require(0, name);
  return 0;
}
static uintptr_t new_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  require(id == 1, "constructor");
  locals++;
  if (cls == BUILDER_CLASS) return BUILDER;
  require(cls == RATIONAL_CLASS, "class of the new object");
  width = (int)args[0];
  height = (int)args[1];
  return RATIO;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(obj == BUILDER, "method of the builder");
  locals++;
  if (id == 2) { require(args[0] == RATIO, "setAspectRatio"); return BUILDER; }
  if (id == 3) { entered = (int)args[0]; return BUILDER; }
  require(id == 4, "build");
  return PARAMS;
}
static void call(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(id == 5 && obj == 100 && args[0] == PARAMS, "setPictureInPictureParams");
  sets++;
}
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t obj) { require(obj != 0, "release of null"); locals--; }

int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*wire)(uintptr_t, uintptr_t, int, int, uintptr_t) = dlsym(lib, "wire");
  void (*shape)(uintptr_t, int, int, int) = dlsym(lib, "shape");
  int (*requests)(uintptr_t, int) = dlsym(lib, "requests");
  require(context_size && wire && shape && requests, "exports");
  static uintptr_t table[240];
  uintptr_t activity[8] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[23] = (uintptr_t)release;
  table[30] = (uintptr_t)new_object;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[63] = (uintptr_t)call;
  uintptr_t env = (uintptr_t)table;
  uintptr_t *ctx = calloc(1, context_size());
  int fds[2];
  require(pipe(fds) == 0 && fcntl(fds[0], F_SETFL, O_NONBLOCK) == 0, "pipe");
  wire((uintptr_t)ctx, (uintptr_t)activity, fds[0], fds[1], (uintptr_t)&env);
  activity[2] = (uintptr_t)&env;
  activity[3] = 100;
  activity[7] = (uintptr_t)ctx;

  shape((uintptr_t)ctx, 1, 320, 240);
  require(sets == 0, "pip_auto ran on the loop thread");
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(sets == 1 && width == 320 && height == 240 && entered == 1 && locals == 0, "320 by 240, on");
  shape((uintptr_t)ctx, 0, 16, 9);
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(sets == 2 && width == 16 && height == 9 && entered == 0 && locals == 0, "16 by 9, off");
  // Android refuses a shape wider than 2.39 to 1.
  shape((uintptr_t)ctx, 1, 1000, 100);
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(sets == 3 && width == 239 && height == 100 && locals == 0, "the widest shape");

  // With no pipe to the UI thread, the request goes nowhere.
  wire((uintptr_t)ctx, (uintptr_t)activity, fds[0], -1, (uintptr_t)&env);
  shape((uintptr_t)ctx, 1, 320, 240);
  require(sets == 3, "a request with no pipe");
  return 0;
}
`
	androidJNIMockCheck(t, src, "context_size,wire,shape,requests", check)
}
