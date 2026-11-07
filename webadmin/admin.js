(function () {
  const $ = (id) => document.getElementById(id);
  let publicBase = '';

  async function api(path, opts) {
    const r = await fetch(path, Object.assign({ credentials: 'same-origin' }, opts || {}));
    const text = await r.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = text; }
    if (!r.ok) throw new Error(typeof data === 'string' ? data : (data && data.error) || r.statusText);
    return data;
  }

  function originBase() {
    return publicBase || location.origin;
  }

  function parseAliases(s) {
    return String(s || '').split(/[,;\s]+/).map((x) => x.trim()).filter(Boolean);
  }

  function showApp(user) {
    $('login-view').classList.add('hidden');
    $('app-view').classList.remove('hidden');
    $('who').textContent = user || 'admin';
    loadDomain();
    loadAcme();
    refresh();
  }

  async function loadDomain() {
    try {
      const st = await api('/admin/api/domain');
      $('domain-status').textContent = st.message || (st.bound ? '已绑定' : '未绑定');
      if (st.domain) $('domain-host').value = st.domain;
      if (st.aliases && st.aliases.length) $('domain-aliases').value = st.aliases.join(', ');
      if (st.email) $('domain-email').value = st.email;
      publicBase = st.publicBase || '';
      if (publicBase) {
        $('domain-hint').textContent = publicBase + '/app/';
      }
    } catch (e) {
      $('domain-status').textContent = e.message || String(e);
    }
  }

  $('btn-domain-bind').onclick = async () => {
    $('domain-err').textContent = '';
    $('domain-ok').textContent = '';
    $('domain-dns').textContent = '';
    try {
      const r = await api('/admin/api/domain', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          domain: $('domain-host').value.trim(),
          aliases: parseAliases($('domain-aliases').value),
          email: $('domain-email').value.trim(),
        }),
      });
      $('domain-ok').textContent = r.message || '已绑定';
      await loadDomain();
      loadAcme();
      refresh();
    } catch (e) {
      $('domain-err').textContent = e.message || String(e);
    }
  };

  $('btn-domain-check').onclick = async () => {
    $('domain-err').textContent = '';
    $('domain-ok').textContent = '';
    try {
      const r = await api('/admin/api/domain/check', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          domain: $('domain-host').value.trim(),
          aliases: parseAliases($('domain-aliases').value),
        }),
      });
      const lines = (r.checks || []).map((c) => {
        const ips = (c.resolved || []).join(', ') || '（无记录）';
        const local = (c.localIps || []).join(', ') || '—';
        return c.host + '\n  解析: ' + ips + '\n  本机: ' + local + '\n  ' + (c.message || '');
      });
      $('domain-dns').textContent = lines.join('\n\n') + (r.message ? '\n\n' + r.message : '');
      $('domain-ok').textContent = '检测完成';
    } catch (e) {
      $('domain-err').textContent = e.message || String(e);
    }
  };

  async function loadAcme() {
    try {
      const st = await api('/admin/api/acme');
      $('acme-status').textContent = (st.message || '') +
        (st.domain ? ' · ' + st.domain : '') +
        (st.aliases && st.aliases.length ? ' (+' + st.aliases.join(',') + ')' : '') +
        (st.hasCache ? ' · 缓存已有证书材料' : '');
    } catch (e) {
      $('acme-status').textContent = e.message || String(e);
    }
  }

  $('btn-acme').onclick = async () => {
    $('acme-err').textContent = '';
    $('acme-ok').textContent = '';
    try {
      const r = await api('/admin/api/acme', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          enabled: true,
          domain: $('domain-host').value.trim(),
          aliases: parseAliases($('domain-aliases').value),
          email: $('domain-email').value.trim(),
        }),
      });
      $('acme-ok').textContent = r.message || '已启用';
      loadAcme();
      loadDomain();
    } catch (e) {
      $('acme-err').textContent = e.message || String(e);
    }
  };

  function showLogin() {
    $('app-view').classList.add('hidden');
    $('login-view').classList.remove('hidden');
  }

  async function boot() {
    try {
      const me = await api('/admin/api/me');
      showApp(me.user);
    } catch (_) {
      showLogin();
    }
  }

  $('btn-login').onclick = async () => {
    $('login-err').textContent = '';
    try {
      const me = await api('/admin/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ user: $('login-user').value, password: $('login-pass').value }),
      });
      showApp(me.user);
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
    await api('/admin/api/logout', { method: 'POST' });
    showLogin();
  };

  $('btn-password').onclick = async () => {
    $('pw-err').textContent = '';
    $('pw-ok').textContent = '';
    const neu = $('pw-new').value;
    if (neu.length < 8) {
      $('pw-err').textContent = '新密码至少 8 位';
      return;
    }
    if (neu !== $('pw-new2').value) {
      $('pw-err').textContent = '两次输入的新密码不一致';
      return;
    }
    try {
      const r = await api('/admin/api/password', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          oldPassword: $('pw-old').value,
          newPassword: neu,
        }),
      });
      $('pw-ok').textContent = r.message || '密码已修改';
      $('pw-old').value = '';
      $('pw-new').value = '';
      $('pw-new2').value = '';
    } catch (e) {
      $('pw-err').textContent = e.message || String(e);
    }
  };

  $('btn-create').onclick = async () => {
    $('create-err').textContent = '';
    $('create-invite').textContent = '';
    try {
      const v = await api('/admin/api/tenants', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ id: $('new-id').value.trim(), displayName: $('new-name').value.trim() }),
      });
      const url = originBase() + (v.registerUrlPath || '');
      $('create-invite').textContent = '注册链接（发给客户）：' + url;
      $('new-id').value = '';
      $('new-name').value = '';
      refresh();
    } catch (e) {
      $('create-err').textContent = e.message || String(e);
    }
  };

  $('btn-refresh').onclick = () => refresh();

  async function refresh() {
    const data = await api('/admin/api/tenants');
    if (data.publicBase) publicBase = data.publicBase;
    const tb = $('tenant-body');
    tb.innerHTML = '';
    (data.tenants || []).forEach((t) => {
      const tr = document.createElement('tr');
      const reg = t.registerUrlPath
        ? `<div class="mono">${esc(originBase())}${esc(t.registerUrlPath)}</div>`
        : (t.operatorUser ? '<span class="sub">已注册 · ' + esc(t.operatorUser) + '</span>' : '—');
      tr.innerHTML = `
        <td><strong>${esc(t.id)}</strong><div class="sub">${esc(t.displayName || '')}</div></td>
        <td><span class="badge ${esc(t.status)}">${esc(t.status)}</span></td>
        <td>
          PK_内网: ${t.hasInternalPub ? '✓' : '✗'} · 飞书: ${t.feishuBound ? '✓' : '✗'}<br/>
          <span class="sub">${esc(t.contactEmail || '')}</span>
        </td>
        <td>${reg}</td>
        <td class="actions"></td>`;
      const actions = tr.querySelector('.actions');
      addBtn(actions, '换发注册链接', 'ghost', () => inviteOf(t.id));
      if (t.status === 'disabled') addBtn(actions, '启用', 'ghost', () => patch(t.id, { status: 'active' }));
      else addBtn(actions, '禁用', 'ghost', () => patch(t.id, { status: 'disabled' }));
      addBtn(actions, '删除', 'danger', () => del(t.id));
      tb.appendChild(tr);
    });
  }

  function addBtn(parent, label, cls, fn) {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = label;
    b.className = cls || '';
    b.onclick = fn;
    parent.appendChild(b);
  }

  async function inviteOf(id) {
    const v = await api('/admin/api/tenants/' + encodeURIComponent(id) + '/invite', { method: 'POST' });
    alert('注册链接：' + originBase() + (v.registerUrlPath || ''));
    refresh();
  }

  async function patch(id, body) {
    await api('/admin/api/tenants/' + encodeURIComponent(id), {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    });
    refresh();
  }

  async function del(id) {
    if (!confirm('确认删除租户 ' + id + '？')) return;
    await api('/admin/api/tenants/' + encodeURIComponent(id), { method: 'DELETE' });
    refresh();
  }

  function esc(s) {
    return String(s || '').replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  boot();
})();
