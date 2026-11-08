(function () {
  const $ = (id) => document.getElementById(id);
  let me = { authenticated: false };

  async function api(path, opts) {
    const o = Object.assign({ credentials: 'same-origin', headers: {} }, opts || {});
    if (o.body && typeof o.body === 'string' && !o.headers['content-type']) {
      o.headers['content-type'] = 'application/json';
    }
    o.headers['X-Requested-With'] = 'XMLHttpRequest';
    o.headers.Accept = 'application/json';
    const r = await fetch(path, o);
    const text = await r.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = text; }
    if (!r.ok) throw new Error(typeof data === 'string' ? data : (data && data.error) || r.statusText);
    return data;
  }

  function esc(s) {
    return String(s || '').replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  function applyVersionFooter(data) {
    const ver = (data && data.version) || '1.0.1';
    const copy = (data && data.copyright) || '© fasteda.cn';
    const text = 'FDT ' + ver + ' · ' + copy;
    ['login-footer', 'app-footer'].forEach((id) => {
      const el = $(id);
      if (el) el.textContent = text;
    });
  }

  function showLogin() {
    applyVersionFooter(me);
    $('view-login').classList.remove('hidden');
    $('view-app').classList.add('hidden');
  }

  const PANELS = ['imports', 'history', 'fields', 'identity', 'password'];

  function panelFromHash() {
    let h = (location.hash || '').replace(/^#\/?/, '');
    if (h === 'ldap') h = 'identity'; // 旧书签兼容
    return PANELS.indexOf(h) >= 0 ? h : 'imports';
  }

  function showApp() {
    $('view-login').classList.add('hidden');
    $('view-app').classList.remove('hidden');
    applyVersionFooter(me);
    $('who-line').textContent = (me.name || '本地管理员') + ' · ' + (me.account || 'admin');
    $('who-sub').textContent = '本地管理员会话 · 可查看全部记录并配置身份认证';
    $('mode-badge').textContent = me.identityMode || 'admin';
    showPanel(panelFromHash());
  }

  function showPanel(name, opts) {
    if (name === 'ldap') name = 'identity';
    if (PANELS.indexOf(name) < 0) name = 'imports';
    PANELS.forEach((n) => {
      const panel = $('panel-' + n);
      if (panel) panel.classList.toggle('hidden', n !== name);
      const btn = $('btn-tab-' + n);
      if (btn) btn.classList.toggle('on', n === name);
    });
    if (!(opts && opts.skipHash) && location.hash !== '#' + name) {
      history.replaceState(null, '', '#' + name);
    }
    if (name === 'imports') loadImports();
    if (name === 'history') loadHistory();
    if (name === 'fields') loadFields();
    if (name === 'identity') loadIdentity();
    if (name === 'password') {
      $('pwd-err').textContent = '';
      $('pwd-ok').textContent = '';
      $('pwd-old').value = '';
      $('pwd-new').value = '';
      $('pwd-new2').value = '';
      if (me.adminUser) $('pwd-admin-user').textContent = me.adminUser;
    }
  }

  let formState = { version: 1, fields: [] };

  function loadFields() {
    $('fields-err').textContent = '';
    $('fields-ok').textContent = '';
    api('/api/settings/form-fields').then((d) => {
      formState = d.form || { version: 1, fields: [] };
      renderFields();
      $('feishu-preview').textContent = JSON.stringify(d.feishuExport || [], null, 2);
    }).catch((e) => {
      $('fields-err').textContent = e.message || String(e);
    });
  }

  function canDeleteField(f) {
    // reason / cls 是签发 QR#1 必需字段，不可删；其余均可删（含系统映射字段）。
    return f.key !== 'reason' && f.key !== 'cls';
  }

  function deriveFeishu(key, typ) {
    const field = key === 'cls' ? 'class' : key;
    let feishuType = 'input';
    if (typ === 'textarea') feishuType = 'textarea';
    else if (typ === 'number') feishuType = 'number';
    return { feishuField: field, feishuType: feishuType, feishuId: field };
  }

  function renderFields() {
    const body = $('fields-body');
    const indexed = (formState.fields || []).map((f, i) => ({ f, i }));
    indexed.sort((a, b) => (a.f.order || 0) - (b.f.order || 0));
    body.innerHTML = indexed.map(({ f, i }) => {
      const keyLocked = f.key === 'reason' || f.key === 'cls';
      const optVal = (f.options && f.options.length ? f.options.join(',') : '') +
        (f.default ? ((f.options && f.options.length ? ' | ' : '') + f.default) : '');
      return '<tr data-i="' + i + '" data-key="' + esc(f.key || '') + '">' +
        '<td><input type="checkbox" data-k="enabled"' + (f.enabled ? ' checked' : '') + '></td>' +
        '<td><input type="number" data-k="order" value="' + (f.order || 0) + '" style="width:64px"></td>' +
        '<td><input class="k-key" data-k="key" value="' + esc(f.key || '') + '"' + (keyLocked ? ' readonly' : '') + '></td>' +
        '<td><input data-k="label" value="' + esc(f.label || '') + '"></td>' +
        '<td><select data-k="type">' +
          ['input', 'textarea', 'select', 'number'].map((t) =>
            '<option value="' + t + '"' + (f.type === t ? ' selected' : '') + '>' + t + '</option>').join('') +
        '</select></td>' +
        '<td><select data-k="source">' +
          '<option value="user"' + (f.source !== 'system' ? ' selected' : '') + '>user</option>' +
          '<option value="system"' + (f.source === 'system' ? ' selected' : '') + '>system</option>' +
        '</select></td>' +
        '<td><input type="checkbox" data-k="required"' + (f.required ? ' checked' : '') + '></td>' +
        '<td><input data-k="options" placeholder="选项a,b | 默认" value="' + esc(optVal) +
          '" title="select 选项用逗号；可用 | 后写默认值"></td>' +
        '<td>' + (canDeleteField(f)
          ? '<button type="button" class="btn-x" data-del="1" title="删除该字段">删除</button>'
          : '<span class="hint-line">保留</span>') + '</td>' +
        '</tr>';
    }).join('');
  }

  function collectFields() {
    const rows = Array.from($('fields-body').querySelectorAll('tr'));
    const out = [];
    rows.forEach((tr) => {
      const get = (k) => tr.querySelector('[data-k="' + k + '"]');
      const keyEl = get('key');
      if (!keyEl) return;
      const optRaw = (get('options').value || '').trim();
      let options = [];
      let def = '';
      if (optRaw.includes('|')) {
        const parts = optRaw.split('|');
        options = parts[0].split(',').map((s) => s.trim()).filter(Boolean);
        def = (parts[1] || '').trim();
      } else if (optRaw) {
        if (get('type').value === 'select') {
          options = optRaw.split(',').map((s) => s.trim()).filter(Boolean);
        } else {
          def = optRaw;
        }
      }
      const prev = formState.fields[Number(tr.getAttribute('data-i'))] || {};
      const key = keyEl.value.trim();
      const typ = get('type').value;
      const derived = deriveFeishu(key, typ);
      out.push({
        key: key,
        label: get('label').value.trim(),
        type: typ,
        source: get('source').value,
        required: get('required').checked,
        enabled: get('enabled').checked,
        order: Number(get('order').value) || 0,
        options: options,
        default: def,
        maxLen: prev.maxLen || (typ === 'textarea' ? 200 : 64),
        feishuField: derived.feishuField,
        feishuType: derived.feishuType,
        feishuId: derived.feishuId,
        builtin: !!prev.builtin,
        hint: prev.hint || '',
      });
    });
    return out;
  }

  $('btn-field-add').onclick = () => {
    formState.fields = collectFields();
    const key = 'custom_' + (formState.fields.length + 1);
    const derived = deriveFeishu(key, 'input');
    formState.fields.push({
      key: key,
      label: '自定义字段',
      type: 'input',
      source: 'user',
      required: false,
      enabled: true,
      order: 50 + formState.fields.length,
      options: [],
      default: '',
      maxLen: 64,
      feishuField: derived.feishuField,
      feishuType: derived.feishuType,
      feishuId: derived.feishuId,
      builtin: false,
    });
    renderFields();
  };

  $('fields-body').onclick = (ev) => {
    const btn = ev.target.closest('[data-del]');
    if (!btn) return;
    const tr = btn.closest('tr');
    if (!tr) return;
    const keyEl = tr.querySelector('[data-k="key"]');
    const key = (keyEl && keyEl.value || tr.getAttribute('data-key') || '').trim();
    if (!key) return;
    if (key === 'reason' || key === 'cls') {
      alert('内置字段 reason / cls 不能删除');
      return;
    }
    if (!confirm('确认删除字段「' + key + '」？删除后需点「保存」才会生效。')) return;
    // 先收集当前编辑，再按 key 删除（避免排序后下标错乱）
    formState.fields = collectFields().filter((f) => f.key !== key);
    renderFields();
    $('fields-ok').textContent = '已从列表移除「' + key + '」，请点「保存」写入配置';
  };

  $('btn-fields-save').onclick = async () => {
    $('fields-err').textContent = '';
    $('fields-ok').textContent = '';
    try {
      const body = { version: formState.version || 1, fields: collectFields() };
      const r = await api('/api/settings/form-fields', { method: 'POST', body: JSON.stringify(body) });
      formState = r.form || body;
      renderFields();
      $('feishu-preview').textContent = JSON.stringify(r.feishuExport || [], null, 2);
      $('fields-ok').textContent = r.message || '已保存';
    } catch (e) {
      $('fields-err').textContent = e.message || String(e);
    }
  };

  function statusLabel(st) {
    return ({
      DRAFT: '草稿',
      PENDING_APPROVAL: '待审批 / 已发 QR#1',
      REDEEMED: '已兑付',
      EXPIRED: '已过期',
      CANCELLED: '已取消',
      QUARANTINED: '隔离中',
      SCANNING: '扫描中',
      RELEASED: '已导入释放',
      BLOCKED: '已阻断',
    })[st] || st || '—';
  }

  function statusClass(st) {
    if (st === 'REDEEMED' || st === 'RELEASED') return 'ok';
    if (st === 'PENDING_APPROVAL' || st === 'SCANNING' || st === 'QUARANTINED') return 'warn';
    if (st === 'BLOCKED' || st === 'EXPIRED' || st === 'CANCELLED') return 'bad';
    return 'muted';
  }

  function fmtTime(v) {
    if (!v) return '—';
    const d = new Date(v);
    if (Number.isNaN(d.getTime()) || d.getTime() === 0) return '—';
    const p = (n) => (n < 10 ? '0' + n : '' + n);
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) +
      ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + ' B';
    if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
    return (n / 1073741824).toFixed(2) + ' GB';
  }

  async function loadHistory() {
    $('hist-err').textContent = '';
    $('hist-empty').classList.add('hidden');
    $('hist-detail').classList.add('hidden');
    try {
      const d = await api('/requests');
      const items = d.items || [];
      $('hist-scope').textContent = '全部申请（共 ' + items.length + ' 条）';
      const list = $('hist-list');
      if (!items.length) {
        list.innerHTML = '';
        $('hist-empty').classList.remove('hidden');
        return;
      }
      list.innerHTML = items.map((it) => histItemHTML(it)).join('');
    } catch (e) {
      $('hist-list').innerHTML = '';
      $('hist-err').textContent = e.message || String(e);
    }
  }

  function fileNamesLabel(it) {
    const names = it.fileNames || [];
    if (!names.length) return '';
    if (names.length === 1) return names[0];
    if (names.length <= 3) return names.join('、');
    return names.slice(0, 3).join('、') + ' 等 ' + (it.fileCount || names.length) + ' 个';
  }

  function itemTitle(it) {
    return fileNamesLabel(it) || it.id;
  }

  function zipDownloadName(it) {
    const names = it.fileNames || [];
    if (names.length === 1) {
      const base = String(names[0]).split('/').pop() || names[0];
      return base.replace(/[/\\?%*:|"<>]/g, '_') + '.zip';
    }
    return 'fdt-import-' + it.id + '.zip';
  }

  function histItemHTML(it) {
    const who = esc(it.applicantName || it.applicantId || '—');
    const reason = esc(it.reason || '');
    const dirTag = it.dir === 'import' ? '导入' : '导出';
    const canDL = !!it.downloadable;
    const title = itemTitle(it);
    const showId = title !== it.id;
    const dlBtn = canDL
      ? '<a class="btn dl-btn" href="/requests/' + encodeURIComponent(it.id) + '/download" download="' +
        esc(zipDownloadName(it)) + '" data-download="1">下载 ZIP</a>'
      : '';
    return '<div class="hist-item" data-id="' + esc(it.id) + '">' +
      '<button type="button" class="hist-item-main" data-id="' + esc(it.id) + '">' +
      '<div><div class="t1 file-title" title="' + esc(title) + '">' + esc(title) + '</div>' +
      (showId ? '<div class="t2 mono-id">' + esc(it.id) + '</div>' : '') +
      '<div class="t2">' + dirTag + ' · ' + who + ' · ' + esc(it.applicantId || '') +
      ' · ' + esc(it.cls || '') + ' · ' + (it.fileCount || 0) + ' 文件 · ' + fmtBytes(it.totalSize) +
      (canDL ? ' · <span class="dl-tag">可下载</span>' : '') +
      (reason ? '<br>' + reason : '') + '</div></div>' +
      '<div class="right"><div class="st ' + statusClass(it.status) + '">' + esc(statusLabel(it.status)) + '</div>' +
      '<div class="t2">' + fmtTime(it.createdAt) + '</div></div></button>' + dlBtn + '</div>';
  }

  async function loadImports() {
    $('imports-err').textContent = '';
    $('imports-empty').classList.add('hidden');
    try {
      const d = await api('/requests');
      const items = (d.items || []).filter((it) => it.downloadable);
      $('imports-scope').textContent = '全部可下载导入包（共 ' + items.length + ' 个）';
      const list = $('imports-list');
      if (!items.length) {
        list.innerHTML = '';
        $('imports-empty').classList.remove('hidden');
        return;
      }
      list.innerHTML = items.map((it) => {
        const who = esc(it.applicantName || it.applicantId || '—');
        const reason = esc(it.reason || '');
        const title = itemTitle(it);
        const showId = title !== it.id;
        return '<div class="hist-item import-dl-item">' +
          '<div><div class="t1 file-title" title="' + esc(title) + '">' + esc(title) + '</div>' +
          (showId ? '<div class="t2 mono-id">' + esc(it.id) + '</div>' : '') +
          '<div class="t2">导入 · ' + who + ' · ' + esc(it.applicantId || '') +
          ' · ' + esc(it.cls || '') + ' · ' + (it.fileCount || 0) + ' 文件 · ' + fmtBytes(it.totalSize) +
          ' · ' + fmtTime(it.createdAt) +
          (reason ? '<br>' + reason : '') + '</div></div>' +
          '<a class="btn dl-btn primary-dl" href="/requests/' + encodeURIComponent(it.id) +
          '/download" download="' + esc(zipDownloadName(it)) + '">下载 ZIP</a></div>';
      }).join('');
    } catch (e) {
      $('imports-list').innerHTML = '';
      $('imports-err').textContent = e.message || String(e);
    }
  }

  function closeHistDetail() {
    $('hist-detail').classList.add('hidden');
  }

  function setHistDownload(req, id) {
    const btn = $('btn-hist-download');
    const hint = $('hist-download-hint');
    const can = !!(req && (req.downloadable || (
      (req.Status || req.status) === 'RELEASED' &&
      (req.Dir || req.dir) === 'import'
    )));
    if (btn) {
      btn.classList.toggle('hidden', !can);
      btn.href = can ? ('/requests/' + encodeURIComponent(id) + '/download') : '#';
      btn.setAttribute('download', can ? ('fdt-import-' + id + '.zip') : '');
    }
    if (hint) hint.classList.toggle('hidden', !can);
  }

  async function openHistoryDetail(id) {
    $('hist-err').textContent = '';
    try {
      const req = await api('/requests/' + encodeURIComponent(id));
      const names = req.fileNames || [];
      $('hist-reqid').textContent = names.length ? fileNamesLabel(req) : (req.ID || req.id || id);
      $('hist-detail-meta').textContent =
        statusLabel(req.Status || req.status) + ' · ' +
        ((req.Dir || req.dir) === 'import' ? '导入' : '导出') + ' · ' +
        (req.ApplicantName || req.applicantName || '') + ' · ' +
        fmtTime(req.CreatedAt || req.createdAt);
      $('hist-qr').src = '/requests/' + encodeURIComponent(id) + '/qr1.png?t=' + Date.now();
      const extras = req.Extras || req.extras || {};
      const extraLines = Object.keys(extras).map((k) => '扩展.' + k + ': ' + extras[k]);
      const fileLines = names.length
        ? ['文件:'].concat(names.map((n) => '  - ' + n))
        : [];
      $('hist-detail-body').textContent = [
        '申请单: ' + (req.ID || req.id || id),
        '方向: ' + ((req.Dir || req.dir) === 'import' ? '导入' : '导出'),
        '事由: ' + (req.Reason || req.reason || ''),
        '密级: ' + (req.Cls || req.cls || ''),
        '申请人 ID: ' + (req.ApplicantID || req.applicantId || ''),
        '文件数: ' + (req.FileCount != null ? req.FileCount : req.fileCount),
        '大小: ' + fmtBytes(req.TotalSize != null ? req.TotalSize : req.totalSize),
        'Merkle: ' + (req.MerkleRoot || req.merkleRoot || ''),
        '兑付 ATM: ' + (req.RedeemATM || req.redeemAtm || '—'),
        'U 盘: ' + (req.USBSerial || req.usbSerial || '—'),
        '飞书审批: ' + (req.FsApproval || req.fsApproval || '—'),
        '数据目录: ' + (req.DataDir || req.dataDir || '—'),
      ].concat(fileLines).concat(extraLines).join('\n');
      setHistDownload(req, id);
      $('hist-detail').classList.remove('hidden');
    } catch (e) {
      $('hist-err').textContent = e.message || String(e);
    }
  }

  let lastIdentityMode = 'none';
  let pamAvailable = true;

  // 域控（AD）常用默认值：空配置时写入可编辑的真实值，不是 placeholder。
  const LDAP_AD_DEFAULTS = {
    searchFilter: '(sAMAccountName=%s)',
    attrName: 'displayName',
    attrID: 'employeeID',
    attrDept: 'department',
    attrMail: 'mail',
  };

  function setIdentityMode(mode, opts) {
    const m = mode || 'none';
    $('identity-mode').value = m;
    document.querySelectorAll('.id-mode-card').forEach((el) => {
      el.classList.toggle('on', el.getAttribute('data-mode') === m);
    });
    ['ldap', 'pam', 'none', 'mock'].forEach((n) => {
      const panel = $('id-panel-' + n);
      if (panel) panel.classList.toggle('hidden', n !== m);
    });
    const hint = $('identity-mode-hint');
    const testRow = $('id-test-row');
    const testBtn = $('btn-ldap-test');
    if (hint) {
      if (m === 'ldap') hint.textContent = '使用 LDAP/AD 认证申请人；保存后 IDENTITY=ldap。';
      else if (m === 'pam') hint.textContent = '使用本机 PAM 认证；保存后 IDENTITY=pam，走 /etc/pam.d 服务栈。';
      else if (m === 'none') hint.textContent = '关闭目录认证（IDENTITY=none）。不会回退到 mock。';
      else hint.textContent = '⚠️ mock 含弱口令样例账号，仅限离线联调；生产请选 LDAP 或本机 PAM。';
    }
    if (testRow) testRow.classList.toggle('hidden', m === 'none' || m === 'mock');
    if (testBtn) testBtn.classList.toggle('hidden', m === 'none' || m === 'mock');
    const pamWarn = $('pam-unavailable');
    if (pamWarn) pamWarn.classList.toggle('hidden', !(m === 'pam' && !pamAvailable));
    if (!(opts && opts.skipConfirm) && m === 'mock' && (lastIdentityMode === 'ldap' || lastIdentityMode === 'pam')) {
      if (!confirm('当前已是生产身份源。切换到 mock 会启用弱口令样例账号，生产环境禁止。确定继续？')) {
        setIdentityMode(lastIdentityMode, { skipConfirm: true });
      }
    }
  }

  async function loadIdentity() {
    $('ldap-err').textContent = '';
    $('ldap-ok').textContent = '';
    try {
      const d = await api('/api/settings/ldap');
      const L = d.ldap || {};
      const P = d.pam || {};
      pamAvailable = P.available !== false;
      const mode = d.identityMode || (L.enabled ? 'ldap' : 'none');
      lastIdentityMode = mode;
      $('pam-service').value = P.service || 'password-auth';
      $('ldap-url').value = L.url || '';
      $('ldap-starttls').checked = !!L.startTLS;
      $('ldap-insecure').checked = !!L.insecureSkipVerify;
      $('ldap-base').value = L.searchBase || '';
      $('ldap-bind-dn').value = L.serviceBindDN || '';
      $('ldap-filter').value = L.searchFilter || LDAP_AD_DEFAULTS.searchFilter;
      $('ldap-attr-name').value = L.attrName || LDAP_AD_DEFAULTS.attrName;
      $('ldap-attr-id').value = L.attrID || LDAP_AD_DEFAULTS.attrID;
      $('ldap-attr-dept').value = L.attrDept || LDAP_AD_DEFAULTS.attrDept;
      $('ldap-attr-mail').value = L.attrMail || LDAP_AD_DEFAULTS.attrMail;
      if (L.hasServicePassword) $('ldap-bind-pass').placeholder = '已保存（留空不改）';
      setIdentityMode(mode, { skipConfirm: true });
    } catch (e) {
      $('ldap-err').textContent = e.message || String(e);
    }
  }

  function identityBody() {
    const identityMode = $('identity-mode').value;
    return {
      identityMode,
      pamService: $('pam-service').value.trim() || 'password-auth',
      enabled: identityMode === 'ldap',
      url: $('ldap-url').value.trim(),
      startTLS: $('ldap-starttls').checked,
      insecureSkipVerify: $('ldap-insecure').checked,
      searchBase: $('ldap-base').value.trim(),
      serviceBindDN: $('ldap-bind-dn').value.trim(),
      servicePassword: $('ldap-bind-pass').value,
      searchFilter: $('ldap-filter').value.trim(),
      attrName: $('ldap-attr-name').value.trim(),
      attrID: $('ldap-attr-id').value.trim(),
      attrDept: $('ldap-attr-dept').value.trim(),
      attrMail: $('ldap-attr-mail').value.trim(),
    };
  }

  async function boot() {
    try {
      me = await api('/api/me');
      applyVersionFooter(me);
      if (me.authenticated && me.canManageSettings) showApp();
      else showLogin();
    } catch (_) {
      showLogin();
    }
  }

  $('btn-login').onclick = async () => {
    $('login-err').textContent = '';
    try {
      me = await api('/api/admin/login', {
        method: 'POST',
        body: JSON.stringify({ user: $('login-user').value, password: $('login-pass').value }),
      });
      $('login-pass').value = '';
      if (!me.canManageSettings) throw new Error('登录成功但未获得管理权限');
      showApp();
    } catch (e) {
      $('login-err').textContent = e.message || String(e);
    }
  };

  ['login-user', 'login-pass'].forEach((id) => {
    const el = $(id);
    if (!el) return;
    el.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); $('btn-login').click(); }
    });
  });

  $('btn-logout').onclick = async () => {
    await api('/api/logout', { method: 'POST' });
    me = { authenticated: false };
    showLogin();
  };

  $('btn-tab-imports').onclick = () => showPanel('imports');
  $('btn-tab-history').onclick = () => showPanel('history');
  $('btn-tab-fields').onclick = () => showPanel('fields');
  $('btn-tab-identity').onclick = () => showPanel('identity');
  $('btn-tab-password').onclick = () => showPanel('password');
  $('btn-imports-refresh').onclick = () => loadImports();
  window.addEventListener('hashchange', () => {
    if ($('view-app').classList.contains('hidden')) return;
    showPanel(panelFromHash(), { skipHash: true });
  });
  document.querySelectorAll('.id-mode-card').forEach((el) => {
    el.onclick = () => setIdentityMode(el.getAttribute('data-mode'));
  });

  $('btn-pwd-save').onclick = async () => {
    $('pwd-err').textContent = '';
    $('pwd-ok').textContent = '';
    const oldPassword = $('pwd-old').value;
    const newPassword = $('pwd-new').value;
    const new2 = $('pwd-new2').value;
    if (!oldPassword || !newPassword) {
      $('pwd-err').textContent = '请填写当前密码与新密码';
      return;
    }
    if (newPassword.length < 8) {
      $('pwd-err').textContent = '新密码至少 8 位';
      return;
    }
    if (newPassword !== new2) {
      $('pwd-err').textContent = '两次输入的新密码不一致';
      return;
    }
    try {
      const r = await api('/api/admin/password', {
        method: 'POST',
        body: JSON.stringify({ oldPassword, newPassword }),
      });
      $('pwd-ok').textContent = r.message || '密码已修改';
      $('pwd-old').value = '';
      $('pwd-new').value = '';
      $('pwd-new2').value = '';
    } catch (e) {
      $('pwd-err').textContent = e.message || String(e);
    }
  };
  $('btn-hist-refresh').onclick = () => loadHistory();
  $('btn-hist-close').onclick = closeHistDetail;
  $('btn-hist-ok').onclick = closeHistDetail;
  $('hist-detail').onclick = (e) => { if (e.target === $('hist-detail')) closeHistDetail(); };
  $('hist-list').onclick = (ev) => {
    if (ev.target.closest('[data-download], a.dl-btn')) return;
    const btn = ev.target.closest('.hist-item-main, .hist-item');
    if (!btn) return;
    const id = btn.getAttribute('data-id');
    if (id) openHistoryDetail(id);
  };

  $('btn-ldap-save').onclick = async () => {
    $('ldap-err').textContent = '';
    $('ldap-ok').textContent = '';
    try {
      const body = identityBody();
      if (body.identityMode === 'mock' && (lastIdentityMode === 'ldap' || lastIdentityMode === 'pam')) {
        if (!confirm('将切回 mock（联调样例）。生产请选 LDAP 或本机 PAM。确定保存？')) return;
      }
      if (body.identityMode === 'pam' && !pamAvailable) {
        throw new Error('当前二进制不支持 PAM，请使用 Linux 版 Portal（已链接 libpam）');
      }
      const r = await api('/api/settings/ldap', { method: 'POST', body: JSON.stringify(body) });
      $('ldap-ok').textContent = r.message || '已保存';
      lastIdentityMode = r.identityMode || body.identityMode;
      if (r.pam && r.pam.available === false) pamAvailable = false;
      setIdentityMode(lastIdentityMode, { skipConfirm: true });
      $('ldap-bind-pass').value = '';
      if (me) me.identityMode = lastIdentityMode;
      if ($('mode-badge')) $('mode-badge').textContent = lastIdentityMode;
    } catch (e) {
      $('ldap-err').textContent = e.message || String(e);
    }
  };

  $('btn-ldap-test').onclick = async () => {
    $('ldap-err').textContent = '';
    $('ldap-ok').textContent = '';
    $('ldap-test-out').classList.add('hidden');
    try {
      const body = Object.assign(identityBody(), {
        account: $('ldap-test-user').value.trim(),
        password: $('ldap-test-pass').value,
      });
      if (body.identityMode !== 'ldap' && body.identityMode !== 'pam') {
        throw new Error('请先选择 LDAP 或本机 PAM 再测试');
      }
      const r = await api('/api/settings/ldap/test', { method: 'POST', body: JSON.stringify(body) });
      $('ldap-ok').textContent = r.message || '测试成功';
      if (r.user) {
        $('ldap-test-out').textContent = JSON.stringify(r.user, null, 2);
        $('ldap-test-out').classList.remove('hidden');
      }
    } catch (e) {
      $('ldap-err').textContent = e.message || String(e);
    }
  };

  boot();
})();
