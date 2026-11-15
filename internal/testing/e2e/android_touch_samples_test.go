package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// touch_samples gives each place of the motion event of the Touch step that
// runs: the places that Android collected, from the oldest, and then the
// place of the event, each with its time. It gives nothing outside such a
// step.
func TestAndroidTouchSamples(t *testing.T) {
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
pub function hold(ctx: usize, event: usize): void { unsafe { Context.c_store_motion(ctx, event); } }
pub function sample_count(ctx: usize): i32 { return touch_samples(ctx).len(); }
pub function sample_ns(ctx: usize, i: i32): i64 { return touch_samples(ctx)[i].ns; }
pub function sample_x(ctx: usize, i: i32): i32 { return touch_samples(ctx)[i].x as i32; }
pub function sample_y(ctx: usize, i: i32): i32 { return touch_samples(ctx)[i].y as i32; }
`
	// A motion event of this libandroid.so has n places. The last one is the
	// place of the event, and the places before it are its history.
	const android = `#include <stddef.h>
#include <stdint.h>
struct event { size_t n; int64_t ns[4]; float x[4], y[4]; };
size_t AMotionEvent_getHistorySize(const struct event *e) { return e->n - 1; }
int64_t AMotionEvent_getHistoricalEventTime(const struct event *e, size_t i) { return e->ns[i]; }
float AMotionEvent_getHistoricalX(const struct event *e, size_t pointer, size_t i) { return pointer == 0 ? e->x[i] : -1; }
float AMotionEvent_getHistoricalY(const struct event *e, size_t pointer, size_t i) { return pointer == 0 ? e->y[i] : -1; }
int64_t AMotionEvent_getEventTime(const struct event *e) { return e->ns[e->n - 1]; }
float AMotionEvent_getX(const struct event *e, size_t pointer) { return pointer == 0 ? e->x[e->n - 1] : -1; }
float AMotionEvent_getY(const struct event *e, size_t pointer) { return pointer == 0 ? e->y[e->n - 1] : -1; }
`
	const check = `#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <dlfcn.h>

struct event { size_t n; int64_t ns[4]; float x[4], y[4]; };
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*hold)(uintptr_t, uintptr_t) = dlsym(lib, "hold");
  int (*count)(uintptr_t) = dlsym(lib, "sample_count");
  int64_t (*ns)(uintptr_t, int) = dlsym(lib, "sample_ns");
  int (*x)(uintptr_t, int) = dlsym(lib, "sample_x");
  int (*y)(uintptr_t, int) = dlsym(lib, "sample_y");
  if (!context_size || !hold || !count || !ns || !x || !y) return 3;
  uintptr_t ctx = (uintptr_t)calloc(1, context_size());
  if (count(ctx) != 0) { fprintf(stderr, "%d samples with no event\n", count(ctx)); return 4; }
  struct event three = { 3, { 1000, 2000, 3000 }, { 10, 20, 30 }, { 11, 21, 31 } };
  hold(ctx, (uintptr_t)&three);
  if (count(ctx) != 3) { fprintf(stderr, "%d samples, want 3\n", count(ctx)); return 5; }
  for (int i = 0; i < 3; i++) {
    if (ns(ctx, i) != 1000 * (i + 1) || x(ctx, i) != 10 * (i + 1) || y(ctx, i) != 10 * (i + 1) + 1) {
      fprintf(stderr, "sample %d: %lld %d %d\n", i, (long long)ns(ctx, i), x(ctx, i), y(ctx, i));
      return 6;
    }
  }
  struct event one = { 1, { 7000 }, { 5 }, { 6 } };
  hold(ctx, (uintptr_t)&one);
  if (count(ctx) != 1 || ns(ctx, 0) != 7000 || x(ctx, 0) != 5 || y(ctx, 0) != 6) return 7;
  hold(ctx, 0);
  return count(ctx) == 0 ? 0 : 8;
}
`
	androidMockCheck(t, src, "context_size,hold,sample_count,sample_ns,sample_x,sample_y", check, android)
}
