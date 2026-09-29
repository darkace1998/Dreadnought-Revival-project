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
  if (name === "players") { loadPlayers(); loadBans(); loadSessions(); }
  if (name === "queue") loadQueue();
  if (name === "matches") { loadInstances(); loadResults(); loadMatchesList(); loadHistory(); }
  if (name === "market") loadCatalog();
  if (name === "audit") loadAudit();
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

  // stuck queue: players waiting while nothing forms and nothing runs.
  if (data.queued_players > 0 && data.active_matches === 0 && data.instances === 0) state.queueStuck = (state.queueStuck || 0) + 1;
  else state.queueStuck = 0;
  state.lastStatus = data;
  refreshAlerts();
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
    resultsCache = list;
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
    renderStats();
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
    <p><b>Recent results:</b><br>${resRows}</p>
    <div id="player-progress"><p class="muted">Loading career …</p></div>`;
    showPlayerProgress(pid).then((html) => {
      const el = $("player-progress");
      if (el) el.innerHTML = html;
    });
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

/* ---------- alerts ---------- */
async function refreshAlerts() {
  const box = $("alerts");
  if (!box) return;
  const st = state.lastStatus;
  const alerts = [];
  if (st && st.up < st.total) alerts.push(["bad", `${st.total - st.up} service(s) down — see Services below.`]);
  if ((state.queueStuck || 0) >= 12) alerts.push(["warn", `Queue stuck: ${st.queued_players} waiting for ~60s with nothing forming.`]);
  // Config + backups at most once a minute; status polls every 5s.
  if (!state.alertsAt || Date.now() - state.alertsAt > 60000) {
    state.alertsAt = Date.now();
    try {
      const cfg = await api("/api/config");
      const cert = cfg.cert || {};
      if (cert.expired) alerts.push(["bad", "TLS certificate EXPIRED — regenerate via gen-certs.sh."]);
      else if (cert.not_after) {
        const days = (new Date(cert.not_after) - Date.now()) / 86400000;
        if (days < 30) alerts.push(["warn", `TLS certificate expires in ${Math.max(0, Math.round(days))} days.`]);
      }
    } catch (e) { /* config optional for alerts */ }
    try {
      const bk = await api("/api/backups");
      if (!bk.count) alerts.push(["warn", "No backups yet — run backup.sh --install-cron."]);
    } catch (e) { /* backups optional for alerts */ }
    state.alertCache = alerts;
  } else if (state.alertCache) {
    alerts.push(...state.alertCache);
  }
  box.innerHTML = alerts.map(([k, t]) => `<div class="ev ${k === "bad" ? "bad" : ""}">${esc(t)}</div>`).join("");
}

/* ---------- match center ---------- */
let matchesCache = [];
async function loadMatchesList() {
  try {
    const data = await api("/api/matches?limit=50");
    matchesCache = data.matches || [];
    const sel = $("match-select");
    const cur = sel.value;
    sel.innerHTML = "";
    const sorted = [...matchesCache].sort((a, b) => {
      if ((a.status === "active") !== (b.status === "active")) return a.status === "active" ? -1 : 1;
      return b.created_at.localeCompare(a.created_at);
    });
    for (const m of sorted) {
      const o = document.createElement("option");
      o.value = m.id;
      o.textContent = `${m.status === "active" ? "● " : ""}${m.game_mode} ${m.map} · ${m.players}p · ${m.created_at}`;
      sel.appendChild(o);
    }
    if (cur) sel.value = cur;
    if (sel.value) showMatchDetail(sel.value);
    else $("match-detail").innerHTML = '<p class="muted">No matches yet.</p>';
  } catch (e) { toast("Matches: " + e.message, "err"); }
}

async function showMatchDetail(id) {
  const box = $("match-detail");
  box.innerHTML = '<p class="muted">Loading …</p>';
  try {
    const d = await api("/api/match/" + id);
    const m = d.match || {};
    const slots = d.slots || [];
    const t1 = slots.filter((s) => s.team === 1).length;
    const t2 = slots.filter((s) => s.team === 2).length;
    let extra = "";
    if (m.instance_id) {
      try {
        const inst = await api("/api/instance/" + m.instance_id);
        extra = `<dt>host</dt><dd>${inst.ready === true ? "ready" : inst.ready === false ? "loading" : "unknown"}${inst.port ? " · port " + esc(inst.port) : ""}</dd>`;
      } catch (e) { extra = `<dt>host</dt><dd>gone</dd>`; }
    }
    let logHint = "";
    if (m.server_port) {
      try {
        const logs = await api("/api/logs?name=battle-logs");
        const hit = (logs.files || []).find((f) => f.includes("port" + m.server_port));
        if (hit) logHint = `<dt>battle log</dt><dd><code>${esc(hit)}</code> (Logs tab)</dd>`;
      } catch (e) { /* logs optional */ }
    }
    const rows = slots.map((s) => `<td><code>${esc(s.user_id.slice(0, 8))}…</code></td><td>${esc(s.team)}</td>`).join("");
    box.innerHTML = `<dl class="kv">
      <dt>mode</dt><dd>${esc(m.game_mode)} on ${esc(m.map)}</dd>
      <dt>status</dt><dd>${esc(m.status)} · teams ${t1}v${t2}</dd>
      <dt>address</dt><dd>${esc(m.server_ip)}:${esc(m.server_port)}</dd>
      <dt>formed</dt><dd>${esc(m.created_at)}${m.server_ready_at ? " · host ready " + esc(m.server_ready_at) : ""}</dd>
      ${extra}${logHint}</dl>
      <div class="table-wrap"><table><thead><tr><th>Player</th><th>Team</th></tr></thead><tbody>${rows || '<tr><td colspan="2" class="muted">No slots.</td></tr>'}</tbody></table></div>`;
  } catch (e) { box.innerHTML = `<p class="error">${esc(e.message)}</p>`; }
}

/* ---------- statistics ---------- */
let resultsCache = [];
function renderStats() {
  const list = resultsCache;
  const by = (k) => list.filter((r) => r.outcome === k).length;
  const wins = by("win"), losses = by("loss"), draws = by("draw"), unknown = list.length - wins - losses - draws;
  const kills = list.reduce((a, r) => a + (r.kills || 0), 0);
  const credits = list.reduce((a, r) => a + (r.credits || 0), 0);
  const xp = list.reduce((a, r) => a + (r.xp || 0), 0);
  const modes = {};
  for (const r of list) {
    const m = r.game_mode || "?";
    modes[m] = modes[m] || { n: 0, win: 0 };
    modes[m].n++;
    if (r.outcome === "win") modes[m].win++;
  }
  const modeRows = Object.entries(modes).map(([m, v]) =>
    `<dt>${esc(m)}</dt><dd>${v.n} reported · ${v.n ? Math.round((100 * v.win) / v.n) : 0}% won</dd>`).join("");
  $("stats-box").innerHTML = `<dl class="kv">
    <dt>reported</dt><dd>${list.length}</dd>
    <dt>win rate</dt><dd>${list.length ? Math.round((100 * wins) / list.length) : 0}% (${wins}W/${losses}L/${draws}D/${unknown}?)</dd>
    <dt>avg kills</dt><dd>${list.length ? (kills / list.length).toFixed(1) : 0}</dd>
    <dt>paid out</dt><dd>${credits} credits · ${xp} XP</dd>${modeRows}</dl>`;
  drawOutcomeBars($("chart-outcomes"), { wins, losses, draws, unknown });
}

function drawOutcomeBars(canvas, v) {
  const ctx = canvas.getContext("2d");
  const W = (canvas.width = canvas.clientWidth * 2);
  const H = (canvas.height = 280);
  ctx.clearRect(0, 0, W, H);
  const total = Math.max(1, v.wins + v.losses + v.draws + v.unknown);
  const bars = [
    ["win", v.wins, "#34d399"], ["loss", v.losses, "#f87171"],
    ["draw", v.draws, "#fbbf24"], ["?", v.unknown, "#8b98b8"],
  ];
  const bw = W / bars.length;
  ctx.font = "22px sans-serif"; ctx.textAlign = "center";
  bars.forEach(([label, n, color], i) => {
    const h = ((H - 60) * n) / total;
    ctx.fillStyle = color;
    ctx.fillRect(i * bw + bw * 0.25, H - 30 - h, bw * 0.5, h);
    ctx.fillStyle = "#e8eefc";
    ctx.fillText(`${label} ${n}`, i * bw + bw / 2, H - 8);
  });
}

/* ---------- history ---------- */
async function loadHistory() {
  try {
    const data = await api("/api/history?limit=30");
    const list = data.matches || [];
    $("history-count").textContent = list.length + " matches";
    const tb = tbodyFor("history-table");
    tb.innerHTML = "";
    for (const m of list) {
      const roster = (m.players || []).map((p) => `${esc(p.user_id.slice(0, 8))} T${esc(p.team)} ${esc(p.kills)}k`).join(", ") || "–";
      const tr = document.createElement("tr");
      tr.innerHTML = `<td><code>${esc(m.id.slice(0, 8))}…</code></td><td>${esc(m.mode)}</td><td>${esc(m.map)}</td>
        <td>${esc(m.started_at)}</td><td>${esc(m.players ? m.players.length : 0)}</td><td>${roster}</td>`;
      tb.appendChild(tr);
    }
  } catch (e) { toast("History: " + e.message, "err"); }
}

/* ---------- catalog ---------- */
let catalogCache = [];
async function loadCatalog() {
  try {
    const data = await api("/api/catalog");
    catalogCache = data.ships || [];
    $("catalog-count").textContent = catalogCache.length + " hulls";
    renderCatalog();
  } catch (e) { toast("Catalog: " + e.message, "err"); }
}
function renderCatalog() {
  const q = ($("catalog-search").value || "").toLowerCase();
  const tb = tbodyFor("catalog-table");
  tb.innerHTML = "";
  for (const s of catalogCache) {
    const hay = `${s.name} ${s.line} ${s.manufacturer} ${s.tier}`.toLowerCase();
    if (q && !hay.includes(q)) continue;
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${esc(s.name)}</td><td>${esc(s.tier)}</td><td>${esc(s.line)}</td>
      <td>${esc(s.manufacturer)}</td><td>${s.hero ? "hero" : "–"}</td>
      <td>${Number(s.price_credits).toLocaleString()}</td><td>${esc(s.owners)}</td>`;
    tb.appendChild(tr);
  }
}

/* ---------- audit ---------- */
async function loadAudit() {
  try {
    const data = await api("/api/audit?lines=500");
    const list = data.entries || [];
    $("audit-count").textContent = list.length + " entries";
    const tb = tbodyFor("audit-table");
    tb.innerHTML = "";
    for (const line of list) {
      let time = "", action = "", detail = line;
      try {
        const o = JSON.parse(line);
        time = o.time || ""; action = o.action || ""; detail = o.detail || "";
      } catch (e) { /* raw line */ }
      const tr = document.createElement("tr");
      tr.innerHTML = `<td>${esc(time)}</td><td><code>${esc(action)}</code></td><td>${esc(detail)}</td>`;
      tb.appendChild(tr);
    }
  } catch (e) { toast("Audit: " + e.message, "err"); }
}

/* ---------- crash reports ---------- */
async function loadCrashes() {
  try {
    const data = await api("/api/crashes");
    const list = data.entries || [];
    const dirSel = $("crash-dir");
    const cur = dirSel.value;
    dirSel.innerHTML = "";
    if (!list.length) {
      $("crash-view").textContent = "No crash reports yet.";
      return;
    }
    for (const e of list) {
      const o = document.createElement("option");
      o.value = (e.dir ? "d:" : "f:") + e.name;
      o.textContent = (e.dir ? "📁 " : "📄 ") + e.name;
      dirSel.appendChild(o);
    }
    if (cur) dirSel.value = cur;
    showCrash();
  } catch (e) { toast("Crashes: " + e.message, "err"); }
}

async function showCrash() {
  const v = $("crash-dir").value || "";
  const isDir = v.startsWith("d:");
  const name = v.slice(2);
  const fileSel = $("crash-file");
  try {
    if (isDir) {
      const data = await api("/api/crashes?dir=" + encodeURIComponent(name));
      const files = data.files || [];
      fileSel.classList.remove("hidden");
      fileSel.innerHTML = "";
      for (const f of files) {
        const o = document.createElement("option");
        o.value = f.name; o.textContent = `${f.name} (${fmtSize(f.size)})`;
        fileSel.appendChild(o);
      }
      if (!files.length) { $("crash-view").textContent = "(empty report folder)"; return; }
      const first = await api(`/api/crashes?dir=${encodeURIComponent(name)}&file=${encodeURIComponent(files[0].name)}&lines=200`);
      $("crash-view").textContent = (first.lines || []).join("\n") || "(empty)";
      return;
    }
    fileSel.classList.add("hidden");
    const data = await api("/api/crashes?file=" + encodeURIComponent(name) + "&lines=200");
    $("crash-view").textContent = (data.lines || []).join("\n") || "(empty)";
  } catch (e) { $("crash-view").textContent = e.message; }
}

/* ---------- sessions ---------- */
async function loadSessions() {
  try {
    const data = await api("/api/sessions");
    const list = data.sessions || [];
    $("sessions-count").textContent = list.length + " active";
    const tb = tbodyFor("sessions-table");
    tb.innerHTML = "";
    for (const s of list) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td>${esc(s.username)}</td><td>${esc(s.created_at)}</td><td>${esc(s.expires_at)}</td>
        <td>${s.expired ? '<span class="badge warn">expired</span>' : '<span class="badge ok">live</span>'}</td><td></td>`;
      const btn = document.createElement("button");
      btn.className = "btn small danger"; btn.textContent = "Revoke";
      btn.onclick = () => confirmAction("Revoke session?", `${s.username} will be signed out.`, async () => {
        await api("/api/sessions/" + s.id, { method: "DELETE" });
        toast("Session revoked.", "ok");
        loadSessions();
      });
      tr.lastChild.appendChild(btn);
      tb.appendChild(tr);
    }
  } catch (e) { toast("Sessions: " + e.message, "err"); }
}

/* ---------- player progress (career, season, contracts) ---------- */
async function showPlayerProgress(pid) {
  try {
    const d = await api("/api/player/" + pid + "/progress");
    const goals = (d.goals || []).map((g) => {
      const stages = (g.stages || []).map((s) => s.amount).join("/");
      return `${esc(g.title)} [${esc(g.category)}]: ${esc(g.progress)}${stages ? " (stages " + esc(stages) + ")" : ""}`;
    }).join("<br>") || "–";
    const seasons = (d.seasons || []).map((s) => `${esc(s.season_id)}: level ${esc(s.level)} (${esc(s.xp)} XP)`).join("<br>") || "–";
    const contracts = (d.contracts || []).map((c) => `${esc(c.contract_id)}: ${esc(c.state)} ${esc(c.progress)}%`).join("<br>") || "–";
    const counters = (d.counters || []).slice(0, 8).map((c) => `${esc(c.counter_id)}${c.counter_sub_id ? "/" + esc(c.counter_sub_id) : ""}: ${esc(c.value)}`).join("<br>") || "–";
    return `<p><b>Career goals:</b><br>${goals}</p>
      <p><b>Seasons:</b><br>${seasons}</p>
      <p><b>Contracts:</b><br>${contracts}</p>
      <p><b>Top counters:</b><br>${counters}</p>`;
  } catch (e) { return `<p class="error">${esc(e.message)}</p>`; }
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
  $("backups-reload").onclick = loadBackups;
  $("match-select").onchange = (e) => showMatchDetail(e.target.value);
  $("match-reload").onclick = loadMatchesList;
  $("history-reload").onclick = loadHistory;
  $("catalog-reload").onclick = loadCatalog;
  $("catalog-search").oninput = renderCatalog;
  $("audit-reload").onclick = loadAudit;
  $("crash-reload").onclick = loadCrashes;
  $("crash-dir").onchange = showCrash;
  $("crash-file").onchange = () => {
    const v = $("crash-dir").value || "";
    if (!v.startsWith("d:")) return;
    const dir = v.slice(2), file = $("crash-file").value;
    if (!file) return;
    api(`/api/crashes?dir=${encodeURIComponent(dir)}&file=${encodeURIComponent(file)}&lines=200`)
      .then((d) => { $("crash-view").textContent = (d.lines || []).join("\n") || "(empty)"; })
      .catch((e) => { $("crash-view").textContent = e.message; });
  };
  $("sessions-reload").onclick = loadSessions;
  $("queue-reload").onclick = loadQueue;
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
