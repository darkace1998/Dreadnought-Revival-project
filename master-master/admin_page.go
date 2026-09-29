package main

import "net/http"

// serveAdminPage serves the operator dashboard: every cluster (online,
// stale, blocked), MOTD editing, block/unblock/delete. Same origin as the
// JSON API, so the browser's Basic-auth session covers the fetch calls with
// no extra login code.
func serveAdminPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(masterAdminPageHTML))
}

const masterAdminPageHTML = `<!doctype html>
<meta charset="utf-8">
<title>Master-Master — clusters</title>
<style>
  :root { color-scheme: dark; }
  body { font: 14px/1.5 system-ui, sans-serif; color:#d8e2ea; background:#0a1119;
    margin:0; padding:22px 26px; }
  h1 { font-size:18px; color:#6fd3ff; letter-spacing:.06em; margin:0 0 4px; }
  p.note { color:#7f93a5; font-size:12.5px; margin:0 0 16px; }
  .row { display:flex; gap:8px; align-items:center; margin-bottom:14px; }
  button { padding:7px 12px; font:inherit; font-size:13px; cursor:pointer; color:#e6eef5;
    background:#12293c; border:1px solid #2f5a78; border-radius:6px; }
  button.danger { border-color:#5a2e2e; color:#ff9b8f; background:transparent; }
  button:disabled { opacity:.5; }
  table { width:100%; border-collapse:collapse; font-size:13px; }
  th, td { text-align:left; padding:7px 9px; border-bottom:1px solid #21384d; vertical-align:top; }
  th { color:#7f93a5; text-transform:uppercase; font-size:11px; letter-spacing:.06em; }
  code { font:12px Consolas, monospace; color:#9fb4c4; word-break:break-all; }
  .badge { border-radius:99px; padding:2px 9px; font-size:11.5px; white-space:nowrap; }
  .ok { color:#8fe0a8; border:1px solid #2e5a3f; }
  .bad { color:#ff9b8f; border:1px solid #5a2e2e; }
  .warn { color:#ffd98f; border:1px solid #5a4a2e; }
  .msg { min-height:18px; font-size:13px; margin:10px 0; }
  .msg.bad { color:#ff9b8f; } .msg.good { color:#8fe0a8; }
  dialog { background:#0c141d; color:#d8e2ea; border:1px solid #2f5a78; border-radius:8px; padding:20px; width:min(480px,92vw); }
  dialog input { width:100%; padding:9px 11px; font:inherit; color:#e6eef5; background:#0c1621;
    border:1px solid #24405a; border-radius:6px; box-sizing:border-box; }
  dialog menu { display:flex; justify-content:flex-end; gap:8px; padding:0; margin:14px 0 0; }
</style>
<h1>Cluster directory</h1>
<p class="note">Every registered cluster, including stale and blocked ones. Browsers only see fresh online rows.</p>
<div class="row"><button onclick="load()">↻ Reload</button><span id="count"></span></div>
<div class="msg" id="msg"></div>
<h2>Clusters</h2>
<table>
  <thead><tr><th>Name</th><th>Status</th><th>Players</th><th>Servers</th><th>Address</th><th>Contact</th><th>Sync secret</th><th>Last sync</th><th>Actions</th></tr></thead>
  <tbody id="rows"></tbody>
</table>
<h2>Accounts (mirrored)</h2>
<div class="row"><input id="q" placeholder="Search username, email, id…" style="flex:1;max-width:320px"><button onclick="loadUsers()">Search</button><span id="ucount"></span></div>
<table>
  <thead><tr><th>Username</th><th>Email</th><th>Credits</th><th>Rank</th><th>Ships</th><th>Banned</th><th>Source</th><th>Synced</th></tr></thead>
  <tbody id="users"></tbody>
</table>
<h2>Sync log</h2>
<table>
  <thead><tr><th>Time</th><th>Cluster</th><th>Dir</th><th>Endpoint</th><th>Users</th><th>Status</th><th>Detail</th></tr></thead>
  <tbody id="synclog"></tbody>
</table>
<dialog id="motdDlg">
  <h3 style="margin-top:0">Message of the day</h3>
  <input id="motdText" maxlength="500" placeholder="Welcome…">
  <menu><button id="motdCancel">Cancel</button><button id="motdSave">Save</button></menu>
</dialog>
<dialog id="secretDlg">
  <h3 style="margin-top:0">Sync secret — copy it now</h3>
  <p class="note">Shown <b>once</b>: mail it to the cluster owner yourself. It is stored hashed only and can never be displayed again (revoke + generate rotates).</p>
  <div class="fp" id="secretText" style="font:13px Consolas,monospace;word-break:break-all;background:#0c1621;border:1px solid #24405a;border-radius:6px;padding:8px 10px;"></div>
  <p class="note" id="secretSent"></p>
  <menu><button id="secretClose">Done</button></menu>
</dialog>
<script>
  const $ = id => document.getElementById(id);
  const esc = v => String(v == null ? "" : v);
  let motdId = null;
  function say(t, bad) { $('msg').textContent = t; $('msg').className = 'msg ' + (bad ? 'bad' : 'good'); }
  async function call(method, path, body) {
    const r = await fetch(path, { method, headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body) });
    const doc = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(doc.error || ('HTTP ' + r.status));
    return doc;
  }
  async function load() {
    try {
      const d = await call('GET', '/admin/api/clusters');
      $('count').textContent = d.count + ' cluster(s)';
      const tb = $('rows');
      tb.textContent = '';
      for (const c of (d.clusters || [])) {
        const tr = document.createElement('tr');
        const badge = c.blocked ? '<span class="badge bad">blocked</span>'
          : c.status === 'online' ? '<span class="badge ok">online</span>' : '<span class="badge warn">stale</span>';
        const secret = c.has_secret ? '<span class="badge ok">set</span>' : '<span class="badge warn">none</span>';
        tr.innerHTML = '<td><b></b><br><code></code></td><td>' + badge + '</td><td></td><td></td><td><code></code></td><td></td><td></td><td></td><td></td><td></td>';
        const t = tr.children;
        t[0].querySelector('b').textContent = c.name;
        t[0].querySelector('code').textContent = c.id.slice(0, 8) + ' · ' + (c.version || '');
        t[2].textContent = c.players;
        t[3].textContent = c.servers;
        t[4].querySelector('code').textContent = c.web_url + ' / ' + c.battle_ip;
        t[5].textContent = c.contact_email || '–';
        t[6].innerHTML = secret;
        t[7].textContent = c.last_sync || '–';
        const act = t[9];
        const mk = (label, danger, fn) => {
          const b = document.createElement('button');
          b.textContent = label;
          if (danger) b.className = 'danger';
          b.onclick = fn;
          act.append(b, document.createTextNode(' '));
        };
        mk('MOTD', false, () => { motdId = c.id; $('motdText').value = c.motd || ''; $('motdDlg').showModal(); });
        mk('Secret…', false, async () => {
          const how = prompt('Generate a fresh secret (shown once, mail it yourself), or send one straight to the cluster?\nType: generate / send / revoke', 'generate');
          if (!how) return;
          try {
            const r = await call('POST', '/admin/api/clusters/' + c.id + '/secret', { action: how.trim().toLowerCase() });
            if (r.secret) {
              $('secretText').textContent = r.secret;
              $('secretSent').textContent = r.sent ? 'Pushed to the cluster agent over HTTPS.' : (r.send_error ? 'Push failed: ' + r.send_error + ' — mail it instead.' : 'Mail it to ' + (c.contact_email || 'the owner') + ' yourself.');
              $('secretDlg').showModal();
            } else say(r.status || 'Done.');
            load();
          } catch (e) { say(String(e && e.message || e), true); }
        });
        if (c.blocked) mk('Unblock', false, async () => {
          try { await call('POST', '/admin/api/clusters/' + c.id + '/unblock'); say('Unblocked; returns on next heartbeat.'); load(); }
          catch (e) { say(String(e && e.message || e), true); }
        });
        else mk('Block', true, async () => {
          if (!confirm('Kick "' + c.name + '" out of the browser? It cannot re-list until unblocked.')) return;
          try { await call('POST', '/admin/api/clusters/' + c.id + '/block'); say('Blocked.'); load(); }
          catch (e) { say(String(e && e.message || e), true); }
        });
        mk('Delete', true, async () => {
          if (!confirm('Delete "' + c.name + '" entirely?')) return;
          try { await call('DELETE', '/admin/api/clusters/' + c.id); say('Deleted.'); load(); }
          catch (e) { say(String(e && e.message || e), true); }
        });
        tb.append(tr);
      }
      if (!tb.children.length) tb.innerHTML = '<tr><td colspan="10">No clusters registered yet.</td></tr>';
    } catch (e) { say(String(e && e.message || e), true); }
    loadUsers();
    loadSyncLog();
  }
  async function loadUsers() {
    try {
      const d = await call('GET', '/admin/api/syncusers?q=' + encodeURIComponent($('q').value) + '&limit=50');
      $('ucount').textContent = (d.count || 0) + ' account(s)';
      const tb = $('users');
      tb.textContent = '';
      for (const u of (d.users || [])) {
        const tr = document.createElement('tr');
        tr.innerHTML = '<td><b></b></td><td></td><td></td><td></td><td></td><td></td><td></td><td></td>';
        const t = tr.children;
        t[0].querySelector('b').textContent = u.username;
        t[1].textContent = u.email || '–';
        t[2].textContent = u.credits;
        t[3].textContent = u.rank;
        t[4].textContent = u.ships;
        t[5].innerHTML = u.banned ? '<span class="badge bad">banned</span>' : '–';
        t[6].textContent = u.source_cluster || '–';
        t[7].textContent = u.synced_at || '–';
        tb.append(tr);
      }
      if (!tb.children.length) tb.innerHTML = '<tr><td colspan="8">No mirrored accounts yet — they arrive with the first sync push.</td></tr>';
    } catch (e) { say(String(e && e.message || e), true); }
  }
  async function loadSyncLog() {
    try {
      const d = await call('GET', '/admin/api/synclog?limit=100');
      const tb = $('synclog');
      tb.textContent = '';
    for (const e of (d.entries || [])) {
      const tr = document.createElement('tr');
      tr.innerHTML = '<td></td><td></td><td></td><td><code></code></td><td></td><td></td><td></td>';
      const t = tr.children;
        t[0].textContent = e.time;
        t[1].textContent = e.cluster || e.cluster_id.slice(0, 8);
        t[2].textContent = e.direction;
        t[3].querySelector('code').textContent = e.endpoint;
        t[4].textContent = e.users;
        t[5].textContent = e.status;
        t[6].textContent = e.detail || '–';
        tb.append(tr);
      }
      if (!tb.children.length) tb.innerHTML = '<tr><td colspan="7">No sync traffic yet.</td></tr>';
    } catch (e) { say(String(e && e.message || e), true); }
  }
      if (!tb.children.length) tb.innerHTML = '<tr><td colspan="9">No clusters registered yet.</td></tr>';
    } catch (e) { say(String(e && e.message || e), true); }
  }
  $('motdCancel').onclick = () => $('motdDlg').close();
  $('motdSave').onclick = async () => {
    try {
      await call('POST', '/admin/api/clusters/' + motdId + '/motd', { motd: $('motdText').value });
      $('motdDlg').close();
      say('MOTD saved.');
      load();
    } catch (e) { say(String(e && e.message || e), true); }
  };
  load();
</script>
`
