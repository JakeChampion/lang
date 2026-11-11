// AAudio.h defines clockid_t and two int64_t output pointers for this API.
#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <time.h>

static int mode;
static int calls;
static int64_t custom_position, custom_time;

typedef struct AAudioStream AAudioStream;
typedef int32_t aaudio_result_t;

void mock_timestamp_mode(int value) { mode = value; calls = 0; }
int mock_timestamp_calls(void) { return calls; }
void mock_timestamp_value(int64_t position, int64_t time_ns) {
    mode = 10; calls = 0; custom_position = position; custom_time = time_ns;
}

aaudio_result_t AAudioStream_getTimestamp(AAudioStream *stream, clockid_t clock,
                                        int64_t *position, int64_t *time_ns) {
    _Static_assert(sizeof(clockid_t) == sizeof(int32_t), "clockid_t ABI");
    assert(stream == (AAudioStream *)123 && clock == CLOCK_MONOTONIC);
    assert(position != NULL && time_ns != NULL && position != time_ns);
    assert(time_ns == position + 1);
    assert(*position == -1 && *time_ns == -1);
    calls++;
    switch (mode) {
    case 0: *position = INT64_C(1099511627776); *time_ns = INT64_C(1125899906842624); return 0;
    case 1: *position = 0; *time_ns = 0; return 0;
    case 2: return -895; // AAUDIO_ERROR_INVALID_STATE, including asynchronous startup.
    case 3: *position = 48000; *time_ns = 1000000000; return -899;
    case 4: *position = -1; *time_ns = 1000000000; return 0;
    case 5: *position = 48000; *time_ns = -1; return 0;
    case 6: *position = 48000; return 0;
    case 7: *time_ns = 1000000000; return 0;
    case 8: *position = INT64_MAX; *time_ns = INT64_MAX; return 0;
    case 9: *position = 48000; *time_ns = 1000000000; return 5;
    case 10: *position = custom_position; *time_ns = custom_time; return 0;
    default: abort();
    }
}

// Every other operation aborts, so the timestamp query must remain read-only.
void AAudio_createStreamBuilder(void) { abort(); }
void AAudioStreamBuilder_setDirection(void) { abort(); }
void AAudioStreamBuilder_setSampleRate(void) { abort(); }
void AAudioStreamBuilder_setChannelCount(void) { abort(); }
void AAudioStreamBuilder_setFormat(void) { abort(); }
void AAudioStreamBuilder_setPerformanceMode(void) { abort(); }
void AAudioStreamBuilder_setUsage(void) { abort(); }
void AAudioStreamBuilder_setContentType(void) { abort(); }
void AAudioStreamBuilder_setInputPreset(void) { abort(); }
void AAudioStreamBuilder_openStream(void) { abort(); }
void AAudioStreamBuilder_delete(void) { abort(); }
void AAudioStream_requestStart(void) { abort(); }
void AAudioStream_requestStop(void) { abort(); }
void AAudioStream_close(void) { abort(); }
void AAudioStream_read(void) { abort(); }
void AAudioStream_write(void) { abort(); }
void AAudioStream_getSampleRate(void) { abort(); }
void AAudioStream_getBufferCapacityInFrames(void) { abort(); }
void AAudioStream_setBufferSizeInFrames(void) { abort(); }
