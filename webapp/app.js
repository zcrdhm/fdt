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

  function setError(msg) {
    state.error = msg;
    $('error-text').textContent = msg;
    show('error');
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
    state.reqId = '';
    state.instanceId = '';
    state.status = '';
    state.manifestText = '';
    state.qr2Url = '';
    state.error = '';
    state.fromHistory = false;
    $('frame-input').value = '';
    show('idle');
  }

  function showQR2(reqId, status) {
    state.reqId = reqId;
    state.status = status || 'APPROVED';
    state.qr2Url = GATEWAY + '/qr2/png?reqId=' + encodeURIComponent(reqId) + '&t=' + Date.now();
    $('qr2-img').src = state.qr2Url;
    $('done-hint').textContent = state.fromHistory
      ? '历史放行码（未过期）。持此码到摆渡终端取数或完成导入。'
      : '持此二维码到摆渡终端：先插入 U 盘，再扫码取数 / 完成导入。';
    show('done');
  }

  function requestAuthCode() {
    return ensureJSAPI().then(() => new Promise((resolve, reject) => {
      const tt = window.tt;
      if (!tt || typeof tt.requestAuthCode !== 'function') {
        reject(new Error('当前环境无法获取飞书身份，请在飞书客户端内打开本应用'));
        return;
      }
      if (!state.appId) {
        reject(new Error('缺少飞书 AppID，无法授权'));
        return;
      }
      tt.requestAuthCode({
        appId: state.appId,
        success: (res) => {
          const code = (res && (res.code || res.authCode)) || '';
          if (!code) reject(new Error('飞书未返回授权码'));
          else resolve(code);
        },
        fail: (err) => reject(new Error('飞书身份授权失败: ' + (err.errMsg || JSON.stringify(err)))),
      });
    }));
  }

  function submit(frame) {
    frame = (frame || '').trim();
    if (!frame || frame.indexOf('FDT1.') !== 0) {
      setError('不是有效的摆渡申请二维码');
      return;
    }
    state.fromHistory = false;
    show('submitting');
    // 先拿飞书登录用户授权码，网关用其覆盖 QR#1 里的申请人（谁扫码谁申请）。
    requestAuthCode()
      .catch((err) => {
        // mock / 非飞书环境：无 authCode 提交，仅 mock 飞书会接受
        console.warn(err.message || err);
        return '';
      })
      .then((authCode) => api('/submit', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ frame: frame, authCode: authCode || '' }),
      }))
      .then((r) => {
        if (!r.ok) {
          setError('网关校验失败: ' + (typeof r.data === 'string' ? r.data : r.status));
          return;
        }
        state.reqId = r.data.reqId || r.data.ReqID || '';
        state.instanceId = r.data.instanceId || r.data.InstanceID || '';
        state.status = r.data.status || r.data.Status || '';
        state.manifestText = r.data.manifestText || r.data.ManifestText || '';
        $('req-id').textContent = state.reqId;
        $('instance-id').textContent = state.instanceId;
        $('status').textContent = state.status;
        $('manifest').textContent = state.manifestText;
        show('pending');
        poll();
      })
      .catch((err) => setError('网络错误: ' + (err.message || err)));
  }

  function poll() {
    if (state.step === 'done') return;
    api('/qr2?reqId=' + encodeURIComponent(state.reqId), { method: 'GET' })
      .then((r) => {
        if (r.ok && r.data && (r.data.qr2Frame || r.data.QR2Frame)) {
          showQR2(state.reqId, r.data.status || r.data.Status || 'APPROVED');
          return;
        }
        if (r.ok && r.data) {
          state.status = r.data.status || r.data.Status || state.status;
          $('status').textContent = state.status;
        }
        setTimeout(poll, POLL_MS);
      })
      .catch(() => setTimeout(poll, POLL_MS));
  }

  function statusBadge(it) {
    const st = String(it.status || '');
    if (it.qr2Valid || (it.hasQR2 && st === 'APPROVED')) return { cls: 'ok', text: '可取数' };
    if (st === 'APPROVED' && it.hasQR2) return { cls: 'bad', text: '已过期' };
    if (st === 'PENDING' || st === 'pending') return { cls: 'wait', text: '审批中' };
    if (st === 'REJECTED' || st === 'rejected') return { cls: 'bad', text: '已拒绝' };
    return { cls: '', text: st || '未知' };
  }

  function loadHistory() {
    show('history');
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
        btn.className = 'hist-item';
        const openable = !!it.qr2Valid;
        btn.disabled = !openable && !(it.hasQR2 && String(it.status) === 'APPROVED');
        btn.innerHTML =
          '<div class="hist-top"><span class="hist-id">' + esc(it.reqId) + '</span>' +
          '<span class="hist-badge ' + b.cls + '">' + esc(b.text) + '</span></div>' +
          '<div class="hist-meta">' +
          esc(it.applicant || '') + (it.reason ? ' · ' + esc(it.reason) : '') +
          (it.tenant ? '<br/>租户 ' + esc(it.tenant) : '') +
          (it.exp ? '<br/>QR#2 到期 ' + new Date(it.exp * 1000).toLocaleString() : '') +
          '</div>';
        btn.onclick = () => {
          if (!it.hasQR2 || String(it.status) !== 'APPROVED') return;
          state.fromHistory = true;
          if (it.qr2Valid === false) {
            setError('该放行码已过期，请重新扫 QR#1 发起审批');
            return;
          }
          showQR2(it.reqId, it.status);
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
            onSuccess: () => { state.jsReady = true; resolve(); },
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

  $('btn-scan').addEventListener('click', scanQR1);
  $('btn-paste').addEventListener('click', () => submit($('frame-input').value));
  $('btn-reset-done').addEventListener('click', reset);
  $('btn-reset-err').addEventListener('click', reset);
  $('btn-history').addEventListener('click', loadHistory);
  $('btn-history-back').addEventListener('click', reset);
  $('btn-done-history').addEventListener('click', loadHistory);
})();
