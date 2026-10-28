# The cost of each build profile

Status: [reference] measurements for [SEC-048](https://github.com/JakeChampion/lang/issues/13550).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
Lane: `scripts/profile-costs`, compared against `.github/profile-costs-baseline.txt` by the perf workflow.

## What the lane measures

The script builds the empty program and eight bench programs under the four profiles of `PROFILES.md`. It builds them for both native targets from one build of the self-host compiler.
Per row it reports the emitted instruction count, as indented lines of assembly, and the size of the linked image.
On an x86-64 host with valgrind it also reports the retired instructions under callgrind, which is where a check on a hot path shows.
Every number is deterministic for a commit, so the comparator's default tolerance applies.
The arm64 rows are cross-compiled on the x86-64 runner and carry no dynamic count.

`safe` is the defaults, `hardened` is `-harden -pie`, `diagnostic` is `-sanitize -backtrace -g`, and `minimal` is `-size`.
The empty program is the runtime's own cost: its startup, its fault handler, and under `hardened` the canary read, the shadow stack request and the relocation loop.

## Measurements

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, valgrind 3.22.0, measured on 2026-10-27.
The percentage is the change against `safe`.

### Emitted instructions, x86-64-linux

| Program | safe | hardened | diagnostic | minimal |
|---|---|---|---|---|
| `startup` | 229 | 301 (+31.4%) | 437 (+90.8%) | 169 (-26.2%) |
| `int_loop` | 250 | 322 (+28.8%) | 462 (+84.8%) | 190 (-24.0%) |
| `call_overhead` | 305 | 377 (+23.6%) | 514 (+68.5%) | 245 (-19.7%) |
| `closure_call` | 972 | 1052 (+8.2%) | 1145 (+17.8%) | 874 (-10.1%) |
| `array_append` | 1138 | 1215 (+6.8%) | 1198 (+5.3%) | 1049 (-7.8%) |
| `map_string` | 6737 | 7018 (+4.2%) | 7362 (+9.3%) | 6203 (-7.9%) |
| `enum_match` | 1098 | 1179 (+7.4%) | 1242 (+13.1%) | 950 (-13.5%) |
| `http_hello` | 17491 | 17723 (+1.3%) | 16481 (-5.8%) | 14318 (-18.1%) |
| `deflate_decode` | 13324 | 13638 (+2.4%) | 12959 (-2.7%) | 11698 (-12.2%) |

### Image bytes, x86-64-linux

| Program | safe | hardened | diagnostic | minimal |
|---|---|---|---|---|
| `startup` | 4096 | 4128 (+0.8%) | 5824 (+42.2%) | 4096 (+0.0%) |
| `int_loop` | 4096 | 4128 (+0.8%) | 6200 (+51.4%) | 4096 (+0.0%) |
| `call_overhead` | 4096 | 4128 (+0.8%) | 6456 (+57.6%) | 4096 (+0.0%) |
| `closure_call` | 8224 | 8272 (+0.6%) | 12960 (+57.6%) | 4128 (-49.8%) |
| `array_append` | 8224 | 8256 (+0.4%) | 12336 (+50.0%) | 8224 (+0.0%) |
| `map_string` | 28144 | 33952 (+20.6%) | 77680 (+176.0%) | 28144 (+0.0%) |
| `enum_match` | 8240 | 8304 (+0.8%) | 12424 (+50.8%) | 8240 (+0.0%) |
| `http_hello` | 72720 | 79936 (+9.9%) | 127304 (+75.1%) | 60432 (-16.9%) |
| `deflate_decode` | 53376 | 58912 (+10.4%) | 114976 (+115.4%) | 49280 (-7.7%) |

### Emitted instructions, arm64-linux

| Program | safe | hardened | diagnostic | minimal |
|---|---|---|---|---|
| `startup` | 316 | 376 (+19.0%) | 472 (+49.4%) | 253 (-19.9%) |
| `int_loop` | 330 | 390 (+18.2%) | 490 (+48.5%) | 267 (-19.1%) |
| `call_overhead` | 388 | 484 (+24.7%) | 551 (+42.0%) | 325 (-16.2%) |
| `closure_call` | 1195 | 1310 (+9.6%) | 1378 (+15.3%) | 1091 (-8.7%) |
| `array_append` | 1353 | 1442 (+6.6%) | 1446 (+6.9%) | 1259 (-6.9%) |
| `map_string` | 7174 | 8394 (+17.0%) | 7495 (+4.5%) | 6641 (-7.4%) |
| `enum_match` | 1315 | 1433 (+9.0%) | 1474 (+12.1%) | 1162 (-11.6%) |
| `http_hello` | 18617 | 20282 (+8.9%) | 17737 (-4.7%) | 14954 (-19.7%) |
| `deflate_decode` | 14219 | 15518 (+9.1%) | 13915 (-2.1%) | 12443 (-12.5%) |

### Image bytes, arm64-linux

| Program | safe | hardened | diagnostic | minimal |
|---|---|---|---|---|
| `startup` | 65792 | 65792 (+0.0%) | 67360 (+2.4%) | 65792 (+0.0%) |
| `int_loop` | 65792 | 65792 (+0.0%) | 67728 (+2.9%) | 65792 (+0.0%) |
| `call_overhead` | 65792 | 65792 (+0.0%) | 68024 (+3.4%) | 65792 (+0.0%) |
| `closure_call` | 66304 | 66304 (+0.0%) | 72000 (+8.6%) | 66304 (+0.0%) |
| `array_append` | 66304 | 66304 (+0.0%) | 71392 (+7.7%) | 66304 (+0.0%) |
| `map_string` | 70688 | 70688 (+0.0%) | 121472 (+71.8%) | 70688 (+0.0%) |
| `enum_match` | 66304 | 66304 (+0.0%) | 71480 (+7.8%) | 66304 (+0.0%) |
| `http_hello` | 140472 | 140472 (+0.0%) | 201128 (+43.2%) | 74936 (-46.7%) |
| `deflate_decode` | 71184 | 71184 (+0.0%) | 133768 (+87.9%) | 71184 (+0.0%) |

### Retired instructions under callgrind, x86-64-linux

| Program | safe | hardened | diagnostic | minimal |
|---|---|---|---|---|
| `startup` | 60 | 90 (+50.00%) | 192 (+220.00%) | 60 (+0.00%) |
| `int_loop` | 12000077 | 12000107 (+0.00%) | 12000209 (+0.00%) | 12000077 (+0.00%) |
| `call_overhead` | 8250814 | 8250844 (+0.00%) | 8551046 (+3.64%) | 8250814 (+0.00%) |
| `closure_call` | 63030162 | 87030200 (+38.08%) | 78168323 (+24.02%) | 63018162 (-0.02%) |
| `array_append` | 13371022 | 13371057 (+0.00%) | 44622663 (+233.73%) | 13370822 (-0.00%) |
| `map_string` | 58391336 | 61305081 (+4.99%) | 63794906 (+9.25%) | 61168968 (+4.76%) |
| `enum_match` | 48825092 | 48825151 (+0.00%) | 54900221 (+12.44%) | 57825092 (+18.43%) |
| `http_hello` | 23728450 | 23811453 (+0.35%) | 35720341 (+50.54%) | 25120450 (+5.87%) |
| `deflate_decode` | 34703269 | 34763444 (+0.17%) | 46682910 (+34.52%) | 43446703 (+25.19%) |

## What the numbers say

- The hardened runtime costs 72 instructions of text on x86-64 and 60 on arm64, and 30 retired instructions at startup on x86-64. The startup request for the shadow stack is most of that.
- A framed function pays a canary store and a canary check. Small programs grow by a fifth to a third in text, and large ones by a few percent.
- An indirect call pays the landing-pad compare. `closure_call`, which does nothing else, retires 38% more instructions under `hardened`. The other programs retire less than 5% more.
- A `-pie` image on x86-64 is 32 bytes larger than the fixed-address image, and larger programs gain their relocation table.
- `diagnostic` is the sanitizer: an allocation-heavy program retires up to three times the instructions, and the debug information doubles some images.
- `minimal` trades speed for size. `enum_match` and `deflate_decode` retire a fifth more instructions.

## Platform support

| Profile | x86-64-linux | arm64-linux | arm64-darwin | arm64-android | wasm32-wasi | freestanding |
|---|---|---|---|---|---|---|
| `safe` | yes | yes | yes | yes | yes | check-only, exit 2 |
| `hardened` | yes | yes | refused, exit 2 | refused, exit 2 | refused, exit 2 | refused, exit 2 |
| `diagnostic` | yes | yes | yes | yes | yes, with the leak census only | check-only, exit 2 |
| `minimal` | yes | yes | yes | yes | yes | check-only, exit 2 |

`TestSelfHostProfiles` pins the refusal of `hardened` on wasm32-wasi. The refusal message names the two targets that accept it.

## Limits

- The lane does not measure wall time, which varies on a shared runner. The retired instruction count is the dynamic proxy.
- The arm64 dynamic cost is not measured, because the runner is x86-64 and QEMU counts are not comparable.
- The hardened x86-64 rows ran on a host without the user shadow stack, so its enabled cost is not in them.
