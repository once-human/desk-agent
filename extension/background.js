// desk-agent browser extension.
//
// Sends exactly two things to the local desk-agent (over native messaging, never
// the network):
//   host            hostname of the active tab of the focused window, or "" when
//                   there is nothing to report (private window, browser not
//                   focused, a non-http(s) page)
//   youtubeAudible  true while a non-private YouTube tab is making sound
//
// It never reads or sends a URL path, query string, page title, page content,
// cookies or history. The "tabs" permission is needed only to read a tab's URL,
// and this file reduces it to a hostname before anything leaves it.

const api = globalThis.browser ?? globalThis.chrome;
const HOST = "desk_agent";

const YOUTUBE = new Set([
  "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com",
  "youtu.be", "youtube-nocookie.com", "www.youtube-nocookie.com",
]);

let port = null;
let timer = null;

function hostOf(url) {
  try {
    const u = new URL(url);
    if (u.protocol !== "http:" && u.protocol !== "https:") return "";
    return u.hostname; // hostname only: no port, path, query, fragment or credentials
  } catch {
    return "";
  }
}

async function snapshot() {
  let host = "";
  let youtubeAudible = false;
  try {
    const win = await api.windows.getLastFocused({ populate: true });
    // `focused` must be true: if the user is in a window this extension cannot
    // see (e.g. a private window it has not been allowed in), the last window
    // it knows about is NOT focused, and nothing is reported.
    if (win && win.focused === true && win.incognito !== true && win.type === "normal") {
      const tab = (win.tabs || []).find((t) => t.active);
      if (tab && tab.incognito !== true) host = hostOf(tab.url);
    }
  } catch (_) { /* report nothing */ }
  try {
    const audible = await api.tabs.query({ audible: true });
    youtubeAudible = audible.some((t) => t.incognito !== true && YOUTUBE.has(hostOf(t.url)));
  } catch (_) { /* report false */ }
  return { host, youtubeAudible };
}

function connect() {
  if (port) return port;
  try {
    port = api.runtime.connectNative(HOST);
    port.onDisconnect.addListener(() => {
      port = null; // the agent side is gone; the next tick reconnects
      if (api.runtime.lastError) { /* read it so the browser does not log it */ }
    });
  } catch (_) {
    port = null;
  }
  return port;
}

async function send() {
  const p = connect();
  if (!p) return;
  try {
    p.postMessage(await snapshot());
  } catch (_) {
    port = null;
  }
}

function soon() {
  clearTimeout(timer);
  timer = setTimeout(send, 250);
}

api.tabs.onActivated.addListener(soon);
api.tabs.onRemoved.addListener(soon);
api.tabs.onUpdated.addListener((_id, change) => {
  if (change.url !== undefined || change.audible !== undefined || change.status === "complete") soon();
});
api.windows.onFocusChanged.addListener(soon);
api.windows.onRemoved.addListener(soon);

// Heartbeat: the agent drops a report that is not refreshed (75 s), so a
// crashed browser or a sleeping extension never leaves a stale site behind.
api.alarms.create("desk-agent-tick", { periodInMinutes: 0.5 });
api.alarms.onAlarm.addListener((a) => { if (a.name === "desk-agent-tick") send(); });
setInterval(send, 15000);
api.runtime.onStartup.addListener(send);
api.runtime.onInstalled.addListener(send);
send();
