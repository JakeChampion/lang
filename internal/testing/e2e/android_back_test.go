package e2e

import "testing"

// Android gives a press of the back key to the back callback of the
// activity, to the input queue, or to both. new_back takes the first report
// of a press and refuses the report of the other source that follows it.
// queue_back refuses a back key that the keyboard of the system took.
func TestAndroidBackComesOncePerPress(t *testing.T) {
	src := androidSource(t) + `
pub function context_size(): i32 { return Context.c_size() as i32; }
pub function from_callback(ctx: usize, ns: i64): boolean { return new_back(ctx, BACK_FROM_CALLBACK, ns); }
pub function from_queue(ctx: usize, ns: i64): boolean { return new_back(ctx, BACK_FROM_QUEUE, ns); }
pub function key(ctx: usize, action: i32, ns: i64): boolean { return queue_back(ctx, action, ns); }
pub function keyboard(ctx: usize, shows: i32): void { unsafe { Context.c_store_ime(ctx, shows); } }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <dlfcn.h>

static int step;
static void require(int ok) { step++; if (!ok) { fprintf(stderr, "back: check %d failed\n", step); exit(1); } }
#define MS 1000000LL
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int (*context_size)(void) = dlsym(lib, "context_size");
  int (*callback)(uintptr_t, int64_t) = dlsym(lib, "from_callback");
  int (*queue)(uintptr_t, int64_t) = dlsym(lib, "from_queue");
  int (*key)(uintptr_t, int, int64_t) = dlsym(lib, "key");
  void (*keyboard)(uintptr_t, int) = dlsym(lib, "keyboard");
  if (!context_size || !callback || !queue || !key || !keyboard) return 3;
  // A device that gives each press to the input queue only.
  uintptr_t ctx = (uintptr_t)calloc(1, context_size());
  require(queue(ctx, 1000 * MS));
  require(queue(ctx, 1100 * MS));
  // A device that gives each press to the callback only.
  ctx = (uintptr_t)calloc(1, context_size());
  require(callback(ctx, 1000 * MS));
  require(callback(ctx, 1100 * MS));
  // A device that gives each press to both: the second report is not a press.
  ctx = (uintptr_t)calloc(1, context_size());
  require(queue(ctx, 1000 * MS));
  require(!callback(ctx, 1010 * MS));
  require(queue(ctx, 1200 * MS));
  require(!callback(ctx, 1210 * MS));
  // The other source after the pair time is a new press.
  require(callback(ctx, 2000 * MS));
  require(!queue(ctx, 2020 * MS));
  require(queue(ctx, 2400 * MS));
  // A key of the input queue is a press when the key goes up.
  ctx = (uintptr_t)calloc(1, context_size());
  require(!key(ctx, 0, 1000 * MS));
  require(key(ctx, 1, 1050 * MS));
  // The keyboard takes a back that comes while it shows. It hides between
  // the down and the up of the key, and the key is still not a press.
  keyboard(ctx, 1);
  require(!key(ctx, 0, 2000 * MS));
  keyboard(ctx, 0);
  require(!key(ctx, 1, 2050 * MS));
  // The next back, with no keyboard, is a press.
  require(!key(ctx, 0, 3000 * MS));
  require(key(ctx, 1, 3050 * MS));
  // A keyboard that shows when the key goes up takes the back too.
  require(!key(ctx, 0, 4000 * MS));
  keyboard(ctx, 1);
  require(!key(ctx, 1, 4050 * MS));
  return 0;
}
`
	androidMockCheck(t, src, "context_size,from_callback,from_queue,key,keyboard", check, "")
}
