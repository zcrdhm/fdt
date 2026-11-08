(function () {
  const $ = (id) => document.getElementById(id);
  let me = { authenticated: false, authRequired: true };

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

  function applyVersionFooter(data) {
    const ver = (data && data.version) || '1.0.1';
    const copy = (data && data.copyright) || '© fasteda.cn';
    const text = 'FDT ' + ver + ' · ' + copy;
    ['login-footer', 'app-footer'].forEach((id) => {
      const el = $(id);
      if (el) el.textContent = text;
    });
  }

  function updateLoginHints(mode) {
    const m = String(mode || '').toLowerCase();
    const hint = $('login-mode-hint');
    const mockHint = $('login-mock-hint');
    if (hint) {
      if (m === 'ldap') hint.textContent = '使用域账号登录。退出后需重新登录。';
      else if (m === 'pam') hint.textContent = '使用本机 PAM 账号登录。退出后需重新登录。';
      else if (m === 'mock') hint.textContent = '使用 mock 账号登录。退出后需重新登录。';
      else if (m === 'none') hint.textContent = '当前未启用身份认证（IDENTITY=none）。';
      else hint.textContent = '请使用系统配置的身份源账号登录。退出后需重新登录。';
    }
    if (mockHint) {
      // 仅提示账号名，不显示密码
      mockHint.classList.toggle('hidden', m !== 'mock');
    }
  }

  function showLogin() {
    updateLoginHints(me && me.identityMode);
    $('view-login').classList.remove('hidden');
    $('view-app').classList.add('hidden');
  }

  const PANELS = ['export', 'imports', 'history', 'bind'];

  function panelFromHash() {
    const h = (location.hash || '').replace(/^#\/?/, '');
    return PANELS.indexOf(h) >= 0 ? h : 'export';
  }

  function showApp() {
    $('view-login').classList.add('hidden');
    $('view-app').classList.remove('hidden');
    fillWho();
    updateFeishuGate();
    if (needsFeishuBind()) showPanel('bind');
    else showPanel(panelFromHash());
    loadUserForm();
  }

  function needsFeishuBind() {
    return !!(me && me.authenticated && !me.feishuId);
  }

  function updateFeishuGate() {
    const banner = $('feishu-bind-banner');
    const go = $('btn-go');
    const need = needsFeishuBind();
    if (banner) banner.classList.toggle('hidden', !need);
    if (go) {
      go.disabled = need;
      go.title = need ? '请先绑定飞书 ID' : '';
    }
  }

  function requireFeishuBound(action) {
    if (!needsFeishuBind()) return true;
    const tip = '尚未绑定飞书 ID，无法' + (action || '发起导出') + '。请先完成绑定。';
    if ($('err')) $('err').textContent = tip;
    showPanel('bind');
    if ($('bind-err')) $('bind-err').textContent = tip;
    return false;
  }

  let userFields = [];

  async function loadUserForm() {
    try {
      const d = await api('/api/form-fields');
      userFields = d.fields || [];
      renderUserForm();
    } catch (_) {
      userFields = [
        { key: 'cls', label: '密级', type: 'select', required: true,
          options: ['internal', 'confidential', 'secret', 'topsecret'], default: 'secret' },
        { key: 'reason', label: '事由', type: 'textarea', required: true, default: '交付客户A的版图数据' },
      ];
      renderUserForm();
    }
  }

  function renderUserForm() {
    const box = $('dynamic-fields');
    if (!box) return;
    box.innerHTML = userFields.map((f) => {
      const id = 'f-' + f.key;
      const req = f.required ? ' *' : '';
      let ctrl = '';
      if (f.type === 'select') {
        const opts = (f.options || []).map((o) =>
          '<option value="' + esc(o) + '"' + (o === f.default ? ' selected' : '') + '>' + esc(o) + '</option>').join('');
        ctrl = '<select id="' + id + '" data-field="' + esc(f.key) + '">' + opts + '</select>';
      } else if (f.type === 'textarea') {
        ctrl = '<textarea id="' + id + '" data-field="' + esc(f.key) + '" rows="3" placeholder="' +
          esc(f.hint || '') + '">' + esc(f.default || '') + '</textarea>';
      } else {
        ctrl = '<input id="' + id + '" data-field="' + esc(f.key) + '" type="' +
          (f.type === 'number' ? 'number' : 'text') + '" value="' + esc(f.default || '') +
          '" placeholder="' + esc(f.hint || '') + '" />';
      }
      return '<label>' + esc(f.label) + req + '</label>' + ctrl;
    }).join('');
  }

  function collectUserFields() {
    const out = {};
    document.querySelectorAll('#dynamic-fields [data-field]').forEach((el) => {
      out[el.getAttribute('data-field')] = (el.value || '').trim();
    });
    return out;
  }

  function fillWho() {
    if (!me.authenticated) {
      $('who-line').textContent = '未登录（IDENTITY=none 时可直接提交）';
      $('who-sub').textContent = '';
      $('mode-badge').textContent = me.identityMode || 'none';
      return;
    }
    $('who-line').innerHTML = '<b>' + esc(me.name || me.account) + '</b> · 账号 ' + esc(me.account) +
      ' · 目录 ID ' + esc(me.id);
    const fid = me.feishuId
      ? ('已绑定飞书 ID <b>' + esc(me.feishuId) + '</b>（写入 QR#1 申请人）')
      : '<span style="color:var(--warn)">尚未绑定飞书 ID · 请到「飞书 ID」页粘贴手机端显示的 ou_… / user_id</span>';
    $('who-sub').innerHTML = fid;
    $('mode-badge').textContent = me.identityMode || 'mock';
    $('mode-badge').className = 'badge ' + ((me.identityMode === 'ldap' || me.identityMode === 'pam') ? 'live' : 'warn');
    $('bind-dir-id').value = me.id || '';
    $('bind-feishu').value = me.feishuId || '';
    updateFeishuGate();
  }

  function showPanel(name, opts) {
    if (PANELS.indexOf(name) < 0) name = 'export';
    if (name === 'export' && needsFeishuBind()) {
      if ($('bind-err')) {
        $('bind-err').textContent = '尚未绑定飞书 ID，无法发起导出。请先在此页完成绑定。';
      }
      name = 'bind';
    }
    PANELS.forEach((n) => {
      const el = $('panel-' + n);
      if (el) el.classList.toggle('hidden', n !== name);
      const btn = $('btn-tab-' + n);
      if (btn) btn.classList.toggle('on', n === name);
    });
    if (!(opts && opts.skipHash) && location.hash !== '#' + name) {
      history.replaceState(null, '', '#' + name);
    }
    if (name === 'history') loadHistory();
    if (name === 'imports') loadImports();
  }

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
      const all = !!(d.scope && d.scope.all);
      $('hist-scope').textContent = all
        ? '管理员视图：显示全部申请（共 ' + items.length + ' 条）'
        : '我的申请（共 ' + items.length + ' 条；仅本人可见）';
      const list = $('hist-list');
      if (!items.length) {
        list.innerHTML = '';
        $('hist-empty').classList.remove('hidden');
        return;
      }
      list.innerHTML = items.map((it) => histItemHTML(it, { all: all, showDownload: true })).join('');
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

  function histItemHTML(it, opts) {
    const all = !!(opts && opts.all);
    const who = esc(it.applicantName || it.applicantId || '—');
    const reason = esc(it.reason || '');
    const dirTag = it.dir === 'import' ? '导入' : '导出';
    const canDL = !!(opts && opts.showDownload && it.downloadable);
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
      '<div class="t2">' + dirTag + ' · ' + who + (all ? ' · ' + esc(it.applicantId || '') : '') +
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
      const all = !!(d.scope && d.scope.all);
      $('imports-scope').textContent = all
        ? '全部可下载导入包（共 ' + items.length + ' 个）'
        : '我的可下载导入包（共 ' + items.length + ' 个）';
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
          '<div class="t2">导入 · ' + who + ' · ' + esc(it.cls || '') + ' · ' +
          (it.fileCount || 0) + ' 文件 · ' + fmtBytes(it.totalSize) +
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

  function openQrModal() {
    $('qr-modal').classList.remove('hidden');
  }

  function closeQrModal() {
    $('qr-modal').classList.add('hidden');
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

  function esc(s) {
    return String(s || '').replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  async function boot() {
    try {
      me = await api('/api/me');
      applyVersionFooter(me);
      if (me.authRequired && !me.authenticated) showLogin();
      else showApp();
    } catch (e) {
      showLogin();
      $('login-err').textContent = e.message || String(e);
    }
  }

  $('btn-login').onclick = async () => {
    $('login-err').textContent = '';
    try {
      me = await api('/api/login', {
        method: 'POST',
        body: JSON.stringify({ user: $('login-user').value, password: $('login-pass').value }),
      });
      $('login-pass').value = '';
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
    me = { authenticated: false, authRequired: true };
    showLogin();
  };

  $('btn-tab-export').onclick = () => {
    if (!requireFeishuBound('发起导出')) return;
    showPanel('export');
  };
  $('btn-tab-imports').onclick = () => showPanel('imports');
  $('btn-tab-history').onclick = () => showPanel('history');
  $('btn-tab-bind').onclick = () => showPanel('bind');
  $('btn-imports-refresh').onclick = () => loadImports();
  $('btn-goto-bind').onclick = () => showPanel('bind');
  window.addEventListener('hashchange', () => {
    if ($('view-app').classList.contains('hidden')) return;
    const name = panelFromHash();
    if (name === 'export' && !requireFeishuBound('发起导出')) return;
    showPanel(name, { skipHash: true });
  });
  $('btn-hist-refresh').onclick = () => loadHistory();
  $('btn-hist-close').onclick = closeHistDetail;
  $('btn-hist-ok').onclick = closeHistDetail;
  $('hist-detail').onclick = (e) => { if (e.target === $('hist-detail')) closeHistDetail(); };
  $('btn-qr-close').onclick = closeQrModal;
  $('btn-qr-ok').onclick = closeQrModal;
  $('qr-modal').onclick = (e) => { if (e.target === $('qr-modal')) closeQrModal(); };
  $('hist-list').onclick = (ev) => {
    if (ev.target.closest('[data-download], a.dl-btn')) return;
    const btn = ev.target.closest('.hist-item-main, .hist-item');
    if (!btn) return;
    const id = btn.getAttribute('data-id');
    if (id) openHistoryDetail(id);
  };

  $('btn-bind').onclick = async () => {
    $('bind-err').textContent = '';
    $('bind-ok').textContent = '';
    try {
      const fid = $('bind-feishu').value.trim();
      if (!fid) {
        $('bind-err').textContent = '请填写飞书用户 ID';
        return;
      }
      me = await api('/api/feishu-id', {
        method: 'POST',
        body: JSON.stringify({ feishuId: fid }),
      });
      $('bind-ok').textContent = '已保存。现在可以发起导出。';
      fillWho();
      showPanel('export');
    } catch (e) {
      $('bind-err').textContent = e.message || String(e);
    }
  };

  $('btn-unbind').onclick = async () => {
    $('bind-err').textContent = '';
    $('bind-ok').textContent = '';
    try {
      me = await api('/api/feishu-id', { method: 'POST', body: JSON.stringify({ feishuId: '' }) });
      $('bind-feishu').value = '';
      $('bind-ok').textContent = '已清除飞书 ID 绑定';
      fillWho();
      showPanel('bind');
    } catch (e) {
      $('bind-err').textContent = e.message || String(e);
    }
  };

  // Selected files bag: FileList is immutable, so we keep our own list for remove/add.
  let selectedFiles = [];

  function fmtSize(n) {
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1024 * 1024 * 1024) return (n / (1024 * 1024)).toFixed(1) + ' MB';
    return (n / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
  }

  function syncFileInput() {
    const dt = new DataTransfer();
    selectedFiles.forEach((f) => dt.items.add(f));
    $('files').files = dt.files;
  }

  function renderFileList() {
    const list = $('file-list');
    const summary = $('file-summary');
    const clearBtn = $('btn-clear-files');
    if (!selectedFiles.length) {
      list.classList.add('hidden');
      list.innerHTML = '';
      summary.textContent = '未选择任何文件';
      clearBtn.classList.add('hidden');
      syncFileInput();
      return;
    }
    let total = 0;
    selectedFiles.forEach((f) => { total += f.size; });
    summary.textContent = '已选 ' + selectedFiles.length + ' 个文件 · 共 ' + fmtSize(total);
    clearBtn.classList.remove('hidden');
    list.classList.remove('hidden');
    list.innerHTML = selectedFiles.map((f, i) => {
      const name = f.webkitRelativePath || f.name;
      return '<div class="file-row" data-i="' + i + '">' +
        '<span class="file-name" title="' + esc(name) + '">' + esc(name) + '</span>' +
        '<span class="file-size">' + fmtSize(f.size) + '</span>' +
        '<button type="button" data-remove="' + i + '" title="移除">移除</button>' +
        '</div>';
    }).join('');
    syncFileInput();
  }

  function addFiles(fileList) {
    const seen = new Set(selectedFiles.map((f) => (f.webkitRelativePath || f.name) + '\0' + f.size + '\0' + f.lastModified));
    for (const f of fileList) {
      const key = (f.webkitRelativePath || f.name) + '\0' + f.size + '\0' + f.lastModified;
      if (seen.has(key)) continue;
      seen.add(key);
      selectedFiles.push(f);
    }
    renderFileList();
  }

  $('btn-pick').onclick = () => $('files').click();
  $('btn-clear-files').onclick = () => {
    selectedFiles = [];
    renderFileList();
  };
  $('files').addEventListener('change', () => {
    if ($('files').files && $('files').files.length) addFiles($('files').files);
    else renderFileList();
  });
  $('file-list').addEventListener('click', (ev) => {
    const btn = ev.target.closest('[data-remove]');
    if (!btn) return;
    const i = Number(btn.getAttribute('data-remove'));
    if (!Number.isFinite(i)) return;
    selectedFiles.splice(i, 1);
    renderFileList();
  });

  $('btn-go').onclick = async () => {
    $('err').textContent = '';
    if (!requireFeishuBound('发起导出')) return;
    $('btn-go').disabled = true;
    try {
      const fields = collectUserFields();
      const files = selectedFiles;
      if (!files || !files.length) {
        throw new Error('请先选择要导出的文件');
      }
      const fd = new FormData();
      Object.keys(fields).forEach((k) => fd.append(k, fields[k]));
      for (const f of files) fd.append('files', f, f.webkitRelativePath || f.name);
      const r = await fetch('/requests', {
        method: 'POST', body: fd, credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest', Accept: 'application/json' },
      });
      const text = await r.text();
      let d = null;
      try { d = text ? JSON.parse(text) : null; } catch (_) { d = text; }
      if (r.status === 401) { showLogin(); throw new Error('请先登录'); }
      if (!r.ok) throw new Error(typeof d === 'string' ? d : r.statusText);
      $('reqid').textContent = d.reqId;
      $('mode').textContent = d.mode;
      $('qr').src = '/requests/' + d.reqId + '/qr1.png?t=' + Date.now();
      $('summary').textContent = '文件数 ' + d.count + ' · 总大小 ' + d.totalSize + ' 字节 · Merkle ' + d.merkleRoot;
      const man = $('manifest');
      if (man) man.textContent = '';
      if ($('export-ok')) {
        $('export-ok').textContent = '已生成申请单 ' + d.reqId + '，请用飞书扫码审批。';
        $('export-ok').classList.remove('hidden');
      }
      openQrModal();
    } catch (e) {
      const msg = e.message || String(e);
      if (/飞书|feishu/i.test(msg)) {
        $('err').textContent = msg;
        showPanel('bind');
        if ($('bind-err')) $('bind-err').textContent = msg;
      } else {
        $('err').textContent = '失败: ' + msg;
      }
    } finally {
      updateFeishuGate();
    }
  };

  boot();
})();
