# Wi-Fi Direct on Android

This module connects two phones with Wi-Fi Direct, for a NativeActivity app of `std/android`.

## Model

Wi-Fi Direct connects two phones with no access point: one phone becomes the group owner, and the other connects to it. `WifiP2pManager` reports the state, the peers and the group through broadcasts. The app declares a @jni class that extends `android.content.BroadcastReceiver`, with a native `onReceive` that calls `received`.

The module calls Java only through `std/android/p2p/bindings`, which `jnibind` writes from `bindings.spec`. `start` registers the receiver. `received` sends the broadcasts to the live loop of the app with `android.tell`. The loop gives them to the app as `Posted` events. `event` decodes them.

Discovery needs `NEARBY_WIFI_DEVICES` from API level 33, and `ACCESS_FINE_LOCATION` before that. The commands report no result: the broadcasts that follow give the state. `connect` makes Android ask the user of the other phone to accept the invitation.

A `Peer` has the status of `WifiP2pDevice`: 0 connected, 1 invited, 2 failed, 3 available and 4 unavailable.

## Contracts

The app gives each `Posted` event to `event`: a list of peers and a group come in memory that `event` frees. Call each function except `received` on the loop thread, and call `stop` before Destroy.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<events>>

<<Wi-Fi Direct>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module connects two phones with Wi-Fi Direct.
import "std/android";
import "std/android/p2p/bindings";
import "std/jni";
import "std/result";
```

### `events`

```fern
<<events>>=
// Wi-Fi Direct is on or off, the list of peers changed, or the group
// changed.
pub enum Event {
  Enabled(boolean),
  Peers(Peer[]),
  Group(Group),
}

const STATE: i32 = 14;

const PEERS: i32 = 15;

const GROUP: i32 = 16;

// The Wi-Fi Direct event in the event `e` of the loop, or None.
pub function event(e: android.Event): Option[Event] {
  match (e) {
    android.Posted(kind, arg) => {
      if (kind == STATE) {
        return Some(Enabled(arg != 0 as usize));
      }
      if (kind == PEERS) {
        return Some(Peers(peers_of(android.taken_text(arg))));
      }
      if (kind == GROUP) {
        return Some(Group(group_of(android.taken_text(arg))));
      }
      return None;
    },
    _ => {
      return None;
    }
  }
}
```

### `Wi-Fi Direct`

```fern
<<Wi-Fi Direct>>=
pub struct Peer { name: string, address: string, status: i32 }

// A group is formed when the two phones are connected. The owner address
// is the IP address of the group owner.
pub struct Group { formed: boolean, owner: boolean, owner_address: string }

// The global references of the manager, the channel and the receiver.
pub struct Manager { manager: usize, channel: usize, receiver: usize }

const ACTIONS: string[] = ["android.net.wifi.p2p.STATE_CHANGED", "android.net.wifi.p2p.PEERS_CHANGED", "android.net.wifi.p2p.CONNECTION_STATE_CHANGE"];

// The value of EXTRA_WIFI_STATE when Wi-Fi Direct is on.
const ENABLED: i32 = 2;

// The commands report their results through the broadcasts, not a listener.
function no_listener(): bindings.WifiP2pManager_ActionListener {
  return bindings.WifiP2pManager_ActionListener { ref: 0 as usize };
}

function manager_of(p: Manager): bindings.WifiP2pManager {
  return bindings.WifiP2pManager { ref: p.manager };
}

function channel_of(p: Manager): bindings.WifiP2pManager_Channel {
  return bindings.WifiP2pManager_Channel { ref: p.channel };
}

function filter(e: usize, c: bindings.Classes): Result[bindings.IntentFilter, jni.JavaError] {
  let filter = c.intent_filter.new(e)?;
  for action in ACTIONS {
    c.intent_filter.add_action(e, filter, action)?;
  }
  return Ok(filter);
}

// The app's receiver has a public no-argument constructor. Load it through
// the app's class loader because the loop runs on a native thread.
function new_receiver(e: usize, c: bindings.Classes, activity: bindings.Context, name: string): Result[bindings.BroadcastReceiver, jni.JavaError] {
  let loader = c.context.get_class_loader(e, activity)?;
  let class = c.class_loader.load_class(e, loader, name)?;
  let instance = c.class.new_instance(e, class)?;
  return Ok(bindings.BroadcastReceiver { ref: instance.ref });
}

// A global reference to `ref`, or None when the VM refuses it.
function global(e: usize, ref: usize): Option[usize] {
  let kept = jni.new_global_ref(e, ref);
  if (kept == 0 as usize) {
    jni.exception_clear(e);
    return None;
  }
  return Some(kept);
}

// Global references to the three objects, or None with none of them kept.
function kept(e: usize, manager: usize, channel: usize, receiver: usize): Option[Manager] {
  let Some(m) = global(e, manager) else {
    return None;
  };
  let Some(ch) = global(e, channel) else {
    jni.delete_global_ref(e, m);
    return None;
  };
  let Some(r) = global(e, receiver) else {
    jni.delete_global_ref(e, ch);
    jni.delete_global_ref(e, m);
    return None;
  };
  return Some(Manager { manager: m, channel: ch, receiver: r });
}

function release(e: usize, p: Manager): void {
  jni.delete_global_ref(e, p.receiver);
  jni.delete_global_ref(e, p.channel);
  jni.delete_global_ref(e, p.manager);
}

// The manager of the activity with its receiver registered. A successful
// registration may return a null sticky intent.
function registered(e: usize, c: bindings.Classes, activity: bindings.Context, class: string): Result[Option[Manager], jni.JavaError] {
  let service = c.context.get_system_service(e, activity, "wifip2p")?;
  if (service.is_null()) {
    return Ok(None);
  }
  let manager = bindings.WifiP2pManager { ref: service.ref };
  let looper = c.context.get_main_looper(e, activity)?;
  let channel = c.wifi_p2p_manager.initialize(e, manager, activity, looper, bindings.WifiP2pManager_ChannelListener { ref: 0 as usize })?;
  if (channel.is_null()) {
    return Ok(None);
  }
  let receiver = new_receiver(e, c, activity, class)?;
  let filter = filter(e, c)?;
  let Some(p) = kept(e, manager.ref, channel.ref, receiver.ref) else {
    return Ok(None);
  };
  let Ok(_) = c.context.register_receiver(e, activity, receiver, filter) else {
    release(e, p);
    return Ok(None);
  };
  return Ok(Some(p));
}

// Starts Wi-Fi Direct and registers the receiver `class`, the Java name of
// the app's @jni subclass of BroadcastReceiver. It is None when the device
// has no Wi-Fi Direct. Call it on the loop thread.
pub function start(ctx: usize, class: string): Option[Manager] {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 32)) {
    return None;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.classes(e) else {
    return None;
  };
  defer c.release(e);
  let Ok(p) = registered(e, c, bindings.Context { ref: android.activity_object(ctx) }, class) else {
    return None;
  };
  return p;
}

// Looks for peers. Peers events follow. It is false when Android
// refuses, for example without the permission.
pub function discover(ctx: usize, p: Manager): boolean {
  let e = android.env(ctx);
  let Ok(c) = bindings.wifi_p2p_manager_class(e) else {
    return false;
  };
  defer c.release(e);
  return c.discover_peers(e, manager_of(p), channel_of(p), no_listener()).is_ok();
}

function invite(e: usize, p: Manager, address: string): Result[void, jni.JavaError] {
  let m = bindings.wifi_p2p_manager_class(e)?;
  defer m.release(e);
  let k = bindings.wifi_p2p_config_class(e)?;
  defer k.release(e);
  let config = k.new(e)?;
  k.set_device_address(e, config, address)?;
  return m.connect(e, manager_of(p), channel_of(p), config, no_listener());
}

// Invites the peer with the MAC address `address`. A Group event
// follows when the other user accepts.
pub function connect(ctx: usize, p: Manager, address: string): boolean {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 8)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  return invite(e, p, address).is_ok();
}

// Leaves the group and stops looking for peers. Each step runs even
// when the step before it fails.
pub function leave(ctx: usize, p: Manager): void {
  let e = android.env(ctx);
  let Ok(c) = bindings.wifi_p2p_manager_class(e) else {
    return;
  };
  defer c.release(e);
  let cancelled = c.cancel_connect(e, manager_of(p), channel_of(p), no_listener());
  let removed = c.remove_group(e, manager_of(p), channel_of(p), no_listener());
  let stopped = c.stop_peer_discovery(e, manager_of(p), channel_of(p), no_listener());
}

// Leaves the group and unregisters the receiver. Call it before Destroy.
pub function stop(ctx: usize, p: Manager): void {
  leave(ctx, p);
  let e = android.env(ctx);
  defer release(e, p);
  let Ok(c) = bindings.context_class(e) else {
    return;
  };
  defer c.release(e);
  let removed = c.unregister_receiver(e, bindings.Context { ref: android.activity_object(ctx) }, bindings.BroadcastReceiver { ref: p.receiver });
}

// The onReceive of the app's receiver: `env`, `receiver` and `intent` are
// its arguments. It sends the state, the peers or the group to the loop
// thread.
pub function received(e: usize, receiver: usize, intent: usize): void {
  if (android.live_loop(e) == 0 as usize || intent == 0 as usize) {
    return;
  }
  if (!jni.push_local_frame(e, 32)) {
    return;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.classes(e) else {
    return;
  };
  defer c.release(e);
  let sent = broadcast(e, c, bindings.Intent { ref: intent });
}

function broadcast(e: usize, c: bindings.Classes, intent: bindings.Intent): Result[void, jni.JavaError] {
  let action = c.intent.get_action(e, intent)?;
  if (action == ACTIONS[0]) {
    let sent = android.tell(e, STATE, enabled(e, c, intent)?);
  } else if (action == ACTIONS[1]) {
    let list = c.intent.get_parcelable_extra(e, intent, "wifiP2pDeviceList")?;
    let text = peers_text(e, c, bindings.WifiP2pDeviceList { ref: list.ref })?;
    let sent = android.tell(e, PEERS, android.text_buffer(text));
  } else if (action == ACTIONS[2]) {
    let info = c.intent.get_parcelable_extra(e, intent, "wifiP2pInfo")?;
    let text = group_text(e, c, bindings.WifiP2pInfo { ref: info.ref })?;
    let sent = android.tell(e, GROUP, android.text_buffer(text));
  }
  return Ok(());
}

// 1 when the state broadcast `intent` tells that Wi-Fi Direct is on, else 0.
function enabled(e: usize, c: bindings.Classes, intent: bindings.Intent): Result[usize, jni.JavaError] {
  if (c.intent.get_int_extra(e, intent, "wifi_p2p_state", 0)? == ENABLED) {
    return Ok(1 as usize);
  }
  return Ok(0 as usize);
}

function flag(on: boolean): string {
  if (on) {
    return "1";
  }
  return "0";
}

// The status, the address and the name of the device `d`, separated by
// tabs.
function device_line(e: usize, c: bindings.Classes, d: bindings.WifiP2pDevice): Result[string, jni.JavaError] {
  let status = c.wifi_p2p_device.status(e, d)?;
  let address = c.wifi_p2p_device.device_address(e, d)?;
  let name = c.wifi_p2p_device.device_name(e, d)?;
  return Ok(status.to_string() + "\t" + address + "\t" + name + "\n");
}

// One line for each device of the WifiP2pDeviceList `list`.
function peers_text(e: usize, c: bindings.Classes, list: bindings.WifiP2pDeviceList): Result[string, jni.JavaError] {
  if (list.is_null()) {
    return Ok("");
  }
  let devices = c.collection.to_array(e, c.wifi_p2p_device_list.get_device_list(e, list)?)?;
  let out = "";
  for k in 0..jni.get_array_length(e, devices.ref) {
    let d = bindings.WifiP2pDevice { ref: jni.get_object_array_element(e, devices.ref, k) };
    let line = device_line(e, c, d);
    jni.delete_local_ref(e, d.ref);
    out += line?;
  }
  return Ok(out);
}

// The WifiP2pInfo `info`: formed, owner and the owner address.
function group_text(e: usize, c: bindings.Classes, info: bindings.WifiP2pInfo): Result[string, jni.JavaError] {
  if (info.is_null()) {
    return Ok("0\t0\t");
  }
  let address = c.wifi_p2p_info.group_owner_address(e, info)?;
  let host = "";
  if (!address.is_null()) {
    host = c.inet_address.get_host_address(e, address)?;
  }
  let formed = c.wifi_p2p_info.group_formed(e, info)?;
  let owner = c.wifi_p2p_info.is_group_owner(e, info)?;
  return Ok(flag(formed) + "\t" + flag(owner) + "\t" + host);
}

function digit_of(s: string): i32 {
  if (s.len() == 1 && s[0] >= b'0' && s[0] <= b'9') {
    return (s[0] - b'0') as i32;
  }
  return 4;
}

function peers_of(text: string): Peer[] {
  let out: Peer[] = [];
  for line in text.split("\n") {
    let f: string[] = line.split("\t");
    if (f.len() >= 3) {
      out = out.append(Peer { status: digit_of(f[0]), address: f[1], name: f[2] });
    }
  }
  return out;
}

function group_of(text: string): Group {
  let f: string[] = text.split("\t");
  if (f.len() < 3) {
    return Group { formed: false, owner: false, owner_address: "" };
  }
  return Group { formed: f[0] == "1", owner: f[1] == "1", owner_address: f[2] };
}
```
