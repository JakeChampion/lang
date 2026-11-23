package e2e

import "testing"

// std/android/speech against a JNI mock with two JNIEnv values: one of the
// loop thread and one of the main thread. The loop thread makes the listener
// and the Handler, and sends each command as a Message. Only the main thread
// calls the SpeechRecognizer, in the native handleMessage of the listener.
// The listener sends its events to the live loop, and event decodes them.
func TestAndroidSpeech(t *testing.T) {
	const src = `import "std/android";
import "std/android/handler";
import "std/android/speech";
function of(l: usize, h: usize): speech.Recognizer {
  return speech.Recognizer { listener: l, handler: handler.Handler { ref: h } };
}
function flag(on: boolean): i32 {
  if (on) {
    return 1;
  }
  return 0;
}
pub function make(ctx: usize): i32 {
  let Some(r) = speech.new(ctx) else { return 0; };
  return 1;
}
pub function act(ctx: usize, l: usize, h: usize, op: i32): i32 {
  let r = of(l, h);
  if (op == 0) { return flag(speech.start(ctx, r, "en-GB")); }
  if (op == 1) { return flag(speech.start(ctx, r, "")); }
  if (op == 2) { return flag(speech.stop(ctx, r)); }
  if (op == 3) { return flag(speech.cancel(ctx, r)); }
  if (op == 4) { return flag(speech.available(ctx)); }
  if (op == 5) { return flag(speech.allowed(ctx)); }
  speech.close(ctx, r);
  return 1;
}
pub function decode(kind: i32, arg: usize): i32 {
  match (speech.event(android.Posted(kind, arg))) {
    Some(speech.Ready) => { return 1; },
    Some(speech.Partial(t)) => { return if (t == "play") { 2 } else { -2 }; },
    Some(speech.Words(t)) => { return if (t == "play some jazz") { 3 } else { -3 }; },
    Some(speech.Failed(code)) => { return 100 + code; },
    None => { return 0; }
  }
}`
	const check = `
#include <dlfcn.h>
#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

enum { ACTIVITY = 100, LOADER, LISTENER_CLASS, LISTENER, LOOPER, HANDLER, RECOGNIZER, INTENT, BUNDLE, LIST, EXCEPTION, MSG = 1000, CLASS = 2000, STRING = 3000 };
static uintptr_t loop_env, main_env;
static int frames, globals, pending, nnames, nclasses, nstrings, queued, head;
static int available = 1, granted = 1, no_recognizer, nlist = 2, live;
static const char *throw_on = "";
static uintptr_t held;
static char log_[8192], names[128][64], classes[32][64], strings[256][96], lock_[64];
static struct { int what, arg1; uintptr_t obj; } messages[64];
static uintptr_t ctx[40];

static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "speech: %s (frames=%d globals=%d)\nlog: %s\n", what, frames, globals, log_); exit(1); } }
static char thread_of(uintptr_t e) {
  require(e == (uintptr_t)&loop_env || e == (uintptr_t)&main_env, "a JNIEnv of one of the two threads");
  return e == (uintptr_t)&loop_env ? 'L' : 'M';
}
static void note(uintptr_t e, const char *what) {
  char line[256];
  snprintf(line, sizeof line, "%c:%s;", thread_of(e), what);
  require(strlen(log_) + strlen(line) < sizeof log_, "the log is full");
  strcat(log_, line);
  if (!strcmp(what, throw_on)) pending = 1;
}
static int has(const char *part) { return strstr(log_, part) != 0; }
static int count(const char *part) {
  int n = 0;
  for (const char *p = strstr(log_, part); p; p = strstr(p + 1, part)) n++;
  return n;
}
static uintptr_t text(const char *s) {
  require(nstrings < 256 && strlen(s) < 96, "a short string");
  strcpy(strings[nstrings], s);
  return STRING + nstrings++;
}
static const char *str(uintptr_t id) { return id >= STRING && id < STRING + (uintptr_t)nstrings ? strings[id - STRING] : 0; }
static int is(uintptr_t id, const char *name) { return !strcmp(names[id - 1], name); }

static uintptr_t find_class(uintptr_t e, const char *name) {
  for (int k = 0; k < nclasses; k++) if (!strcmp(classes[k], name)) return CLASS + k;
  require(nclasses < 32, "too many classes");
  strcpy(classes[nclasses], name);
  return CLASS + nclasses++;
}
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { return CLASS - 1; }
static uintptr_t member(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  for (int k = 0; k < nnames; k++) if (!strcmp(names[k], name)) return k + 1;
  require(nnames < 128, "too many members");
  strcpy(names[nnames], name);
  return ++nnames;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (is(id, "getClassLoader")) { require(obj == ACTIVITY, "the loader of the activity"); return LOADER; }
  if (is(id, "loadClass")) { require(obj == LOADER && !strcmp(str(args[0]), "dev.fern.android.Speech"), "the class of the listener"); return LISTENER_CLASS; }
  if (is(id, "newInstance")) { require(obj == LISTENER_CLASS, "an object of the listener class"); return LISTENER; }
  if (is(id, "obtainMessage")) {
    require(thread_of(e) == 'L' && obj == HANDLER && queued < 64, "a message of the Handler, on the loop thread");
    messages[queued].what = (int32_t)args[0];
    messages[queued].arg1 = (int32_t)args[1];
    messages[queued].obj = args[3];
    return MSG + queued++;
  }
  if (is(id, "get") && obj == LISTENER) { require(thread_of(e) == 'M', "the main thread reads the recognizer"); return held; }
  if (is(id, "get")) { require(obj == LIST && args[0] == 0, "the first of the results"); return text(nlist == 1 ? "play" : "play some jazz"); }
  if (is(id, "getStringArrayList")) { require(obj == BUNDLE && !strcmp(str(args[0]), "results_recognition"), "the results of the Bundle"); return nlist < 0 ? 0 : LIST; }
  if (is(id, "putExtra")) {
    char line[200];
    require(obj == INTENT, "an extra of the intent");
    if (str(args[1])) snprintf(line, sizeof line, "putExtra(%s,%s)", str(args[0]), str(args[1]));
    else snprintf(line, sizeof line, "putExtra(%s,%d)", str(args[0]), (int)(args[1] & 255));
    note(e, line);
    return INTENT;
  }
  if (is(id, "toString")) { require(obj == EXCEPTION, "the text of the exception"); return 0; }
  require(0, names[id - 1]);
  return 0;
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  if (is(id, "getMainLooper")) { require(thread_of(e) == 'L', "the loop thread makes the Handler"); return LOOPER; }
  if (is(id, "createSpeechRecognizer")) {
    require(thread_of(e) == 'M' && args[0] == ACTIVITY, "the main thread makes the recognizer with the activity");
    note(e, "create");
    return no_recognizer ? 0 : RECOGNIZER;
  }
  require(is(id, "getProperty"), names[id - 1]);
  if (!live) return 0;
  char number[32];
  snprintf(number, sizeof number, "%lu", !strcmp(str(args[0]), "dev.fern.android.lock") ? (unsigned long)lock_ : (unsigned long)ctx);
  return text(number);
}
static uint8_t static_bool(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  require(is(id, "isRecognitionAvailable") && args[0] == ACTIVITY, "isRecognitionAvailable of the activity");
  return available;
}
static uintptr_t new_object(uintptr_t e, uintptr_t cls, uintptr_t ctor, const uint64_t *args) {
  if (!strcmp(classes[cls - CLASS], "android/os/Handler")) {
    require(thread_of(e) == 'L' && args[0] == LOOPER && args[1] == LISTENER, "a Handler of the main Looper for the listener");
    return HANDLER;
  }
  require(!strcmp(classes[cls - CLASS], "android/content/Intent") && thread_of(e) == 'M', "an Intent on the main thread");
  require(!strcmp(str(args[0]), "android.speech.action.RECOGNIZE_SPEECH"), "the action of the intent");
  return INTENT;
}
static void vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (is(id, "sendToTarget")) {
    char line[32];
    require(thread_of(e) == 'L' && obj == MSG + (uintptr_t)queued - 1, "the loop thread sends the message");
    snprintf(line, sizeof line, "send %d", messages[queued - 1].what);
    note(e, line);
    return;
  }
  require(thread_of(e) == 'M', "only the main thread calls the recognizer and its holder");
  if (is(id, "set")) { require(obj == LISTENER, "the listener holds the recognizer"); held = args[0]; note(e, "set"); return; }
  require(obj == RECOGNIZER, names[id - 1]);
  if (is(id, "setRecognitionListener")) require(args[0] == LISTENER, "the listener of the recognizer");
  if (is(id, "startListening")) require(args[0] == INTENT, "the intent of startListening");
  note(e, names[id - 1]);
}
static int32_t icall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (is(id, "size")) { require(obj == LIST, "the size of the results"); return nlist; }
  require(is(id, "checkSelfPermission") && obj == ACTIVITY && !strcmp(str(args[0]), "android.permission.RECORD_AUDIO"), names[id - 1]);
  return granted ? 0 : -1;
}
static int32_t int_field(uintptr_t e, uintptr_t obj, uintptr_t id) {
  require(obj >= MSG && obj < MSG + (uintptr_t)queued, "a field of a message");
  return is(id, "what") ? messages[obj - MSG].what : messages[obj - MSG].arg1;
}
static uintptr_t object_field(uintptr_t e, uintptr_t obj, uintptr_t id) {
  require(obj >= MSG && obj < MSG + (uintptr_t)queued && is(id, "obj"), "the object of a message");
  return messages[obj - MSG].obj;
}
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  char s[96];
  require(n < 96, "a short string");
  for (uintptr_t i = 0; i < n; i++) s[i] = (char)units[i];
  s[n] = 0;
  return text(s);
}
static int32_t utf_length(uintptr_t e, uintptr_t s) { return (int32_t)strlen(str(s)); }
static const char *utf_chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return str(s); }
static void utf_release(uintptr_t e, uintptr_t s, const char *chars) {}
static int32_t push(uintptr_t e, int32_t n) { require(frames == 0 && n > 0, "one local frame at a time"); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { require(frames == 1 && !keep, "the end of the local frame"); frames--; return 0; }
static uintptr_t new_global(uintptr_t e, uintptr_t ref) { require(ref != 0, "a global reference to an object"); globals++; return ref; }
static void delete_global(uintptr_t e, uintptr_t ref) { require(globals > 0, "a global reference deleted twice"); globals--; }
static void delete_local(uintptr_t e, uintptr_t ref) {}
static uintptr_t exception(uintptr_t e) { return pending ? EXCEPTION : 0; }
static void clear(uintptr_t e) { pending = 0; }

static uint8_t (*handle)(uintptr_t, uintptr_t, uintptr_t);
static int32_t (*decode)(int32_t, uintptr_t);
static int message_fd, reply_fd;
// The main thread: each message that waits, in its order.
static void drain(void) {
  while (head < queued) {
    require(handle((uintptr_t)&main_env, LISTENER, MSG + head) == 1 && frames == 0 && !pending, "handleMessage takes the message");
    head++;
  }
}
// A message with no Handler, for a command that the loop thread cannot send.
static void direct(int what, uintptr_t obj) {
  messages[queued].what = what;
  messages[queued].arg1 = 0;
  messages[queued++].obj = obj;
  drain();
}
// The loop thread: the event of the message that waits, as decode reads it,
// or -1000 with no message.
static int32_t taken(void) {
  struct pollfd p = { message_fd, POLLIN, 0 };
  struct { int32_t kind; uintptr_t arg; } m;
  if (poll(&p, 1, 0) != 1) return -1000;
  require(read(message_fd, &m, sizeof m) == sizeof m, "a whole message");
  return decode(m.kind, m.arg);
}
static void reply(void) { require(write(reply_fd, "", 1) == 1, "the reply of the loop"); }

int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int32_t (*make)(uintptr_t) = dlsym(lib, "make");
  int32_t (*act)(uintptr_t, uintptr_t, uintptr_t, int32_t) = dlsym(lib, "act");
  decode = dlsym(lib, "decode");
  handle = dlsym(lib, "Java_dev_fern_android_Speech_handleMessage");
  void (*on_ready)(uintptr_t, uintptr_t, uintptr_t) = dlsym(lib, "Java_dev_fern_android_Speech_onReadyForSpeech");
  void (*on_error)(uintptr_t, uintptr_t, int32_t) = dlsym(lib, "Java_dev_fern_android_Speech_onError");
  void (*on_results)(uintptr_t, uintptr_t, uintptr_t) = dlsym(lib, "Java_dev_fern_android_Speech_onResults");
  void (*on_partial)(uintptr_t, uintptr_t, uintptr_t) = dlsym(lib, "Java_dev_fern_android_Speech_onPartialResults");
  require(make && act && decode && handle && on_ready && on_error && on_results && on_partial, "the entries");
  static uintptr_t table[240];
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)exception;
  table[17] = (uintptr_t)clear;
  table[19] = (uintptr_t)push;
  table[20] = (uintptr_t)pop;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[23] = (uintptr_t)delete_local;
  table[30] = (uintptr_t)new_object;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)member;
  table[36] = (uintptr_t)object;
  table[51] = (uintptr_t)icall;
  table[63] = (uintptr_t)vcall;
  table[94] = (uintptr_t)member;
  table[95] = (uintptr_t)object_field;
  table[100] = (uintptr_t)int_field;
  table[113] = (uintptr_t)member;
  table[116] = (uintptr_t)static_object;
  table[119] = (uintptr_t)static_bool;
  table[163] = (uintptr_t)new_string;
  table[168] = (uintptr_t)utf_length;
  table[169] = (uintptr_t)utf_chars;
  table[170] = (uintptr_t)utf_release;
  loop_env = main_env = (uintptr_t)table;
  uintptr_t activity[10] = {0};
  int to_loop[2], from_loop[2];
  require(pipe(to_loop) == 0 && pipe(from_loop) == 0, "the pipes of the loop");
  message_fd = to_loop[0];
  reply_fd = from_loop[1];
  ((int32_t *)ctx)[1] = to_loop[1];
  ((int32_t *)ctx)[2] = from_loop[0];
  activity[3] = ACTIVITY;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&loop_env;
  uintptr_t c = (uintptr_t)ctx, m = (uintptr_t)&main_env;

  require(act(c, 0, 0, 5) == 1, "allowed with the permission");
  granted = 0;
  require(act(c, 0, 0, 5) == 0 && frames == 0, "not allowed with no permission");

  available = 0;
  require(act(c, 0, 0, 4) == 0 && make(c) == 0 && queued == 0 && globals == 0 && frames == 0, "no recognizer on a device with no recognition service");
  available = 1;
  require(act(c, 0, 0, 4) == 1 && make(c) == 1 && frames == 0 && globals == 2, "new keeps the listener and the Handler");
  require(queued == 1 && messages[0].what == 1 && messages[0].obj == ACTIVITY && has("L:send 1;") && !has("M:"), "CREATE goes to the main thread with the activity");

  // START runs before the CREATE that waits.
  head = 1;
  direct(2, text("en-GB"));
  require(!has("M:"), "a command before CREATE does nothing");
  head = 0;
  queued = 1;
  drain();
  require(has("M:create;M:setRecognitionListener;M:set;") && held == RECOGNIZER, "CREATE makes the recognizer, gives it the listener, and the listener holds it");

  log_[0] = 0;
  require(act(c, LISTENER, HANDLER, 0) == 1 && frames == 0 && !has("M:") && !strcmp(str(messages[queued - 1].obj), "en-GB"), "start sends START with the language tag, and calls no recognizer");
  drain();
  require(has("M:putExtra(android.speech.extra.LANGUAGE_MODEL,free_form);M:putExtra(android.speech.extra.PARTIAL_RESULTS,1);M:putExtra(android.speech.extra.LANGUAGE,en-GB);M:startListening;"), "START listens for free speech in the language, with partial results");

  log_[0] = 0;
  require(act(c, LISTENER, HANDLER, 1) == 1, "start with the language of the device");
  drain();
  require(has("M:startListening;") && !has("extra.LANGUAGE,"), "an empty tag gives no language extra");

  log_[0] = 0;
  require(act(c, LISTENER, HANDLER, 2) == 1 && act(c, LISTENER, HANDLER, 3) == 1 && has("L:send 3;L:send 4;") && !has("M:"), "stop and cancel send their commands");
  drain();
  require(has("M:stopListening;M:cancel;"), "the main thread stops and cancels in the order of the commands");

  throw_on = "startListening";
  require(act(c, LISTENER, HANDLER, 0) == 1, "a start that fails on the main thread still goes");
  drain();
  throw_on = "";
  require(held == RECOGNIZER && globals == 2, "a command that fails keeps the recognizer");

  // The listener, with no live loop: no event goes, and nothing waits.
  on_results(m, LISTENER, BUNDLE);
  require(taken() == -1000 && frames == 0, "no event with no live loop");

  live = 1;
  reply(); on_ready(m, LISTENER, 0);
  require(taken() == 1, "onReadyForSpeech gives Ready");
  reply(); on_error(m, LISTENER, 9);
  require(taken() == 109, "onError gives Failed with the code");
  nlist = 1;
  reply(); on_partial(m, LISTENER, BUNDLE);
  require(taken() == 2 && frames == 0, "onPartialResults gives Partial with the first words");
  nlist = 2;
  reply(); on_results(m, LISTENER, BUNDLE);
  require(taken() == 3 && frames == 0, "onResults gives Words with the first words");
  nlist = 0;
  on_partial(m, LISTENER, BUNDLE);
  require(taken() == -1000, "partial results with no words give no event");
  reply(); on_results(m, LISTENER, BUNDLE);
  require(taken() == 107, "results with no words give Failed(ERROR_NO_MATCH)");
  nlist = -1;
  reply(); on_results(m, LISTENER, BUNDLE);
  require(taken() == 107 && frames == 0, "results with no list give Failed(ERROR_NO_MATCH)");
  reply(); on_results(m, LISTENER, 0);
  require(taken() == 107 && frames == 0, "no Bundle gives Failed(ERROR_NO_MATCH)");
  live = 0;

  log_[0] = 0;
  require(act(c, LISTENER, HANDLER, 6) == 1 && globals == 0 && frames == 0 && has("L:send 5;") && !has("M:"), "close sends RELEASE and deletes both references");
  drain();
  require(has("M:set;M:destroy;") && held == 0, "RELEASE destroys the recognizer, and the listener lets it go");
  log_[0] = 0;
  direct(3, 0);
  require(!has("M:"), "a command after RELEASE does nothing");

  no_recognizer = 1;
  direct(1, ACTIVITY);
  require(held == 0 && !has("M:set"), "CREATE with no recognizer holds nothing");
  no_recognizer = 0;
  throw_on = "setRecognitionListener";
  log_[0] = 0;
  direct(1, ACTIVITY);
  throw_on = "";
  require(held == 0 && has("M:destroy;") && !has("M:set;"), "a recognizer that refuses its listener is destroyed");
  require(globals == 0 && frames == 0 && !pending, "nothing is left");
  return 0;
}
`
	androidJNIMockCheck(t, src, "make,act,decode", check)
}
