# Wake a live connection without a timer poll

A WebSocket reader can wait for client input while a separate producer sends room changes.
Each subscription owns a loopback datagram socket. A publication makes that socket readable immediately.
The event loop parks on its descriptor. Idle connections do no periodic application work.

A listener registry contains only descriptor numbers, addresses, and identifiers. It never stores connections inside a `Cell`.
The producer owns the descriptor lifetime and closes it after cancellation. A close callback removes the listener and wakes the producer.

```fern
<<*>>=
<<Subscription types>>
<<Create a subscription>>
<<Publish and wait>>
<<End a subscription>>
```

## Subscription types

```fern
<<Subscription types>>=
import "std/net";
import "std/async";
import "std/http";
import "std/result";
import "std/option";
import "std/array";
import "./common";

pub struct Listener { id: string, room: string, fd: i32, address: net.SocketAddr }

pub struct State { listeners: Cell[Listener[]] }

pub struct Subscription { state: State, listener: Listener, alive: Cell[boolean] }

pub function state(): State {
  return State { listeners: cell_new([] as Listener[]) };
}
```

## Create a subscription

One socket supplies both ends of a local notification. The message is only a wake signal.
Authoritative state and the revision journal retain the actual data, so several notifications can coalesce.
The socket uses nonblocking I/O. A full notification queue already contains a pending wake signal.

```fern
<<Create a subscription>>=
pub function subscribe(state: State, room: string): Result[Subscription, http.HttpError] {
  if (state.listeners.get().len() >= 256) {
    return Err(http.fail(503, "all live subscriptions are in use"));
  }
  let fd: i32 = net.udp_socket(net.socket_addr(net.ipv4_loopback(), 0)).map_err((e: net.NetError): http.HttpError => http.fail(503, "cannot create a live notification"))?;
  let result: Result[net.SocketAddr, net.NetError] = net.local_addr(fd);
  let Ok(address) = result else {
    let closed = net.close(fd);
    return Err(http.fail(503, "cannot locate a live notification"));
  };
  if (net.set_nonblocking(fd, true).is_err()) {
    let closed = net.close(fd);
    return Err(http.fail(503, "cannot configure a live notification"));
  }
  let listener: Listener = Listener { id: common.token(), room: room, fd: fd, address: address };
  state.listeners.set(state.listeners.get().append(listener));
  return Ok(Subscription { state: state, listener: listener, alive: cell_new(true) });
}

pub function move(subscription: Subscription, room: string): void {
  let listeners: Listener[] = subscription.state.listeners.get();
  let index: i32 = 0;
  while (index < listeners.len()) {
    if (listeners[index].id == subscription.listener.id) {
      listeners = listeners.with(index, Listener { ...listeners[index], room: room });
    }
    index = index + 1;
  }
  subscription.state.listeners.set(listeners);
}
```

## Publish and wait

A publication signals every subscriber in the room, with an optional sender to exclude.
A producer drains the notification queue before it reads the latest shared state.
Task cancellation and a close notification end the wait. The producer needs no timer poll.

```fern
<<Publish and wait>>=
function signal(listener: Listener): void {
  let sent = net.send_to(listener.fd, [1 as u8], listener.address);
}

pub function notify(state: State, room: string, except: string): void {
  for listener in state.listeners.get() {
    if (listener.room == room && listener.id != except) {
      signal(listener);
    }
  }
}

pub function wake(subscription: Subscription): void {
  signal(subscription.listener);
}

pub function wait(subscription: Subscription): boolean {
  return wait_for(subscription, -1);
}

pub function wait_for(subscription: Subscription, timeout: i32): boolean {
  if (!subscription.alive.get() || async.task_cancelled()) {
    return false;
  }
  let ready: i32 = async.wait_any([subscription.listener.fd, 1], timeout);
  if (!subscription.alive.get() || async.task_cancelled()) {
    return false;
  }
  let buffer: u8[] = __alloc_u8(64);
  while (net.recv(subscription.listener.fd, buffer).is_ok()) {}
  return true;
}
```

## End a subscription

Remove a listener before the operating system reuses its descriptor. Only the producer calls `dispose` after a successful upgrade.
An upgrade error calls `dispose` directly because no producer started.

```fern
<<End a subscription>>=
pub function cancel(subscription: Subscription): void {
  if (!subscription.alive.get()) {
    return;
  }
  subscription.alive.set(false);
  let kept: Listener[] = [];
  for listener in subscription.state.listeners.get() {
    if (listener.id != subscription.listener.id) {
      kept = kept.append(listener);
    }
  }
  subscription.state.listeners.set(kept);
  wake(subscription);
}

pub function dispose(subscription: Subscription): void {
  cancel(subscription);
  let closed = net.close(subscription.listener.fd);
}
```
