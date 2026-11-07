/* FDT 飞书网页应用：扫 QR#1 → 飞书授权身份 → POST /submit → 轮询 /qr2 → 展示 /qr2/png。
 * 申请人 = 当前飞书登录用户（谁扫码谁申请），ATM/Portal 导入无需再填飞书 ID。
 * 与网关同源部署时 GATEWAY 为空；也可 ?gateway=https://其他域名 覆盖。 */
(function () {
  const params = new URLSearchParams(location.search);
  const GATEWAY = (params.get('gateway') || '').replace(/\/$/, '');
  const POLL_MS = 3000;

  const state = {
    step: 'idle',
    reqId: '',
    instanceId: '',
    status: '',
    direction: '', // export | import
    manifestText: '',
    qr2Url: '',
    error: '',
    jsReady: false,
    appId: '',
    fromHistory: false,
  };

  const $ = (id) => document.getElementById(id);

  function show(step) {
    state.step = step;
    ['idle', 'history', 'submitting', 'pending', 'done', 'error'].forEach((name) => {
      const el = $('view-' + name);
      if (el) el.classList.toggle('hidden', name !== step);
    });
  }

  function pageURL(hash) {
    const base = location.pathname + location.search;
    if (!hash || hash === 'idle') return base;
    return base + '#/' + hash;
  }

  function stepFromHash() {
    const h = (location.hash || '').replace(/^#\/?/, '');
    if (['history', 'done', 'error', 'pending', 'submitting'].indexOf(h) >= 0) return h;
    return 'idle';
  }

  // 子页 push 一条历史，飞书顶栏「返回」才会回到首页而不是退出应用。
  function navigate(step, mode) {
    show(step);
    const url = pageURL(step === 'idle' ? '' : step);
    try {
      if (mode === 'silent') return;
      if (mode === 'replace') {
        history.replaceState({ step: step }, '', url);
        return;
      }
      const cur = (history.state && history.state.step) || stepFromHash();
      if (cur === step) history.replaceState({ step: step }, '', url);
      else history.pushState({ step: step }, '', url);
    } catch (_) {}
  }

  function applyStepFromHistory(step) {
    if (step === 'idle') {
      state.fromHistory = false;
      show('idle');
      return;
    }
    if (step === 'history') {
      // 只恢复列表页；数据可能需刷新
      show('history');
      return;
    }
    show(step);
  }

  function goHome() {
    state.reqId = '';
    state.instanceId = '';
    state.status = '';
    state.direction = '';
    state.manifestText = '';
    state.qr2Url = '';
    state.error = '';
    state.fromHistory = false;
    if ($('frame-input')) $('frame-input').value = '';
    navigate('idle', 'replace');
  }

  function setError(msg, opts) {
    state.error = msg;
    if (opts && opts.fromHistory) state.fromHistory = true;
    $('error-text').textContent = msg;
    const backBtn = $('btn-reset-err');
    if (backBtn) backBtn.textContent = state.fromHistory ? '返回历史' : '返回首页';
    navigate('error');
  }

  function leaveError() {
    if (state.fromHistory) {
      state.error = '';
      // 失效提示是从历史页 push 出来的，返回应回到历史列表
      try {
        if (history.state && history.state.step === 'error') {
          history.back();
          return;
        }
      } catch (_) {}
      loadHistory();
      return;
    }
    goHome();
  }

  function api(path, opts) {
    return fetch(GATEWAY + path, opts).then(async (r) => {
      const text = await r.text();
      let data = null;
      try { data = text ? JSON.parse(text) : null; } catch (_) { data = text; }
      return { ok: r.ok, status: r.status, data };
    });
  }

  function reset() {
    // 若当前是 push 出来的子页，优先走浏览器/飞书返回栈。
    if (state.step !== 'idle' && history.state && history.state.step && history.state.step !== 'idle') {
      try {
        history.back();
        return;
      } catch (_) {}
    }
    goHome();
  }

  function isImport(dir) {
    return String(dir || state.direction || '').toLowerCase() === 'import';
  }

  function qr2Copy(dir, fromHistory) {
    if (isImport(dir)) {
      return {
        title: '审批通过 · 导入放行码',
        hint: fromHistory
          ? '历史导入放行码（未过期）。到 ATM「导入兑付」扫此码即可放行，无需再插同一块 U 盘。'
          : '到 ATM 打开「导入兑付」，扫此码完成放行。无需 U 盘（数据已在隔离区）。',
      };
    }
    return {
      title: '审批通过 · 导出放行码',
      hint: fromHistory
        ? '历史导出放行码（未过期）。到 ATM「导出兑付」：先插 U 盘，再扫此码写盘取数。'
        : '到 ATM 打开「导出兑付」：先插入 U 盘，再扫此码，将数据写入 U 盘。',
    };
  }

  function showQR2(reqId, status, direction) {
    state.reqId = reqId;
    state.status = status || 'APPROVED';
    if (direction) state.direction = direction;
    state.qr2Url = GATEWAY + '/qr2/png?reqId=' + encodeURIComponent(reqId) + '&t=' + Date.now();
    $('qr2-img').src = state.qr2Url;
    const copy = qr2Copy(state.direction, state.fromHistory);
    const title = document.querySelector('#view-done .done-title');
    if (title) title.textContent = copy.title;
    $('done-hint').textContent = copy.hint;
    navigate('done');
  }

  function pagePath() {
    return location.href.split('?')[0].split('#')[0];
  }

  function updateAuthHelp(extra) {
    const el = $('auth-help');
    if (!el) return;
    el.textContent =
      '当前页面（须原样加入「重定向 URL」）:\n' + pagePath() + '\n\n' +
      '网关 AppId:\n' + (state.appId || '(尚未拿到)') + '\n\n' +
      '开放平台：安全设置 → 重定向 URL 增加上面整行（推荐 https://fdt.do2.cn/app 无尾斜杠）→ 创建版本并发布 → 完全退出飞书再进本应用\n' +
      (extra ? ('\n' + extra) : '');
  }

  function showIdentity(info, note) {
    const label = $('bind-id-label');
    const value = $('bind-id-value');
    const copyBtn = $('btn-copy-bind-id');
    const hint = $('auth-diag');
    if (!info || !info.applicantId) {
      if (label) label.textContent = note || '尚未获取到飞书身份';
      if (value) value.textContent = '—';
      if (copyBtn) copyBtn.disabled = true;
      if (hint) hint.textContent = note || '';
      return;
    }
    // 优先 user_id（Portal 绑定正确值）；权限未生效时暂显示 open_id。
    const bindID = info.userId || info.applicantId || info.openId || '';
    const isUserID = !!info.userId;
    if (label) {
      label.textContent = (info.name ? info.name + ' · ' : '') +
        (isUserID
          ? '复制下面 ID 到 Portal「飞书 ID」绑定（飞书用户 ID）'
          : '暂未取到 user_id，先显示 open_id（开通权限并发布后再进本页）');
    }
    if (value) value.textContent = bindID || '—';
    if (copyBtn) copyBtn.disabled = !bindID;
    if (hint) {
      if (isUserID) {
        hint.textContent = note || '此为飞书「用户 ID」。粘贴到 Portal「飞书 ID」页即可。';
      } else {
        hint.textContent = note ||
          '请确认已勾选 contact:user.employee_id:readonly → 创建版本并发布 → 完全退出飞书再进。发布生效后这里应显示类似 g976f94f 的短 ID。';
      }
    }
  }

  function refreshIdentity() {
    const fetchBtn = $('btn-fetch-bind-id');
    if (fetchBtn) {
      fetchBtn.disabled = true;
      fetchBtn.textContent = '获取中…';
    }
    showIdentity(null, '正在授权并解析 userid…');
    return requestAuthCode()
      .then((authCode) => api('/whoami', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ authCode }),
      }))
      .then((r) => {
        if (!r.ok) {
          const msg = typeof r.data === 'string' ? r.data : ('HTTP ' + r.status);
          showIdentity(null, '身份解析失败: ' + msg);
          updateAuthHelp('最近失败: ' + msg);
          return;
        }
        state.identity = r.data;
        showIdentity(r.data);
        updateAuthHelp();
      })
      .catch((err) => {
        const msg = err && (err.message || String(err)) || '未知错误';
        showIdentity(null, '未能获取身份: ' + msg);
        updateAuthHelp('最近失败: ' + msg);
      })
      .finally(() => {
        if (fetchBtn) {
          fetchBtn.disabled = false;
          fetchBtn.textContent = '获取我的飞书 ID';
        }
      });
  }

  function requestAuthCode() {
    return ensureJSAPI().then(() => new Promise((resolve, reject) => {
      const tt = window.tt;
      if (!tt || (typeof tt.requestAuthCode !== 'function' && typeof tt.requestAccess !== 'function')) {
        reject(new Error('当前环境无法获取飞书身份，请在飞书客户端内打开本应用'));
        return;
      }
      if (!state.appId) {
        reject(new Error('缺少飞书 AppID，无法授权'));
        return;
      }
      // 与飞书重定向 URL 常见配置 https://host/app（无尾斜杠）对齐
      try {
        if (location.pathname === '/app/' || /\/app\/+$/i.test(location.pathname)) {
          history.replaceState(null, '', '/app' + location.search + location.hash);
        }
      } catch (_) {}
      updateAuthHelp();
      const errText = (err) => {
        if (!err) return 'unknown';
        const parts = [];
        if (err.errno != null) parts.push('errno=' + err.errno);
        if (err.errMsg) parts.push(err.errMsg);
        if (err.errString) parts.push(err.errString);
        if (!parts.length) parts.push(JSON.stringify(err));
        return parts.join(' ');
      };
      const fail = (err) => {
        const detail = errText(err);
        updateAuthHelp('最近一次授权失败: ' + detail);
        reject(new Error(
          '飞书身份授权失败: ' + detail +
          '；页面=' + pagePath() + '；appId=' + state.appId +
          '（请把该页面原样配进该 appId 的重定向 URL，并重新发布应用）'
        ));
      };
      const ok = (res) => {
        const code = (res && (res.code || res.authCode)) || '';
        if (!code) reject(new Error('飞书未返回授权码'));
        else resolve(code);
      };
      // 免登用 requestAuthCode（需重定向 URL）。requestAccess 为增量授权，空 scope 时常失败，不作首选。
      if (typeof tt.requestAuthCode !== 'function') {
        fail({ errMsg: '无 requestAuthCode' });
        return;
      }
      tt.requestAuthCode({ appId: state.appId, success: ok, fail: fail });
    }));
  }

  function submit(frame) {
    frame = (frame || '').trim();
    if (!frame || frame.indexOf('FDT1.') !== 0) {
      setError('不是有效的摆渡申请二维码');
      return;
    }
    state.fromHistory = false;
    navigate('submitting');
    // 先拿飞书登录用户授权码，网关用其覆盖 QR#1 里的申请人（谁扫码谁申请）。
    let authErrMsg = '';
    requestAuthCode()
      .catch((err) => {
        // mock / 非飞书环境：无 authCode 提交，仅 mock 飞书会接受；live 会由网关拒绝并拼上本错误。
        authErrMsg = err && (err.message || String(err)) || '未知授权错误';
        console.warn(authErrMsg);
        updateAuthHelp('最近一次授权失败: ' + authErrMsg);
        return '';
      })
      .then((authCode) => api('/submit', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ frame: frame, authCode: authCode || '' }),
      }))
      .then((r) => {
        if (!r.ok) {
          let msg = typeof r.data === 'string' ? r.data : String(r.status);
          if (authErrMsg && /谁扫码谁申请|授权后提交|authCode|飞书身份|invalid url/i.test(msg + authErrMsg)) {
            msg = '未能获取飞书登录授权（authCode 为空）。\n' +
              authErrMsg + '\n\n' +
              '请把下面地址原样加入【该 AppId】的重定向 URL，并重新发布应用：\n' +
              pagePath() + '\nAppId=' + (state.appId || '?');
          }
          setError('网关校验失败: ' + msg);
          return;
        }
        state.reqId = r.data.reqId || r.data.ReqID || '';
        state.instanceId = r.data.instanceId || r.data.InstanceID || '';
        state.status = r.data.status || r.data.Status || '';
        state.direction = r.data.direction || r.data.Direction || state.direction || '';
        state.manifestText = r.data.manifestText || r.data.ManifestText || '';
        $('req-id').textContent = state.reqId;
        $('instance-id').textContent = state.instanceId;
        $('status').textContent = state.status + (state.direction ? ' · ' + (isImport() ? '导入' : '导出') : '');
        $('manifest').textContent = state.manifestText;
        navigate('pending', 'replace');
        poll();
      })
      .catch((err) => setError('网络错误: ' + (err.message || err)));
  }

  function poll() {
    if (state.step === 'done') return;
    api('/qr2?reqId=' + encodeURIComponent(state.reqId), { method: 'GET' })
      .then((r) => {
        if (r.ok && r.data) {
          const dir = r.data.direction || r.data.Direction || state.direction;
          if (dir) state.direction = dir;
        }
        if (r.ok && r.data && (r.data.qr2Frame || r.data.QR2Frame)) {
          showQR2(state.reqId, r.data.status || r.data.Status || 'APPROVED',
            r.data.direction || r.data.Direction || state.direction);
          return;
        }
        if (r.ok && r.data) {
          state.status = r.data.status || r.data.Status || state.status;
          $('status').textContent = state.status + (state.direction ? ' · ' + (isImport() ? '导入' : '导出') : '');
        }
        setTimeout(poll, POLL_MS);
      })
      .catch(() => setTimeout(poll, POLL_MS));
  }

  function statusBadge(it) {
    const st = String(it.status || '');
    const imp = isImport(it.direction);
    // 仅 qr2Valid=true 才算可兑付；过期的 APPROVED 必须标失效（勿用 hasQR2||APPROVED 误判）。
    if (it.qr2Valid) {
      return { cls: 'ok', text: imp ? '可导入兑付' : '可导出兑付' };
    }
    if (st === 'APPROVED' && it.hasQR2) {
      return { cls: 'bad', text: imp ? '导入已失效' : '导出已失效' };
    }
    if (st === 'PENDING' || st === 'pending') return { cls: 'wait', text: '审批中' };
    if (st === 'REJECTED' || st === 'rejected') return { cls: 'bad', text: '已拒绝' };
    return { cls: '', text: st || '未知' };
  }

  function truncText(s, max) {
    s = String(s || '');
    if (!max || s.length <= max) return s;
    return s.slice(0, Math.max(1, max - 1)) + '…';
  }

  function shortReqId(id) {
    id = String(id || '');
    if (id.length <= 12) return id;
    return id.slice(0, 8) + '…' + id.slice(-4);
  }

  function filesLine(it) {
    const names = it.fileNames || [];
    const count = it.fileCount || names.length || 0;
    if (!names.length) {
      return count > 0 ? ('共 ' + count + ' 个文件') : '';
    }
    const shown = names.slice(0, 2).map((n) => {
      const base = String(n).split('/').pop() || n;
      return truncText(base, 28);
    });
    let line = shown.join('、');
    if (count > shown.length) line += ' 等' + count + '个';
    return line;
  }

  function loadHistory(opts) {
    if (!(opts && opts.silent)) navigate('history');
    else show('history');
    $('history-list').innerHTML = '<p class="hint">加载中…</p>';
    $('history-empty').classList.add('hidden');
    api('/history?limit=50', { method: 'GET' }).then((r) => {
      const list = $('history-list');
      list.innerHTML = '';
      const items = (r.ok && r.data && r.data.items) ? r.data.items : [];
      if (!items.length) {
        list.innerHTML = '';
        $('history-empty').classList.remove('hidden');
        $('history-empty').textContent = '暂无记录（提交过审批的申请会出现在这里）';
        return;
      }
      $('history-empty').classList.add('hidden');
      items.forEach((it) => {
        const b = statusBadge(it);
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'hist-item' + (b.cls === 'bad' && String(it.status) === 'APPROVED' ? ' expired' : '');
        const openable = !!it.qr2Valid;
        const expired = String(it.status) === 'APPROVED' && !!it.hasQR2 && !it.qr2Valid;
        // 可兑付可点；已失效也可点（提示重新发起）；审批中/拒绝不可点开 QR#2
        btn.disabled = !openable && !expired;
        const dirLabel = isImport(it.direction) ? '导入' : (it.direction ? '导出' : '');
        const files = filesLine(it);
        btn.innerHTML =
          '<div class="hist-top"><span class="hist-id" title="' + esc(it.reqId) + '">' + esc(shortReqId(it.reqId)) + '</span>' +
          '<span class="hist-badge ' + b.cls + '">' + esc(b.text) + '</span></div>' +
          (files ? '<div class="hist-files" title="' + esc((it.fileNames || []).join('\n')) + '">' + esc(files) + '</div>' : '') +
          '<div class="hist-meta">' +
          (dirLabel ? esc(dirLabel) + ' · ' : '') +
          esc(it.applicant || '') + (it.reason ? ' · ' + esc(truncText(it.reason, 24)) : '') +
          (it.tenant ? '<br/>租户 ' + esc(it.tenant) : '') +
          (it.exp ? '<br/>QR#2 到期 ' + new Date(it.exp * 1000).toLocaleString() +
            (expired ? ' · 已失效' : '') : '') +
          '</div>';
        btn.onclick = () => {
          if (!it.hasQR2 || String(it.status) !== 'APPROVED') return;
          state.fromHistory = true;
          if (!it.qr2Valid) {
            setError((isImport(it.direction) ? '导入' : '导出') +
              '放行码已失效，请重新扫 QR#1 发起审批', { fromHistory: true });
            return;
          }
          showQR2(it.reqId, it.status, it.direction);
        };
        list.appendChild(btn);
      });
    }).catch((err) => {
      $('history-list').innerHTML = '';
      setError('加载历史失败: ' + (err.message || err));
    });
  }

  function esc(s) {
    return String(s || '').replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    })[c]);
  }

  function ensureJSAPI() {
    if (state.jsReady && state.appId) return Promise.resolve();
    if (!window.h5sdk || typeof window.h5sdk.config !== 'function') {
      return Promise.reject(new Error('请在飞书客户端内打开本应用，或使用下方手动粘贴'));
    }
    const pageURL = location.href.split('#')[0];
    return api('/jsapi/config?url=' + encodeURIComponent(pageURL), { method: 'GET' })
      .then((r) => {
        if (!r.ok || !r.data) {
          return Promise.reject(new Error(typeof r.data === 'string' ? r.data : 'JSAPI 鉴权参数获取失败'));
        }
        if (r.data.mock) {
          return Promise.reject(new Error('扫码需真实飞书 JSAPI：请在 /console 绑定 live 飞书并确保应用已发布；或先用下方手动粘贴令牌'));
        }
        state.appId = r.data.appId || '';
        return new Promise((resolve, reject) => {
          window.h5sdk.config({
            appId: r.data.appId,
            timestamp: Number(r.data.timestamp),
            nonceStr: r.data.nonceStr,
            signature: r.data.signature,
            jsApiList: ['scanCode', 'requestAuthCode'],
            onSuccess: () => { state.jsReady = true; wireFeishuBack(); resolve(); },
            onFail: (err) => reject(new Error('JSAPI 鉴权失败: ' + JSON.stringify(err))),
          });
        });
      });
  }

  function scanQR1() {
    ensureJSAPI()
      .then(() => new Promise((resolve, reject) => {
        const fn = (window.tt && window.tt.scanCode) || (window.h5sdk && window.h5sdk.scanCode);
        if (!fn) {
          reject(new Error('当前环境不支持扫码'));
          return;
        }
        fn.call(window.tt || window.h5sdk, {
          scanType: ['qrCode'],
          success: (res) => resolve(res.result),
          fail: (err) => reject(new Error('扫码失败: ' + (err.errMsg || JSON.stringify(err)))),
        });
      }))
      .then(submit)
      .catch((err) => setError(err.message || String(err)));
  }

  window.addEventListener('popstate', (ev) => {
    const step = (ev.state && ev.state.step) || stepFromHash();
    applyStepFromHistory(step);
    if (step === 'history') loadHistory({ silent: true });
  });

  function wireFeishuBack() {
    try {
      const tt = window.tt;
      if (!tt || typeof tt.onHistoryBack !== 'function') return;
      tt.onHistoryBack(() => {
        if (state.step && state.step !== 'idle') {
          // 优先消费 H5 history，回到上一屏（历史→首页）
          try { history.back(); } catch (_) { goHome(); }
          return true; // 拦截，不退出网页应用
        }
        return false;
      });
    } catch (_) {}
  }

  $('btn-scan').addEventListener('click', scanQR1);
  $('btn-paste').addEventListener('click', () => submit($('frame-input').value));
  $('btn-reset-done').addEventListener('click', goHome);
  $('btn-reset-err').addEventListener('click', leaveError);
  $('btn-history').addEventListener('click', loadHistory);
  $('btn-history-back').addEventListener('click', () => {
    if (history.state && history.state.step === 'history') history.back();
    else goHome();
  });
  $('btn-done-history').addEventListener('click', () => {
    if (history.state && history.state.step === 'done') history.back();
    else loadHistory();
  });

  $('btn-fetch-bind-id').addEventListener('click', () => {
    refreshIdentity();
  });

  $('btn-copy-bind-id').addEventListener('click', async () => {
    const id = ($('bind-id-value').textContent || '').trim();
    if (!id || id === '—') return;
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(id);
      } else {
        const ta = document.createElement('textarea');
        ta.value = id;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      }
      $('btn-copy-bind-id').textContent = '已复制';
      setTimeout(() => { $('btn-copy-bind-id').textContent = '复制到剪贴板'; }, 1600);
    } catch (err) {
      $('auth-diag').textContent = '复制失败，请长按上方 ID 手动选择复制';
    }
  });

  updateAuthHelp();
  showIdentity(null, '点「获取我的飞书 ID」后再请求。');
  try {
    const boot = stepFromHash();
    history.replaceState({ step: boot === 'idle' ? 'idle' : boot }, '', pageURL(boot === 'idle' ? '' : boot));
    if (boot === 'history') loadHistory({ silent: true });
    else if (boot !== 'idle') applyStepFromHistory(boot);
  } catch (_) {}
  wireFeishuBack();
})();
