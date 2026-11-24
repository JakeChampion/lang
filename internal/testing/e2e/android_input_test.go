package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// androidSource is std/android as one Fern source, so that a test can add
// exported entries that call its private functions.
func androidSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// A step that asks for the next frame between a vsync and its Vsync event
// does not lose that event. The loop reads a touch event and a vsync in one
// poll when both are ready, and an app asks for a frame in the step of the
// touch event.
func TestAndroidVsyncIsNotLost(t *testing.T) {
	src := androidSource(t) + `
pub function context_size(): i32 { return Context.c_size() as i32; }
pub function ask(ctx: usize): void { request_frame(ctx); }
pub function vsync(ctx: usize, ns: i64): void { on_vsync(ns, ctx); }
pub function take(ctx: usize): i64 {
  match (frame_event(ctx)) {
    Some(Vsync(ns)) => {
      return ns;
    },
    _ => {
      return -1i64;
    }
  }
}
`
	const android = `static int posted;
void *AChoreographer_getInstance(void) { return (void *)8; }
void AChoreographer_postFrameCallback(void *choreographer, void *callback, void *data) { posted++; }
int choreographer_posts(void) { return posted; }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <dlfcn.h>

static int step;
static void require(int ok) { step++; if (!ok) { fprintf(stderr, "frame flags: check %d failed\n", step); exit(1); } }
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*ask)(uintptr_t) = dlsym(lib, "ask");
  void (*vsync)(uintptr_t, int64_t) = dlsym(lib, "vsync");
  int64_t (*take)(uintptr_t) = dlsym(lib, "take");
  int (*posts)(void) = dlsym(lib, "choreographer_posts");
  if (!context_size || !ask || !vsync || !take || !posts) return 3;
  uintptr_t ctx = (uintptr_t)calloc(1, context_size());
  require(take(ctx) == -1);
  ask(ctx);
  ask(ctx);
  require(posts() == 1);
  vsync(ctx, 1000);
  // The step of a touch event asks for the next frame before the loop
  // gives the Vsync event of the vsync that came.
  ask(ctx);
  require(posts() == 2);
  require(take(ctx) == 1000);
  require(take(ctx) == -1);
  // The frame after it is asked for, so one more call posts nothing.
  ask(ctx);
  require(posts() == 2);
  vsync(ctx, 2000);
  require(take(ctx) == 2000);
  ask(ctx);
  require(posts() == 3);
  return 0;
}
`
	androidMockCheck(t, src, "context_size,ask,vsync,take", check, android)
}

// touch_pointers gives each pointer of the motion event of the Touch step
// that runs, with its id, its tool and its places from the oldest, and
// touch_index gives the pointer of an action 5 or 6. Both give nothing
// outside such a step.
func TestAndroidTouchPointers(t *testing.T) {
	src := androidSource(t) + `
pub function context_size(): i32 { return Context.c_size() as i32; }
pub function hold(ctx: usize, event: usize): void { unsafe { Context.c_store_motion(ctx, event); } }
pub function pointer_count(ctx: usize): i32 { return touch_pointers(ctx).len(); }
pub function pointer_id(ctx: usize, p: i32): i32 { return touch_pointers(ctx)[p].id; }
pub function pointer_tool(ctx: usize, p: i32): i32 { return touch_pointers(ctx)[p].tool; }
pub function place_count(ctx: usize, p: i32): i32 { return touch_pointers(ctx)[p].samples.len(); }
pub function place_ns(ctx: usize, p: i32, i: i32): i64 { return touch_pointers(ctx)[p].samples[i].ns; }
pub function place_x(ctx: usize, p: i32, i: i32): i32 { return touch_pointers(ctx)[p].samples[i].x as i32; }
pub function place_y(ctx: usize, p: i32, i: i32): i32 { return touch_pointers(ctx)[p].samples[i].y as i32; }
pub function changed(ctx: usize): i32 { return touch_index(ctx); }
`
	// A motion event of this libandroid.so has `places` places for each of
	// its pointers. The last one is the place of the event, and the places
	// before it are its history.
	const event = `#include <stddef.h>
#include <stdint.h>
struct event { int32_t action; size_t pointers, places; int32_t id[3], tool[3]; int64_t ns[4]; float x[3][4], y[3][4]; };
`
	const android = event + `int32_t AMotionEvent_getAction(const struct event *e) { return e->action; }
size_t AMotionEvent_getPointerCount(const struct event *e) { return e->pointers; }
int32_t AMotionEvent_getPointerId(const struct event *e, size_t p) { return e->id[p]; }
int32_t AMotionEvent_getToolType(const struct event *e, size_t p) { return e->tool[p]; }
size_t AMotionEvent_getHistorySize(const struct event *e) { return e->places - 1; }
int64_t AMotionEvent_getHistoricalEventTime(const struct event *e, size_t i) { return e->ns[i]; }
float AMotionEvent_getHistoricalX(const struct event *e, size_t p, size_t i) { return e->x[p][i]; }
float AMotionEvent_getHistoricalY(const struct event *e, size_t p, size_t i) { return e->y[p][i]; }
int64_t AMotionEvent_getEventTime(const struct event *e) { return e->ns[e->places - 1]; }
float AMotionEvent_getX(const struct event *e, size_t p) { return e->x[p][e->places - 1]; }
float AMotionEvent_getY(const struct event *e, size_t p) { return e->y[p][e->places - 1]; }
`
	const check = event + `#include <stdio.h>
#include <stdlib.h>
#include <dlfcn.h>

static int step;
static void require(int ok) { step++; if (!ok) { fprintf(stderr, "touch pointers: check %d failed\n", step); exit(1); } }
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*hold)(uintptr_t, uintptr_t) = dlsym(lib, "hold");
  int (*pointers)(uintptr_t) = dlsym(lib, "pointer_count");
  int (*id)(uintptr_t, int) = dlsym(lib, "pointer_id");
  int (*tool)(uintptr_t, int) = dlsym(lib, "pointer_tool");
  int (*places)(uintptr_t, int) = dlsym(lib, "place_count");
  int64_t (*ns)(uintptr_t, int, int) = dlsym(lib, "place_ns");
  int (*x)(uintptr_t, int, int) = dlsym(lib, "place_x");
  int (*y)(uintptr_t, int, int) = dlsym(lib, "place_y");
  int (*changed)(uintptr_t) = dlsym(lib, "changed");
  if (!context_size || !hold || !pointers || !id || !tool || !places || !ns || !x || !y || !changed) return 3;
  uintptr_t ctx = (uintptr_t)calloc(1, context_size());
  require(pointers(ctx) == 0 && changed(ctx) == 0);
  // A move of two fingers with three places each. The ids are not the
  // indexes: the finger with the id 0 went up before.
  struct event move = { 2, 2, 3, { 4, 1 }, { 1, 1 }, { 1000, 2000, 3000 },
    { { 10, 20, 30 }, { 110, 120, 130 } }, { { 11, 21, 31 }, { 111, 121, 131 } } };
  hold(ctx, (uintptr_t)&move);
  require(pointers(ctx) == 2 && changed(ctx) == 0);
  require(id(ctx, 0) == 4 && id(ctx, 1) == 1 && tool(ctx, 0) == 1);
  for (int p = 0; p < 2; p++) {
    require(places(ctx, p) == 3);
    for (int i = 0; i < 3; i++) {
      require(ns(ctx, p, i) == 1000 * (i + 1) && x(ctx, p, i) == 100 * p + 10 * (i + 1) && y(ctx, p, i) == 100 * p + 10 * (i + 1) + 1);
    }
  }
  // The second pointer of three goes down: the action 5 with the index 1 in
  // the bits from 8. The third pointer is a mouse.
  struct event down = { 5 | 1 << 8, 3, 1, { 4, 7, 1 }, { 1, 2, 3 }, { 9000 }, { { 1 }, { 2 }, { 3 } }, { { 4 }, { 5 }, { 6 } } };
  hold(ctx, (uintptr_t)&down);
  require(pointers(ctx) == 3 && changed(ctx) == 1 && id(ctx, 1) == 7 && tool(ctx, 1) == 2 && tool(ctx, 2) == 3);
  require(places(ctx, 2) == 1 && ns(ctx, 2, 0) == 9000 && x(ctx, 2, 0) == 3 && y(ctx, 2, 0) == 6);
  hold(ctx, 0);
  require(pointers(ctx) == 0 && changed(ctx) == 0);
  return 0;
}
`
	androidMockCheck(t, src, "context_size,hold,pointer_count,pointer_id,pointer_tool,place_count,place_ns,place_x,place_y,changed", check, android)
}

// windowJNI is a JNIEnv for the checks of the window requests: an activity
// with a window, a decor view, and a display with three modes. The first
// mode is the one in use, the second has the same size and more refreshes
// in a second, and the third has more refreshes at another size.
const windowJNI = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static int step, live, source = -1, asked, preferred, applied;
static void require(int ok) { step++; if (!ok) { fprintf(stderr, "window: check %d failed (live=%d source=%d preferred=%d applied=%d)\n", step, live, source, preferred, applied); exit(1); } }
static const char *names[32];
static int count;
static uintptr_t named(const char *name) {
  for (int i = 0; i < count; i++) if (!strcmp(names[i], name)) return i + 1;
  names[count] = strdup(name);
  return ++count;
}
static int is(uintptr_t id, const char *name) { return id >= 1 && id <= (uintptr_t)count && !strcmp(names[id - 1], name); }
static int mode(uintptr_t obj) { return obj >= 601 && obj <= 603; }
// The bindings look up each class by its name, keep a global reference to it, and work in a local frame.
static int saved_live, frames, globals;
static uintptr_t find_class(uintptr_t e, const char *name) { live++; return 9000; }
static uintptr_t new_global(uintptr_t e, uintptr_t obj) { globals++; return obj; }
static void delete_global(uintptr_t e, uintptr_t obj) { globals--; }
static int32_t push(uintptr_t e, int32_t n) { frames++; saved_live = live; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { frames--; live = saved_live; return 0; }
static uintptr_t member(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { return named(name); }
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t args) {
  live++;
  if (obj == 100 && is(id, "getWindow")) return 200;
  if (obj == 200 && is(id, "getDecorView")) return 300;
  if (obj == 100 && is(id, "getWindowManager")) return 400;
  if (obj == 400 && is(id, "getDefaultDisplay")) return 500;
  if (obj == 500 && is(id, "getMode")) return 601;
  if (obj == 500 && is(id, "getSupportedModes")) return 600;
  if (obj == 200 && is(id, "getAttributes")) return 700;
  require(0);
  return 0;
}
static int32_t integer(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t args) {
  require(mode(obj));
  if (is(id, "getPhysicalWidth")) return obj == 603 ? 720 : 1080;
  if (is(id, "getPhysicalHeight")) return obj == 603 ? 1600 : 2400;
  require(is(id, "getModeId"));
  return obj - 600;
}
static float real(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t args) {
  require(mode(obj) && is(id, "getRefreshRate"));
  return obj == 601 ? 60.0f : obj == 602 ? 120.0f : 144.0f;
}
static void call(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (obj == 300 && is(id, "requestUnbufferedDispatch")) { source = (int)args[0]; asked++; return; }
  require(obj == 200 && is(id, "setAttributes") && args[0] == 700);
  applied++;
}
static int32_t length(uintptr_t e, uintptr_t array) { require(array == 600); return 3; }
static uintptr_t element(uintptr_t e, uintptr_t array, int32_t i) { require(array == 600 && i >= 0 && i < 3); live++; return 601 + i; }
static void set_field(uintptr_t e, uintptr_t obj, uintptr_t id, int32_t value) { require(obj == 700 && is(id, "preferredDisplayModeId")); preferred = value; }
static uintptr_t exception(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t obj) { live--; }
static uintptr_t table[240], env, activity[8];
static void *window(const char *path) {
  void *lib = dlopen(path, RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); exit(2); }
  table[6] = (uintptr_t)find_class;
  table[19] = (uintptr_t)push;
  table[20] = (uintptr_t)pop;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[15] = (uintptr_t)exception;
  table[17] = (uintptr_t)exception;
  table[23] = (uintptr_t)release;
  table[33] = (uintptr_t)member;
  table[36] = (uintptr_t)object;
  table[51] = (uintptr_t)integer;
  table[57] = (uintptr_t)real;
  table[63] = (uintptr_t)call;
  table[94] = (uintptr_t)member;
  table[109] = (uintptr_t)set_field;
  table[171] = (uintptr_t)length;
  table[173] = (uintptr_t)element;
  env = (uintptr_t)table;
  activity[2] = (uintptr_t)&env;
  activity[3] = 100;
  return lib;
}
`

// unbatched_touch asks the decor view of the window for each place of a
// pointer at once, on Android 11 and later, and for collected places again
// when it is off. On Android 10 it calls nothing.
func TestAndroidUnbatchedTouch(t *testing.T) {
	src := androidSource(t) + `
pub function context_size(): i32 { return Context.c_size() as i32; }
pub function wire(ctx: usize, activity: usize, sdk: i32): void {
  unsafe {
    Context.c_store_activity(ctx, activity);
    Context.c_store_sdk(ctx, sdk);
  }
}
pub function at_once(ctx: usize, on: i32): void { touch_at_once(ctx, on != 0); }
`
	const check = windowJNI + `
int main(int argc, char **argv) {
  void *lib = window(argv[1]);
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*wire)(uintptr_t, uintptr_t, int) = dlsym(lib, "wire");
  void (*at_once)(uintptr_t, int) = dlsym(lib, "at_once");
  if (!context_size || !wire || !at_once) return 3;
  uintptr_t ctx = (uintptr_t)calloc(1, context_size());
  wire(ctx, (uintptr_t)activity, 29);
  at_once(ctx, 1);
  require(asked == 0 && live == 0);
  wire(ctx, (uintptr_t)activity, 30);
  at_once(ctx, 1);
  // InputDevice.SOURCE_CLASS_POINTER is 2.
  require(asked == 1 && source == 2 && live == 0);
  at_once(ctx, 0);
  require(asked == 2 && source == 0 && live == 0 && frames == 0 && globals == 0);
  return 0;
}
`
	androidJNIMockCheck(t, src, "context_size,wire,at_once", check)
}

// prefer_fast_display gives the window the mode of the display with the
// most refreshes in a second among the modes of the size in use.
func TestAndroidFastDisplay(t *testing.T) {
	src := androidSource(t) + `
pub function fast(activity: usize): void { fast_display(activity); }
`
	const check = windowJNI + `
int main(int argc, char **argv) {
  void *lib = window(argv[1]);
  void (*fast)(uintptr_t) = dlsym(lib, "fast");
  if (!fast) return 3;
  fast((uintptr_t)activity);
  // The mode 2 has the size of the mode in use and 120 refreshes. The mode
  // 3 has more refreshes, at another size.
  require(preferred == 2 && applied == 1 && live == 0 && frames == 0 && globals == 0);
  return 0;
}
`
	androidJNIMockCheck(t, src, "fast", check)
}
