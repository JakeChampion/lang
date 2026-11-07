# Wi-Fi Direct on Android

This module connects two phones with Wi-Fi Direct, for a NativeActivity app of `std/android`.

## Model

Wi-Fi Direct connects two phones with no access point: one phone becomes the group owner, and the other connects to it. `WifiP2pManager` reports the state, the peers and the group through broadcasts. The app declares a @jni class that extends `android.content.BroadcastReceiver`, with a native `onReceive` that calls `received`.

`start` registers the receiver, and stores the `ANativeActivity` in the static field `fern` of the class. Then `received` can send the broadcasts to the loop thread, and the loop gives them to the app as `Posted` events. `event` decodes them.

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
import "std/jni";
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
        return Some(Peers(peers_of(taken_text(arg))));
      }
      if (kind == GROUP) {
        return Some(Group(group_of(taken_text(arg))));
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

const CHANNEL_SIG: string = "Landroid/net/wifi/p2p/WifiP2pManager$Channel;";

const LISTENER_SIG: string = "Landroid/net/wifi/p2p/WifiP2pManager$ActionListener;";

function filter(e: usize): usize {
  let class: usize = jni.find_class(e, "android/content/IntentFilter");
  let none: usize = jni.jvalue_alloc(0);
  let filter: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  for action in ACTIONS {
    let added: boolean = jni.call_void(e, filter, "addAction", "(Ljava/lang/String;)V", [jni.new_string(e, action)]);
  }
  return filter;
}

// Starts Wi-Fi Direct and registers the receiver `class`, the Java name of
// the app's @jni subclass of BroadcastReceiver. It is None when the device
// has no Wi-Fi Direct. Call it on the loop thread.
pub function start(ctx: usize, class: string): Option[Manager] {
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let manager: usize = jni.call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "wifip2p")]);
  if (manager == 0 as usize) {
    return None;
  }
  let looper: usize = jni.call_object(e, obj, "getMainLooper", "()Landroid/os/Looper;", []);
  let channel: usize = jni.call_object(e, manager, "initialize", "(Landroid/content/Context;Landroid/os/Looper;Landroid/net/wifi/p2p/WifiP2pManager$ChannelListener;)" + CHANNEL_SIG, [obj, looper, 0 as usize]);
  if (channel == 0 as usize) {
    return None;
  }
  let receiver_class: usize = jni.load_class(e, obj, class);
  let activity: usize = android.activity(ctx);
  let stored: usize = jni.set_static_long_field(e, receiver_class, jni.get_static_field_id(e, receiver_class, "fern", "J"), activity);
  let none: usize = jni.jvalue_alloc(0);
  let receiver: usize = jni.new_object_a(e, receiver_class, jni.get_method_id(e, receiver_class, "<init>", "()V"), none);
  jni.free_buffer(none);
  let intent: usize = jni.call_object(e, obj, "registerReceiver", "(Landroid/content/BroadcastReceiver;Landroid/content/IntentFilter;)Landroid/content/Intent;", [receiver, filter(e)]);
  return Some(Manager { manager: jni.new_global_ref(e, manager), channel: jni.new_global_ref(e, channel), receiver: jni.new_global_ref(e, receiver) });
}

// Looks for peers. Peers events follow. It is false when Android
// refuses, for example without the permission.
pub function discover(ctx: usize, p: Manager): boolean {
  return jni.call_void(android.env(ctx), p.manager, "discoverPeers", "(" + CHANNEL_SIG + LISTENER_SIG + ")V", [p.channel, 0 as usize]);
}

// Invites the peer with the MAC address `address`. A Group event
// follows when the other user accepts.
pub function connect(ctx: usize, p: Manager, address: string): boolean {
  let e: usize = android.env(ctx);
  let class: usize = jni.find_class(e, "android/net/wifi/p2p/WifiP2pConfig");
  let none: usize = jni.jvalue_alloc(0);
  let config: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  let set: usize = jni.set_object_field(e, config, jni.get_field_id(e, class, "deviceAddress", "Ljava/lang/String;"), jni.new_string(e, address));
  return jni.call_void(e, p.manager, "connect", "(" + CHANNEL_SIG + "Landroid/net/wifi/p2p/WifiP2pConfig;" + LISTENER_SIG + ")V", [p.channel, config, 0 as usize]);
}

// Leaves the group and stops looking for peers.
pub function leave(ctx: usize, p: Manager): void {
  let e: usize = android.env(ctx);
  let sig: string = "(" + CHANNEL_SIG + LISTENER_SIG + ")V";
  let cancelled: boolean = jni.call_void(e, p.manager, "cancelConnect", sig, [p.channel, 0 as usize]);
  let removed: boolean = jni.call_void(e, p.manager, "removeGroup", sig, [p.channel, 0 as usize]);
  let stopped: boolean = jni.call_void(e, p.manager, "stopPeerDiscovery", sig, [p.channel, 0 as usize]);
}

// Leaves the group and unregisters the receiver. Call it before Destroy.
pub function stop(ctx: usize, p: Manager): void {
  leave(ctx, p);
  let e: usize = android.env(ctx);
  let unregistered: boolean = jni.call_void(e, android.activity_object(ctx), "unregisterReceiver", "(Landroid/content/BroadcastReceiver;)V", [p.receiver]);
  jni.delete_global_ref(e, p.receiver);
  jni.delete_global_ref(e, p.channel);
  jni.delete_global_ref(e, p.manager);
}

// The onReceive of the app's receiver: `env`, `receiver` and `intent` are
// its arguments. It sends the state, the peers or the group to the loop
// thread.
pub function received(e: usize, receiver: usize, intent: usize): void {
  let class: usize = jni.get_object_class(e, receiver);
  let activity: usize = jni.get_static_long_field(e, class, jni.get_static_field_id(e, class, "fern", "J"));
  if (activity == 0 as usize || intent == 0 as usize) {
    return;
  }
  let action: string = android.intent_action(e, intent);
  if (action == ACTIONS[0]) {
    let enabled: i32 = jni.call_int_method_a(e, intent, jni.get_method_id(e, jni.get_object_class(e, intent), "getIntExtra", "(Ljava/lang/String;I)I"), jni.jvalues([jni.new_string(e, "wifi_p2p_state"), 0 as usize]));
    let on: usize = 0 as usize;
    if (enabled == 2) {
      on = 1 as usize;
    }
    android.send(activity, STATE, on);
  } else if (action == ACTIONS[1]) {
    android.send(activity, PEERS, text_buffer(peers_text(e, intent_extra(e, intent, "wifiP2pDeviceList"))));
  } else if (action == ACTIONS[2]) {
    android.send(activity, GROUP, text_buffer(group_text(e, intent_extra(e, intent, "wifiP2pInfo"))));
  }
}

function intent_extra(e: usize, intent: usize, name: string): usize {
  return jni.call_object(e, intent, "getParcelableExtra", "(Ljava/lang/String;)Landroid/os/Parcelable;", [jni.new_string(e, name)]);
}

function string_field(e: usize, obj: usize, name: string): string {
  return jni.string_of(e, jni.get_object_field(e, obj, jni.get_field_id(e, jni.get_object_class(e, obj), name, "Ljava/lang/String;")));
}

function int_field(e: usize, obj: usize, name: string): i32 {
  return jni.get_int_field(e, obj, jni.get_field_id(e, jni.get_object_class(e, obj), name, "I"));
}

function flag_field(e: usize, obj: usize, name: string): string {
  if ((jni.get_boolean_field(e, obj, jni.get_field_id(e, jni.get_object_class(e, obj), name, "Z")) & 255) != 0) {
    return "1";
  }
  return "0";
}

// One line for each device of the WifiP2pDeviceList `list`: the status,
// the address and the name, separated by tabs.
function peers_text(e: usize, list: usize): string {
  if (list == 0 as usize) {
    return "";
  }
  let devices: usize = jni.call_object(e, jni.call_object(e, list, "getDeviceList", "()Ljava/util/Collection;", []), "toArray", "()[Ljava/lang/Object;", []);
  let out: string = "";
  for k in 0..jni.get_array_length(e, devices) {
    let d: usize = jni.get_object_array_element(e, devices, k);
    out = out + int_field(e, d, "status").to_string() + "\t" + string_field(e, d, "deviceAddress") + "\t" + string_field(e, d, "deviceName") + "\n";
    let deleted: usize = jni.delete_local_ref(e, d);
  }
  return out;
}

// The WifiP2pInfo `info`: formed, owner and the owner address.
function group_text(e: usize, info: usize): string {
  if (info == 0 as usize) {
    return "0\t0\t";
  }
  let address: usize = jni.get_object_field(e, info, jni.get_field_id(e, jni.get_object_class(e, info), "groupOwnerAddress", "Ljava/net/InetAddress;"));
  let host: string = "";
  if (address != 0 as usize) {
    host = jni.string_of(e, jni.call_object(e, address, "getHostAddress", "()Ljava/lang/String;", []));
  }
  return flag_field(e, info, "groupFormed") + "\t" + flag_field(e, info, "isGroupOwner") + "\t" + host;
}

// A heap copy of `text` for the loop thread: its length, then its bytes.
function text_buffer(text: string): usize {
  let b: u8[] = text.bytes();
  unsafe {
    let buf: usize = __alloc(4 + b.len());
    __store_i32(buf, b.len());
    for k in 0..b.len() {
      __store_u8(buf + (4 + k) as usize, b[k] as i32);
    }
    return buf;
  }
}

// The text of a buffer from text_buffer, which it frees.
function taken_text(buf: usize): string {
  let b: u8[] = [];
  unsafe {
    let n: i32 = __load_i32(buf);
    for k in 0..n {
      b = b.append(__load_u8(buf + (4 + k) as usize) as u8);
    }
    __free(buf, 4 + n);
  }
  return unsafe { string_from_bytes_unchecked(b) };
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
