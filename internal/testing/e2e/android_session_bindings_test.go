package e2e

import "testing"

// std/android/session calls Java only through its typed bindings. The
// mock checks each lookup against its signature in android.jar: a wrong
// signature finds no member, and the next JNI call then ends the process
// on a device. It also checks that each call ends its local frame and
// deletes each global reference that it does not give to the caller.
func TestAndroidSession(t *testing.T) {
	const src = `import "std/android";
import "std/android/draw";
import "std/android/session";
import "std/jni";
pub function play(ctx: usize): i32 {
  let Some(s) = session.new(ctx, "dev.fern.tune.Buttons", "tune") else { return 0; };
  let art = draw.Image { width: 2, height: 1, pixels: [1, 2, 3, 4, 5, 6, 7, 8] };
  session.set_track(ctx, s, "Title", "Artist", "Album", 1500i64, Some(art));
  let rewind = session.TransportAction { id: "rewind", label: "Back 15 seconds", icon: 17301542 };
  session.set_transport_actions(ctx, s, 6, 1500i64, 1.0f32, 1823i64, [rewind]);
  session.set_state(ctx, s, false, 20i64);
  let token = session.token(ctx, s);
  if (token == 0 as usize) { return 2; }
  jni.delete_global_ref(android.env(ctx), token);
  session.close(ctx, s);
  return 1;
}
`
	const check = `#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define STATE "Landroid/media/session/PlaybackState$Builder;"
#define TRACK "Landroid/media/MediaMetadata$Builder;"
enum { C_CONTEXT=1, C_LOADER, C_CLASS, C_LOOPER, C_HANDLER, C_SESSION, C_CALLBACK, C_STATE, C_TRACK, C_BITMAP,
  C_CONFIG, C_METADATA, C_PLAYBACK, C_THROWABLE, CLASSES };
static const char *classes[] = {"", "android/content/Context", "java/lang/ClassLoader", "java/lang/Class",
  "android/os/Looper", "android/os/Handler", "android/media/session/MediaSession",
  "android/media/session/MediaSession$Callback", "android/media/session/PlaybackState$Builder",
  "android/media/MediaMetadata$Builder", "android/graphics/Bitmap", "android/graphics/Bitmap$Config",
  "android/media/MediaMetadata", "android/media/session/PlaybackState"};
enum { GET_LOADER=1, LOAD_CLASS, NEW_INSTANCE, MAIN_LOOPER, NEW_HANDLER, NEW_SESSION, SET_CALLBACK, SET_ACTIVE,
  SET_METADATA, SET_PLAYBACK, GET_TOKEN, RELEASE, NEW_STATE, SET_ACTIONS, SET_STATE, ADD_ACTION, BUILD_STATE,
  NEW_TRACK, PUT_STRING, PUT_LONG, PUT_BITMAP, BUILD_TRACK, CREATE_BITMAP, ARGB, TO_STRING, MEMBERS };
static const struct { int class; const char *name, *sig; } members[] = { {0, "", ""},
  {C_CONTEXT, "getClassLoader", "()Ljava/lang/ClassLoader;"},
  {C_LOADER, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;"},
  {C_CLASS, "newInstance", "()Ljava/lang/Object;"},
  {C_LOOPER, "getMainLooper", "()Landroid/os/Looper;"},
  {C_HANDLER, "<init>", "(Landroid/os/Looper;)V"},
  {C_SESSION, "<init>", "(Landroid/content/Context;Ljava/lang/String;)V"},
  {C_SESSION, "setCallback", "(Landroid/media/session/MediaSession$Callback;Landroid/os/Handler;)V"},
  {C_SESSION, "setActive", "(Z)V"},
  {C_SESSION, "setMetadata", "(Landroid/media/MediaMetadata;)V"},
  {C_SESSION, "setPlaybackState", "(Landroid/media/session/PlaybackState;)V"},
  {C_SESSION, "getSessionToken", "()Landroid/media/session/MediaSession$Token;"},
  {C_SESSION, "release", "()V"},
  {C_STATE, "<init>", "()V"},
  {C_STATE, "setActions", "(J)" STATE},
  {C_STATE, "setState", "(IJF)" STATE},
  {C_STATE, "addCustomAction", "(Ljava/lang/String;Ljava/lang/String;I)" STATE},
  {C_STATE, "build", "()Landroid/media/session/PlaybackState;"},
  {C_TRACK, "<init>", "()V"},
  {C_TRACK, "putString", "(Ljava/lang/String;Ljava/lang/String;)" TRACK},
  {C_TRACK, "putLong", "(Ljava/lang/String;J)" TRACK},
  {C_TRACK, "putBitmap", "(Ljava/lang/String;Landroid/graphics/Bitmap;)" TRACK},
  {C_TRACK, "build", "()Landroid/media/MediaMetadata;"},
  {C_BITMAP, "createBitmap", "([IIILandroid/graphics/Bitmap$Config;)Landroid/graphics/Bitmap;"},
  {C_CONFIG, "ARGB_8888", "Landroid/graphics/Bitmap$Config;"},
  {C_THROWABLE, "toString", "()Ljava/lang/String;"} };
enum { ACTIVITY=100, LOADER, LOADED, CALLBACK, LOOPER, HANDLER, SESSION, STATE_BUILDER, TRACK_BUILDER, STATE_BUILT,
  TRACK_BUILT, TOKEN, PIXELS, CONFIG, BITMAP, EXCEPTION };
static char texts[16][80];
static int text_count, frames, globals, pending, refuse;
static int callbacks, actives, releases, custom, states, tracks, strings, longs, bitmaps;
static uint64_t last_actions;
static int last_state;

static uintptr_t find(uintptr_t e, const char *name) {
  for (int i=1; i<C_THROWABLE; i++) if (!strcmp(name, classes[i])) return i;
  fprintf(stderr, "no such class: %s\n", name); abort();
}
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { assert(obj == EXCEPTION); return C_THROWABLE; }
static uintptr_t member(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  for (int i=1; i<MEMBERS; i++) if (members[i].class == (int)cls && !strcmp(name, members[i].name)) {
    if (strcmp(sig, members[i].sig)) { fprintf(stderr, "%s has the signature %s, not %s\n", name, members[i].sig, sig); abort(); }
    return i;
  }
  fprintf(stderr, "class %s has no member %s\n", classes[cls], name); abort();
}
static const char *text(uintptr_t ref) { assert(ref >= 1000 && ref < 1000 + (uintptr_t)text_count); return texts[ref - 1000]; }
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) {
  assert(n >= 0 && n < 80 && text_count < 16);
  for (int i=0; i<n; i++) texts[text_count][i]=(char)s[i];
  texts[text_count][n]=0;
  return 1000 + text_count++;
}
static uintptr_t construct(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  assert(frames == 1);
  switch (id) {
  case NEW_SESSION: assert(cls == C_SESSION && args[0] == ACTIVITY && !strcmp(text(args[1]), "tune")); return SESSION;
  case NEW_HANDLER: assert(cls == C_HANDLER && args[0] == LOOPER); return HANDLER;
  case NEW_STATE: assert(cls == C_STATE); return STATE_BUILDER;
  case NEW_TRACK: assert(cls == C_TRACK); return TRACK_BUILDER;
  default: abort();
  }
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  switch (id) {
  case GET_LOADER: assert(obj == ACTIVITY); return LOADER;
  case LOAD_CLASS: assert(obj == LOADER && !strcmp(text(args[0]), "dev.fern.tune.Buttons")); return LOADED;
  case NEW_INSTANCE: assert(obj == LOADED); return CALLBACK;
  case SET_ACTIONS: assert(obj == STATE_BUILDER); last_actions=args[0]; return STATE_BUILDER;
  case SET_STATE: assert(obj == STATE_BUILDER); last_state=(int)args[0]; states++; return STATE_BUILDER;
  case ADD_ACTION:
    assert(obj == STATE_BUILDER && !strcmp(text(args[0]), "rewind") && !strcmp(text(args[1]), "Back 15 seconds") && (int)args[2] == 17301542);
    custom++; return STATE_BUILDER;
  case BUILD_STATE: assert(obj == STATE_BUILDER); return STATE_BUILT;
  case PUT_STRING: {
    static const char *want[][2] = {{"android.media.metadata.TITLE", "Title"}, {"android.media.metadata.ARTIST", "Artist"}, {"android.media.metadata.ALBUM", "Album"}};
    assert(obj == TRACK_BUILDER && strings < 3 && !strcmp(text(args[0]), want[strings][0]) && !strcmp(text(args[1]), want[strings][1]));
    strings++; return TRACK_BUILDER;
  }
  case PUT_LONG: assert(obj == TRACK_BUILDER && !strcmp(text(args[0]), "android.media.metadata.DURATION") && args[1] == 1500); longs++; return TRACK_BUILDER;
  case PUT_BITMAP: assert(obj == TRACK_BUILDER && !strcmp(text(args[0]), "android.media.metadata.ALBUM_ART") && args[1] == BITMAP); bitmaps++; return TRACK_BUILDER;
  case BUILD_TRACK: assert(obj == TRACK_BUILDER && strings == 3 && longs == 1 && bitmaps == 1); return TRACK_BUILT;
  case GET_TOKEN: assert(obj == SESSION); return TOKEN;
  case TO_STRING: assert(obj == EXCEPTION); return 0;
  default: abort();
  }
}
static uintptr_t void_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(obj == SESSION);
  switch (id) {
  case SET_CALLBACK: assert(args[0] == CALLBACK && args[1] == HANDLER); callbacks++; pending=refuse; return 0;
  case SET_ACTIVE: assert((args[0] & 255) == 1); actives++; return 0;
  case SET_METADATA: assert(args[0] == TRACK_BUILT); tracks++; return 0;
  case SET_PLAYBACK: assert(args[0] == STATE_BUILT); return 0;
  case RELEASE: releases++; return 0;
  default: abort();
  }
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  if (id == MAIN_LOOPER) { assert(cls == C_LOOPER); return LOOPER; }
  assert(id == CREATE_BITMAP && cls == C_BITMAP && args[0] == PIXELS && (int)args[1] == 2 && (int)args[2] == 1 && args[3] == CONFIG);
  return BITMAP;
}
static uintptr_t static_field(uintptr_t e, uintptr_t cls, uintptr_t id) { assert(cls == C_CONFIG && id == ARGB); return CONFIG; }
static uintptr_t int_array(uintptr_t e, int n) { assert(n == 2); return PIXELS; }
static void set_ints(uintptr_t e, uintptr_t array, int start, int n, const int32_t *values) {
  assert(array == PIXELS && start == 0 && n == 2 && (uint32_t)values[0] == 0x04010203u && (uint32_t)values[1] == 0x08050607u);
}
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
  int (*play)(uintptr_t)=dlsym(lib, "play");
  assert(play);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)exception, [17]=(uintptr_t)clear,
    [19]=(uintptr_t)push, [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release,
    [23]=(uintptr_t)local, [30]=(uintptr_t)construct, [31]=(uintptr_t)get_class, [33]=(uintptr_t)member,
    [36]=(uintptr_t)object, [63]=(uintptr_t)void_call, [113]=(uintptr_t)member, [116]=(uintptr_t)static_object,
    [144]=(uintptr_t)member, [145]=(uintptr_t)static_field, [163]=(uintptr_t)string, [179]=(uintptr_t)int_array,
    [211]=(uintptr_t)set_ints};
  uintptr_t activity[10] = {[3]=ACTIVITY}, env=(uintptr_t)table, ctx[40] = {[2]=(uintptr_t)activity, [4]=(uintptr_t)&env};
  // Android refuses the callback: no session, and the new session is released.
  refuse=1;
  assert(!play((uintptr_t)ctx) && callbacks == 1 && actives == 0 && releases == 1); clean();
  refuse=0;
  assert(play((uintptr_t)ctx) == 1); clean();
  assert(callbacks == 2 && actives == 1 && tracks == 1 && custom == 1 && states == 2 && releases == 2);
  // set_state gives the paused state with the default controls.
  assert(last_state == 2 && last_actions == 1847);
  return 0;
}
`
	androidJNIMockCheck(t, src, "play", check)
}
