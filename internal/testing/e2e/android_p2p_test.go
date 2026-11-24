package e2e

import "testing"

// std/android/p2p calls Java only through its typed bindings. Each start
// that fails deletes the global references that it made, and stop deletes
// the rest.
func TestAndroidWifiDirect(t *testing.T) {
	const src = `import "std/android/p2p";
pub function direct(ctx: usize): i32 {
  let Some(p) = p2p.start(ctx, "dev.fern.walkie.Direct") else { return 0; };
  if (!p2p.discover(ctx, p) || !p2p.connect(ctx, p, "02:00:00:00:00:01")) { return 2; }
  p2p.stop(ctx, p);
  return 1;
}`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum { ACTIVITY=100, SERVICE, LOOPER, CHANNEL, LOADER, CLASS, RECEIVER, FILTER, CONFIG, EXCEPTION,
  S_SERVICE, S_CLASS, S_ACTION, S_ADDRESS, FILTER_CLASS=500, CONFIG_CLASS };
static const char *methods[] = {"", "getClassLoader", "getMainLooper", "getSystemService", "registerReceiver",
  "unregisterReceiver", "getAction", "getIntExtra", "getParcelableExtra", "<init>", "addAction",
  "cancelConnect", "connect", "discoverPeers", "initialize", "removeGroup", "stopPeerDiscovery",
  "getDeviceList", "getHostAddress", "toArray", "newInstance", "loadClass", "toString", 0};
enum { GET_LOADER=1, MAIN_LOOPER, SYSTEM_SERVICE, REGISTER, UNREGISTER, GET_ACTION, INT_EXTRA, PARCELABLE_EXTRA,
  INIT, ADD_ACTION, CANCEL, CONNECT, DISCOVER, INITIALIZE, REMOVE_GROUP, STOP_DISCOVERY, DEVICE_LIST,
  HOST_ADDRESS, TO_ARRAY, NEW_INSTANCE, LOAD_CLASS, TO_STRING };
static const char *fields[] = {"", "deviceAddress", "deviceName", "status", "groupFormed", "groupOwnerAddress", "isGroupOwner", 0};
enum { DEVICE_ADDRESS=1 };
static int frames, globals, pending, refuse, no_service;
static int registrations, actions, discovers, connects, addresses, cancels, removes, stops, unregisters;
static uintptr_t find(uintptr_t e, const char *name) {
  if (!strcmp(name, "android/content/IntentFilter")) return FILTER_CLASS;
  if (!strcmp(name, "android/net/wifi/p2p/WifiP2pConfig")) return CONFIG_CLASS;
  return 1;
}
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { assert(obj == EXCEPTION); return 1; }
static uintptr_t lookup(const char **names, const char *name) {
  for (int i=1; names[i]; i++) if (!strcmp(name, names[i])) return i;
  abort();
}
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { return lookup(methods, name); }
static uintptr_t field(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { return lookup(fields, name); }
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  switch (id) {
  case SYSTEM_SERVICE: assert(frames == 1 && obj == ACTIVITY && args[0] == S_SERVICE); return no_service ? 0 : SERVICE;
  case MAIN_LOOPER: assert(obj == ACTIVITY); return LOOPER;
  case INITIALIZE: assert(obj == SERVICE && args[0] == ACTIVITY && args[1] == LOOPER && args[2] == 0); return CHANNEL;
  case GET_LOADER: assert(obj == ACTIVITY); return LOADER;
  case LOAD_CLASS: assert(obj == LOADER && args[0] == S_CLASS); return CLASS;
  case NEW_INSTANCE: assert(obj == CLASS); return RECEIVER;
  case REGISTER:
    assert(obj == ACTIVITY && args[0] == RECEIVER && args[1] == FILTER && actions == 3);
    registrations++; pending=refuse; return 0;
  case TO_STRING: assert(obj == EXCEPTION); return 0;
  default: abort();
  }
}
static uintptr_t construct(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  assert(id == INIT);
  if (cls == FILTER_CLASS) return FILTER;
  assert(cls == CONFIG_CLASS && frames == 1); return CONFIG;
}
static uintptr_t void_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  if (id == ADD_ACTION) { assert(obj == FILTER && args[0] == S_ACTION); actions++; return 0; }
  if (id == UNREGISTER) { assert(obj == ACTIVITY && args[0] == RECEIVER); unregisters++; return 0; }
  assert(obj == SERVICE && args[0] == CHANNEL);
  switch (id) {
  case DISCOVER: assert(args[1] == 0); discovers++; return 0;
  case CONNECT: assert(args[1] == CONFIG && args[2] == 0 && addresses == 1); connects++; return 0;
  case CANCEL: assert(args[1] == 0); cancels++; return 0;
  case REMOVE_GROUP: assert(args[1] == 0); removes++; return 0;
  case STOP_DISCOVERY: assert(args[1] == 0); stops++; return 0;
  default: abort();
  }
}
static uintptr_t set_object(uintptr_t e, uintptr_t obj, uintptr_t id, uintptr_t value) {
  assert(obj == CONFIG && id == DEVICE_ADDRESS && value == S_ADDRESS); addresses++; return 0;
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) {
  char buf[80];
  assert(n >= 0 && n < sizeof(buf));
  for (int i=0; i<n; i++) buf[i]=(char)s[i];
  buf[n]=0;
  if (!strcmp(buf, "wifip2p")) return S_SERVICE;
  if (!strcmp(buf, "dev.fern.walkie.Direct")) return S_CLASS;
  if (!strcmp(buf, "02:00:00:00:00:01")) return S_ADDRESS;
  assert(!strncmp(buf, "android.net.wifi.p2p.", 21)); return S_ACTION;
}
static int push(uintptr_t e, int n) { assert(!frames); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames == 1 && keep == 0); frames--; return 0; }
static uintptr_t global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void release(uintptr_t e, uintptr_t ref) { assert(globals > 0); globals--; }
static void local(uintptr_t e, uintptr_t ref) {}
static uintptr_t exception(uintptr_t e) { return pending ? EXCEPTION : 0; }
static void clear(uintptr_t e) { pending=0; }
static void clean(void) { assert(!frames && !globals && !pending); }

int main(int argc, char **argv) {
  void *lib=dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*direct)(uintptr_t)=dlsym(lib, "direct");
  assert(direct);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)exception, [17]=(uintptr_t)clear,
    [19]=(uintptr_t)push, [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release,
    [23]=(uintptr_t)local, [30]=(uintptr_t)construct, [31]=(uintptr_t)get_class, [33]=(uintptr_t)method,
    [36]=(uintptr_t)object, [63]=(uintptr_t)void_call, [94]=(uintptr_t)field, [104]=(uintptr_t)set_object,
    [163]=(uintptr_t)string};
  uintptr_t activity[10] = {[3]=ACTIVITY}, env=(uintptr_t)table, ctx[40] = {[2]=(uintptr_t)activity, [4]=(uintptr_t)&env};
  no_service=1;
  assert(!direct((uintptr_t)ctx) && registrations == 0); clean();
  no_service=0; refuse=1;
  assert(!direct((uintptr_t)ctx) && registrations == 1 && unregisters == 0); clean();
  refuse=0; actions=0;
  assert(direct((uintptr_t)ctx) == 1 && registrations == 2);
  assert(discovers == 1 && connects == 1 && cancels == 1 && removes == 1 && stops == 1 && unregisters == 1);
  clean();
  return 0;
}
`
	androidJNIMockCheck(t, src, "direct", check)
}
