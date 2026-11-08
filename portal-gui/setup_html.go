package main

const setupHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>FDT Portal 首次配置</title>
<style>
  :root { --bg:#0f1419; --text:#e7ecf3; --muted:#8b9bb4; --acc:#3d8bfd; --ok:#3dd68c; --bad:#f07178; --line:#2a3548; }
  * { box-sizing: border-box; }
  body { margin:0; font-family:"Segoe UI",system-ui,sans-serif; background:var(--bg); color:var(--text); }
  main { max-width:860px; margin:0 auto; padding:28px 20px 64px; }
  h1 { font-size:1.45rem; margin:0 0 6px; }
  h2 { font-size:1.05rem; margin:28px 0 10px; padding-bottom:6px; border-bottom:1px solid var(--line); color:#c5d0de; }
  .sub { color:var(--muted); font-size:.92rem; margin-bottom:18px; line-height:1.5; }
  label { display:block; font-size:.85rem; color:var(--muted); margin:12px 0 6px; }
  input, textarea, select { width:100%; padding:10px 12px; border-radius:8px; border:1px solid var(--line); background:#121a24; color:var(--text); font-size:1rem; }
  textarea { min-height:110px; font-family:ui-monospace,monospace; font-size:.8rem; }
  .row { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
  @media (max-width:700px){ .row { grid-template-columns:1fr; } }
  .hint { font-size:.8rem; color:var(--muted); margin-top:6px; line-height:1.45; }
  .err { color:var(--bad); margin-top:12px; min-height:1.2em; white-space:pre-wrap; }
  .ok { color:var(--ok); margin-top:12px; white-space:pre-wrap; }
  code { background:#121a24; padding:2px 6px; border-radius:4px; }
  .check { display:flex; align-items:center; gap:8px; margin-top:10px; color:var(--muted); font-size:.9rem; }
  .check input { width:auto; }
  .perm-ok { color:var(--ok); }
  .perm-bad { color:var(--bad); }
  .btn-row { display:flex; flex-wrap:wrap; gap:10px; margin-top:8px; }
  .btn-row button, .btn-row label.file-btn {
    margin:0; width:auto; flex:1; min-width:130px; padding:10px 12px; font-size:.9rem; font-weight:600;
    text-align:center; border:0; border-radius:10px; cursor:pointer; background:#243044; color:var(--text);
  }
  .btn-row input[type=file] { display:none; }
  .btn-row label.file-btn { display:inline-block; }
  button#save { margin-top:24px; width:100%; padding:14px 16px; border:0; border-radius:10px; background:var(--acc); color:#fff; font-size:1.05rem; font-weight:650; cursor:pointer; }
  button#save:disabled { opacity:.55; cursor:default; }
  .pill { display:inline-block; font-size:.75rem; padding:2px 8px; border-radius:999px; background:#1c2838; color:var(--muted); margin-left:6px; }
</style>
</head>
<body>
<main>
  <h1>FDT Portal 首次配置</h1>
  <p class="sub">配置写入当前用户目录 <code id="home"></code>，一般<strong>无需管理员</strong>。<br/>
  保存并关闭本窗口后，程序会<strong>安装为系统服务</strong>（后台常驻）并打开浏览器。也可 <code>-setup</code> / <code>-setup-cli</code> 重配，<code>-foreground</code> 前台跑。</p>
  <p class="hint" style="margin-top:-8px;margin-bottom:18px">FDT 1.0.1 · © fasteda.cn</p>

  <h2>网络与访问</h2>
  <div class="row">
    <div>
      <label>Portal UI 监听 LISTEN</label>
      <input id="listen" placeholder="0.0.0.0:8080"/>
    </div>
    <div>
      <label>ATM 数据口 DATA_LISTEN</label>
      <input id="dataListen" placeholder="0.0.0.0:8081"/>
      <div class="hint">仅 /data /import，勿与 UI 同端口</div>
    </div>
  </div>
  <div class="row">
    <div>
      <label>域名 / 主机名 DOMAIN</label>
      <input id="domain" placeholder="portal.corp.local 或 192.168.0.10"/>
    </div>
    <div>
      <label>对外访问根地址 PUBLIC_BASE（可选）</label>
      <input id="publicBase" placeholder="https://portal.example.com"/>
      <div class="hint">空则按 DOMAIN + 端口推导</div>
    </div>
  </div>
  <div class="row">
    <div>
      <label>租户 ID TENANT</label>
      <input id="tenant" placeholder="default"/>
    </div>
    <div>
      <label class="check" style="margin-top:28px"><input id="https" type="checkbox"/> 启用 HTTPS（TLS=1）</label>
    </div>
  </div>
  <label>TLS 证书 server.pem（可选粘贴；启用 HTTPS 时需要）</label>
  <textarea id="tlsCert" placeholder="-----BEGIN CERTIFICATE-----"></textarea>
  <label>TLS 私钥 server.key</label>
  <textarea id="tlsKey" placeholder="-----BEGIN PRIVATE KEY-----"></textarea>

  <h2>存储位置</h2>
  <div class="row">
    <div>
      <label>数据目录 DATA_DIR</label>
      <input id="dataDir" placeholder="留空 → 用户目录/var"/>
      <div class="btn-row">
        <button type="button" class="ghost" id="btnPickData">浏览…</button>
        <button type="button" class="ghost" id="btnTestData">权限测试</button>
      </div>
      <div class="hint" id="dataPermHint">含 SQLite、staging/quarantine/released/uploads、feishu_bindings.json</div>
    </div>
    <div>
      <label>密钥目录 KEYS_DIR</label>
      <input id="keysDir" placeholder="留空 → 用户目录/keys"/>
      <div class="btn-row">
        <button type="button" class="ghost" id="btnPickKeys">浏览…</button>
        <button type="button" class="ghost" id="btnTestKeys">权限测试</button>
      </div>
      <div class="hint" id="keysPermHint">存放 sk/pk_internal、sk/pk_gateway、可选 TLS 证书</div>
    </div>
  </div>

  <h2>管理与身份</h2>
  <div class="row">
    <div>
      <label>管理员用户</label>
      <input id="adminUser" placeholder="admin"/>
    </div>
    <div>
      <label>管理员密码</label>
      <input id="adminPass" type="password" placeholder="至少 4 位" autocomplete="new-password"/>
    </div>
  </div>
  <label>额外管理员账号（目录账户，逗号分隔）</label>
  <input id="adminUsers" placeholder="alice,bob"/>
  <div class="row">
    <div>
      <label>身份源 IDENTITY</label>
      <select id="identity">
        <option value="mock">mock（内置样例，联调）</option>
        <option value="ldap">ldap（AD，管理页可配）</option>
        <option value="pam">pam（本机 PAM）</option>
        <option value="none">none（请求体自填，仅开发）</option>
      </select>
    </div>
    <div>
      <label>ClamAV CLAMD（可选）</label>
      <input id="clamd" placeholder="unix:///var/run/clamd.sock 或 host:3310"/>
    </div>
  </div>

  <h2>ATM 设备（可选预注册）</h2>
  <div class="row">
    <div>
      <label>DEVICE_ID</label>
      <input id="deviceId" placeholder="atm-01"/>
    </div>
    <div>
      <label>DEVICE_SECRET</label>
      <input id="deviceSecret" placeholder="与 ATM 一致" autocomplete="off"/>
    </div>
  </div>

  <h2>密钥 <span class="pill" id="keyStatus">尚未生成</span></h2>
  <div class="btn-row">
    <button type="button" id="btnDownloadPK">生成并下载 pk_internal.pem</button>
  </div>
  <div class="hint" id="pkDownloadHint">尚未生成：点击上方按钮生成密钥并下载公钥。</div>

  <h2>表单字段 · 导出（Portal 申请）</h2>
  <div class="hint">写入 <code>form-fields.json</code>。可浏览 JSON，或勾选使用默认（含事由/密级及系统字段）。保存后亦可在 Portal 管理页继续编辑。</div>
  <div class="btn-row">
    <label class="file-btn">浏览导出字段 JSON<input id="formFile" type="file" accept=".json,application/json"/></label>
    <button type="button" id="btnFormDefault">恢复默认</button>
  </div>
  <label class="check"><input id="useFormDefault" type="checkbox"/> 使用内置默认导出字段</label>
  <textarea id="formFieldsJSON"></textarea>

  <h2>表单字段 · 导入（ATM 模板）</h2>
  <div class="hint">写入本机 <code>import-fields.json</code> 模板，可拷到 ATM 的同名文件。ATM GUI 也可单独配置。</div>
  <div class="btn-row">
    <label class="file-btn">浏览导入字段 JSON<input id="impFile" type="file" accept=".json,application/json"/></label>
    <button type="button" id="btnImpDefault">恢复默认</button>
  </div>
  <label class="check"><input id="useImpDefault" type="checkbox"/> 使用内置默认导入字段</label>
  <textarea id="importFieldsJSON"></textarea>

  <button id="save" type="button">保存并继续</button>
  <div class="err" id="err"></div>
  <div class="ok" id="ok" hidden></div>
</main>
<script>
let formDefault = '', impDefault = '';
function syncDisable(checkId, areaId, fileId, btnId) {
  const on = document.getElementById(checkId).checked;
  document.getElementById(areaId).disabled = on;
  if (fileId) document.getElementById(fileId).disabled = on;
  if (btnId) document.getElementById(btnId).disabled = on;
}
function bindFile(inputId, areaId, checkId) {
  document.getElementById(inputId).onchange = async (ev) => {
    const err = document.getElementById('err'); err.textContent = '';
    const f = ev.target.files && ev.target.files[0]; if (!f) return;
    try {
      const t = await f.text(); JSON.parse(t);
      document.getElementById(areaId).value = t.trim();
      document.getElementById(checkId).checked = false;
      syncDisable(checkId, areaId, inputId, null);
    } catch (e) { err.textContent = 'JSON 无效: ' + (e.message||e); }
  };
}
async function load() {
  const r = await fetch('/api/setup'); const j = await r.json();
  document.getElementById('home').textContent = j.home || '';
  document.getElementById('listen').value = j.listen || '0.0.0.0:8080';
  document.getElementById('dataListen').value = j.dataListen || '0.0.0.0:8081';
  document.getElementById('domain').value = j.domain || '';
  document.getElementById('publicBase').value = j.publicBase || '';
  document.getElementById('tenant').value = j.tenant || 'default';
  document.getElementById('https').checked = !!j.https;
  document.getElementById('dataDir').value = j.dataDir || '';
  document.getElementById('keysDir').value = j.keysDir || '';
  document.getElementById('adminUser').value = j.adminUser || 'admin';
  document.getElementById('adminPass').value = j.adminPass || '';
  document.getElementById('adminUsers').value = j.adminUsers || '';
  document.getElementById('identity').value = j.identity || 'mock';
  document.getElementById('deviceId').value = j.deviceId || 'atm-01';
  document.getElementById('deviceSecret').value = j.deviceSecret || '';
  document.getElementById('clamd').value = j.clamd || '';
  formDefault = (j.formFieldsDefault||'').trim();
  impDefault = (j.importFieldsDefault||'').trim();
  document.getElementById('formFieldsJSON').value = (j.formFieldsJSON||'').trim() || formDefault;
  document.getElementById('importFieldsJSON').value = (j.importFieldsJSON||'').trim() || impDefault;
  const noCustomForm = !(j.formFieldsJSON||'').trim();
  const noCustomImp = !(j.importFieldsJSON||'').trim();
  document.getElementById('useFormDefault').checked = noCustomForm;
  document.getElementById('useImpDefault').checked = noCustomImp;
  updateKeyUI(!!j.hasInternalKey);
  syncDisable('useFormDefault','formFieldsJSON','formFile','btnFormDefault');
  syncDisable('useImpDefault','importFieldsJSON','impFile','btnImpDefault');
}
let hasInternalKey = false;
function keysDirValue() {
  const v = document.getElementById('keysDir').value.trim();
  return v;
}
function pkPathHint() {
  const kd = keysDirValue();
  if (!kd) return '';
  const sep = kd.indexOf('\\') >= 0 ? '\\' : '/';
  return kd.replace(/[\\\/]+$/, '') + sep + 'pk_internal.pem';
}
function updateKeyUI(on) {
  hasInternalKey = !!on;
  const btn = document.getElementById('btnDownloadPK');
  const hint = document.getElementById('pkDownloadHint');
  document.getElementById('keyStatus').textContent = on ? '已就绪（保存时不覆盖）' : '尚未生成';
  btn.textContent = on ? '下载 pk_internal.pem' : '生成并下载 pk_internal.pem';
  btn.title = on ? '下载已有公钥' : '立即生成密钥对并下载公钥；Portal 将使用这份密钥';
  btn.disabled = false;
  if (hint) {
    const p = pkPathHint();
    hint.textContent = on
      ? (p ? ('密钥已在：' + p + '。保存配置会沿用，不会覆盖。') : '密钥已在 KEYS_DIR。保存配置会沿用，不会覆盖。')
      : '尚未生成：点上方按钮立即生成并下载；生成后保存配置不会再覆盖。';
    hint.className = on ? 'hint perm-ok' : 'hint';
  }
}
document.getElementById('useFormDefault').onchange = () => syncDisable('useFormDefault','formFieldsJSON','formFile','btnFormDefault');
document.getElementById('useImpDefault').onchange = () => syncDisable('useImpDefault','importFieldsJSON','impFile','btnImpDefault');
document.getElementById('btnFormDefault').onclick = () => {
  document.getElementById('formFieldsJSON').value = formDefault;
  document.getElementById('useFormDefault').checked = false;
  syncDisable('useFormDefault','formFieldsJSON','formFile','btnFormDefault');
};
document.getElementById('btnImpDefault').onclick = () => {
  document.getElementById('importFieldsJSON').value = impDefault;
  document.getElementById('useImpDefault').checked = false;
  syncDisable('useImpDefault','importFieldsJSON','impFile','btnImpDefault');
};
bindFile('formFile','formFieldsJSON','useFormDefault');
bindFile('impFile','importFieldsJSON','useImpDefault');
document.getElementById('btnDownloadPK').onclick = async () => {
  const err = document.getElementById('err'); err.textContent = '';
  const ok = document.getElementById('ok');
  if (ok) { ok.hidden = true; ok.textContent = ''; }
  const keysDir = keysDirValue();
  let pkPath = '';
  try {
    if (!hasInternalKey) {
      const r = await fetch('/api/setup/generate-keys', {
        method: 'POST', headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ keysDir, force: false })
      });
      const j = await r.json();
      if (!r.ok) throw new Error(j.error || ('HTTP ' + r.status));
      if (j.keysDir && !keysDir) document.getElementById('keysDir').value = j.keysDir;
      if (j.pkInternal) pkPath = j.pkInternal;
      updateKeyUI(true);
    }
    const q = keysDirValue() ? ('?keysDir=' + encodeURIComponent(keysDirValue())) : '';
    const r2 = await fetch('/api/setup/pk-internal' + q);
    if (!r2.ok) {
      const t = await r2.text();
      throw new Error(t || ('HTTP ' + r2.status));
    }
    const hdrPath = r2.headers.get('X-PK-Internal-Path') || '';
    const hdrKeys = r2.headers.get('X-Keys-Dir') || '';
    if (hdrKeys && !keysDirValue()) document.getElementById('keysDir').value = hdrKeys;
    if (hdrPath) pkPath = hdrPath;
    if (!pkPath) pkPath = pkPathHint();
    const blob = await r2.blob();
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'pk_internal.pem';
    document.body.appendChild(a); a.click(); a.remove();
    URL.revokeObjectURL(a.href);
    const msg = pkPath
      ? ('已下载。服务端密钥：' + pkPath + '\n浏览器另存了一份 pk_internal.pem（见系统「下载」文件夹）。请把该公钥交给网关。')
      : '已下载 pk_internal.pem（见系统「下载」文件夹）。保存配置会沿用 KEYS_DIR 中的密钥。';
    const hint = document.getElementById('pkDownloadHint');
    if (hint) {
      hint.className = 'hint perm-ok';
      hint.textContent = msg.replace(/\n/g, ' ');
    }
    if (ok) {
      ok.hidden = false;
      ok.textContent = msg;
    }
  } catch (e) {
    err.textContent = '生成/下载失败：' + (e.message || e);
  }
};

async function pickNativeDir(kind) {
  const err = document.getElementById('err'); err.textContent = '';
  const inputId = kind === 'keys' ? 'keysDir' : 'dataDir';
  const start = document.getElementById(inputId).value.trim();
  try {
    const r = await fetch('/api/setup/pick-dir', {
      method:'POST', headers:{'content-type':'application/json'},
      body: JSON.stringify({ kind, start })
    });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || ('HTTP '+r.status));
    if (j.cancelled || !j.path) return;
    document.getElementById(inputId).value = j.path;
    await testPerm(kind, false);
  } catch (e) {
    err.textContent = e.message || String(e);
  }
}
async function testPerm(kind, alertOk) {
  const inputId = kind === 'keys' ? 'keysDir' : 'dataDir';
  const hintId = kind === 'keys' ? 'keysPermHint' : 'dataPermHint';
  const hint = document.getElementById(hintId);
  const path = document.getElementById(inputId).value.trim();
  hint.className = 'hint';
  hint.textContent = '测试中…';
  try {
    const r = await fetch('/api/setup/test-perm', {
      method:'POST', headers:{'content-type':'application/json'},
      body: JSON.stringify({ kind, path })
    });
    const j = await r.json();
    if (j.path) document.getElementById(inputId).value = j.path;
    if (j.ok) {
      hint.className = 'hint perm-ok';
      hint.textContent = '✓ ' + (j.message || '读写正常') + ' → ' + (j.path || path);
    } else {
      hint.className = 'hint perm-bad';
      hint.textContent = '✗ ' + (j.message || '权限不足') + (j.path ? (' → ' + j.path) : '');
    }
  } catch (e) {
    hint.className = 'hint perm-bad';
    hint.textContent = '✗ ' + (e.message || e);
  }
}
document.getElementById('btnPickData').onclick = () => pickNativeDir('data');
document.getElementById('btnPickKeys').onclick = () => pickNativeDir('keys');
document.getElementById('btnTestData').onclick = () => testPerm('data', true);
document.getElementById('btnTestKeys').onclick = () => testPerm('keys', true);

document.getElementById('save').onclick = async () => {
  const err = document.getElementById('err'); const ok = document.getElementById('ok');
  err.textContent = ''; ok.hidden = true; ok.textContent = '';
  const btn = document.getElementById('save'); btn.disabled = true;
  const useFormDef = document.getElementById('useFormDefault').checked;
  const useImpDef = document.getElementById('useImpDefault').checked;
  let formJSON = document.getElementById('formFieldsJSON').value.trim();
  let impJSON = document.getElementById('importFieldsJSON').value.trim();
  try {
    if (!useFormDef && formJSON) JSON.parse(formJSON);
    if (!useImpDef && impJSON) JSON.parse(impJSON);
  } catch (e) {
    err.textContent = '字段 JSON 无效: ' + (e.message||e); btn.disabled = false; return;
  }
  const body = {
    listen: document.getElementById('listen').value.trim(),
    dataListen: document.getElementById('dataListen').value.trim(),
    domain: document.getElementById('domain').value.trim(),
    publicBase: document.getElementById('publicBase').value.trim(),
    tenant: document.getElementById('tenant').value.trim(),
    https: document.getElementById('https').checked,
    tlsCertPEM: document.getElementById('tlsCert').value,
    tlsKeyPEM: document.getElementById('tlsKey').value,
    dataDir: document.getElementById('dataDir').value.trim(),
    keysDir: document.getElementById('keysDir').value.trim(),
    adminUser: document.getElementById('adminUser').value.trim(),
    adminPass: document.getElementById('adminPass').value,
    adminUsers: document.getElementById('adminUsers').value.trim(),
    identity: document.getElementById('identity').value,
    deviceId: document.getElementById('deviceId').value.trim(),
    deviceSecret: document.getElementById('deviceSecret').value.trim(),
    clamd: document.getElementById('clamd').value.trim(),
    formFieldsJSON: useFormDef ? '' : formJSON,
    importFieldsJSON: useImpDef ? '' : impJSON,
    genKeys: false,
    exportPKInternal: ''
  };
  try {
    const r = await fetch('/api/setup', { method:'POST', headers:{'content-type':'application/json'}, body: JSON.stringify(body) });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || ('HTTP '+r.status));
    ok.hidden = false;
    ok.textContent = '已保存到 ' + (j.envPath||'') + '\n公钥: ' + (j.pkInternal||'') + '\n请关闭本窗口：将安装后台服务并启动 Portal。';
  } catch (e) {
    err.textContent = e.message || String(e); btn.disabled = false;
  }
};
load().catch(e => { document.getElementById('err').textContent = e.message; });
</script>
</body>
</html>
`
