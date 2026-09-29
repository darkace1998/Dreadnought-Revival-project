//go:build windows

package main

// The browser page: cluster list first, then per-cluster certificate,
// sign-in and home. Same dark style as dn-launcher's page.

const browserPageHTML = `<!doctype html>
<meta charset="utf-8">
<title>Dreadnought — Server Browser</title>
<style>
  :root { color-scheme: dark; --line:#21384d; --dim:#7f93a5; --accent:#6fd3ff; }
  * { box-sizing: border-box; }
  html, body { margin:0; height:100%; }
  body {
    font: 15px/1.5 "Segoe UI", system-ui, sans-serif; color:#d8e2ea; user-select:none;
    background: radial-gradient(120% 90% at 50% 0%, #16283a 0%, #0a1119 60%, #060a0f 100%);
    display:flex; flex-direction:column;
  }
  header { display:flex; align-items:center; gap:14px; padding:16px 26px; border-bottom:1px solid var(--line); }
  header h1 { margin:0; font-size:20px; letter-spacing:.2em; text-transform:uppercase; color:var(--accent); }
  header .sub { color:var(--dim); font-size:12.5px; letter-spacing:.06em; }
  header .spacer { flex:1; }
  .pill { font-size:12px; padding:3px 10px; border-radius:99px; border:1px solid var(--line); color:var(--dim); }
  .pill.on { color:#8fe0a8; border-color:#2e5a3f; }
  .pill.off { color:#ff9b8f; border-color:#5a2e2e; }
  main { flex:1; display:flex; min-height:0; }
  .view { flex:1; display:none; min-height:0; }
  .view.shown { display:flex; }
  .panel { width:min(560px,94vw); margin:auto; padding:28px; background:rgba(12,20,30,.82);
    border:1px solid var(--line); border-radius:10px; box-shadow:0 18px 50px rgba(0,0,0,.55); }
  .panel.wide { width:min(760px,96vw); }
  label { display:block; margin:12px 0 5px; font-size:12px; color:#90a5b7; letter-spacing:.05em; }
  input, textarea, select { width:100%; padding:10px 12px; font:inherit; color:#e6eef5; background:#0c1621;
    border:1px solid #24405a; border-radius:6px; user-select:text; }
  input:focus, textarea:focus { outline:none; border-color:#3d7fa8; box-shadow:0 0 0 3px rgba(61,127,168,.18); }
  textarea { min-height:90px; font:12px Consolas, monospace; }
  .go { width:100%; margin-top:20px; padding:12px 0; font:inherit; font-weight:600; letter-spacing:.1em;
    text-transform:uppercase; cursor:pointer; color:#04121c; background:linear-gradient(180deg,#7fd8ff,#3ba7d8);
    border:0; border-radius:6px; }
  .go:disabled { opacity:.55; cursor:default; }
  .msg { margin-top:12px; min-height:19px; font-size:13px; }
  .msg.bad { color:#ff9b8f; } .msg.good { color:#8fe0a8; }
  .fp { font:12px/1.5 Consolas, monospace; color:#9fb4c4; word-break:break-all; user-select:text;
    background:#0c1621; border:1px solid #24405a; border-radius:6px; padding:8px 10px; margin:4px 0 6px; }
  #cert h2, h2 { margin:0 0 10px; font-size:18px; color:#9fe4ff; letter-spacing:.04em; }
  #cert p, .note { margin:0 0 10px; color:#b8c7d3; font-size:13.5px; }
  .row { display:flex; gap:8px; align-items:center; }
  .row .grow { flex:1; }
  .srv { border:1px solid var(--line); border-radius:8px; padding:14px 16px; margin:0 0 10px;
    background:rgba(12,20,30,.6); cursor:pointer; }
  .srv:hover { border-color:#3d7fa8; }
  .srv.sel { border-color:#3ba7d8; box-shadow:0 0 0 3px rgba(61,127,168,.18); }
  .srv h3 { margin:0 0 4px; font-size:16px; color:#e6eef5; }
  .srv h3 .n { float:right; font-size:12.5px; color:var(--dim); font-weight:400; }
  .srv p { margin:0; font-size:12.5px; color:var(--dim); }
  .srv .motd { color:#b8c7d3; font-size:13px; margin-top:4px; }
  .link { background:none; border:0; color:var(--dim); font:inherit; font-size:12.5px; cursor:pointer; text-decoration:underline; padding:0; }
  .tabs { display:flex; gap:6px; margin-bottom:16px; }
  .tabs button { flex:1; padding:9px 0; font:inherit; font-size:13px; cursor:pointer; background:transparent;
    color:var(--dim); border:1px solid var(--line); border-radius:6px; }
  .tabs button[aria-selected="true"] { background:#12293c; color:#9fe4ff; border-color:#2f5a78; }
  #home { flex-direction:row; }
  .news { flex:1; overflow:auto; padding:22px 26px; display:grid; grid-template-columns:1fr 1fr; gap:14px; align-content:start; }
  .tile { background:rgba(12,20,30,.82); border:1px solid var(--line); border-radius:8px; padding:16px 18px; }
  .tile.full { grid-column:1 / -1; }
  .tile h3 { margin:0 0 6px; font-size:15px; color:#9fe4ff; letter-spacing:.04em; }
  .tile p { margin:0; color:#b8c7d3; font-size:13.5px; white-space:pre-wrap; }
  aside { width:300px; border-left:1px solid var(--line); padding:26px 24px; display:flex; flex-direction:column; gap:10px; }
  aside .who { font-size:12px; color:var(--dim); letter-spacing:.08em; text-transform:uppercase; }
  aside .name { font-size:22px; color:#e6eef5; margin-top:-6px; word-break:break-all; }
  aside .spacer { flex:1; }
  .motd { font-size:13px; color:#b8c7d3; background:#0c1621; border:1px solid #24405a;
    border-radius:6px; padding:8px 10px; white-space:pre-wrap; }
  .play { padding:18px 0; font-size:20px; }
  .path { font-size:12.5px; color:#b8c7d3; word-break:break-all; user-select:text; }
  .path.missing { color:#ff9b8f; }
</style>
<header>
  <h1>Dreadnought</h1><span class="sub">Server browser</span>
  <span class="spacer"></span>
  <span class="pill" id="status">Connecting…</span>
</header>
<main>
  <section class="view" id="clusters">
    <div class="panel wide">
      <div class="row"><h2 class="grow" style="margin:0">Servers</h2>
        <button class="link" onclick="refreshDir()">Refresh</button></div>
      <div class="row" style="margin-top:10px">
        <input id="dir-url" class="grow" placeholder="Directory address" spellcheck="false">
        <button class="link" onclick="setDirectory()">Use</button>
      </div>
      <p class="note">use: http://91.51.31.83:8091</p>
      <div id="list" style="margin-top:12px;max-height:32vh;overflow:auto"></div>
      <div class="msg" id="dirmsg"></div>
      <h2 style="margin-top:18px">Add a server by hand</h2>
      <p class="note">For servers that stay unlisted (opt-out). Ask its operator for the
        address and the <b>ca.crt</b> text — paste both, the rest is automatic.</p>
      <label for="m-name">Name</label>
      <input id="m-name" placeholder="A friend's server" maxlength="64">
      <label for="m-url">Address (https URL)</label>
      <input id="m-url" placeholder="https://203.0.113.7" spellcheck="false">
      <label for="m-ca">ca.crt contents</label>
      <textarea id="m-ca" placeholder="-----BEGIN CERTIFICATE-----…" spellcheck="false"></textarea>
      <button class="go" id="m-go" onclick="addManual()">Add server</button>
      <div class="msg" id="mmsg"></div>
    </div>
  </section>
  <section class="view" id="cert">
    <div class="panel">
      <h2>Trust this server?</h2>
      <p>First join on <b id="cert-server"></b>. Compare the fingerprint with the
        one its operator publishes — for directory servers it is shown next to
        the directory's own value.</p>
      <div class="fp" id="cert-fp"></div>
      <p class="note" id="cert-dir"></p>
      <p class="note" id="cert-known" hidden>Matches your earlier approval — only the Windows confirmation remains.</p>
      <p>Installing adds it to <b>your</b> Windows user's trusted certificates.
        No administrator rights are needed. Windows asks you to confirm; choose <b>Yes</b>.</p>
      <button class="go" id="cert-go" onclick="confirmCert()">Trust &amp; continue</button>
      <div class="msg" id="certmsg"></div>
      <button class="link" style="margin-top:8px" onclick="backToServers()">Back to servers</button>
    </div>
  </section>
  <section class="view" id="signin">
    <div class="panel">
      <h2 id="si-title">Sign in</h2>
      <div class="tabs" role="tablist">
        <button role="tab" id="tab-login" aria-selected="true" onclick="pick('login')">Sign in</button>
        <button role="tab" id="tab-register" aria-selected="false" onclick="pick('register')">Create account</button>
      </div>
      <form id="form" onsubmit="submitForm(event)">
        <div id="username-row" hidden>
          <label for="username">Callsign</label>
          <input id="username" autocomplete="username" maxlength="32">
        </div>
        <label for="identifier" id="identifier-label">Email or callsign</label>
        <input id="identifier" type="text" autocomplete="email">
        <label for="password">Password</label>
        <input id="password" type="password" autocomplete="current-password">
        <button class="go" id="go" type="submit">Sign in</button>
      </form>
      <div class="msg" id="msg"></div>
      <button class="link" style="margin-top:8px" onclick="backToServers()">Back to servers</button>
    </div>
  </section>
  <section class="view" id="home">
    <div class="news" id="news"></div>
    <aside>
      <span class="who" id="srvname">Server</span>
      <span class="name" id="name"></span>
      <div class="motd" id="srvmotd" hidden></div>
      <button class="link" onclick="signOut()">Sign out</button>
      <button class="link" onclick="backToServers()">Change server</button>
      <span class="who" style="margin-top:18px">Game folder</span>
      <span class="path" id="gamepath"></span>
      <button class="link" onclick="pickGame()">Change…</button>
      <div class="msg" id="gamemsg"></div>
      <span class="who" style="margin-top:10px">Options</span>
      <label class="opt"><input type="checkbox" id="opt-logWindow" onchange="setOpt('logWindow', this)">
        <span>Show the game's log window</span></label>
      <label class="opt"><input type="checkbox" id="opt-verboseLog" onchange="setOpt('verboseLog', this)">
        <span>Detailed log<small>For bug reports. Makes the log file much larger.</small></span></label>
      <button class="link" onclick="openLogs()">Open game log folder</button>
      <span class="spacer"></span>
      <div class="msg" id="playmsg"></div>
      <button class="link" id="recheck" hidden onclick="checkPresence()">Check again</button>
      <button class="go play" id="play" onclick="play()">Play</button>
    </aside>
  </section>
</main>
<script>
  let mode = 'login';
  let currentCluster = null;
  const $ = id => document.getElementById(id);
  function show(view) {
    for (const v of ['clusters', 'cert', 'signin', 'home']) $(v).classList.toggle('shown', v === view);
  }
  function say(el, text, kind) { $(el).textContent = text; $(el).className = 'msg ' + (kind || ''); }
  // ---- clusters ----
  async function refreshDir() {
    say('dirmsg', 'Contacting the directory…');
    dnRefreshDir();
  }
  function dnDirectoryResult(r) {
    if (r.directory && !$('dir-url').value) $('dir-url').value = r.directory;
    if (r.error && !(r.clusters || []).length && !(r.manual || []).length) { say('dirmsg', r.error, 'bad'); }
    else say('dirmsg', r.error || '');
    renderList(r);
  }
  async function setDirectory() {
    say('dirmsg', 'Checking…');
    dnSetDirectory($('dir-url').value);
  }
  function dnSetDirectoryResult(r) {
    if (!r.ok) { say('dirmsg', r.error, 'bad'); return; }
    refreshDir();
  }
  function renderList(r) {
    r = r || { clusters: [], manual: [] };
    const box = $('list');
    box.textContent = '';
    const s = $('status');
    const add = (el) => box.append(el);
    for (const c of (r.clusters || [])) {
      const el = document.createElement('div');
      el.className = 'srv';
      el.innerHTML = '<h3></h3><p></p><div class="motd"></div>';
      el.querySelector('h3').textContent = c.name;
      const n = document.createElement('span');
      n.className = 'n';
      n.textContent = c.players + ' playing · ' + c.servers + ' server(s)';
      el.querySelector('h3').append(n);
      el.querySelector('p').textContent = ((c.version || '') + ' · ' + c.battle_ip).trim();
      if (c.motd) el.querySelector('.motd').textContent = c.motd;
      el.onclick = () => selectCluster(c.id);
      add(el);
    }
    for (const m of (r.manual || [])) {
      const el = document.createElement('div');
      el.className = 'srv';
      el.innerHTML = '<h3></h3><p></p>';
      el.querySelector('h3').textContent = m.name + ' (manual)';
      el.querySelector('p').textContent = m.url;
      el.onclick = () => selectManual(m.url);
      const rm = document.createElement('button');
      rm.className = 'link'; rm.textContent = 'Forget';
      rm.onclick = (e) => { e.stopPropagation(); removeManual(m.url); };
      el.append(rm);
      add(el);
    }
    if (!box.children.length) {
      box.innerHTML = '<p class="note">No servers. Check the directory address, or add one by hand below.</p>';
    }
    if (s) { s.textContent = r.error ? 'Directory unreachable' : 'Directory online'; s.className = 'pill ' + (r.error ? 'off' : 'on'); }
  }
  async function selectCluster(id) {
    dnSelectCluster(id);
  }
  async function selectManual(url) {
    dnSelectManual(url);
  }
  function dnEnterCluster(r) {
    if (!r || !r.ok) { say('dirmsg', (r && r.error) || 'Failed.', 'bad'); return; }
    enterCluster(r);
  }
  function enterCluster(r) {
    currentCluster = (r && r.cluster) || null;
    if (r.cert && r.cert.state === 'pending') {
      $('cert-server').textContent = (r.cluster && r.cluster.name) || '';
      $('cert-fp').textContent = r.cert.fingerprint || '';
      $('cert-dir').textContent = r.cert.directory
        ? 'Matches the directory value — the listing vouches for it.'
        : (r.cert.dirfp ? 'DIFFERS from the directory value — stop unless you know why!' : '');
      $('cert-known').hidden = !r.cert.remembered;
      show('cert');
      return;
    }
    if (r.cert && r.cert.state === 'missing') {
      say('dirmsg', r.cert.error, 'bad');
      return;
    }
    afterCert(r);
  }
  async function confirmCert() {
    $('cert-go').disabled = true;
    say('certmsg', 'Waiting for Windows…');
    dnConfirmCert();
  }
  function dnConfirmCertResult(r) {
    $('cert-go').disabled = false;
    if (!r.ok) { say('certmsg', r.error, 'bad'); return; }
    say('certmsg', 'Installed.', 'good');
    setTimeout(() => afterCert(), 600);
  }
  function afterCert(r) {
    if (r && r.signedIn) home(r.username);
    else { pick('login'); show('signin'); }
  }
  async function addManual() {
    $('m-go').disabled = true;
    say('mmsg', 'Checking the server…');
    dnAddManual($('m-name').value, $('m-url').value, $('m-ca').value);
  }
  function dnAddManualResult(r) {
    $('m-go').disabled = false;
    if (!r.ok) { say('mmsg', r.error, 'bad'); return; }
    say('mmsg', 'Added.', 'good');
    enterCluster(r);
  }
  async function removeManual(url) {
    await dnRemoveManual(url);
    refreshDir();
  }
  function backToServers() { currentCluster = null; refreshDir(); show('clusters'); }
  // ---- sign-in ----
  function pick(next) {
    mode = next;
    $('tab-login').setAttribute('aria-selected', next === 'login');
    $('tab-register').setAttribute('aria-selected', next === 'register');
    $('username-row').hidden = next !== 'register';
    $('identifier-label').textContent = next === 'register' ? 'Email' : 'Email or callsign';
    $('go').textContent = next === 'register' ? 'Create account' : 'Sign in';
    say('msg', '');
  }
  function submitForm(e) {
    e.preventDefault();
    $('go').disabled = true;
    say('msg', 'Contacting the server…');
    dnSubmit(mode, $('username').value, $('identifier').value, $('password').value);
  }
  function dnAuthResult(r) {
    $('go').disabled = false;
    if (!r.ok) { say('msg', r.error || 'Sign-in failed.', 'bad'); return; }
    $('password').value = '';
    home(r.username);
  }
  function home(username) {
    $('name').textContent = username;
    const c = currentCluster || {};
    $('srvname').textContent = c.name || 'Server';
    const motd = $('srvmotd');
    const bits = [];
    if (c.version) bits.push(c.version);
    if (typeof c.players === 'number') bits.push(c.players + (c.players === 1 ? ' player' : ' players'));
    if (c.motd) bits.push(c.motd);
    motd.hidden = !bits.length;
    motd.textContent = bits.join(' · ');
    say('playmsg', '');
    $('recheck').hidden = true;
    $('play').disabled = false;
    show('home');
    // One account, one match: ask the directory whether this account is
    // already mid-match on another cluster before enabling Play.
    checkPresence();
    // News tiles belong to the selected cluster (transport is cluster-bound),
    // so they load here — not at page boot, when no cluster is selected yet.
    // This is also why the tiles were missing: the one boot-time fetch ran
    // against no cluster and never repeated.
    dnNews();
  }
  function signOut() { dnSignOut(); pick('login'); show('signin'); }
  function checkPresence() {
    say('playmsg', 'Checking whether this account is already in a match…');
    dnCheckPresence();
  }
  function dnPresenceResult(r) {
    r = r || {};
    if (r.checked && r.inMatch) {
      const where = r.cluster ? " on '" + r.cluster + "'" : ' on another server';
      say('playmsg', "You're already connected to a match" + where + '. ' +
        'Finish or leave it there first — one account can only be in one match at a time.', 'bad');
      $('play').disabled = true;
      $('recheck').hidden = false;
      return;
    }
    // Unknown (no directory, unreachable) lets the player through: a dead
    // directory must not strand anyone. The launch itself re-checks.
    say('playmsg', '');
    $('recheck').hidden = true;
    $('play').disabled = false;
  }
  function play() {
    $('play').disabled = true;
    say('playmsg', 'Launching Dreadnought…');
    dnPlay();
  }
  function dnPlayResult(r) {
    if (r.ok) { say('playmsg', 'Game started. Good hunting, Captain.', 'good'); return; }
    say('playmsg', r.error, 'bad');
    // A presence block keeps Play disabled (with "Check again" visible) so
    // the player cannot hammer launch while the other match runs.
    if (r.blocked) { $('play').disabled = true; $('recheck').hidden = false; return; }
    $('play').disabled = false;
    if (r.cert) show('clusters');
    if (r.game) $('gamepath').classList.add('missing');
    if (r.signIn) signOut();
  }
  function showGame(g) {
    g = g || {};
    $('gamepath').textContent = g.path || 'Not found. Choose the folder where Dreadnought is installed.';
    $('gamepath').classList.toggle('missing', !g.path);
  }
  async function pickGame() {
    say('gamemsg', '');
    const r = await dnPickGame();
    if (r.ok) { showGame(r.game); say('gamemsg', 'Saved.', 'good'); }
    else if (r.error) say('gamemsg', r.error, 'bad');
  }
  async function openLogs() {
    const r = await dnOpenLogs();
    say('gamemsg', r.ok ? 'Opened ' + r.path : r.error, r.ok ? 'good' : 'bad');
  }
  async function setOpt(name, box) {
    if (!(await dnSetOption(name, box.checked))) { box.checked = !box.checked; say('gamemsg', 'Could not save the option.', 'bad'); }
  }
  function dnNewsResult(r) {
    const s = $('status');
    s.textContent = r.online ? 'Server online' : 'Server unreachable';
    s.className = 'pill ' + (r.online ? 'on' : 'off');
    const news = $('news');
    news.textContent = '';
    for (const t of (r.tiles || [])) {
      const el = document.createElement('article');
      el.className = 'tile' + (t.section_size === 'full' ? ' full' : '');
      const h = document.createElement('h3'); h.textContent = t.title;
      const p = document.createElement('p'); p.textContent = t.body;
      el.append(h, p);
      news.append(el);
    }
  }
  (async () => {
    pick('login');
    show('clusters');
    refreshDir();
    const s = await dnInit();
    showGame(s.game);
    $('opt-logWindow').checked = !!s.logWindow;
    $('opt-verboseLog').checked = !!s.verboseLog;
    dnNews();
  })();
</script>
`
