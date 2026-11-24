package e2e

import "testing"

// std/android/service calls Java only through its typed bindings. The mock
// checks the arguments of each call, that each call ends its local frame,
// and that each global reference is deleted. It also checks the two
// members that an older device does not have: the startForeground of API
// level 29 and the setAuthenticationRequired of API level 31.
func TestAndroidService(t *testing.T) {
	const src = `import "std/android/service";

const CLASS: string = "dev.fern.app.Keep";

function notice(): service.Notice {
  return service.Notice { channel: "keep", channel_name: "Keep", title: "T", text: "X", action: "Talk", stop: "Stop", unlock: true };
}

pub function begin(ctx: usize): i32 {
  if (!service.start_media(ctx, CLASS, 77 as usize)) {
    return 1;
  }
  service.publish_media(ctx, CLASS, notice(), 77 as usize, [service.MediaButton { label: "Next", action: "dev.fern.app.NEXT", icon: 5 }]);
  let awake = service.stay_awake(ctx, "keep");
  if (awake.wake == 0 as usize || awake.wifi == 0 as usize) {
    return 2;
  }
  service.release_awake(ctx, awake);
  service.stop(ctx, CLASS);
  return 0;
}

pub function halt(ctx: usize): void {
  service.stop(ctx, CLASS);
}

pub function on_command(e: usize, svc: usize, intent: usize): i32 {
  return service.command_media(e, svc, intent, notice(), []);
}
`
	const check = `#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *classes[] = {"", "android/content/Context", "java/lang/ClassLoader", "java/lang/Class", "android/app/Service",
  "android/content/pm/ServiceInfo", "android/content/Intent", "android/content/pm/PackageManager", "android/app/PendingIntent",
  "android/app/NotificationManager", "android/app/NotificationChannel", "android/app/Notification",
  "android/app/Notification$Builder", "android/app/Notification$Action$Builder", "android/app/Notification$MediaStyle",
  "android/os/PowerManager", "android/os/PowerManager$WakeLock", "android/net/wifi/WifiManager",
  "android/net/wifi/WifiManager$WifiLock", "android/R$drawable", 0};
enum { C_INTENT=6, C_CHANNEL=10, C_BUILDER=12, C_ACTION_BUILDER=13, C_STYLE=14, C_OF_SERVICE=900, C_OF_OTHER };
enum { TOKEN=77, ACTIVITY=100, LOADER, LOADED, INTENT, COMPONENT, MANAGER, CHANNEL, BUILDER, PENDING, PENDING_OPEN,
  ACTION_BUILDER, ACTION, STYLE, NOTIFICATION, POWER, WAKE, APP, WIFI, WIFI_LOCK, PACKAGES, LAUNCH, POSITIONS,
  SERVICE=200, START_INTENT, FINISH_INTENT, EXCEPTION, PACKAGE_NAME=1900, FINISH_TEXT };
static struct { char name[48], sig[160]; } members[128];
static char texts[64][64], last_action[64];
static int member_count, text_count, frames, globals, pending, old, refuse_send;
static int foregrounds, extras, sends, stop_services, channels, notifies, unlocked, plain, acquires, lock_releases, stops, fg2, fg3, styles;

static uintptr_t find(uintptr_t e, const char *name) {
  assert(frames == 1 || !strncmp(name, "android/os/PowerManager$", 24) || !strncmp(name, "android/net/wifi/WifiManager$", 29));
  for (int i=1; classes[i]; i++) if (!strcmp(name, classes[i])) return i;
  fprintf(stderr, "no such class: %s\n", name); abort();
}
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { return obj == SERVICE ? C_OF_SERVICE : C_OF_OTHER; }
static uintptr_t member(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  assert(cls);
  if (old && (!strcmp(name, "setAuthenticationRequired") || !strcmp(sig, "(ILandroid/app/Notification;I)V"))) { pending=1; return 0; }
  for (int i=0; i<member_count; i++) if (!strcmp(members[i].name, name) && !strcmp(members[i].sig, sig)) return i + 1;
  assert(member_count < 128 && strlen(name) < 48 && strlen(sig) < 160);
  strcpy(members[member_count].name, name); strcpy(members[member_count].sig, sig);
  return ++member_count;
}
static int is(uintptr_t id, const char *name) { assert(id >= 1 && id <= (uintptr_t)member_count); return !strcmp(members[id - 1].name, name); }
static const char *text(uintptr_t ref) {
  if (ref == PACKAGE_NAME) return "dev.fern.app";
  if (ref == FINISH_TEXT) return "dev.fern.android.FINISH";
  assert(ref >= 1000 && ref < 1000 + (uintptr_t)text_count); return texts[ref - 1000];
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) {
  assert(n >= 0 && n < 64 && text_count < 64);
  for (int i=0; i<n; i++) texts[text_count][i]=(char)s[i];
  texts[text_count][n]=0;
  return 1000 + text_count++;
}
static int utf_length(uintptr_t e, uintptr_t ref) { return (int)strlen(text(ref)); }
static const char *utf_chars(uintptr_t e, uintptr_t ref, void *copy) { return text(ref); }
static void utf_release(uintptr_t e, uintptr_t ref, const char *chars) {}
static int context(uintptr_t obj) { return obj == ACTIVITY || obj == SERVICE; }
static uintptr_t construct(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  assert(frames == 1 && is(id, "<init>"));
  switch (cls) {
  case C_INTENT: assert(context(args[0]) && (args[1] == LOADED || args[1] == C_OF_SERVICE)); return INTENT;
  case C_CHANNEL: assert(!strcmp(text(args[0]), "keep") && !strcmp(text(args[1]), "Keep") && (int)args[2] == 2); return CHANNEL;
  case C_BUILDER: assert(context(args[0]) && !strcmp(text(args[1]), "keep")); return BUILDER;
  case C_ACTION_BUILDER: assert((int)args[0] == 17301668 && !strcmp(text(args[1]), "Talk") && args[2] == PENDING); return ACTION_BUILDER;
  case C_STYLE: return STYLE;
  default: abort();
  }
}
static uintptr_t services(uintptr_t obj, const char *name) {
  if (!strcmp(name, "notification")) { assert(context(obj)); return MANAGER; }
  if (!strcmp(name, "power")) { assert(obj == ACTIVITY); return POWER; }
  assert(!strcmp(name, "wifi") && obj == APP); return WIFI;
}
static uintptr_t add_action(uintptr_t id, const uintptr_t *args) {
  if (!strcmp(members[id - 1].sig, "(Landroid/app/Notification$Action;)Landroid/app/Notification$Builder;")) { assert(args[0] == ACTION); unlocked++; return BUILDER; }
  const char *label = text(args[1]);
  assert(args[2] == PENDING && (!strcmp(label, "Talk") || !strcmp(label, "Stop") || !strcmp(label, "Next")));
  plain++; return BUILDER;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(obj);
  if (is(id, "getClassLoader")) { assert(obj == ACTIVITY); return LOADER; }
  if (is(id, "loadClass")) { assert(obj == LOADER && !strcmp(text(args[0]), "dev.fern.app.Keep")); return LOADED; }
  if (is(id, "putExtra")) { assert(obj == INTENT && !strcmp(text(args[0]), "dev.fern.android.SESSION") && args[1] == TOKEN); extras++; return INTENT; }
  if (is(id, "getParcelableExtra")) { assert(obj == START_INTENT && !strcmp(text(args[0]), "dev.fern.android.SESSION")); return TOKEN; }
  if (is(id, "getAction")) return obj == FINISH_INTENT ? FINISH_TEXT : 0;
  if (is(id, "setAction")) { assert(obj == INTENT); strcpy(last_action, text(args[0])); return INTENT; }
  if (is(id, "startForegroundService")) { assert(obj == ACTIVITY && args[0] == INTENT); foregrounds++; return COMPONENT; }
  if (is(id, "startService")) { assert(obj == ACTIVITY && args[0] == INTENT && !strcmp(last_action, "dev.fern.android.FINISH")); sends++; return refuse_send ? 0 : COMPONENT; }
  if (is(id, "getSystemService")) return services(obj, text(args[0]));
  if (is(id, "getApplicationContext")) { assert(obj == ACTIVITY); return APP; }
  if (is(id, "getPackageManager")) { assert(context(obj)); return PACKAGES; }
  if (is(id, "getPackageName")) { assert(context(obj)); return PACKAGE_NAME; }
  if (is(id, "getLaunchIntentForPackage")) { assert(obj == PACKAGES && !strcmp(text(args[0]), "dev.fern.app")); return LAUNCH; }
  if (is(id, "setContentTitle")) { assert(obj == BUILDER && !strcmp(text(args[0]), "T")); return BUILDER; }
  if (is(id, "setContentText")) { assert(obj == BUILDER && !strcmp(text(args[0]), "X")); return BUILDER; }
  if (is(id, "setSmallIcon")) { assert(obj == BUILDER && ((int)args[0] == 17301668 || (int)args[0] == 17301540)); return BUILDER; }
  if (is(id, "setOngoing")) { assert(obj == BUILDER && (args[0] & 255) == 1); return BUILDER; }
  if (is(id, "addAction")) { assert(obj == BUILDER); return add_action(id, args); }
  if (is(id, "setStyle")) { assert(obj == BUILDER && args[0] == STYLE); styles++; return BUILDER; }
  if (is(id, "setContentIntent")) { assert(obj == BUILDER && args[0] == PENDING_OPEN); return BUILDER; }
  if (is(id, "setAuthenticationRequired")) { assert(obj == ACTION_BUILDER && (args[0] & 255) == 1); return ACTION_BUILDER; }
  if (is(id, "build")) { assert(obj == BUILDER || obj == ACTION_BUILDER); return obj == BUILDER ? NOTIFICATION : ACTION; }
  if (is(id, "setMediaSession")) { assert(obj == STYLE && args[0] == TOKEN); return STYLE; }
  if (is(id, "setShowActionsInCompactView")) { assert(obj == STYLE && args[0] == POSITIONS); return STYLE; }
  if (is(id, "newWakeLock")) { assert(obj == POWER && (int)args[0] == 1 && !strcmp(text(args[1]), "keep")); return WAKE; }
  if (is(id, "createWifiLock")) { assert(obj == WIFI && (int)args[0] == 3 && !strcmp(text(args[1]), "keep")); return WIFI_LOCK; }
  if (is(id, "toString")) { assert(obj == EXCEPTION); return 0; }
  fprintf(stderr, "no object call: %s\n", members[id - 1].name); abort();
}
static uintptr_t void_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  if (is(id, "createNotificationChannel")) { assert(obj == MANAGER && args[0] == CHANNEL); channels++; return 0; }
  if (is(id, "notify")) { assert(obj == MANAGER && (int)args[0] == 1 && args[1] == NOTIFICATION); notifies++; return 0; }
  if (is(id, "acquire")) { assert(obj == WAKE || obj == WIFI_LOCK); acquires++; return 0; }
  if (is(id, "release")) { assert(obj == WAKE || obj == WIFI_LOCK); lock_releases++; return 0; }
  if (is(id, "stopSelf")) { assert(obj == SERVICE); stops++; return 0; }
  assert(is(id, "startForeground") && obj == SERVICE && (int)args[0] == 1 && args[1] == NOTIFICATION);
  if (!strcmp(members[id - 1].sig, "(ILandroid/app/Notification;I)V")) { assert((int)args[2] == -1 && !old); fg3++; } else { assert(old); fg2++; }
  return 0;
}
static uint8_t boolean_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(is(id, "stopService") && obj == ACTIVITY && args[0] == INTENT); stop_services++; return 1;
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  assert(context(args[0]) && args[1] == 0 && (int)args[3] == 201326592);
  if (is(id, "getService")) { assert(args[2] == INTENT); return PENDING; }
  assert(is(id, "getActivity") && args[2] == LAUNCH); return PENDING_OPEN;
}
static uintptr_t int_array(uintptr_t e, int n) { return POSITIONS; }
static void set_ints(uintptr_t e, uintptr_t array, int start, int n, const int32_t *values) { assert(array == POSITIONS); }
static int push(uintptr_t e, int n) { assert(!frames && n >= 16); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames == 1 && keep == 0); frames--; text_count=0; return 0; }
static uintptr_t global(uintptr_t e, uintptr_t ref) { assert(ref); globals++; return ref; }
static void release(uintptr_t e, uintptr_t ref) { assert(ref && globals > 0); globals--; }
static void local(uintptr_t e, uintptr_t ref) {}
static uintptr_t exception(uintptr_t e) { return pending ? EXCEPTION : 0; }
static void clear(uintptr_t e) { pending=0; }
static void clean(void) { assert(!frames && !globals && !pending); }

int main(int argc, char **argv) {
  void *lib=dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*begin)(uintptr_t)=dlsym(lib, "begin");
  void (*halt)(uintptr_t)=dlsym(lib, "halt");
  int (*on_command)(uintptr_t, uintptr_t, uintptr_t)=dlsym(lib, "on_command");
  assert(begin && halt && on_command);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)exception, [17]=(uintptr_t)clear,
    [19]=(uintptr_t)push, [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release,
    [23]=(uintptr_t)local, [30]=(uintptr_t)construct, [31]=(uintptr_t)get_class, [33]=(uintptr_t)member,
    [36]=(uintptr_t)object, [39]=(uintptr_t)boolean_call, [63]=(uintptr_t)void_call, [113]=(uintptr_t)member,
    [116]=(uintptr_t)static_object, [144]=(uintptr_t)member, [163]=(uintptr_t)string, [168]=(uintptr_t)utf_length,
    [169]=(uintptr_t)utf_chars, [170]=(uintptr_t)utf_release, [179]=(uintptr_t)int_array, [211]=(uintptr_t)set_ints};
  uintptr_t activity[10] = {[3]=ACTIVITY}, env=(uintptr_t)table, e=(uintptr_t)&env, ctx[40] = {[2]=(uintptr_t)activity, [4]=e};
  // The activity starts the service, shows its notification, holds the locks and stops the service.
  assert(begin((uintptr_t)ctx) == 0); clean();
  assert(foregrounds == 1 && extras == 1 && channels == 1 && notifies == 1 && unlocked == 1 && plain == 2 && styles == 1);
  assert(acquires == 2 && lock_releases == 2 && sends == 1 && stop_services == 0);
  // Android refuses the finish command, so stop calls stopService.
  refuse_send=1;
  halt((uintptr_t)ctx); clean();
  assert(sends == 2 && stop_services == 1);
  // The service stops for no intent and for the finish command.
  assert(on_command(e, SERVICE, 0) == 2 && stops == 1); clean();
  assert(on_command(e, SERVICE, FINISH_INTENT) == 2 && stops == 2); clean();
  // A start command makes the service a foreground service with the types of the manifest.
  assert(on_command(e, SERVICE, START_INTENT) == 2 && fg3 == 1 && fg2 == 0 && channels == 2 && unlocked == 2 && plain == 3 && styles == 2); clean();
  // Before API level 29 and 31: the short startForeground, and an action with no unlock rule.
  old=1;
  assert(on_command(e, SERVICE, START_INTENT) == 2 && fg3 == 1 && fg2 == 1 && unlocked == 2 && plain == 5); clean();
  return 0;
}
`
	androidJNIMockCheck(t, src, "begin,halt,on_command", check)
}
