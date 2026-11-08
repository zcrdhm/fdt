package main

const setupHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>FDT ATM 首次配置</title>
<style>
  :root { --bg:#0f1419; --card:#1a2332; --text:#e7ecf3; --muted:#8b9bb4; --acc:#3d8bfd; --ok:#3dd68c; --bad:#f07178; }
  * { box-sizing: border-box; }
  body { margin:0; font-family: "Segoe UI", system-ui, sans-serif; background:var(--bg); color:var(--text); }
  main { max-width: 720px; margin: 0 auto; padding: 28px 20px 48px; }
  h1 { font-size: 1.45rem; margin: 0 0 6px; }
  .sub { color: var(--muted); font-size: .92rem; margin-bottom: 22px; line-height: 1.5; }
  label { display:block; font-size:.85rem; color:var(--muted); margin: 14px 0 6px; }
  input, textarea, select { width:100%; padding:10px 12px; border-radius:8px; border:1px solid #2a3548; background:#121a24; color:var(--text); font-size:1rem; }
  textarea { min-height: 120px; font-family: ui-monospace, monospace; font-size:.82rem; }
  #importFieldsJSON { min-height: 160px; }
  .row { display:grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  @media (max-width: 640px) { .row { grid-template-columns: 1fr; } }
  button { margin-top: 22px; width:100%; padding: 12px 16px; border:0; border-radius:10px; background:var(--acc); color:#fff; font-size:1.05rem; font-weight:600; cursor:pointer; }
  button:disabled { opacity:.55; cursor:default; }
  .btn-row { display:flex; flex-wrap:wrap; gap:10px; margin-top:8px; }
  .btn-row button, .btn-row label.file-btn {
    margin-top:0; width:auto; flex:1; min-width:140px; padding:10px 12px; font-size:.92rem; font-weight:600;
    text-align:center;
  }
  .btn-row .ghost {
    background:#243044; color:var(--text);
  }
  .btn-row label.file-btn { display:inline-block; cursor:pointer; background:#243044; color:var(--text); border-radius:10px; }
  .btn-row input[type=file] { display:none; }
  .hint { font-size:.8rem; color:var(--muted); margin-top:6px; }
  .err { color:var(--bad); margin-top:12px; min-height:1.2em; }
  .ok { color:var(--ok); margin-top:12px; }
  code { background:#121a24; padding:2px 6px; border-radius:4px; }
  .check { display:flex; align-items:center; gap:8px; margin-top:10px; color:var(--muted); font-size:.9rem; }
  .check input { width:auto; }
</style>
</head>
<body>
<main>
  <h1>FDT ATM 首次配置</h1>
  <p class="sub">配置保存在当前用户目录 <code id="home"></code>，<strong>无需管理员/root</strong>。<br/>
  Portal 请填数据口（默认 <code>:8081</code>），不是网页口 :8080。<br/>
  保存成功后请<strong>关闭本窗口</strong>，程序会自动进入 ATM。</p>
  <p class="hint" style="margin-top:-8px;margin-bottom:18px">FDT 1.0.1 · © fasteda.cn</p>

  <label>Portal 数据口地址</label>
  <input id="portalBase" placeholder="http://192.168.0.135:8081" autocomplete="off"/>
  <div class="hint">将自动生成 …/data 与 …/import</div>

  <div class="row">
    <div>
      <label>设备 ID</label>
      <input id="deviceId" placeholder="atm-01"/>
    </div>
    <div>
      <label>设备密钥 DEVICE_SECRET</label>
      <input id="deviceSecret" placeholder="与内网 Portal 一致" autocomplete="off"/>
    </div>
  </div>

  <label>pk_gateway.pem（粘贴全文）</label>
  <textarea id="gatewayPub" placeholder="-----BEGIN FDT ED25519 PUBLIC KEY-----&#10;…&#10;-----END FDT ED25519 PUBLIC KEY-----"></textarea>

  <label>初始维护密码</label>
  <input id="maintPass" type="password" placeholder="至少 4 位" autocomplete="new-password"/>
  <div class="hint">进入 ATM 后可在「维护模式」修改密码（哈希存储）</div>

  <div class="row">
    <div>
      <label>U 盘检测</label>
      <select id="usb">
        <option value="windows">windows（Windows 真 U 盘）</option>
        <option value="sysfs">sysfs（Linux，通常需 root）</option>
        <option value="mock">mock（目录模拟，适合无权限联调）</option>
      </select>
    </div>
    <div>
      <label>mock 目录（可选）</label>
      <input id="usbDir" placeholder="留空则用用户目录下 var/usb"/>
    </div>
  </div>

  <label>导入申请字段 import-fields.json</label>
  <div class="hint">可浏览已有 JSON；不选或勾选「使用默认」则写入内置默认字段（事由多行 + 密级）。</div>
  <div class="btn-row">
    <label class="file-btn">浏览 JSON 文件
      <input id="importFieldsFile" type="file" accept=".json,application/json,text/json"/>
    </label>
    <button type="button" class="ghost" id="btnResetFields">恢复默认内容</button>
  </div>
  <label class="check"><input id="useDefaultFields" type="checkbox"/> 使用内置默认字段（忽略下方编辑内容）</label>
  <textarea id="importFieldsJSON" spellcheck="false" placeholder="{ &#10;  &quot;version&quot;: 1,&#10;  &quot;fields&quot;: [ … ]&#10;}"></textarea>
  <div class="hint" id="fieldsFileName"></div>

  <button id="save" type="button">保存并继续</button>
  <div class="err" id="err"></div>
  <div class="ok hidden" id="ok"></div>
</main>
<script>
let defaultFieldsJSON = '';
function syncFieldsDisabled() {
  const useDef = document.getElementById('useDefaultFields').checked;
  document.getElementById('importFieldsJSON').disabled = useDef;
  document.getElementById('importFieldsFile').disabled = useDef;
  document.getElementById('btnResetFields').disabled = useDef;
}
async function load() {
  const r = await fetch('/api/setup');
  const j = await r.json();
  document.getElementById('home').textContent = j.home || '';
  document.getElementById('portalBase').value = j.portalBase || '';
  document.getElementById('deviceId').value = j.deviceId || 'atm-01';
  document.getElementById('deviceSecret').value = j.deviceSecret || '';
  document.getElementById('gatewayPub').value = j.gatewayPub || '';
  document.getElementById('usb').value = j.usb || 'mock';
  document.getElementById('usbDir').value = j.usbDir || '';
  defaultFieldsJSON = (j.importFieldsDefault || '').trim();
  const cur = (j.importFieldsJSON || '').trim() || defaultFieldsJSON;
  document.getElementById('importFieldsJSON').value = cur;
  // First-run with only defaults → check "use default" for clarity; existing custom stays editable.
  const isDefault = !j.importFieldsJSON || cur.replace(/\s+/g,'') === defaultFieldsJSON.replace(/\s+/g,'');
  document.getElementById('useDefaultFields').checked = isDefault && !(j.importFieldsJSON || '').trim();
  syncFieldsDisabled();
}
document.getElementById('useDefaultFields').onchange = syncFieldsDisabled;
document.getElementById('btnResetFields').onclick = () => {
  document.getElementById('importFieldsJSON').value = defaultFieldsJSON;
  document.getElementById('fieldsFileName').textContent = '已填入内置默认';
  document.getElementById('useDefaultFields').checked = false;
  syncFieldsDisabled();
};
document.getElementById('importFieldsFile').onchange = async (ev) => {
  const err = document.getElementById('err');
  err.textContent = '';
  const f = ev.target.files && ev.target.files[0];
  if (!f) return;
  try {
    const text = await f.text();
    JSON.parse(text); // quick sanity
    document.getElementById('importFieldsJSON').value = text.trim();
    document.getElementById('fieldsFileName').textContent = '已加载：' + f.name;
    document.getElementById('useDefaultFields').checked = false;
    syncFieldsDisabled();
  } catch (e) {
    err.textContent = '无法读取 JSON：' + (e.message || e);
  }
};
document.getElementById('save').onclick = async () => {
  const err = document.getElementById('err');
  const ok = document.getElementById('ok');
  err.textContent = ''; ok.textContent = ''; ok.classList.add('hidden');
  const btn = document.getElementById('save');
  btn.disabled = true;
  const useDefaultFields = document.getElementById('useDefaultFields').checked;
  let importFieldsJSON = document.getElementById('importFieldsJSON').value;
  if (!useDefaultFields) {
    const raw = importFieldsJSON.trim();
    if (!raw) {
      // empty editor → treat as default
      importFieldsJSON = '';
    } else {
      try { JSON.parse(raw); } catch (e) {
        err.textContent = 'import-fields.json 不是合法 JSON：' + (e.message || e);
        btn.disabled = false;
        return;
      }
      importFieldsJSON = raw;
    }
  }
  try {
    const body = {
      portalBase: document.getElementById('portalBase').value.trim(),
      deviceId: document.getElementById('deviceId').value.trim(),
      deviceSecret: document.getElementById('deviceSecret').value.trim(),
      gatewayPub: document.getElementById('gatewayPub').value,
      maintPass: document.getElementById('maintPass').value,
      usb: document.getElementById('usb').value,
      usbDir: document.getElementById('usbDir').value.trim(),
      importFieldsJSON: useDefaultFields ? '' : importFieldsJSON,
      useDefaultFields: useDefaultFields || !String(importFieldsJSON || '').trim()
    };
    const r = await fetch('/api/setup', { method:'POST', headers:{'content-type':'application/json'}, body: JSON.stringify(body) });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || ('HTTP '+r.status));
    ok.textContent = '已保存到 ' + (j.envPath||'') + '。请关闭本窗口以启动 ATM。';
    ok.classList.remove('hidden');
  } catch (e) {
    err.textContent = e.message || String(e);
    btn.disabled = false;
  }
};
load().catch(e => { document.getElementById('err').textContent = e.message; });
</script>
</body>
</html>
`