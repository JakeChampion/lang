package e2e

import "testing"

func TestAndroidLocationListener(t *testing.T) {
	const src = `import "std/android/location";
pub function listen(ctx: usize): i32 {
  let Some(l) = location.start(ctx, "dev.fern.Where", true, 5000000000i64) else { return 0; };
  location.stop(ctx, l);
  return 1;
}
pub function last(ctx: usize): i32 {
  let Some(f) = location.last(ctx, true) else { return 0; };
  return if (f.latitude == 1.25 && f.longitude == -2.5 && f.altitude == 3.75 && f.accuracy == 4.5f32 && f.speed == 5.5f32 && f.bearing == 6.5f32 && f.ms == 5000000000i64) { 1 } else { -1 };
}`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum { ACTIVITY=100, MANAGER, LOADER, CLASS, LISTENER, LOOPER, FIX, EXCEPTION };
enum { PERMISSION=1, SERVICE, GET_LOADER, LOAD, NEW_INSTANCE, MAIN_LOOPER, REQUEST, REMOVE, LAST,
  LAT, LON, ALT, ACC, SPEED, BEARING, MS, TO_STRING };
static int frames, globals, registrations, removals, pending, refuse, no_ref, missing;
// android.has_permission looks up the Context class with no frame of its own, and deletes the one local reference.
static uintptr_t find(uintptr_t e, const char *name) { assert(frames == 1 || !strcmp(name, "android/content/Context")); return 1; }
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { assert(obj==ACTIVITY || obj==EXCEPTION); return 1; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  const char *names[] = {"", "checkSelfPermission", "getSystemService", "getClassLoader", "loadClass",
    "newInstance", "getMainLooper", "requestLocationUpdates", "removeUpdates", "getLastKnownLocation",
    "getLatitude", "getLongitude", "getAltitude", "getAccuracy", "getSpeed", "getBearing", "getTime", "toString"};
  for (int i=1; i<=TO_STRING; i++) if (!strcmp(name,names[i])) return i;
  // The bindings look up each member of a class. The test calls only the members above.
  return TO_STRING + 1;
}
enum { FINE=200, LOCATION, CLASS_NAME, GPS };
static int permission(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(obj==ACTIVITY && id==PERMISSION && args[0]==FINE); return 0;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  switch (id) {
  case SERVICE: assert(obj==ACTIVITY && args[0]==LOCATION); return MANAGER;
  case GET_LOADER: assert(obj==ACTIVITY); return LOADER;
  case LOAD: assert(obj==LOADER && args[0]==CLASS_NAME); return CLASS;
  case NEW_INSTANCE: assert(obj==CLASS); return LISTENER;
  case MAIN_LOOPER: assert(obj==ACTIVITY); return LOOPER;
  case LAST: assert(obj==MANAGER && args[0]==GPS); return missing ? 0 : FIX;
  case TO_STRING: assert(obj==EXCEPTION); return 0;
  default: abort();
  }
}
static uintptr_t void_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(frames==1 && obj==MANAGER);
  if (id==REQUEST) {
    assert(args[0]==GPS && args[1]==5000000000ULL && args[2]==0 && args[3]==LISTENER && args[4]==LOOPER);
    registrations++; pending=refuse;
  } else { assert(id==REMOVE && args[0]==LISTENER); removals++; }
  return 0;
}
static double coord(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t args) {
  assert(obj==FIX && (id==LAT || id==LON || id==ALT)); return id==LAT ? 1.25 : id==LON ? -2.5 : 3.75;
}
static float measure(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t args) {
  assert(obj==FIX && (id==ACC || id==SPEED || id==BEARING)); return id==ACC ? 4.5f : id==SPEED ? 5.5f : 6.5f;
}
static int64_t timestamp(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t args) {
  assert(obj==FIX && id==MS); return 5000000000LL;
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) {
  char buf[80]; assert(n>=0 && n<sizeof(buf));
  for (int i=0; i<n; i++) buf[i]=(char)s[i];
  buf[n]=0;
  if (!strcmp(buf,"android.permission.ACCESS_FINE_LOCATION")) return FINE;
  if (!strcmp(buf,"location")) return LOCATION;
  if (!strcmp(buf,"dev.fern.Where")) return CLASS_NAME;
  assert(!strcmp(buf,"gps")); return GPS;
}
static int push(uintptr_t e, int n) { assert(!frames); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames == 1); frames--; return 0; }
static uintptr_t global(uintptr_t e, uintptr_t ref) {
  if (no_ref && ref == LISTENER) { pending=1; return 0; }
  globals++; return ref;
}
static void release(uintptr_t e, uintptr_t ref) { assert(globals>0); globals--; }
static void local(uintptr_t e, uintptr_t ref) {}
static uintptr_t exception(uintptr_t e) { return pending ? EXCEPTION : 0; }
static void clear(uintptr_t e) { pending=0; }
static void clean(void) { assert(!frames && !globals && !pending); }

int main(int argc, char **argv) {
  void *lib=dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*listen_)(uintptr_t)=dlsym(lib,"listen");
  int (*last_)(uintptr_t)=dlsym(lib,"last");
  assert(listen_ && last_);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)exception, [17]=(uintptr_t)clear,
    [19]=(uintptr_t)push, [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release,
    [23]=(uintptr_t)local, [31]=(uintptr_t)get_class, [33]=(uintptr_t)method,
    [36]=(uintptr_t)object, [51]=(uintptr_t)permission, [54]=(uintptr_t)timestamp, [57]=(uintptr_t)measure, [60]=(uintptr_t)coord, [63]=(uintptr_t)void_call, [163]=(uintptr_t)string};
  uintptr_t activity[10] = {[3]=ACTIVITY}, env=(uintptr_t)table, ctx[40] = {[2]=(uintptr_t)activity, [4]=(uintptr_t)&env};
  assert(last_((uintptr_t)ctx)==1); clean();
  missing=1; assert(!last_((uintptr_t)ctx)); clean();
  refuse=1;
  assert(!listen_((uintptr_t)ctx) && registrations==1 && removals==0); clean();
  refuse=0; no_ref=1;
  assert(!listen_((uintptr_t)ctx) && registrations==1 && removals==0); clean();
  no_ref=0;
  assert(listen_((uintptr_t)ctx) && registrations==2 && removals==1); clean();
  return 0;
}
`
	androidJNIMockCheck(t, src, "listen,last", check)
}
