(function () {
  const $ = (id) => document.getElementById(id);
  let camOn = false;
  let stream = null;
  let scanTimer = null;
  let redeeming = false;
  let importXferring = false;
  let importRedeeming = false;
  let importReqId = '';
  let hasUSB = false;
  let usbMounted = false;
  let modalResolver = null;
  let usbBrowsePath = '';
  let importSelected = {}; // path -> { path, type, name }
  let maintMode = true; // 默认维护模式 · 非全屏
  let allowExitFullscreen = false;
  let maintPassMode = 'unlock'; // unlock | change
  let fsGuardTimer = null;
  // Chromium --kiosk 窗口（独立 profile）才能真正挡住 Esc；普通浏览器全屏做不到。
  const osKiosk = /(?:\?|&)kiosk=1(?:&|$)/.test(location.search) ||
    window.matchMedia('(display-mode: fullscreen)').matches ||
    window.matchMedia('(display-mode: standalone)').matches;

  function isFullscreen() {
    return !!(document.fullscreenElement || document.webkitFullscreenElement);
  }

  async function lockEscapeKey() {
    // Chromium Keyboard Lock：仅在安全上下文 + 全屏 API 下有效；短按 Esc 交给页面，长按仍可退出。
    try {
      if (navigator.keyboard && navigator.keyboard.lock) {
        // 无参 = 锁定全部键（含 Escape）
        await navigator.keyboard.lock();
      }
    } catch (_) { /* Safari/Firefox 或不在安全上下文 */ }
  }

  function unlockEscapeKey() {
    try {
      if (navigator.keyboard && navigator.keyboard.unlock) navigator.keyboard.unlock();
    } catch (_) {}
  }

  async function enterFullscreen() {
    const el = document.documentElement;
    try {
      if (!isFullscreen()) {
        if (el.requestFullscreen) {
          await el.requestFullscreen({ navigationUI: 'hide' });
        } else if (el.webkitRequestFullscreen) {
          el.webkitRequestFullscreen();
        }
      }
      await lockEscapeKey();
    } catch (_) { /* 部分浏览器需用户手势；已由按钮触发 */ }
  }

  async function leaveFullscreen() {
    allowExitFullscreen = true;
    if (fsGuardTimer) { clearInterval(fsGuardTimer); fsGuardTimer = null; }
    unlockEscapeKey();
    try {
      if (document.exitFullscreen) await document.exitFullscreen();
      else if (document.webkitExitFullscreen) document.webkitExitFullscreen();
    } catch (_) {}
    // 稍后再清标志，避免 exit 触发的 fullscreenchange 又拉回去
    setTimeout(() => { allowExitFullscreen = false; }, 400);
  }

  function startFullscreenGuard() {
    if (fsGuardTimer) clearInterval(fsGuardTimer);
    fsGuardTimer = setInterval(() => {
      if (maintMode || allowExitFullscreen) return;
      if (!isFullscreen()) enterFullscreen();
      else lockEscapeKey();
    }, 300);
  }

  function stopFullscreenGuard() {
    if (fsGuardTimer) { clearInterval(fsGuardTimer); fsGuardTimer = null; }
  }

  function applyMaintUI() {
    document.body.classList.toggle('maint-mode', maintMode);
    document.body.classList.toggle('kiosk', !maintMode);
    const bar = $('maint-bar');
    const fab = $('btn-enter-maint');
    if (bar) bar.classList.toggle('hidden', !maintMode);
    if (fab) fab.classList.toggle('hidden', maintMode);
    // 全屏运营态锁键；维护模式解锁
    document.body.style.userSelect = maintMode ? '' : 'none';
  }

  function isPassField(el) {
    return el && (
      el.id === 'maint-pass-input' ||
      el.id === 'maint-pass-new' ||
      el.id === 'maint-pass-new2' ||
      el.id === 'fields-editor-pass'
    );
  }

  /** 全屏运营态拦截浏览器/系统常用退出快捷键（无法拦截 Ctrl+Alt+Del / 系统 Win 键，需配合 kiosk 浏览器）。 */
  function shouldBlockSystemKey(e) {
    if (maintMode) return false;
    const k = e.key;
    const code = e.code || '';
    // 维护密码框内允许正常输入；Esc 只关弹窗，不放行退出全屏
    if (isPassField(e.target)) {
      if (k === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        closeMaintPassModal();
        return true;
      }
      if (!e.ctrlKey && !e.altKey && !e.metaKey && k !== 'F11' && k !== 'F5' && k !== 'F12') {
        return false;
      }
    }
    if (k === 'Escape' || k === 'F11' || k === 'F5' || k === 'F12' || k === 'F10') return true;
    if (k === 'Meta' || k === 'OS' || code === 'MetaLeft' || code === 'MetaRight' || code === 'OSLeft' || code === 'OSRight') return true;
    if (e.altKey && (k === 'F4' || k === 'Tab' || k === 'Meta' || code === 'F4')) return true;
    if (e.ctrlKey || e.metaKey) {
      const bad = 'rwntupsliRWNTUPSLI';
      if (bad.indexOf(k) >= 0) return true;
      if (e.shiftKey && (k === 'i' || k === 'I' || k === 'j' || k === 'J' || k === 'c' || k === 'C')) return true;
      if (k === 'Tab') return true;
    }
    return false;
  }

  function lockSystemKeys(e) {
    if (!shouldBlockSystemKey(e)) return;
    e.preventDefault();
    e.stopPropagation();
    if (typeof e.stopImmediatePropagation === 'function') e.stopImmediatePropagation();
    if (!isFullscreen() && !allowExitFullscreen) enterFullscreen();
  }

  ['keydown', 'keyup', 'keypress'].forEach((type) => {
    window.addEventListener(type, lockSystemKeys, true);
  });
  document.addEventListener('contextmenu', (e) => {
    if (!maintMode) {
      e.preventDefault();
      e.stopPropagation();
    }
  }, true);
  window.addEventListener('beforeunload', (e) => {
    if (maintMode) return;
    e.preventDefault();
    e.returnValue = '';
  });
  document.addEventListener('visibilitychange', () => {
    if (maintMode || allowExitFullscreen) return;
    if (document.visibilityState === 'visible' && !isFullscreen()) enterFullscreen();
  });

  function openMaintPassModal(mode) {
    maintPassMode = mode || 'unlock';
    $('maint-pass-err').textContent = '';
    $('maint-pass-input').value = '';
    $('maint-pass-new').value = '';
    $('maint-pass-new2').value = '';
    const changing = maintPassMode === 'change';
    const quitting = maintPassMode === 'quit';
    $('maint-pass-title').textContent = changing
      ? '修改维护密码'
      : (quitting ? '退出 ATM 程序' : '进入维护模式');
    $('maint-pass-hint').textContent = changing
      ? '请输入当前维护密码，并设置新密码（至少 4 位）。'
      : (quitting
        ? '请输入维护密码以退出程序。关窗或系统热键无法真正退出。'
        : '全屏运营中已屏蔽 Esc/F11 等快捷键。输入维护密码后才能退出全屏。');
    $('maint-pass-extra').classList.toggle('hidden', !changing);
    $('maint-pass-modal').classList.remove('hidden');
    $('maint-pass-input').focus();
  }

  async function quitGUIWithPassword() {
    const pw = ($('maint-pass-input').value || '');
    try {
      await api('/api/maint/quit', { method: 'POST', body: JSON.stringify({ password: pw }) });
      closeMaintPassModal();
      await showModal('正在退出', '程序即将关闭。', 'ok');
    } catch (e) {
      $('maint-pass-err').textContent = e.message || String(e);
    }
  }

  function closeMaintPassModal() {
    $('maint-pass-modal').classList.add('hidden');
  }

  async function exitMaintenance() {
    // 已在 OS kiosk 窗口：只切运营态，并再请求一次全屏 + Keyboard Lock。
    if (osKiosk) {
      maintMode = false;
      applyMaintUI();
      await enterFullscreen();
      startFullscreenGuard();
      return;
    }
    // 普通浏览器：Fullscreen API 无法可靠屏蔽 Esc → 拉起独立 Chromium --kiosk。
    try {
      const r = await api('/api/maint/kiosk', { method: 'POST', body: '{}' });
      maintMode = false;
      applyMaintUI();
      await enterFullscreen();
      startFullscreenGuard();
      await showModal(
        '已启动锁屏窗口',
        (r && r.message) || '已打开 Chromium 全屏锁屏。请在新窗口运营；本页可关闭。普通浏览器标签页无法真正屏蔽 Esc。',
        'ok'
      );
    } catch (e) {
      maintMode = false;
      applyMaintUI();
      await enterFullscreen();
      startFullscreenGuard();
      await showModal(
        '锁屏启动失败',
        (e && e.message) || String(e) +
          '。请在本机执行：/opt/fdt-atm/bin/fdt-atm-kiosk',
        'warn'
      );
    }
  }

  async function enterMaintenanceWithPassword() {
    const pw = ($('maint-pass-input').value || '');
    try {
      await api('/api/maint/unlock', { method: 'POST', body: JSON.stringify({ password: pw }) });
      closeMaintPassModal();
      stopFullscreenGuard();
      await leaveFullscreen();
      maintMode = true;
      applyMaintUI();
    } catch (e) {
      $('maint-pass-err').textContent = e.message || String(e);
    }
  }

  async function changeMaintPassword() {
    const oldPassword = $('maint-pass-input').value || '';
    const newPassword = $('maint-pass-new').value || '';
    const new2 = $('maint-pass-new2').value || '';
    if (newPassword !== new2) {
      $('maint-pass-err').textContent = '两次输入的新密码不一致';
      return;
    }
    try {
      await api('/api/maint/password', {
        method: 'POST',
        body: JSON.stringify({ oldPassword, newPassword }),
      });
      closeMaintPassModal();
      await showModal('密码已更新', '维护密码已保存。下次退出全屏请使用新密码。', 'ok');
    } catch (e) {
      $('maint-pass-err').textContent = e.message || String(e);
    }
  }

  let fieldsEditorDraft = [];
  let fieldsEditorVersion = 1;

  function closeFieldsEditor() {
    const m = $('import-fields-modal');
    if (m) m.classList.add('hidden');
  }

  function renderFieldsEditor() {
    const list = $('fields-editor-list');
    if (!list) return;
    const types = ['text', 'textarea', 'select', 'number'];
    list.innerHTML = fieldsEditorDraft.map((f, idx) => {
      const builtin = !!f.builtin || f.key === 'reason' || f.key === 'cls';
      const opts = (f.options || []).join('\n');
      const typeOpts = types.map((t) =>
        '<option value="' + t + '"' + (f.type === t ? ' selected' : '') + '>' + t + '</option>'
      ).join('');
      return (
        '<div class="field-edit-card" data-idx="' + idx + '">' +
          '<div class="field-edit-head">' +
            '<span class="key">' + escAttr(f.key || '(新字段)') + (builtin ? ' · 内置' : '') + '</span>' +
            (builtin ? '' : '<button type="button" class="ghost nm" data-fields-del="' + idx + '">删除</button>') +
          '</div>' +
          '<div class="field-edit-grid">' +
            (builtin
              ? '<div><label>key</label><input value="' + escAttr(f.key) + '" disabled /></div>'
              : '<div><label>key *</label><input data-fe="key" data-idx="' + idx + '" value="' + escAttr(f.key || '') + '" placeholder="英文小写，如 receiver" /></div>') +
            '<div><label>标签 *</label><input data-fe="label" data-idx="' + idx + '" value="' + escAttr(f.label || '') + '" /></div>' +
            '<div><label>类型</label><select data-fe="type" data-idx="' + idx + '">' + typeOpts + '</select></div>' +
            '<div><label>默认值</label><input data-fe="default" data-idx="' + idx + '" value="' + escAttr(f.default || '') + '" /></div>' +
            '<div class="span2"><label>提示</label><input data-fe="hint" data-idx="' + idx + '" value="' + escAttr(f.hint || '') + '" /></div>' +
            '<div class="span2"><label>选项（select：每行一项，可用 value|显示名）</label>' +
              '<textarea data-fe="options" data-idx="' + idx + '" rows="2"' + (f.type === 'select' ? '' : ' disabled') + '>' +
              escAttr(opts) + '</textarea></div>' +
          '</div>' +
          '<div class="row-checks">' +
            '<label><input type="checkbox" data-fe="required" data-idx="' + idx + '"' + (f.required ? ' checked' : '') + ' /> 必填</label>' +
            '<label><input type="checkbox" data-fe="enabled" data-idx="' + idx + '"' + (f.enabled !== false ? ' checked' : '') + ' /> 启用</label>' +
            '<label>排序 <input type="number" data-fe="order" data-idx="' + idx + '" value="' + (f.order || (idx + 1) * 10) + '" style="width:72px;padding:6px" /></label>' +
          '</div>' +
        '</div>'
      );
    }).join('');

    list.querySelectorAll('[data-fe]').forEach((el) => {
      const apply = () => {
        const i = Number(el.getAttribute('data-idx'));
        const fe = el.getAttribute('data-fe');
        if (!fieldsEditorDraft[i]) return;
        if (fe === 'required' || fe === 'enabled') {
          fieldsEditorDraft[i][fe] = !!el.checked;
        } else if (fe === 'order') {
          fieldsEditorDraft[i].order = Number(el.value) || 0;
        } else if (fe === 'options') {
          fieldsEditorDraft[i].options = String(el.value || '').split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
        } else if (fe === 'type') {
          fieldsEditorDraft[i].type = el.value;
          renderFieldsEditor();
        } else {
          fieldsEditorDraft[i][fe] = el.value;
        }
      };
      el.addEventListener('change', apply);
      el.addEventListener('input', apply);
    });
    list.querySelectorAll('[data-fields-del]').forEach((btn) => {
      btn.onclick = () => {
        const i = Number(btn.getAttribute('data-fields-del'));
        fieldsEditorDraft.splice(i, 1);
        renderFieldsEditor();
      };
    });
  }

  async function openFieldsEditor() {
    const err = $('fields-editor-err');
    if (err) err.textContent = '';
    if ($('fields-editor-pass')) $('fields-editor-pass').value = '';
    try {
      const d = await api('/api/maint/import-fields');
      if (d.editable === false) {
        await showModal('无法编辑', '未配置 ATM_IMPORT_FIELDS，无法写入字段文件。', 'warn');
        return;
      }
      fieldsEditorVersion = d.version || 1;
      fieldsEditorDraft = (d.fields || []).map((f) => ({
        key: f.key || '',
        label: f.label || '',
        type: f.type || 'text',
        required: !!f.required,
        enabled: f.enabled !== false,
        default: f.default || '',
        hint: f.hint || '',
        order: f.order || 0,
        options: Array.isArray(f.options) ? f.options.slice() : [],
        builtin: !!f.builtin || f.key === 'reason' || f.key === 'cls',
      }));
      const pathEl = $('fields-editor-path');
      if (pathEl) {
        pathEl.textContent = '保存到：' + (d.path || 'import-fields.json') + '（需维护密码）';
      }
      renderFieldsEditor();
      $('import-fields-modal').classList.remove('hidden');
    } catch (e) {
      await showModal('加载失败', e.message || String(e), 'bad');
    }
  }

  async function saveFieldsEditor() {
    const err = $('fields-editor-err');
    if (err) err.textContent = '';
    const password = ($('fields-editor-pass') && $('fields-editor-pass').value) || '';
    if (!password) {
      if (err) err.textContent = '请输入维护密码';
      return;
    }
    const fields = fieldsEditorDraft.map((f, i) => ({
      key: String(f.key || '').trim(),
      label: String(f.label || '').trim(),
      type: f.type || 'text',
      required: !!f.required,
      enabled: f.enabled !== false,
      default: f.default || '',
      hint: f.hint || '',
      order: Number(f.order) || (i + 1) * 10,
      options: f.type === 'select' ? (f.options || []) : undefined,
      builtin: !!f.builtin || f.key === 'reason' || f.key === 'cls',
    }));
    try {
      await api('/api/maint/import-fields', {
        method: 'POST',
        body: JSON.stringify({ password, version: fieldsEditorVersion, fields }),
      });
      closeFieldsEditor();
      await loadImportFields();
      await showModal('已保存', '导入申请字段已更新，表单已刷新。', 'ok');
    } catch (e) {
      if (err) err.textContent = e.message || String(e);
    }
  }

  $('btn-exit-maint').onclick = () => exitMaintenance();
  $('btn-enter-maint').onclick = () => openMaintPassModal('unlock');
  $('btn-change-maint-pass').onclick = () => openMaintPassModal('change');
  if ($('btn-edit-import-fields')) {
    $('btn-edit-import-fields').onclick = () => openFieldsEditor();
  }
  if ($('btn-quit-gui')) {
    $('btn-quit-gui').onclick = () => openMaintPassModal('quit');
  }
  if ($('btn-fields-add')) {
    $('btn-fields-add').onclick = () => {
      fieldsEditorDraft.push({
        key: '', label: '', type: 'text', required: false, enabled: true,
        default: '', hint: '', order: (fieldsEditorDraft.length + 1) * 10, options: [], builtin: false,
      });
      renderFieldsEditor();
    };
  }
  if ($('btn-fields-save')) $('btn-fields-save').onclick = () => saveFieldsEditor();
  if ($('btn-fields-cancel')) $('btn-fields-cancel').onclick = closeFieldsEditor;
  if ($('btn-fields-close')) $('btn-fields-close').onclick = closeFieldsEditor;
  if ($('import-fields-modal')) {
    $('import-fields-modal').onclick = (e) => {
      if (e.target === $('import-fields-modal')) closeFieldsEditor();
    };
  }
  $('maint-pass-cancel').onclick = closeMaintPassModal;
  $('maint-pass-ok').onclick = () => {
    if (maintPassMode === 'change') changeMaintPassword();
    else if (maintPassMode === 'quit') quitGUIWithPassword();
    else enterMaintenanceWithPassword();
  };
  ['maint-pass-input', 'maint-pass-new', 'maint-pass-new2'].forEach((id) => {
    const el = $(id);
    if (!el) return;
    el.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); $('maint-pass-ok').click(); }
    });
  });
  $('maint-pass-modal').onclick = (e) => {
    if (e.target === $('maint-pass-modal')) closeMaintPassModal();
  };

  function onFullscreenChange() {
    if (maintMode || allowExitFullscreen) return;
    if (!isFullscreen()) {
      // Esc 在部分浏览器会先退出全屏再触发本事件：立刻拉回并重新 Keyboard Lock
      setTimeout(() => { if (!maintMode && !allowExitFullscreen) enterFullscreen(); }, 0);
      setTimeout(() => { if (!maintMode && !allowExitFullscreen && !isFullscreen()) enterFullscreen(); }, 50);
      setTimeout(() => { if (!maintMode && !allowExitFullscreen && !isFullscreen()) enterFullscreen(); }, 200);
    } else {
      lockEscapeKey();
    }
  }
  document.addEventListener('fullscreenchange', onFullscreenChange);
  document.addEventListener('webkitfullscreenchange', onFullscreenChange);

  applyMaintUI();
  // --kiosk 新窗口无法继承上次点击的用户手势；Fullscreen/Keyboard Lock 必须再点一次。
  if (osKiosk && /(?:\?|&)run=1(?:&|$)/.test(location.search) && maintMode) {
    const gate = document.createElement('div');
    gate.id = 'kiosk-start-gate';
    gate.style.cssText = 'position:fixed;inset:0;z-index:99999;background:#0b1220;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:16px;color:#e8eefc;';
    gate.innerHTML = '<div style="font-size:28px;font-weight:700">FDT ATM 锁屏已就绪</div>' +
      '<div style="opacity:.75;font-size:15px">点击下方按钮进入运营（锁定 Esc 等按键）</div>' +
      '<button type="button" id="kiosk-start-btn" style="margin-top:8px;padding:14px 28px;font-size:18px;border:0;border-radius:10px;background:#3b82f6;color:#fff;cursor:pointer">进入运营</button>';
    document.body.appendChild(gate);
    document.getElementById('kiosk-start-btn').onclick = async () => {
      gate.remove();
      await exitMaintenance();
    };
  }

  async function api(path, opts) {
    const o = Object.assign({ headers: { Accept: 'application/json' } }, opts || {});
    if (o.body && typeof o.body === 'string' && !o.headers['Content-Type']) {
      o.headers['Content-Type'] = 'application/json';
    }
    const r = await fetch(path, o);
    const text = await r.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = text; }
    if (!r.ok) throw new Error(typeof data === 'string' ? data : (data && data.error) || text || r.statusText);
    return data;
  }

  function showModal(title, body, kind) {
    const mask = $('modal-mask');
    const card = $('modal-card');
    $('modal-title').textContent = title || '提示';
    $('modal-body').textContent = body || '';
    card.className = 'modal-card' + (kind ? ' ' + kind : '');
    mask.classList.remove('hidden');
    $('modal-ok').focus();
    return new Promise((resolve) => { modalResolver = resolve; });
  }

  function hideModal() {
    $('modal-mask').classList.add('hidden');
    const done = modalResolver;
    modalResolver = null;
    if (done) done();
  }

  $('modal-ok').onclick = hideModal;
  $('modal-mask').onclick = (e) => { if (e.target === $('modal-mask')) hideModal(); };

  function classifyError(msg) {
    const m = String(msg || '');
    if (/未检测|请先插入|NO_USB|no.?usb/i.test(m)) {
      return { title: '未检测到 U 盘', body: '请先插入可写 U 盘，再扫码或兑付。\n\n' + m, kind: 'warn' };
    }
    if (/重复兑付|已核销|重复导出|AlreadyConsumed|Duplicate/i.test(m)) {
      return { title: '重复兑付', body: '该放行码已被核销，不能再次兑付。\n请确认是否已写过 U 盘，或联系管理员核对审计记录。\n\n' + m, kind: 'bad' };
    }
    if (/忙|租约|Busy/i.test(m)) {
      return { title: '兑付冲突', body: '另一台终端正在兑付同一申请单，请稍后再试。\n\n' + m, kind: 'warn' };
    }
    if (/只读|写保护|NotWritable/i.test(m)) {
      return { title: 'U 盘不可写', body: '当前 U 盘只读或开启了写保护。\n放行码未核销，请更换可写 U 盘后重新扫码兑付。\n\n' + m, kind: 'warn' };
    }
    if (/空间不足|Insufficient/i.test(m)) {
      return { title: '空间不足', body: 'U 盘剩余空间不够完成本次兑付。\n放行码（QR#2）未核销，请清理或更换更大容量的 U 盘后，用同一放行码重新扫码兑付。\n\n' + m, kind: 'warn' };
    }
    if (/文件系统|4GB|exFAT|FAT|FilesystemLimit/i.test(m)) {
      return { title: '文件系统不兼容', body: '当前 U 盘格式无法写入本次数据（例如 FAT 单文件不能超过 4GB）。\n放行码（QR#2）未核销，请格式化为 exFAT/NTFS 或更换 U 盘后，用同一放行码重新扫码兑付。\n\n' + m, kind: 'warn' };
    }
    if (/验签|有效期|TokenInvalid|非法二维码|BadPayload/i.test(m)) {
      return { title: '令牌无效', body: 'QR#2 验签失败、已过期或内容非法。请确认网关公钥一致，并使用有效期内的放行码。\n\n' + m, kind: 'bad' };
    }
    if (/Merkle|校验失败|RootMismatch|VerifyFailed/i.test(m)) {
      return { title: '校验失败', body: '数据与审批不符或落盘校验失败，已回滚。请勿拔盘，联系管理员。\n\n' + m, kind: 'bad' };
    }
    if (/摄像头|camera|NotAllowed|Permission|getUserMedia|CAMERA/i.test(m)) {
      return { title: '无法打开摄像头', body: '无法访问摄像头。请检查浏览器权限或本机 CAMERA 配置，也可粘贴令牌。\n\n' + m, kind: 'warn' };
    }
    return { title: '操作失败', body: m || '未知错误', kind: 'bad' };
  }

  function alertError(msg) {
    const c = classifyError(msg);
    return showModal(c.title, c.body, c.kind);
  }

  function requireUSB(action) {
    if (hasUSB && usbMounted) return true;
    const tip = !hasUSB
      ? '未检测到 USB 存储设备。请插入 U 盘后再' + action + '。'
      : 'U 盘已插入但尚未挂载成功，请点「手动检测」或检查分区/权限后再' + action + '。';
    showModal('未检测到 U 盘', tip, 'warn');
    return false;
  }

  function stateClass(st) {
    if (st === 'READY' || st === 'DONE') return 'ready';
    if (st === 'IDLE_NO_USB' || st === 'VERIFYING' || st === 'WRITING') return 'wait';
    return '';
  }

  function stateText(st) {
    return ({
      IDLE_NO_USB: '请插入 U 盘',
      READY: '就绪 · 可以扫码',
      VERIFYING: '正在验签…',
      WRITING: '正在写入 U 盘…',
      DONE: '完成 · 可拔盘',
    })[st] || st;
  }

  function applyStatus(s) {
    const foot = $('app-footer');
    if (foot) {
      const ver = s.version || '1.0.1';
      const copy = s.copyright || '© fasteda.cn';
      foot.textContent = 'FDT ' + ver + ' · ' + copy;
    }
    $('device-line').textContent = '设备 ' + (s.deviceId || 'atm') + (s.camera ? ' · 摄像头 ' + s.camera : '');
    // ERROR 已由弹窗提示；状态栏按 U 盘情况显示，避免常驻「出错」误导。
    let st = s.state;
    if (st === 'ERROR') st = s.usb ? 'READY' : 'IDLE_NO_USB';
    const label = $('state-label');
    label.textContent = stateText(st);
    label.className = 'state ' + stateClass(st);
    const eject = $('btn-usb-eject');
    hasUSB = !!s.usb;
    usbMounted = !!(s.usb && s.usb.mounted);
    if (s.usb) {
      if (!s.usb.mounted) {
        $('usb-line').textContent = '已插入 U 盘 ' + s.usb.serial + ' · 尚未挂载（自动挂载中/需权限）';
        eject.disabled = true;
      } else {
        $('usb-line').textContent = 'U 盘 ' + s.usb.serial + ' · ' + (s.usb.path || '') +
          ' · ' + (s.usb.fs || '?') + ' · 可用 ' + fmt(s.usb.free) + ' / ' + fmt(s.usb.capacity) +
          (s.usb.writable ? '' : ' · 只读');
        eject.disabled = !!s.busy;
      }
    } else {
      $('usb-line').textContent = '未检测到 USB 存储设备，请插入 U 盘';
      eject.disabled = true;
      importSelected = {};
      if ($('usb-browser')) $('usb-browser').classList.add('hidden');
    }
    updateImportUSBControls();
    updateRedeemUSBControls();
    const msg = $('msg-line');
    const safeOk = s.message && String(s.message).indexOf('可以拔出') >= 0;
    // 不展示 lastError：失败详情只走弹窗
    msg.textContent = s.message || '';
    msg.className = 'msg ' + (safeOk || (s.message && s.usb) ? 'ok' : (s.message && !s.usb ? 'bad' : ''));
    if (s.busy && s.total > 0) {
      $('prog-wrap').classList.remove('hidden');
      $('prog-bar').style.width = Math.min(100, Math.round(s.written * 100 / s.total)) + '%';
    } else if (!s.busy) {
      $('prog-wrap').classList.add('hidden');
    }
    renderRedeemProgress(s);
    renderImportXferProgress(s);
    renderImportRedeemProgress(s);
    const quitBtn = $('btn-quit-gui');
    if (quitBtn) quitBtn.classList.toggle('hidden', !s.guiQuit);
  }

  function fmtRate(bps) {
    if (!bps || bps <= 0) return '';
    return fmt(bps) + '/s';
  }

  function fmtEta(sec) {
    if (sec == null || sec < 0) return '';
    sec = Math.round(sec);
    if (sec < 60) return '约 ' + sec + ' 秒';
    if (sec < 3600) return '约 ' + Math.ceil(sec / 60) + ' 分钟';
    return '约 ' + (sec / 3600).toFixed(1) + ' 小时';
  }

  function rateEtaText(s) {
    const parts = [];
    if (s.rateBps) parts.push(fmtRate(s.rateBps));
    if (s.etaSec != null && s.busy && s.written < s.total) parts.push('剩余 ' + fmtEta(s.etaSec));
    return parts.length ? ' · ' + parts.join(' · ') : '';
  }

  function renderRedeemProgress(s) {
    const box = $('redeem-box');
    if (!box) return;
    const p = s.progress || {};
    const busy = !!s.busy;
    const exportPhases = { verifying: 1, writing: 1, done: 1 };
    const has = redeeming || (busy && exportPhases[p.phase]) || p.phase === 'writing' || p.phase === 'done' || (p.files && p.files.length);
    if (!has) {
      if (!redeeming) box.classList.add('hidden');
      return;
    }
    box.classList.remove('hidden');
    const outDir = p.outDir || '';
    $('redeem-path').textContent = outDir ? ('兑付路径：' + outDir) : (busy || redeeming ? '兑付路径：准备中…' : '');
    const pct = s.percent != null ? s.percent : (s.total > 0 ? Math.round(s.written * 100 / s.total) : 0);
    if (p.fileName) {
      $('redeem-file').textContent = '正在写入：' + p.fileName +
        (p.fileIndex && p.fileCount ? ('（' + p.fileIndex + '/' + p.fileCount + '）') : '');
    } else if (p.phase === 'verifying') {
      $('redeem-file').textContent = '正在验签 / 拉取清单…';
    } else if (p.phase === 'done') {
      $('redeem-file').textContent = '写入完成';
    } else {
      $('redeem-file').textContent = (busy || redeeming) ? '兑付进行中…' : '';
    }
    $('redeem-prog-wrap').classList.toggle('hidden', !(s.total > 0 || busy || redeeming));
    $('redeem-prog-bar').style.width = Math.min(100, pct) + '%';
    $('redeem-pct').textContent = s.total > 0
      ? (fmt(s.written) + ' / ' + fmt(s.total) + ' · ' + pct + '%' + rateEtaText(s))
      : ((busy || redeeming) ? '进行中…' : '');
    const list = $('redeem-files');
    const files = p.files || [];
    if (!files.length) {
      list.innerHTML = '';
      return;
    }
    list.innerHTML = files.map((name, i) => {
      let cls = '';
      if (p.fileName && name === p.fileName) cls = 'cur';
      else if (p.fileIndex && i + 1 < p.fileIndex) cls = 'done';
      else if (p.phase === 'done') cls = 'done';
      return '<li class="' + cls + '" title="' + escAttr(name) + '">' + escAttr(name) + '</li>';
    }).join('');
  }

  function phaseLabelImport(phase) {
    switch (phase) {
      case 'indexing': return '正在索引 U 盘文件…';
      case 'creating': return '正在创建导入申请…';
      case 'uploading': return '正在上传到隔离区…';
      case 'done': return '上传完成';
      default: return '处理中…';
    }
  }

  function renderImportXferProgress(s) {
    const box = $('import-xfer-box');
    if (!box) return;
    const p = s.progress || {};
    const uploadPhases = { indexing: 1, creating: 1, uploading: 1, done: 1 };
    const show = importXferring || (s.busy && uploadPhases[p.phase]);
    if (!show) {
      if (!importXferring) box.classList.add('hidden');
      return;
    }
    box.classList.remove('hidden');
    const pct = s.percent != null ? s.percent : (s.total > 0 ? Math.round(s.written * 100 / s.total) : 0);
    $('import-xfer-path').textContent = p.phase === 'done' ? '隔离区上传完成' : '请勿拔盘 · 上传进行中';
    let fileLine = phaseLabelImport(p.phase);
    if (p.fileCount && (p.phase === 'uploading' || p.phase === 'creating')) {
      fileLine += '（约 ' + p.fileCount + ' 个文件）';
    }
    $('import-xfer-file').textContent = fileLine;
    const wrap = $('import-xfer-prog-wrap');
    const bar = $('import-xfer-prog-bar');
    wrap.classList.remove('hidden');
    const indeterminate = !s.total || p.phase === 'indexing' || p.phase === 'creating';
    wrap.classList.toggle('indeterminate', indeterminate && p.phase !== 'done');
    if (indeterminate && p.phase !== 'done') {
      bar.style.width = '40%';
    } else {
      bar.style.width = Math.min(100, pct) + '%';
    }
    if (s.total > 0 && (p.phase === 'uploading' || p.phase === 'done')) {
      $('import-xfer-pct').textContent = fmt(s.written) + ' / ' + fmt(s.total) + ' · ' + pct + '%' + rateEtaText(s);
    } else if (p.phase === 'indexing') {
      $('import-xfer-pct').textContent = '正在计算哈希 / 建清单（大文件可能较久）…';
    } else if (p.phase === 'creating') {
      $('import-xfer-pct').textContent = '正在创建导入申请…';
    } else {
      $('import-xfer-pct').textContent = s.written > 0 ? (fmt(s.written) + ' 已传' + rateEtaText(s)) : '准备上传…';
    }
  }

  function renderImportRedeemProgress(s) {
    const box = $('import-redeem-box');
    if (!box) return;
    const p = s.progress || {};
    const show = importRedeeming || (s.busy && (p.phase === 'releasing' || p.phase === 'done'));
    if (!show) {
      if (!importRedeeming) box.classList.add('hidden');
      return;
    }
    box.classList.remove('hidden');
    if (p.phase === 'done' && !s.busy) {
      $('import-redeem-file').textContent = '放行完成';
    } else if (p.phase === 'releasing' || importRedeeming) {
      $('import-redeem-file').textContent = '正在校验并放行隔离区（含扫描，大包可能较久）…';
    } else {
      $('import-redeem-file').textContent = '处理中…';
    }
    $('import-redeem-prog-wrap').classList.remove('hidden');
    $('import-redeem-prog-bar').style.width = (p.phase === 'done' && !s.busy) ? '100%' : '35%';
    $('import-redeem-pct').textContent = (p.phase === 'done' && !s.busy) ? '完成' : '进行中…';
  }

  function escAttr(s) {
    return String(s || '').replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  async function refresh() {
    try {
      applyStatus(await api('/api/status'));
    } catch (e) {
      $('state-label').textContent = '无法连接 ATM 服务';
      $('state-label').className = 'state bad';
      $('msg-line').textContent = e.message || String(e);
    }
  }

  $('btn-usb-detect').onclick = async () => {
    const btn = $('btn-usb-detect');
    btn.disabled = true;
    btn.textContent = '检测中…';
    $('usb-line').textContent = '正在检测 U 盘…';
    try {
      const s = await api('/api/usb/rescan', { method: 'POST', body: '{}' });
      applyStatus(s);
      if (!s.usb) {
        await showModal('未检测到 U 盘', '没有找到 USB 存储设备。请插入 U 盘后重试「手动检测」。', 'warn');
      } else if (!s.usb.mounted) {
        await showModal('U 盘未挂载', '已检测到 U 盘 ' + (s.usb.serial || '') + '，但尚未挂载成功。请检查分区、文件系统或权限后重试。', 'warn');
      } else {
        await showModal('检测成功', '已挂载 U 盘 ' + (s.usb.serial || '') + '\n路径 ' + (s.usb.path || ''), 'ok');
      }
    } catch (e) {
      $('msg-line').textContent = e.message || String(e);
      $('msg-line').className = 'msg bad';
      await alertError(e.message || e);
    } finally {
      btn.disabled = false;
      btn.textContent = '手动检测';
    }
  };

  $('btn-usb-eject').onclick = async () => {
    const btn = $('btn-usb-eject');
    if (btn.disabled) return;
    if (!confirm('确认安全卸载并准备拔出 U 盘？')) return;
    btn.disabled = true;
    btn.textContent = '卸载中…';
    try {
      applyStatus(await api('/api/usb/eject', { method: 'POST', body: '{}' }));
      await showModal('可以拔出', 'U 盘已安全卸载，现在可以拔出。', 'ok');
    } catch (e) {
      $('msg-line').textContent = e.message || String(e);
      $('msg-line').className = 'msg bad';
      btn.disabled = false;
      btn.textContent = '安全拔出';
      await alertError(e.message || e);
      refresh();
      return;
    }
    btn.textContent = '安全拔出';
  };

  function fmt(n) {
    if (n == null) return '—';
    if (n < 1024) return n + ' B';
    if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
    return (n / 1073741824).toFixed(2) + ' GB';
  }

  const TABS = ['export', 'import', 'import-redeem'];

  function tabFromHash() {
    const h = (location.hash || '').replace(/^#\/?/, '');
    return TABS.indexOf(h) >= 0 ? h : 'export';
  }

  function showTab(name, opts) {
    if (TABS.indexOf(name) < 0) name = 'export';
    document.querySelectorAll('.tab').forEach((b) => {
      b.classList.toggle('on', b.getAttribute('data-tab') === name);
    });
    $('panel-export').classList.toggle('hidden', name !== 'export');
    $('panel-import').classList.toggle('hidden', name !== 'import');
    $('panel-import-redeem').classList.toggle('hidden', name !== 'import-redeem');
    if (!(opts && opts.skipHash) && location.hash !== '#' + name) {
      history.replaceState(null, '', '#' + name);
    }
  }

  document.querySelectorAll('.tab').forEach((btn) => {
    btn.onclick = () => showTab(btn.getAttribute('data-tab'));
  });
  window.addEventListener('hashchange', () => showTab(tabFromHash(), { skipHash: true }));
  showTab(tabFromHash());

  async function doRedeem(frame, fromCam) {
    if (!frame || redeeming) return;
    if (!requireUSB('兑付')) {
      if (fromCam) stopCam('请先插入 U 盘后再扫码');
      return;
    }
    redeeming = true;
    updateRedeemUSBControls();
    // 扫到码立刻关摄像头，再兑付。
    if (fromCam) stopCam('已识别二维码，摄像头已关闭 · 正在兑付…');
    $('export-out').textContent = '兑付中…';
    $('redeem-box').classList.remove('hidden');
    $('redeem-path').textContent = '兑付路径：准备中…';
    $('redeem-file').textContent = '正在验签 / 拉取清单…';
    $('redeem-files').innerHTML = '';
    $('redeem-prog-bar').style.width = '0%';
    $('redeem-pct').textContent = '';
    const poll = setInterval(refresh, 400);
    try {
      const r = await api('/api/redeem', { method: 'POST', body: JSON.stringify({ frame }) });
      const names = (r.files && r.files.length) ? r.files.join('\n  - ') : '(见目录)';
      $('export-out').textContent = '✅ 兑付成功\n申请单 ' + r.reqId +
        '\n兑付路径 ' + r.outDir +
        '\n写入 ' + r.writtenBytes + ' 字节 / ' + r.fileCount + ' 文件\n文件:\n  - ' + names +
        '\nMerkle ' + r.merkleRoot;
      $('redeem-path').textContent = '兑付路径：' + r.outDir;
      $('redeem-file').textContent = '写入完成 · ' + r.fileCount + ' 个文件';
      $('redeem-prog-bar').style.width = '100%';
      $('redeem-pct').textContent = fmt(r.writtenBytes) + ' / ' + fmt(r.writtenBytes) + ' · 100%';
      if (r.files && r.files.length) {
        $('redeem-files').innerHTML = r.files.map((n) =>
          '<li class="done" title="' + escAttr(n) + '">' + escAttr(n) + '</li>').join('');
      }
      await showModal('兑付成功', '申请单 ' + r.reqId + '\n路径 ' + r.outDir +
        '\n' + r.fileCount + ' 个文件 · ' + fmt(r.writtenBytes) + '\n可安全拔出 U 盘。', 'ok');
    } catch (e) {
      $('export-out').textContent = '失败: ' + (e.message || e);
      await alertError(e.message || e);
    } finally {
      clearInterval(poll);
      redeeming = false;
      updateRedeemUSBControls();
      refresh();
    }
  }

  function updateRedeemUSBControls() {
    const btn = $('btn-redeem');
    if (!btn) return;
    btn.disabled = !(hasUSB && usbMounted) || redeeming;
  }

  $('btn-redeem').onclick = () => {
    if (!requireUSB('手动兑付')) return;
    const frame = ($('frame').value || '').trim();
    if (!frame) {
      showModal('缺少令牌', '请先扫码或粘贴 QR#2 放行令牌，再点手动兑付。', 'warn');
      return;
    }
    doRedeem(frame, false);
  };

  let camPurpose = 'export'; // export | import
  let serverScanCtrl = null;
  // 摄像头最长开启时间：超时自动关闭，防恶意常开。
  const CAM_SCAN_MAX_MS = 60000;
  let camDeadlineTimer = null;
  let camCountdownTimer = null;
  let camOpenedAt = 0;
  let camTimedOut = false;

  function setScanBtn(on) {
    [['btn-cam-scan', '扫码'], ['btn-import-cam', '扫码']].forEach(([id, label]) => {
      const btn = $(id);
      if (!btn) return;
      btn.textContent = on ? '扫码中…' : label;
      btn.disabled = !!on;
    });
  }

  function openCamModal() {
    const m = $('cam-modal');
    if (m) m.classList.remove('hidden');
  }

  function closeCamModal() {
    const m = $('cam-modal');
    if (m) m.classList.add('hidden');
  }

  function baseCamHint() {
    return camPurpose === 'import'
      ? '请将审批后的放行码（QR#2）对准框内。无需 U 盘，识别后自动放行隔离区数据。'
      : '请将飞书放行码（QR#2）对准框内，识别成功后自动兑付到 U 盘。';
  }

  function clearCamDeadline() {
    if (camDeadlineTimer) { clearTimeout(camDeadlineTimer); camDeadlineTimer = null; }
    if (camCountdownTimer) { clearInterval(camCountdownTimer); camCountdownTimer = null; }
  }

  function camSecondsLeft() {
    return Math.max(0, Math.ceil((CAM_SCAN_MAX_MS - (Date.now() - camOpenedAt)) / 1000));
  }

  function updateCamCountdown(extra) {
    const left = camSecondsLeft();
    const hint = $('cam-modal-hint');
    if (!hint) return;
    const base = extra || baseCamHint();
    hint.textContent = base + ' · 剩余 ' + left + ' 秒（超时自动关闭摄像头）';
  }

  function armCamDeadline() {
    clearCamDeadline();
    camTimedOut = false;
    camOpenedAt = Date.now();
    updateCamCountdown();
    camCountdownTimer = setInterval(() => updateCamCountdown(), 1000);
    camDeadlineTimer = setTimeout(() => {
      camTimedOut = true;
      stopCam('扫码超时（60 秒内未识别），摄像头已关闭。请重新点「扫码」');
    }, CAM_SCAN_MAX_MS);
  }

  function prepareCamModal() {
    openCamModal();
    $('cam-hint').classList.remove('off');
    $('cam-hint').textContent = '正在打开摄像头…';
    if ($('cam-modal-title')) {
      $('cam-modal-title').textContent = camPurpose === 'import' ? '导入兑付 · 扫 QR#2' : '导出兑付 · 扫 QR#2';
    }
    if ($('cam-modal-hint')) {
      $('cam-modal-hint').textContent = baseCamHint() + ' · 最长 60 秒';
    }
  }

  async function onFrameScanned(text, fromCam) {
    if (fromCam) {
      // 先停扫，避免同一帧连触发两次
      if (scanTimer) { clearInterval(scanTimer); scanTimer = null; }
      camOn = false;
    }
    if (camPurpose === 'import') {
      if (fromCam) stopCam('已识别二维码，摄像头已关闭 · 正在导入…');
      $('imp-frame').value = text;
      await completeImport(text);
      return;
    }
    $('frame').value = text;
    await doRedeem(text, fromCam);
  }

  async function startCam(purpose) {
    if (camOn || (serverScanCtrl && !serverScanCtrl.signal.aborted)) return;
    camPurpose = purpose === 'import' ? 'import' : 'export';
    if (camPurpose === 'export' && !requireUSB('扫码')) return;

    prepareCamModal();
    setScanBtn(true);

    const canBrowser = navigator.mediaDevices && navigator.mediaDevices.getUserMedia;
    if (canBrowser) {
      try {
        stream = await navigator.mediaDevices.getUserMedia({
          audio: false,
          video: { facingMode: { ideal: 'environment' }, width: { ideal: 1280 }, height: { ideal: 720 } },
        });
        const v = $('cam');
        v.srcObject = stream;
        await v.play();
        camOn = true;
        $('cam-hint').classList.add('off');
        armCamDeadline();
        scanTimer = setInterval(tickScan, 350);
        return;
      } catch (e) {
        // fall through to server camera
        if (stream) {
          stream.getTracks().forEach((t) => t.stop());
          stream = null;
        }
        $('cam-hint').classList.remove('off');
        $('cam-hint').textContent = '浏览器无法打开摄像头，改用本机识别…';
        if ($('cam-modal-hint')) {
          $('cam-modal-hint').textContent = '请将 QR#2 对准本机摄像头（最长 60 秒），识别后自动继续。';
        }
      }
    } else {
      $('cam-hint').textContent = '改用本机摄像头识别…';
      if ($('cam-modal-hint')) {
        $('cam-modal-hint').textContent = '请将 QR#2 对准本机摄像头（最长 60 秒），识别后自动继续。';
      }
    }
    await serverScanFallback();
  }

  async function serverScanFallback() {
    serverScanCtrl = new AbortController();
    const out = camPurpose === 'import' ? $('import-out') : $('export-out');
    if (out) out.textContent = '本机摄像头识别中（最长 60 秒）…';
    armCamDeadline();
    // 客户端再加一层超时，确保 abort 能关掉请求与设备占用
    const clientTimer = setTimeout(() => {
      try { serverScanCtrl && serverScanCtrl.abort(); } catch (_) {}
    }, CAM_SCAN_MAX_MS);
    try {
      const r = await api('/api/scan/camera', {
        method: 'POST', body: '{}', signal: serverScanCtrl.signal,
      });
      clearTimeout(clientTimer);
      serverScanCtrl = null;
      await onFrameScanned(r.frame, true);
    } catch (e) {
      clearTimeout(clientTimer);
      serverScanCtrl = null;
      const msg = String((e && e.message) || e || '');
      const aborted = e && (e.name === 'AbortError' || /abort/i.test(msg));
      const serverTimeout = /超时|timeout|deadline/i.test(msg);
      if (camTimedOut) {
        // 截止定时器已 stopCam
        return;
      }
      if (serverTimeout) {
        camTimedOut = true;
        stopCam('扫码超时（60 秒内未识别），摄像头已关闭。请重新点「扫码」');
        return;
      }
      stopCam(aborted ? '已取消扫码' : '');
      if (!aborted) {
        if (out) out.textContent = '扫码失败: ' + msg;
        await alertError(msg);
      }
    }
  }

  function stopCam(hint) {
    camOn = false;
    clearCamDeadline();
    if (scanTimer) { clearInterval(scanTimer); scanTimer = null; }
    if (stream) {
      stream.getTracks().forEach((t) => t.stop());
      stream = null;
    }
    if (serverScanCtrl) {
      try { serverScanCtrl.abort(); } catch (_) {}
      serverScanCtrl = null;
    }
    const v = $('cam');
    if (v) v.srcObject = null;
    $('cam-hint').classList.remove('off');
    $('cam-hint').textContent = '正在打开摄像头…';
    closeCamModal();
    setScanBtn(false);
    if (hint) {
      const out = camPurpose === 'import' ? $('import-out') : $('export-out');
      if (out) out.textContent = hint;
    }
  }

  function tickScan() {
    if (!camOn || redeeming || typeof jsQR !== 'function') return;
    const v = $('cam');
    if (!v.videoWidth) return;
    const c = $('cam-canvas');
    c.width = v.videoWidth;
    c.height = v.videoHeight;
    const ctx = c.getContext('2d');
    ctx.drawImage(v, 0, 0, c.width, c.height);
    const img = ctx.getImageData(0, 0, c.width, c.height);
    const code = jsQR(img.data, img.width, img.height, { inversionAttempts: 'dontInvert' });
    if (!code || !code.data) return;
    const text = String(code.data).trim();
    if (!text.startsWith('FDT1.')) {
      updateCamCountdown('扫到非 FDT 码，请继续对准 QR#2…');
      return;
    }
    onFrameScanned(text, true);
  }

  $('btn-cam-scan').onclick = () => startCam('export');
  $('btn-cam-cancel').onclick = () => stopCam('已取消，摄像头已关闭');
  $('btn-cam-close').onclick = () => stopCam('已取消，摄像头已关闭');
  $('cam-modal').onclick = (e) => {
    if (e.target === $('cam-modal')) stopCam('已取消，摄像头已关闭');
  };

  let importFieldDefs = [];

  function splitOpt(o) {
    const s = String(o || '');
    const i = s.indexOf('|');
    if (i >= 0) return { value: s.slice(0, i).trim(), label: s.slice(i + 1).trim() };
    return { value: s, label: s };
  }

  function renderImportFields(fields) {
    const box = $('import-fields');
    if (!box) return;
    importFieldDefs = Array.isArray(fields) ? fields : [];
    box.innerHTML = importFieldDefs.map((f) => {
      const id = 'imp-f-' + f.key;
      const hint = f.hint ? '<div class="hint" style="margin:0 0 6px">' + escAttr(f.hint) + '</div>' : '';
      let ctrl = '';
      if (f.type === 'textarea') {
        const rows = f.key === 'reason' ? 4 : 3;
        ctrl = '<textarea id="' + id + '" rows="' + rows + '" data-imp-key="' + escAttr(f.key) + '">' +
          escAttr(f.default || '') + '</textarea>';
      } else if (f.type === 'select') {
        const opts = (f.options || []).map((o) => {
          const p = splitOpt(o);
          const sel = p.value === (f.default || '') ? ' selected' : '';
          return '<option value="' + escAttr(p.value) + '"' + sel + '>' + escAttr(p.label) + '</option>';
        }).join('');
        ctrl = '<select id="' + id + '" data-imp-key="' + escAttr(f.key) + '">' + opts + '</select>';
      } else {
        const typ = f.type === 'number' ? 'number' : 'text';
        ctrl = '<input id="' + id + '" type="' + typ + '" data-imp-key="' + escAttr(f.key) +
          '" value="' + escAttr(f.default || '') + '" />';
      }
      return '<label>' + escAttr(f.label) + (f.required ? ' *' : '') + '</label>' + hint + ctrl;
    }).join('');
  }

  function collectImportFields() {
    const out = {};
    document.querySelectorAll('[data-imp-key]').forEach((el) => {
      out[el.getAttribute('data-imp-key')] = (el.value || '').trim();
    });
    return out;
  }

  async function loadImportFields() {
    try {
      const d = await api('/api/import/fields');
      renderImportFields(d.fields || []);
    } catch (e) {
      renderImportFields([
        { key: 'reason', label: '事由', type: 'textarea', required: true, default: '导入资料' },
        { key: 'cls', label: '密级', type: 'select', required: true, default: 'confidential',
          options: ['internal', 'confidential', 'secret'] },
      ]);
    }
  }

  function updateImportUSBControls() {
    const browse = $('btn-usb-browse');
    const clear = $('btn-usb-clear');
    const start = $('btn-import-start');
    const ready = hasUSB && usbMounted;
    if (browse) browse.disabled = !ready || redeeming;
    if (clear) clear.disabled = !ready || Object.keys(importSelected).length === 0;
    if (start) start.disabled = !ready || Object.keys(importSelected).length === 0 || redeeming;
    renderImportSelection();
  }

  function renderImportSelection() {
    const keys = Object.keys(importSelected);
    const summary = $('imp-select-summary');
    const list = $('imp-selected');
    if (!summary || !list) return;
    if (!(hasUSB && usbMounted)) {
      summary.textContent = '未选择 · 请先插入 U 盘';
      list.innerHTML = '';
      return;
    }
    if (!keys.length) {
      summary.textContent = '未选择 · 点「浏览 U 盘」勾选文件或目录';
      list.innerHTML = '';
      return;
    }
    summary.textContent = '已选 ' + keys.length + ' 项';
    list.innerHTML = keys.map((p) => {
      const it = importSelected[p];
      const tag = it.type === 'dir' ? '[目录] ' : '';
      return '<li class="done" title="' + escAttr(p) + '">' + tag + escAttr(p) +
        ' <button type="button" data-unsel="' + escAttr(p) + '" style="margin-left:8px;padding:2px 8px;font-size:11px">移除</button></li>';
    }).join('');
  }

  async function loadUSBDir(path) {
    usbBrowsePath = path || '';
    const box = $('usb-browser');
    const entries = $('usb-entries');
    const pathEl = $('usb-path');
    box.classList.remove('hidden');
    pathEl.textContent = '/' + (usbBrowsePath || '');
    entries.innerHTML = '<div class="usb-row"><span class="nm">加载中…</span></div>';
    try {
      const q = usbBrowsePath ? ('?path=' + encodeURIComponent(usbBrowsePath)) : '';
      const d = await api('/api/usb/list' + q);
      const rows = [];
      if (usbBrowsePath) {
        rows.push('<button type="button" class="usb-row dir" data-up="1"><span></span><span class="nm">..（上级）</span><span class="meta">目录</span></button>');
      }
      (d.entries || []).forEach((e) => {
        const checked = !!importSelected[e.path];
        const meta = e.type === 'dir' ? '目录' : fmt(e.size || 0);
        rows.push(
          '<div class="usb-row ' + (e.type === 'dir' ? 'dir' : '') + '">' +
          '<input type="checkbox" data-sel="' + escAttr(e.path) + '" data-type="' + escAttr(e.type) +
          '" data-name="' + escAttr(e.name) + '"' + (checked ? ' checked' : '') + ' />' +
          (e.type === 'dir'
            ? '<button type="button" class="nm" data-enter="' + escAttr(e.path) + '" style="background:none;border:0;color:inherit;text-align:left;padding:0;font:inherit;cursor:pointer">' + escAttr(e.name) + '/</button>'
            : '<span class="nm">' + escAttr(e.name) + '</span>') +
          '<span class="meta">' + meta + '</span></div>'
        );
      });
      entries.innerHTML = rows.join('') || '<div class="usb-row"><span class="nm">空目录</span></div>';
    } catch (e) {
      entries.innerHTML = '<div class="usb-row"><span class="nm">' + escAttr(e.message || e) + '</span></div>';
      await alertError(e.message || e);
    }
  }

  $('btn-usb-browse').onclick = () => {
    if (!hasUSB || !usbMounted) {
      showModal('未检测到 U 盘', '请先插入源 U 盘后再浏览选择。', 'warn');
      return;
    }
    loadUSBDir('');
  };
  $('btn-usb-clear').onclick = () => {
    importSelected = {};
    updateImportUSBControls();
    if (!$('usb-browser').classList.contains('hidden')) loadUSBDir(usbBrowsePath);
  };
  $('usb-entries').onclick = (ev) => {
    const up = ev.target.closest('[data-up]');
    if (up) {
      const parts = usbBrowsePath.split('/').filter(Boolean);
      parts.pop();
      loadUSBDir(parts.join('/'));
      return;
    }
    const enter = ev.target.closest('[data-enter]');
    if (enter) {
      loadUSBDir(enter.getAttribute('data-enter'));
      return;
    }
  };
  $('usb-entries').onchange = (ev) => {
    const cb = ev.target.closest('[data-sel]');
    if (!cb) return;
    const path = cb.getAttribute('data-sel');
    if (cb.checked) {
      importSelected[path] = {
        path: path,
        type: cb.getAttribute('data-type'),
        name: cb.getAttribute('data-name'),
      };
    } else {
      delete importSelected[path];
    }
    updateImportUSBControls();
  };
  $('imp-selected').onclick = (ev) => {
    const btn = ev.target.closest('[data-unsel]');
    if (!btn) return;
    delete importSelected[btn.getAttribute('data-unsel')];
    updateImportUSBControls();
    if (!$('usb-browser').classList.contains('hidden')) loadUSBDir(usbBrowsePath);
  };

  $('btn-import-start').onclick = async () => {
    if (!requireUSB('生成导入单')) return;
    const paths = Object.keys(importSelected);
    if (!paths.length) {
      await showModal('未选择文件', '请点「浏览 U 盘」，勾选要导入的文件或目录。', 'warn');
      return;
    }
    importXferring = true;
    $('btn-import-start').disabled = true;
    $('import-out').textContent = '建单并上传隔离区中（可较久，请勿拔盘）…';
    $('import-xfer-box').classList.remove('hidden');
    $('import-xfer-path').textContent = '请勿拔盘 · 上传进行中';
    $('import-xfer-file').textContent = '正在索引 U 盘文件…';
    $('import-xfer-prog-wrap').classList.remove('hidden');
    $('import-xfer-prog-wrap').classList.add('indeterminate');
    $('import-xfer-prog-bar').style.width = '40%';
    $('import-xfer-pct').textContent = '正在计算哈希 / 建清单（大文件可能较久）…';
    const poll = setInterval(refresh, 400);
    try {
      const fields = collectImportFields();
      const r = await api('/api/import/start', {
        method: 'POST',
        body: JSON.stringify({
          fields: fields,
          reason: fields.reason || '',
          cls: fields.cls || '',
          paths: paths,
        }),
      });
      importReqId = r.reqId;
      const shortMeta = r.count + ' 个文件已进隔离区 · 扫码后可拔盘 · 审批后到「导入兑付」';
      const fullMeta = '申请单 ' + r.reqId + ' · ' + r.count + ' 文件 · Merkle ' + r.merkleRoot +
        '\n数据已进隔离区。请用飞书扫 QR#1；之后可拔盘离开，审批通过后到「导入兑付」扫 QR#2。';
      $('import-qr').src = 'data:image/png;base64,' + r.qr1PngBase64;
      $('import-qr-meta').textContent = shortMeta;
      $('import-meta').textContent = fullMeta;
      $('import-complete').classList.remove('hidden');
      $('btn-import-show-qr').classList.remove('hidden');
      $('import-out').textContent = '上传完成 · QR#1 已生成。可用飞书扫码；通过后到「导入兑付」。';
      $('import-xfer-path').textContent = '隔离区上传完成';
      $('import-xfer-file').textContent = '上传完成';
      $('import-xfer-prog-bar').style.width = '100%';
      refresh();
      openImportQrModal();
    } catch (e) {
      $('import-out').textContent = '失败: ' + (e.message || e);
      await alertError(e.message || e);
    } finally {
      clearInterval(poll);
      importXferring = false;
      updateImportUSBControls();
      refresh();
    }
  };

  function openImportQrModal() {
    const m = $('import-qr-modal');
    if (m) m.classList.remove('hidden');
  }

  function closeImportQrModal() {
    const m = $('import-qr-modal');
    if (m) m.classList.add('hidden');
  }

  $('btn-import-show-qr').onclick = () => {
    if (!importReqId) {
      showModal('尚未生成', '请先生成 QR#1。', 'warn');
      return;
    }
    openImportQrModal();
  };
  $('btn-import-qr-close').onclick = closeImportQrModal;
  $('btn-import-qr-ok').onclick = closeImportQrModal;
  $('import-qr-modal').onclick = (e) => {
    if (e.target === $('import-qr-modal')) closeImportQrModal();
  };

  async function completeImport(frame) {
    frame = (frame || '').trim();
    const out = $('import-redeem-out');
    if (!frame) {
      out.textContent = '缺少 QR#2';
      await showModal('缺少放行码', '请扫描或粘贴审批通过后的 QR#2。无需再插 U 盘。', 'warn');
      return;
    }
    importRedeeming = true;
    out.textContent = '兑付放行中…';
    $('import-redeem-box').classList.remove('hidden');
    $('import-redeem-file').textContent = '正在校验并放行隔离区（含扫描，大包可能较久）…';
    $('import-redeem-prog-bar').style.width = '20%';
    $('import-redeem-pct').textContent = '进行中…';
    const poll = setInterval(refresh, 400);
    try {
      const r = await api('/api/import/complete', {
        method: 'POST',
        body: JSON.stringify({ reqId: importReqId || '', frame }),
      });
      importReqId = r.reqId || importReqId;
      out.textContent = '✅ 导入兑付完成\n状态 ' + r.status + '\n目录 ' + (r.releasedDir || '');
      $('import-redeem-file').textContent = '放行完成';
      $('import-redeem-prog-bar').style.width = '100%';
      $('import-redeem-pct').textContent = '完成';
      refresh();
      await showModal('导入兑付完成', '状态 ' + r.status + '\n目录 ' + (r.releasedDir || '') +
        '\n可在 Portal「获取导入」下载。', 'ok');
    } catch (e) {
      out.textContent = '失败: ' + (e.message || e);
      await alertError(e.message || e);
    } finally {
      clearInterval(poll);
      importRedeeming = false;
      refresh();
    }
  }

  $('btn-import-done').onclick = () => completeImport(($('imp-frame').value || '').trim());
  $('btn-import-cam').onclick = () => startCam('import');

  loadImportFields();
  refresh();
  setInterval(refresh, 1500);
  // 默认不开摄像头；点「扫码」开预览，失败则回退本机 CAMERA；
  // 扫成功或满 60 秒未识别均自动关摄像头。
})();
