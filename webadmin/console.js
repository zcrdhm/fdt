(function () {
  const $ = (id) => document.getElementById(id);

  async function api(path, opts) {
    const r = await fetch(path, Object.assign({ credentials: 'same-origin' }, opts || {}));
    const text = await r.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = text; }
    if (!r.ok) {
      if (r.status === 401 && path !== '/console/api/login') {
        throw new Error('登录已过期，请重新登录（用户名是注册时设的账号，不是联系人姓名）');
      }
      const msg = typeof data === 'string' ? data : (data && data.error) || r.statusText;
      throw new Error(msg === 'unauthorized' || msg === 'invalid credentials'
        ? '账号或密码错误（用户名是注册账号，如 zcrdhm，不是「张长瑞」）'
        : msg);
    }
    return data;
  }

  function showApp(t) {
    $('login-view').classList.add('hidden');
    $('app-view').classList.remove('hidden');
    fill(t);
  }

  function fill(t) {
    $('who').textContent = t.id + (t.operatorUser ? ' · ' + t.operatorUser : '');
    $('status').textContent = t.status;
    $('status').className = 'badge ' + t.status;
    $('meta-line').textContent =
      'PK_内网: ' + (t.hasInternalPub ? '已上传' : '未上传') +
      ' · 飞书: ' + (t.feishuBound ? (t.feishuMode || 'ok') : '未绑定');

    const pkBadge = $('pk-badge');
    const pkStatus = $('pk-status');
    if (t.hasInternalPub) {
      pkBadge.textContent = '已上传';
      pkBadge.className = 'badge active';
      pkStatus.textContent = '当前状态：已上传 ✓（无需重复操作；仅在轮换内网密钥时再覆盖）';
      pkStatus.className = 'ok';
      $('btn-pk').textContent = '重新上传 / 覆盖';
    } else {
      pkBadge.textContent = '未上传';
      pkBadge.className = 'badge pending';
      pkStatus.textContent = '当前状态：未上传 — 请选择 pk_internal.pem 后点上传';
      pkStatus.className = 'sub';
      $('btn-pk').textContent = '上传';
    }

    const provider = t.approvalProvider || 'feishu';
    $('provider').value = provider;
    applyProviderUI(provider);

    const fsBadge = $('feishu-badge');
    const fsStatus = $('feishu-status');
    if (t.feishuBound) {
      fsBadge.textContent = '已绑定';
      fsBadge.className = 'badge active';
      const ch = providerLabel(provider);
      fsStatus.textContent = t.feishuMode === 'mock'
        ? '当前状态：已绑定（mock 自动审批）'
        : ('当前状态：已绑定（' + ch + ' · live）✓');
      fsStatus.className = 'ok';
    } else if (t.approvalProviderNote) {
      fsBadge.textContent = '待接入';
      fsBadge.className = 'badge pending';
      fsStatus.textContent = t.approvalProviderNote;
      fsStatus.className = 'sub';
    } else {
      fsBadge.textContent = '未绑定';
      fsBadge.className = 'badge pending';
      fsStatus.textContent = '当前状态：未绑定';
      fsStatus.className = 'sub';
    }
    $('provider-note').textContent = t.approvalProviderNote || '';

    $('dname').value = t.displayName || '';
    $('cname').value = t.contactName || '';
    $('cmail').value = t.contactEmail || '';
    $('notes').value = t.notes || '';
    if (t.feishuAppId) $('appId').value = t.feishuAppId;
    if (t.feishuApprovalCode) $('approvalCode').value = t.feishuApprovalCode;
    if (t.feishuApprovalCodeImport) $('approvalCodeImport').value = t.feishuApprovalCodeImport;
    $('mock').checked = t.feishuMode === 'mock';
    $('feishu-fields').classList.toggle('hidden', $('mock').checked);
    $('hint-secret').textContent = t.hasFeishuAppSecret ? '（已保存）' : '';
    $('hint-enc').textContent = t.hasFeishuEncryptKey ? '（已保存）' : '';
    $('hint-tok').textContent = t.hasFeishuVerifyToken ? '（已保存）' : '';
    $('cb').textContent = providerLabel(provider) + '回调 URL: ' + location.origin + '/callback/' + t.id;
  }

  function providerLabel(p) {
    if (p === 'dingtalk') return '钉钉';
    if (p === 'wecom') return '企业微信';
    return '飞书';
  }

  function applyProviderUI(p) {
    const feishu = !p || p === 'feishu';
    $('provider-note').textContent = feishu
      ? ''
      : (providerLabel(p) + '接口即将支持：可先填写并保存配置，目前仅飞书可真实发起审批。');
    // 回调验签字段目前仅飞书使用；其它渠道先收起避免误导
    $('callback-fields').classList.toggle('hidden', !feishu);
    $('lbl-app-id').textContent = feishu ? 'App ID' : (providerLabel(p) + ' AppKey / CorpId');
    $('lbl-app-secret').childNodes[0].textContent = feishu ? 'App Secret ' : (providerLabel(p) + ' AppSecret ');
    $('btn-feishu').textContent = '保存渠道配置';
  }

  async function boot() {
    try {
      const t = await api('/console/api/me');
      showApp(t);
    } catch (_) {
      $('login-view').classList.remove('hidden');
      $('app-view').classList.add('hidden');
    }
  }

  $('btn-login').onclick = async () => {
    $('login-err').textContent = '';
    try {
      await api('/console/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ user: $('login-user').value, password: $('login-pass').value }),
      });
      const t = await api('/console/api/me');
      showApp(t);
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
    await api('/console/api/logout', { method: 'POST' });
    location.reload();
  };

  $('mock').onchange = () => {
    $('feishu-fields').classList.toggle('hidden', $('mock').checked);
  };
  $('provider').onchange = () => applyProviderUI($('provider').value);

  $('btn-pk').onclick = async () => {
    $('pk-err').textContent = '';
    $('pk-ok').textContent = '';
    const f = $('pk-file').files[0];
    if (!f) { $('pk-err').textContent = '请选择文件'; return; }
    const fd = new FormData();
    fd.append('pk_internal', f);
    try {
      const t = await api('/console/api/pk-internal', { method: 'POST', body: fd });
      $('pk-ok').textContent = '上传成功：PK_内网已保存' + (t.hasInternalPub ? '（状态：已上传）' : '');
      $('pk-file').value = '';
      fill(t);
    } catch (e) {
      $('pk-err').textContent = e.message || String(e);
    }
  };

  $('btn-feishu').onclick = async () => {
    $('feishu-err').textContent = '';
    $('feishu-ok').textContent = '';
    const provider = $('provider').value || 'feishu';
    const body = $('mock').checked
      ? { mode: 'mock', provider: provider, autoApprove: true, approver: 'mock-approver' }
      : {
          mode: 'live',
          provider: provider,
          appId: $('appId').value.trim(),
          appSecret: $('appSecret').value.trim(),
          approvalCode: $('approvalCode').value.trim(),
          approvalCodeImport: $('approvalCodeImport').value.trim(),
          encryptKey: $('encryptKey').value.trim(),
          verificationToken: $('verificationToken').value.trim(),
        };
    try {
      const t = await api('/console/api/feishu', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      });
      $('feishu-ok').textContent = t.approvalProviderNote
        ? ('已保存（' + t.approvalProviderNote + '）')
        : '已保存';
      fill(t);
    } catch (e) {
      $('feishu-err').textContent = e.message || String(e);
      if (String(e.message || '').includes('重新登录')) {
        setTimeout(() => location.reload(), 1200);
      }
    }
  };

  $('btn-dl').onclick = () => {
    window.open('/console/api/pk-gateway', '_blank');
  };

  async function showGatewayPub() {
    const box = $('pk-view');
    const err = $('pk-view-err');
    const ok = $('pk-copy-ok');
    err.textContent = '';
    ok.textContent = '';
    try {
      const pem = await api('/console/api/pk-gateway?view=1', {
        headers: { Accept: 'text/plain' },
      });
      $('pk-pem').value = typeof pem === 'string' ? pem.trim() + '\n' : String(pem || '');
      box.classList.remove('hidden');
      $('pk-pem').focus();
      $('pk-pem').select();
    } catch (e) {
      box.classList.remove('hidden');
      err.textContent = e.message || String(e);
    }
  }

  $('btn-view-pk').onclick = () => showGatewayPub();
  $('btn-hide-pk').onclick = () => {
    $('pk-view').classList.add('hidden');
    $('pk-copy-ok').textContent = '';
    $('pk-view-err').textContent = '';
  };
  $('btn-copy-pk').onclick = async () => {
    const text = ($('pk-pem').value || '').trim();
    const ok = $('pk-copy-ok');
    const err = $('pk-view-err');
    err.textContent = '';
    ok.textContent = '';
    if (!text) {
      err.textContent = '暂无内容，请先点「查看密钥内容」';
      return;
    }
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text + '\n');
      } else {
        $('pk-pem').select();
        document.execCommand('copy');
      }
      ok.textContent = '已复制，可粘贴到 ATM 配置项';
    } catch (e) {
      $('pk-pem').select();
      err.textContent = '自动复制失败，请手动 Ctrl+C / ⌘C';
    }
  };

  $('btn-regen').onclick = async () => {
    if (!confirm('重新生成后旧 ATM 上的 pk_gateway 将失效，确认？')) return;
    const t = await api('/console/api/regen-gateway', { method: 'POST' });
    fill(t);
    $('pk-view').classList.add('hidden');
    $('pk-pem').value = '';
    alert('已重新生成，请重新下载或「查看密钥内容」后更新所有 ATM');
  };

  $('btn-profile').onclick = async () => {
    $('prof-err').textContent = '';
    $('prof-ok').textContent = '';
    try {
      const t = await api('/console/api/profile', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          displayName: $('dname').value.trim(),
          contactName: $('cname').value.trim(),
          contactEmail: $('cmail').value.trim(),
          notes: $('notes').value,
        }),
      });
      $('prof-ok').textContent = '资料已保存';
      fill(t);
    } catch (e) {
      $('prof-err').textContent = e.message || String(e);
    }
  };

  $('btn-pass').onclick = async () => {
    $('prof-err').textContent = '';
    $('prof-ok').textContent = '';
    try {
      await api('/console/api/password', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          oldPassword: $('old-pass').value,
          newPassword: $('new-pass').value,
        }),
      });
      $('prof-ok').textContent = '密码已修改';
      $('old-pass').value = '';
      $('new-pass').value = '';
    } catch (e) {
      $('prof-err').textContent = e.message || String(e);
    }
  };

  boot();
})();
