// Keep native state with one server process for the lifetime of this page.
let socket,
  opening,
  sequence = 0,
  nextCommand = 0;
const pending = new Map();
function connect(path) {
  if (socket?.readyState === WebSocket.OPEN) return Promise.resolve();
  if (opening) return opening;
  const url = new URL(path, location.href);
  url.pathname = "/" + url.pathname.split("/")[1] + "/ws";
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(url);
  socket = ws;
  opening = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      reject(Error("Server connection timed out."));
      ws.close();
    }, 15000);
    ws.onopen = () => {
      clearTimeout(timeout);
      resolve();
    };
    ws.onerror = () => {
      clearTimeout(timeout);
      reject(Error("Cannot connect to the native server."));
    };
    ws.onclose = () => {
      clearTimeout(timeout);
      opening = null;
      reject(Error("Server connection closed."));
      for (const task of pending.values()) {
        clearTimeout(task.timeout);
        task.reject(
          Error(
            "Connection lost. Refresh before retrying. Temporary state can reset.",
          ),
        );
      }
      pending.clear();
    };
    ws.onmessage = ({ data }) => {
      try {
        const reply = JSON.parse(data),
          task = pending.get(reply.seq);
        if (!task) return;
        pending.delete(reply.seq);
        clearTimeout(task.timeout);
        if (reply.status < 200 || reply.status >= 300) {
          let detail;
          try {
            detail = JSON.parse(reply.body).detail;
          } catch {}
          const error = new Error(detail || `Request failed: ${reply.status}`);
          error.status = reply.status;
          task.reject(error);
        } else task.resolve(reply.body);
      } catch {
        ws.close();
      }
    };
  });
  return opening;
}
export async function rpcText(path, body) {
  await connect(path);
  const delay = Math.max(0, nextCommand - performance.now());
  nextCommand = Math.max(nextCommand, performance.now()) + 6;
  if (delay) await new Promise((resolve) => setTimeout(resolve, delay));
  if (socket.readyState !== WebSocket.OPEN)
    throw Error("Connection lost. Refresh before retrying.");
  if (pending.size >= 8 || socket.bufferedAmount > 65536)
    throw Error("Wait for the current server operation.");
  const seq = ++sequence;
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      pending.delete(seq);
      reject(Error("The server operation timed out. Refresh before retrying."));
      socket.close();
    }, 30000);
    pending.set(seq, { resolve, reject, timeout });
    socket.send(
      JSON.stringify({
        seq,
        path,
        body: body === undefined ? "" : JSON.stringify(body),
        post: body !== undefined,
      }),
    );
  });
}
export async function rpcRequest(path, body) {
  return JSON.parse(await rpcText(path, body));
}
addEventListener("pagehide", () => socket?.close(), { once: true });
