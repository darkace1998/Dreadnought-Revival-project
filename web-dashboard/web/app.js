/* Dreadnought Ops dashboard — vanilla JS, no dependencies. */
"use strict";

const $ = (id) => document.getElementById(id);
// tbodyFor: $() is getElementById and does not understand CSS selectors like
// "table tbody" (returns null -> "Cannot set properties of null").
const tbodyFor = (tableId) => {
  const t = $(tableId);
  return t ? t.querySelector("tbody") : null;
};
const state = {
  history: [], // {t, queue, matches, instances}
  lastUp: null,
  currentTab: "overview",
  logTimer: null,
};

async function api(path, opts = {}) {
  const res = await fetch(path, { credentials: "same-origin", ...opts });
  if (res.status === 401) {
    showLogin();
    throw new Error("not logged in");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || ("HTTP " + res.status));
  return data;
}

function toast(msg, kind = "") {
  const el = document.createElement("div");
  el.className = "toast " + kind;
  el.textContent = msg;
  $("toasts").appendChild(el);
  setTimeout(() => el.remove(), 4000);
}

/* ---------- login ---------- */
function showLogin() { $("login-overlay").classList.remove("hidden"); }
function hideLogin() { $("login-overlay").classList.add("hidden"); $("login-key").value = ""; }

async function doLogin() {
  const key = $("login-key").value;
  $("login-error").classList.add("hidden");
  try {
    await api("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ admin_key: key }),
    });
    hideLogin();
    toast("Signed in.", "ok");
    refreshAll();
  } catch (e) {
    $("login-error").textContent = "Sign-in failed: " + e.message;
    $("login-error").classList.remove("hidden");
  }
}

/* ---------- tabs ---------- */
function switchTab(name) {
  state.currentTab = name;
  document.querySelectorAll("#tabs button").forEach((b) => b.classList.toggle("active", b.dataset.tab === name));
  document.querySelectorAll(".tab").forEach((s) => s.classList.toggle("active", s.id === "tab-" + name));
  if (name === "players") { loadPlayers(); loadBans(); }
  if (name === "queue") loadQueue();
  if (name === "matches") { loadInstances(); loadResults(); }
  if (name === "online") loadOnline();
  if (name === "news") loadTiles();
  if (name === "backups") loadBackups();
  if (name === "servers") loadServers();
  if (name === "chat") loadChat();
  if (name === "logs") initLogs();
  if (name === "metrics") loadMetrics();
  if (name === "config") loadConfig();
  if (name === "overview") loadStatus();
}

/* ---------- overview ---------- */
function esc(v) {
  return String(v == null ? "" : v).replace(/[&<>"]/g, (c) => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));
}

async function loadStatus() {
  let data;
  try {
    data = await api("/api/status");
  } catch (e) { $("live-dot").classList.add("down"); return; }
  $("live-dot").classList.remove("down");
  $("hero-up").textContent = data.up + " / " + data.total;
  $("hero-up-sub").textContent = data.up === data.total ? "all healthy" : "DEGRADED — details below";
  $("hero-queue").textContent = data.queued_players;
  $("hero-matches").textContent = data.active_matches;
  $("hero-instances").textContent = data.instances;
  $("hero-servers").textContent = data.servers;
  $("hero-online").textContent = data.online;

  const grid = $("service-grid");
  grid.innerHTML = "";
  for (const [name, svc] of Object.entries(data.services)) {
    const up = !!svc.up;
    const extra = Object.entries(svc)
      .filter(([k]) => !["up", "http_code", "latency_ms", "status", "service"].includes(k))
      .map(([k, v]) => k + "=" + v).join("\n");
    const div = document.createElement("div");
    div.className = "service";
    div.innerHTML = `<div class="name"><span class="dot ${up ? "ok" : "bad"}"></span>${esc(name)}</div>
      <div class="meta">HTTP ${esc(svc.http_code)} · ${esc(svc.latency_ms)} ms${extra ? "\n" + esc(extra) : ""}</div>`;
    grid.appendChild(div);
  }

  // history ring buffer (~5 min at 5s)
  state.history.push({ t: Date.now(), q: data.queued_players, m: data.active_matches, i: data.instances });
  if (state.history.length > 60) state.history.shift();
  drawHistory($("chart-history"), state.history);

  // event feed on up/down transitions
  const key = data.up + "/" + data.total;
  if (state.lastUp !== null && state.lastUp !== key) {
    const ok = data.up === data.total;
    addEvent(ok ? "All services healthy again." : `Status change: ${data.up}/${data.total} online.`, ok ? "good" : "bad");
  }
  state.lastUp = key;
}

function addEvent(text, kind = "") {
  const feed = $("event-feed");
  if (feed.querySelector("p.muted")) feed.innerHTML = "";
  const div = document.createElement("div");
  div.className = "ev " + kind;
  div.innerHTML = `<span class="ts">${new Date().toLocaleTimeString()}</span> — ${esc(text)}`;
  feed.prepend(div);
  while (feed.children.length > 20) feed.lastChild.remove();
}

function drawHistory(canvas, hist) {
  const ctx = canvas.getContext("2d");
  const W = (canvas.width = canvas.clientWidth * 2);
  const H = (canvas.height = 360);
  ctx.clearRect(0, 0, W, H);
  if (hist.length < 2) {
    ctx.fillStyle = "#8b98b8"; ctx.font = "24px sans-serif";
    ctx.fillText("Collecting data…", 20, 40);
    return;
  }
  const max = Math.max(2, ...hist.map((p) => Math.max(p.q, p.m, p.i)));
  const series = [
    { key: "q", color: "#6ea8fe" },
    { key: "m", color: "#34d399" },
    { key: "i", color: "#a06bff" },
  ];
  ctx.strokeStyle = "rgba(120,150,220,.15)"; ctx.lineWidth = 1;
  for (let g = 0; g <= 4; g++) {
    const y = 20 + ((H - 40) * g) / 4;
    ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke();
  }
  for (const s of series) {
    ctx.strokeStyle = s.color; ctx.lineWidth = 3; ctx.beginPath();
    hist.forEach((p, idx) => {
      const x = (W * idx) / (hist.length - 1);
      const y = H - 20 - ((H - 40) * p[s.key]) / max;
      idx ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
    });
    ctx.stroke();
  }
}

/* ---------- players (every registered account) ---------- */
let accountsCache = [];
async function loadPlayers() {
  try {
    const data = await api("/api/accounts");
    accountsCache = data.accounts || [];
    const withData = accountsCache.filter((a) => a.has_player_data).length;
    $("accounts-count").textContent = `${accountsCache.length} accounts · ${withData} with game data`;
    renderPlayers();
  } catch (e) { toast("Players: " + e.message, "err"); }
}
function renderPlayers() {
  const q = ($("accounts-search").value || "").toLowerCase();
  const tb = tbodyFor("accounts-table");
  tb.innerHTML = "";
  for (const a of accountsCache) {
    const hay = `${a.username} ${a.email} ${a.player_id} ${a.id}`.toLowerCase();
    if (q && !hay.includes(q)) continue;
    const tr = document.createElement("tr");
    const status = a.banned
      ? `<span class="badge bad">banned</span>`
      : a.has_player_data ? `<span class="badge ok">active</span>` : `<span class="badge warn">registered only</span>`;
    tr.innerHTML = `<td>${esc(a.username) || "–"}</td><td>${esc(a.email) || "–"}</td>
      <td>${a.player_id ? `<code>${esc(a.player_id)}</code>` : '<span class="muted">–</span>'}</td>
      <td>${esc(a.credits)}</td><td>${esc(a.premium)}</td><td>${esc(a.free_xp)}</td>
      <td>${a.has_player_data ? esc(a.rank) : "–"}</td><td>${status}</td><td class="row"></td>`;
    const cell = tr.lastChild;
    if (a.player_id) {
      const d = document.createElement("button");
      d.className = "btn small"; d.textContent = "🔍";
      d.title = "Details";
      d.onclick = () => showPlayerDetail(a.player_id, a.username);
      cell.appendChild(d);
      const g = document.createElement("button");
      g.className = "btn small"; g.textContent = "→ Grant";
      g.onclick = () => { $("grant-id").value = a.player_id; toast("ID copied to the grant form."); };
      cell.appendChild(g);
      const p = document.createElement("button");
      p.className = "btn small"; p.textContent = "→ Prov";
      p.onclick = () => { $("prov-id").value = a.player_id; toast("ID copied to the provision form."); };
      cell.appendChild(p);
      const rs = document.createElement("button");
      rs.className = "btn small"; rs.textContent = "→ Reset";
      rs.onclick = () => { $("reset-id").value = a.player_id; toast("ID copied to the reset form."); };
      cell.appendChild(rs);
    }
    if (a.username) {
      const b = document.createElement("button");
      b.className = "btn small"; b.textContent = a.banned ? "→ Unban" : "→ Ban";
      b.onclick = () => { $("ban-user").value = a.username; toast("Name copied to the ban form."); };
      cell.appendChild(b);
    }
    tb.appendChild(tr);
  }
}

async function doGrantAll() {
  const c = parseInt($("grant-all-credits").value, 10) || 0;
  const p = parseInt($("grant-all-premium").value, 10) || 0;
  const x = parseInt($("grant-all-xp").value, 10) || 0;
  if (!c && !p && !x) { toast("Nothing to grant.", "err"); return; }
  const n = accountsCache.filter((a) => a.has_player_data).length;
  confirmAction("Give to all?", `${n} accounts get ${c} credits, ${p} premium, ${x} free XP each (added on top).`, async () => {
    const data = await api("/api/grant-all", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ credits: c, premium: p, free_xp: x }),
    });
    $("grant-all-result").textContent = `${data.granted} granted, ${data.failed} failed.`;
    toast(`Give to all: ${data.granted} ok, ${data.failed} errors.`, data.failed ? "err" : "ok");
    loadPlayers();
  });
}

/* ---------- queue ---------- */
async function loadQueue() {
  try {
    const data = await api("/api/queue");
    const entries = data.queue || [];
    $("queue-count").textContent = entries.length + " entries";
    const tb = tbodyFor("queue-table");
    tb.innerHTML = "";
    for (const e of entries) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td><code>${esc(e.id)}</code></td><td><code>${esc(e.user_id)}</code></td>
        <td>${esc(e.game_mode)}</td><td>${esc(e.tier_min)}–${esc(e.tier_max)}</td>
        <td><span class="badge ${e.status === "waiting" ? "warn" : "ok"}">${esc(e.status)}</span></td>
        <td>${esc(e.queued_at)}</td><td></td>`;
      const kick = document.createElement("button");
      kick.className = "btn small danger"; kick.textContent = "Kick";
      kick.onclick = () => confirmAction("Remove from queue?", `${e.user_id} (${e.game_mode}) leaves the waiting queue.`, async () => {
        await api("/api/queue/kick/" + e.id, { method: "DELETE" });
        toast("Removed.", "ok");
        loadQueue();
      });
      tr.lastChild.appendChild(kick);
      tb.appendChild(tr);
    }
    drawHistory($("chart-queue"), state.history.length ? state.history : [{ t: 0, q: entries.length, m: 0, i: 0 }]);
  } catch (e) { toast("Queue: " + e.message, "err"); }
}

/* ---------- instances ---------- */
async function loadInstances() {
  try {
    const data = await api("/api/instances");
    const list = data.instances || [];
    $("instances-count").textContent = `${list.length} running · ports: ${esc(data.ports_used || 0)}`;
    const tb = tbodyFor("instances-table");
    tb.innerHTML = "";
    for (const i of list) {
      const tr = document.createElement("tr");
      const players = Array.isArray(i.players) ? i.players.length : (i.players || 0);
      tr.innerHTML = `<td><code>${esc(i.id)}</code></td><td>${esc(i.port)}</td><td>${esc(i.game_mode)}</td>
        <td>${esc(i.map)}</td><td>${esc(players)}</td>
        <td>${i.ready === true ? '<span class="badge ok">ready</span>' : i.ready === false ? '<span class="badge warn">loading</span>' : "–"}</td>
        <td>${esc(i.started_at)}</td><td></td>`;
      const btn = document.createElement("button");
      btn.className = "btn small danger"; btn.textContent = "Stop";
      btn.onclick = () => confirmAction("Stop instance?", `${i.id} (${i.map} ${i.game_mode}) will be stopped.`, async () => {
        await api("/api/stop-instance/" + i.id, { method: "POST" });
        toast("Instance stopped.", "ok");
        loadInstances();
      });
      tr.lastChild.appendChild(btn);
      tb.appendChild(tr);
    }
  } catch (e) { toast("Instances: " + e.message, "err"); }
}

/* ---------- servers ---------- */
async function loadServers() {
  try {
    const data = await api("/api/servers");
    const list = data.servers || [];
    $("servers-count").textContent = list.length + " servers";
    const tb = tbodyFor("servers-table");
    tb.innerHTML = "";
    for (const s of list) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td><code>${esc(s.id)}</code></td><td>${esc(s.name)}</td>
        <td>${esc(s.ip)}:${esc(s.port)}</td><td>${esc(s.game_mode)}</td><td>${esc(s.map)}</td>
        <td>${esc(s.current_players)}/${esc(s.max_players)}</td>`;
      tb.appendChild(tr);
    }
  } catch (e) { toast("Servers: " + e.message, "err"); }
}

/* ---------- chat ---------- */
async function loadChat() {
  try {
    const ch = $("chat-channel").value || "global";
    const data = await api("/api/chat?channel=" + encodeURIComponent(ch));
    $("chat-channel-pill").textContent = ch;
    const feed = $("chat-feed");
    feed.innerHTML = "";
    const msgs = data.messages || [];
    if (!msgs.length) feed.innerHTML = '<p class="muted">No messages.</p>';
    for (const m of msgs) {
      const div = document.createElement("div");
      div.className = "ev";
      div.innerHTML = `<span class="ts">${esc(m.sent_at)}</span> <b>${esc(m.sender_id)}</b>: ${esc(m.content)}`;
      feed.appendChild(div);
    }
  } catch (e) { toast("Chat: " + e.message, "err"); }
}

/* ---------- logs ---------- */
async function initLogs() {
  if (!$("log-source").options.length) {
    try {
      const data = await api("/api/logs");
      for (const n of data.sources || []) {
        const o = document.createElement("option");
        o.value = n; o.textContent = n;
        $("log-source").appendChild(o);
      }
      $("log-source").value = "mmog-frames";
    } catch (e) { toast("Logs: " + e.message, "err"); return; }
  }
  loadLog();
}
async function loadLog() {
  const name = $("log-source").value;
  if (name === "battle-logs") {
    // file list mode
    try {
      const file = $("log-file").value;
      const data = await api("/api/logs?name=battle-logs" + (file ? "&file=" + encodeURIComponent(file) + "&lines=" + $("log-lines").value : ""));
      if (data.files) {
        $("log-file").classList.remove("hidden");
        const cur = $("log-file").value;
        $("log-file").innerHTML = "";
        for (const f of data.files) {
          const o = document.createElement("option");
          o.value = f; o.textContent = f;
          $("log-file").appendChild(o);
        }
        if (cur) $("log-file").value = cur;
        $("log-view").textContent = data.files.length ? "Pick a file …" : "No battle logs.";
        return;
      }
      renderLogLines(data.lines || [], data.file || "");
    } catch (e) { toast("Battle logs: " + e.message, "err"); }
    return;
  }
  $("log-file").classList.add("hidden");
  try {
    const data = await api(`/api/logs?name=${encodeURIComponent(name)}&lines=${$("log-lines").value}`);
    $("log-path").textContent = (data.path || "") + (data.truncated ? " · truncated (newest N lines)" : "");
    renderLogLines(data.lines || [], data.note || "");
  } catch (e) { toast("Log: " + e.message, "err"); }
}
function renderLogLines(lines, note) {
  const f = ($("log-filter").value || "").toLowerCase();
  const out = f ? lines.filter((l) => l.toLowerCase().includes(f)) : lines;
  $("log-view").textContent = (note ? note + "\n" : "") + (out.length ? out.join("\n") : "(empty)");
  $("log-view").scrollTop = $("log-view").scrollHeight;
}

/* ---------- metrics ---------- */
async function loadMetrics() {
  try {
    const data = await api("/api/metrics-summary");
    const grid = $("metrics-grid");
    grid.innerHTML = "";
    for (const [svc, gauges] of Object.entries(data)) {
      const box = document.createElement("div");
      box.className = "metric-box";
      let rows = "";
      if (gauges.error) rows = `<dt>error</dt><dd>${esc(gauges.error)}</dd>`;
      else {
        const keys = Object.keys(gauges);
        rows = keys.length
          ? keys.map((k) => `<dt><code>${esc(k)}</code></dt><dd>${esc(gauges[k])}</dd>`).join("")
          : "<dt>–</dt><dd>no dn_ gauges</dd>";
      }
      box.innerHTML = `<h3>${esc(svc)}</h3><dl class="kv">${rows}</dl>`;
      grid.appendChild(box);
    }
  } catch (e) { toast("Metrics: " + e.message, "err"); }
}

/* ---------- config ---------- */
async function loadConfig() {
  try {
    const data = await api("/api/config");
    const kv = (obj) => Object.entries(obj || {}).map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(typeof v === "object" ? JSON.stringify(v) : v)}</dd>`).join("");
    $("config-box").innerHTML = `<dl class="kv">${kv({
      server_ip: data.server_ip, public_host: data.public_host, game_binary: data.game_binary,
      dashboard_addr: data.dashboard_addr, jwt_secret_set: data.jwt_secret_set,
      admin_key_set: data.admin_key_set, internal_key_set: data.internal_key_set,
    })}</dl>`;
    $("cert-box").innerHTML = `<dl class="kv">${kv(data.cert)}</dl>`;
    $("switches-box").innerHTML = `<dl class="kv">${kv(data.switches)}</dl>`;
  } catch (e) { toast("Config: " + e.message, "err"); }
}

/* ---------- actions ---------- */
let modalFn = null;
function confirmAction(title, text, fn) {
  $("modal-title").textContent = title;
  $("modal-text").textContent = text;
  $("modal").classList.remove("hidden");
  modalFn = fn;
}

async function doGrant() {
  const id = $("grant-id").value.trim().toLowerCase();
  const payload = { user_id: id };
  const c = parseInt($("grant-credits").value, 10) || 0;
  const p = parseInt($("grant-premium").value, 10) || 0;
  const x = parseInt($("grant-xp").value, 10) || 0;
  if (c) payload.credits = c;
  if (p) payload.premium = p;
  if (x) payload.free_xp = x;
  confirmAction("Grant balance?", `${id}: ${c} credits, ${p} premium, ${x} free XP`, async () => {
    await api("/api/grant", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    toast("Granted.", "ok");
    loadPlayers();
  });
}

/* ---------- online ---------- */
async function loadOnline() {
  try {
    const data = await api("/api/online");
    const list = data.online || [];
    $("online-count").textContent = list.length + " connected";
    const tb = tbodyFor("online-table");
    tb.innerHTML = "";
    for (const p of list) {
      const where = p.match_id
        ? `<span class="badge ok">Match ${esc(p.game_mode)} T${esc(p.team)}</span>`
        : p.queued_mode ? `<span class="badge warn">Queue ${esc(p.queued_mode)}</span>` : '<span class="muted">Hangar</span>';
      const tr = document.createElement("tr");
      tr.innerHTML = `<td>${esc(p.name) || "–"}</td><td><code>${esc(p.player_id)}</code></td>
        <td>${esc((p.channels || []).join(", "))}</td>
        <td>${p.queued_mode ? esc(p.queued_mode) : "–"}</td><td>${where}</td><td>${p.team || "–"}</td>`;
      tb.appendChild(tr);
    }
  } catch (e) { toast("Online: " + e.message, "err"); }
}

/* ---------- broadcast ---------- */
async function sendBroadcast() {
  const channel = $("bc-channel").value.trim() || "dreadnought.global";
  const content = $("bc-message").value.trim();
  if (!content) { toast("Message missing.", "err"); return; }
  confirmAction("Send broadcast?", `"${content}" to ${channel} (all connected players).`, async () => {
    const data = await api("/api/broadcast", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ channel, content }),
    });
    toast(`Sent (${data.reached} reached).`, "ok");
    $("bc-message").value = "";
    loadChat();
  });
}

/* ---------- results ---------- */
async function loadResults() {
  try {
    const data = await api("/api/results?limit=50");
    const list = data.results || [];
    $("results-count").textContent = list.length + " reports";
    const tb = tbodyFor("results-table");
    tb.innerHTML = "";
    for (const r of list) {
      const cls = r.outcome === "win" ? "ok" : r.outcome === "loss" ? "bad" : "warn";
      const tr = document.createElement("tr");
      tr.innerHTML = `<td><code>${esc(r.match_id.slice(0, 8))}…</code></td><td><code>${esc(r.user_id.slice(0, 8))}…</code></td>
        <td>${esc(r.team)}</td><td><span class="badge ${cls}">${esc(r.outcome)}</span></td>
        <td>${esc(r.kills)}</td><td>${esc(r.credits)}</td><td>${esc(r.xp)}</td><td>${esc(r.reported_at)}</td>`;
      tb.appendChild(tr);
    }
  } catch (e) { toast("Results: " + e.message, "err"); }
}

/* ---------- bans ---------- */
async function loadBans() {
  try {
    const data = await api("/api/bans");
    const list = data.bans || [];
    $("bans-count").textContent = list.length + " active";
    const tb = tbodyFor("bans-table");
    tb.innerHTML = "";
    for (const b of list) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td>${esc(b.username)}</td><td>${esc(b.reason)}</td><td>${esc(b.since)}</td><td></td>`;
      const btn = document.createElement("button");
      btn.className = "btn small"; btn.textContent = "Unban";
      btn.onclick = () => {
        $("ban-user").value = b.username;
        toast("Name copied to the ban form — click Unban there.");
      };
      tr.lastChild.appendChild(btn);
      tb.appendChild(tr);
    }
  } catch (e) { toast("Bans: " + e.message, "err"); }
}

/* ---------- news tiles ---------- */
async function loadTiles() {
  try {
    const data = await api("/api/tiles");
    const list = data.tiles || [];
    $("tiles-count").textContent = list.length + " tiles";
    const tb = tbodyFor("tiles-table");
    tb.innerHTML = "";
    for (const t of list) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td><code>${esc(t.id)}</code></td><td>${esc(t.title)}</td><td>${esc(t.type)}</td>
        <td>${esc(t.section_size)}</td>
        <td>${t.active ? '<span class="badge ok">on</span>' : '<span class="badge warn">off</span>'}</td><td class="row"></td>`;
      const cell = tr.lastChild;
      const edit = document.createElement("button");
      edit.className = "btn small"; edit.textContent = "✎";
      edit.onclick = () => {
        $("tile-id").value = t.id; $("tile-title").value = t.title;
        $("tile-body").value = t.body || ""; $("tile-type").value = t.type;
        $("tile-size").value = t.section_size; $("tile-active").checked = !!t.active;
        toast("Tile copied to the form.");
      };
      const del = document.createElement("button");
      del.className = "btn small danger"; del.textContent = "Delete";
      del.onclick = () => confirmAction("Delete tile?", `${t.id} will be removed from the launcher.`, async () => {
        await api("/api/tiles/" + encodeURIComponent(t.id), { method: "DELETE" });
        toast("Deleted.", "ok");
        loadTiles();
      });
      cell.appendChild(edit); cell.appendChild(del);
      tb.appendChild(tr);
    }
  } catch (e) { toast("News: " + e.message, "err"); }
}

async function saveTile() {
  const payload = {
    id: $("tile-id").value.trim(),
    title: $("tile-title").value.trim(),
    body: $("tile-body").value,
    type: $("tile-type").value,
    section_size: $("tile-size").value,
    active: $("tile-active").checked,
  };
  if (!payload.id || !payload.title) { toast("ID and title are required.", "err"); return; }
  try {
    await api("/api/tiles", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    toast("Saved — the launcher shows it on next load.", "ok");
    loadTiles();
  } catch (e) { toast(e.message, "err"); }
}

/* ---------- provision ---------- */
async function doProvision() {
  const id = $("prov-id").value.trim().toLowerCase();
  if (!/^[0-9a-f]{32}$/.test(id)) { toast("32-hex user ID required.", "err"); return; }
  const payload = {
    user_id: id,
    rank: parseInt($("prov-rank").value, 10) || 20,
    credits: parseInt($("prov-credits").value, 10) || 0,
    premium: parseInt($("prov-premium").value, 10) || 0,
    free_xp: parseInt($("prov-xp").value, 10) || 0,
  };
  confirmAction("Equip test account?", `${id}: rank ${payload.rank}, values are SET (not added), all ships+items unlocked.`, async () => {
    const data = await api("/api/provision", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload),
    });
    const s = data.summary || {};
    $("prov-result").textContent = `Rank ${s.rank}, ${s.ships} ships, ${s.items} items, ${s.loadouts} loadouts.`;
    toast("Equipped.", "ok");
    loadPlayers();
  });
}

/* ---------- player detail ---------- */
async function showPlayerDetail(pid, name) {
  const box = $("player-detail");
  box.innerHTML = '<p class="muted">Loading …</p>';
  try {
    const d = await api("/api/player/" + pid);
    const p = d.player || {};
    const kv = (obj) => Object.entries(obj || {}).map(([k, v]) => {
      const val = (v && typeof v === "object") ? JSON.stringify(v) : String(v == null ? "" : v);
      return `<dt>${esc(k)}</dt><dd>${esc(val)}</dd>`;
    }).join("");
    const fleetRows = (d.fleets || []).map((f) => `${esc(f.name)} (T${esc(f.fleet_type)}${f.active ? ", active" : ""}, ${esc(f.loadouts)} loadouts)`).join("<br>") || "–";
    const shipRows = (d.ships || []).slice(0, 12).map((s) => `${esc(s.name)} — ${esc(s.ship_xp)} XP`).join("<br>") || "–";
    const resRows = (d.recent_results || []).map((r) => `${esc(r.match_id.slice(0, 8))}: ${esc(r.outcome)} (+${esc(r.credits)}c/+${esc(r.xp)}xp)`).join("<br>") || "–";
    box.innerHTML = `<h3>${esc(name || pid)}</h3><dl class="kv">${kv({
      credits: p.credits, premium: p.premium, free_xp: p.free_xp,
      rank: p.current_rank, rank_xp: p.rank_xp, queue: d.queued_mode || "–",
      match: d.in_match ? `${d.live_match.game_mode} ${d.live_match.map} (T${d.live_match.team})` : "–",
      purchases: d.purchases,
    })}</dl>
    <p><b>Fleets:</b><br>${fleetRows}</p>
    <p><b>Ships (top 12 by XP):</b><br>${shipRows}</p>
    <p><b>Recent results:</b><br>${resRows}</p>`;
  } catch (e) { box.innerHTML = `<p class="error">${esc(e.message)}</p>`; }
}

/* ---------- queue actions ---------- */
async function clearQueue() {
  confirmAction("Clear queue?", "All waiting entries will be removed (running matches stay).", async () => {
    const data = await api("/api/queue/clear", { method: "POST" });
    toast(`${data.cleared} entries removed.`, "ok");
    loadQueue();
  });
}

async function forceMatch() {
  confirmAction("Force match?", "The largest waiting group plays now (cap: PLAYERS_PER_MATCH).", async () => {
    const data = await api("/api/force-match", { method: "POST" });
    toast(`Match formed: ${data.players} players (${data.game_mode}).`, "ok");
    loadQueue(); loadInstances();
  });
}

/* ---------- backups ---------- */
function fmtSize(n) {
  if (n > 1048576) return (n / 1048576).toFixed(1) + " MB";
  if (n > 1024) return (n / 1024).toFixed(1) + " KB";
  return n + " B";
}
async function loadBackups() {
  try {
    const data = await api("/api/backups");
    const list = data.backups || [];
    $("backups-count").textContent = list.length + " archives";
    const tb = tbodyFor("backups-table");
    tb.innerHTML = "";
    if (!list.length) tb.innerHTML = '<tr><td colspan="3" class="muted">No backups — set up cron: backup.sh --install-cron</td></tr>';
    for (const b of list) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td><code>${esc(b.name)}</code></td><td>${fmtSize(b.size)}</td><td>${esc(b.mtime)}</td>`;
      tb.appendChild(tr);
    }
  } catch (e) { toast("Backups: " + e.message, "err"); }
}

/* ---------- reset ---------- */
async function doReset() {
  const id = $("reset-id").value.trim().toLowerCase();
  if (!/^[0-9a-f]{32}$/.test(id)) { toast("32-hex user ID required.", "err"); return; }
  const currencies = $("reset-currencies").checked;
  const research = $("reset-research").checked;
  if (!currencies && !research) { toast("Nothing selected.", "err"); return; }
  const what = [currencies && "currencies", research && "research"].filter(Boolean).join(" + ");
  confirmAction("Really reset?", `${id}: ${what} will go back to fresh. Cannot be undone!`, async () => {
    await api("/api/reset", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_id: id, currencies, research }),
    });
    $("reset-result").textContent = `Reset (${what}). Re-login the client.`;
    toast("Reset done.", "ok");
    loadPlayers();
  });
}

/* ---------- polling ---------- */
async function refreshAll() {
  try { await api("/api/me"); hideLogin(); } catch { showLogin(); return; }
  if (state.currentTab === "overview") loadStatus();
  else switchTab(state.currentTab);
}

function tickClock() {
  $("clock").textContent = new Date().toLocaleTimeString();
}

document.addEventListener("DOMContentLoaded", () => {
  document.querySelectorAll("#tabs button").forEach((b) => b.onclick = () => switchTab(b.dataset.tab));
  $("login-btn").onclick = doLogin;
  $("login-key").addEventListener("keydown", (e) => { if (e.key === "Enter") doLogin(); });
  $("logout-btn").onclick = async () => { await api("/api/logout", { method: "POST" }).catch(() => {}); showLogin(); };
  $("accounts-reload").onclick = loadPlayers;
  $("accounts-search").oninput = renderPlayers;
  $("grant-all-btn").onclick = () => doGrantAll().catch((e) => toast(e.message, "err"));
  $("prov-btn").onclick = () => doProvision().catch((e) => toast(e.message, "err"));
  $("reset-btn").onclick = () => doReset().catch((e) => toast(e.message, "err"));
  $("online-reload").onclick = loadOnline;
  $("results-reload").onclick = loadResults;
  $("bc-send").onclick = () => sendBroadcast().catch((e) => toast(e.message, "err"));
  $("tiles-reload").onclick = loadTiles;
  $("tile-save").onclick = saveTile;
  $("queue-clear-btn").onclick = () => clearQueue().catch((e) => toast(e.message, "err"));
  $("force-match-btn").onclick = () => forceMatch().catch((e) => toast(e.message, "err"));
  $("backups-reload").onclick = loadBackups;  $("queue-reload").onclick = loadQueue;
  $("instances-reload").onclick = loadInstances;
  $("servers-reload").onclick = loadServers;
  $("chat-reload").onclick = loadChat;
  $("metrics-reload").onclick = loadMetrics;
  $("log-reload").onclick = loadLog;
  $("log-source").onchange = () => { $("log-file").innerHTML = ""; loadLog(); };
  $("log-file").onchange = loadLog;
  $("log-filter").oninput = loadLog;
  $("grant-btn").onclick = () => doGrant().catch((e) => toast(e.message, "err"));
  $("ban-btn").onclick = () => {
    const u = $("ban-user").value.trim(), r = $("ban-reason").value.trim();
    confirmAction("Ban player?", `${u} — reason: ${r}`, async () => {
      await api("/api/ban", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: u, reason: r }) });
      toast("Banned.", "ok");
    });
  };
  $("unban-btn").onclick = () => {
    const u = $("ban-user").value.trim();
    confirmAction("Unban?", u, async () => {
      await api("/api/unban", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: u }) });
      toast("Unbanned.", "ok");
    });
  };
  $("modal-cancel").onclick = () => { $("modal").classList.add("hidden"); modalFn = null; };
  $("modal-ok").onclick = async () => {
    $("modal").classList.add("hidden");
    try { modalFn && await modalFn(); } catch (e) { toast(e.message, "err"); }
    modalFn = null;
  };
  tickClock();
  setInterval(tickClock, 1000);
  refreshAll();
  setInterval(() => { if (state.currentTab === "overview") loadStatus(); }, 5000);
  state.logTimer = setInterval(() => {
    if (state.currentTab === "logs" && $("log-follow").checked) loadLog();
  }, 5000);
});
