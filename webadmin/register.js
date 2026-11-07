(function () {
  const $ = (id) => document.getElementById(id);
  const token = (location.hash || '').replace(/^#/, '').trim();
  if (!token) { $('bad').classList.remove('hidden'); return; }

  fetch('/register/api/' + encodeURIComponent(token))
    .then(async (r) => { if (!r.ok) throw new Error(); return r.json(); })
    .then((info) => {
      $('form').classList.remove('hidden');
      $('tid').textContent = info.id;
      $('tname').textContent = info.displayName || '';
    })
    .catch(() => $('bad').classList.remove('hidden'));

  $('btn').onclick = async () => {
    $('err').textContent = '';
    try {
      const r = await fetch('/register/api/' + encodeURIComponent(token), {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          user: $('user').value.trim(),
          password: $('pass').value,
          contactName: $('cname').value.trim(),
          contactEmail: $('cmail').value.trim(),
        }),
      });
      const text = await r.text();
      let data; try { data = JSON.parse(text); } catch (_) { data = text; }
      if (!r.ok) throw new Error(typeof data === 'string' ? data : '注册失败');
      $('form').classList.add('hidden');
      $('done').classList.remove('hidden');
    } catch (e) {
      $('err').textContent = e.message || String(e);
    }
  };
  ['user', 'pass', 'cname', 'cmail'].forEach((id) => {
    const el = $(id);
    if (!el) return;
    el.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); $('btn').click(); }
    });
  });
})();
