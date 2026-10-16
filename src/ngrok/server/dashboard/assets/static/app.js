// oneNat 控制台前端交互 (vanilla JS, 零依赖)
/* global PAGE */
'use strict';

// ============================================================
// 主题 (明/暗) — theme_boot 已在 <head> 设置初始主题, 这里负责切换
// ============================================================

function toggleTheme() {
  const cur = document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
  const next = cur === 'dark' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', next);
  try { localStorage.setItem('on_theme', next); } catch (e) { /* 私密模式忽略 */ }
}

// ============================================================
// 移动端抽屉导航
// ============================================================

function toggleMobileNav(ev) {
  if (ev) ev.stopPropagation();
  document.getElementById('sidebar')?.classList.toggle('open');
  document.getElementById('nav-backdrop')?.classList.toggle('active');
}
function closeMobileNav() {
  document.getElementById('sidebar')?.classList.remove('open');
  document.getElementById('nav-backdrop')?.classList.remove('active');
}

// ============================================================
// 基础工具: api / esc / 时间
// ============================================================

async function api(method, url, body) {
  const resp = await fetch(url, {
    method: method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = {};
  try { data = await resp.json(); } catch (e) { /* non-json error page */ }
  if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
  return data;
}

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

function fmtB(n) {
  n = Number(n) || 0;
  if (n < 1024) return n + ' B';
  const units = ['K', 'M', 'G', 'T', 'P', 'E'];
  let u = -1;
  do { n /= 1024; u++; } while (n >= 1024 && u < units.length - 1);
  return (n >= 100 ? n.toFixed(0) : n.toFixed(1)) + ' ' + units[u] + 'B';
}

function relTime(iso) {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (!t) return '—';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return '刚刚';
  if (s < 3600) return Math.floor(s / 60) + ' 分钟前';
  if (s < 86400) return Math.floor(s / 3600) + ' 小时前';
  return Math.floor(s / 86400) + ' 天前';
}

// ============================================================
// Toast 通知栈 (多条并存, 手动/自动关闭)
// ============================================================

function toast(msg, isErr) {
  const wrap = document.getElementById('toasts');
  if (!wrap) { alert(msg); return; }
  const el = document.createElement('div');
  el.className = 'toast' + (isErr ? ' err' : '');
  el.innerHTML =
    '<svg class="ic" aria-hidden="true"><use href="#i-' + (isErr ? 'alert' : 'check') + '"/></svg>' +
    '<span>' + esc(msg) + '</span>' +
    '<button class="toast-close" aria-label="关闭"><svg class="ic" style="width:12px;height:12px"><use href="#i-x"/></svg></button>';
  el.querySelector('.toast-close').addEventListener('click', () => dismiss());
  wrap.appendChild(el);
  while (wrap.children.length > 4) wrap.firstChild.remove();
  const timer = setTimeout(dismiss, 3200);
  function dismiss() {
    clearTimeout(timer);
    if (!el.isConnected) return;
    el.classList.add('toast-out');
    el.addEventListener('animationend', () => el.remove(), { once: true });
  }
}

// ============================================================
// 自定义确认弹窗 (替代原生 confirm, 返回 Promise<boolean>)
// ============================================================

let confirmResolve = null;
function confirmBox(opts) {
  opts = opts || {};
  return new Promise(resolve => {
    confirmResolve = resolve;
    showModal(
      '<div class="confirm-box">' +
        '<div class="confirm-icon ' + (opts.danger ? 'danger' : '') + '">' +
          '<svg class="ic" aria-hidden="true"><use href="#i-' + (opts.danger ? 'trash' : 'alert') + '"/></svg></div>' +
        '<h3>' + esc(opts.title || '请确认操作') + '</h3>' +
        '<p class="confirm-msg">' + esc(opts.message || '') + '</p>' +
        '<div class="confirm-actions">' +
          '<button class="btn" id="cf-cancel">' + esc(opts.cancelText || '取消') + '</button>' +
          '<button class="btn ' + (opts.danger ? 'btn-danger' : 'btn-primary') + '" id="cf-ok">' + esc(opts.confirmText || '确定') + '</button>' +
        '</div></div>'
    );
    document.getElementById('cf-cancel').addEventListener('click', () => settle(false));
    document.getElementById('cf-ok').addEventListener('click', () => settle(true));
    document.getElementById('cf-ok').focus();
  });
  function settle(v) {
    // 先摘除 resolver 再关弹窗: hideModal 会把仍在位的 confirmResolve
    // 当作"取消"以 false 结算, 导致点"确认"也永远返回 false (删除无反应)。
    const r = confirmResolve;
    confirmResolve = null;
    hideModal();
    if (r) r(v);
  }
}

// ============================================================
// 弹窗 / 复制
// ============================================================

function showModal(html) {
  const box = document.getElementById('modal-box');
  const modal = document.getElementById('modal');
  if (!box || !modal) return;
  box.innerHTML = html;
  modal.classList.remove('hidden');
  const first = box.querySelector('input, select, textarea, button.btn-primary');
  if (first) setTimeout(() => first.focus(), 30);
}
function hideModal() {
  document.getElementById('modal')?.classList.add('hidden');
  if (confirmResolve) { confirmResolve(false); confirmResolve = null; }
}

function copyText(text) {
  const done = () => toast('已复制到剪贴板');
  const fail = () => toast('复制失败, 请手动选中复制', true);
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand('copy');
    ta.remove();
    if (ok) { done(); return; }
  } catch (e) { /* continue to clipboard API */ }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(done).catch(fail);
  } else {
    fail();
 }
}

// 复制交互: [data-copy] 元素点击复制并闪烁反馈; [data-copy-btn] 复制目标输入框
document.addEventListener('click', e => {
  const chip = e.target.closest('[data-copy]');
  if (chip) {
    // 复制芯片可能嵌在 <a> 里 (首页速览/卡片视图): 阻止冒泡导航
    const link = chip.closest('a');
    if (link) e.preventDefault();
    copyText(chip.getAttribute('data-copy'));
    chip.classList.add('copied');
    setTimeout(() => chip.classList.remove('copied'), 1200);
    return;
  }
  const btn = e.target.closest('[data-copy-btn]');
  if (btn) {
    const t = document.querySelector(btn.getAttribute('data-copy-btn'));
    if (t) copyText(t.dataset.copy != null && !t.dataset.revealed ? t.dataset.copy : t.value);
  }
});

// 明文切换: [data-reveal-secret] 切换目标输入框的 打码/明文 (真实值在 data-copy)
document.addEventListener('click', e => {
  const btn = e.target.closest('[data-reveal-secret]');
  if (!btn) return;
  const t = document.querySelector(btn.getAttribute('data-reveal-secret'));
  if (!t) return;
  const real = t.dataset.copy || t.value;
  const masked = real.slice(0, 8) + '••••••••';
  if (t.dataset.revealed === '1') {
    t.value = masked; t.dataset.revealed = '';
  } else {
    t.value = real; t.dataset.revealed = '1';
  }
});

// ============================================================
// 全局键盘: Esc 关闭弹窗/抽屉; / 聚焦搜索
// ============================================================

document.addEventListener('keydown', e => {
  if (e.key === 'Escape') { hideModal(); closeMobileNav(); return; }
  const typing = el => el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable);
  if (e.key === '/' && !typing(e.target)) {
    const q = document.getElementById('t-q');
    if (q && !q.closest('[hidden]')) { e.preventDefault(); q.focus(); }
  }
});

// ============================================================
// 首页总览
// ============================================================

function initHomePage() {
  if (!window.PAGE || PAGE.page !== 'home') return;
  // 问候语 + 日期
  const h = new Date().getHours();
  const hi = h < 5 ? '夜深了' : h < 11 ? '早上好' : h < 13 ? '中午好' : h < 18 ? '下午好' : '晚上好';
  const g = document.getElementById('greeting');
  if (g) g.textContent = hi + '，' + (window.HOME && HOME.username ? HOME.username : '');
  const td = document.getElementById('today-str');
  if (td) {
    try { td.textContent = new Date().toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' }); }
    catch (e) { td.textContent = new Date().toLocaleDateString(); }
  }
  const chart = document.getElementById('week-chart');
  if (chart && window.HOME && Array.isArray(HOME.weekly)) renderAreaChart(chart, HOME.weekly);
}

// SVG 面积图: data = [{day:'2026-01-02', bytes:123}] 旧→新
function renderAreaChart(el, data) {
  if (!data.length) { el.innerHTML = '<div class="empty">暂无流量数据</div>'; return; }
  const W = 640, H = 200, L = 52, R = 14, T = 16, B = 28;
  const iw = W - L - R, ih = H - T - B;
  const max = Math.max(1, ...data.map(d => d.bytes));
  const nice = niceMax(max);
  const xs = data.map((_, i) => L + (data.length === 1 ? iw / 2 : (i / (data.length - 1)) * iw));
  const ys = data.map(d => T + ih - (d.bytes / nice) * ih);
  const line = xs.map((x, i) => (i ? 'L' : 'M') + x.toFixed(1) + ' ' + ys[i].toFixed(1)).join(' ');
  const area = line + ' L ' + xs[xs.length - 1].toFixed(1) + ' ' + (T + ih) + ' L ' + xs[0].toFixed(1) + ' ' + (T + ih) + ' Z';
  let grid = '', ylab = '';
  for (let i = 0; i <= 3; i++) {
    const y = T + (ih / 3) * i;
    grid += '<line x1="' + L + '" y1="' + y + '" x2="' + (W - R) + '" y2="' + y + '"/>';
    ylab += '<text class="ac-y" x="' + (L - 8) + '" y="' + (y + 4) + '" text-anchor="end">' + fmtB(nice * (1 - i / 3) * (i === 0 ? 0 : 1) || 0) + '</text>';
  }
  const xlab = data.map((d, i) =>
    '<text class="ac-x" x="' + xs[i].toFixed(1) + '" y="' + (H - 8) + '" text-anchor="middle">' + esc(d.day.slice(5)) + '</text>').join('');
  el.style.position = 'relative';
  el.innerHTML =
    '<svg viewBox="0 0 ' + W + ' ' + H + '">' +
      '<defs><linearGradient id="ac-grad" x1="0" y1="0" x2="0" y2="1">' +
        '<stop offset="0" stop-color="var(--brand)" stop-opacity=".28"/>' +
        '<stop offset="1" stop-color="var(--brand)" stop-opacity=".02"/>' +
      '</linearGradient></defs>' +
      '<g class="ac-grid">' + grid + '</g>' + ylab + xlab +
      '<path class="ac-fill" d="' + area + '"/>' +
      '<path class="ac-line" d="' + line + '"/>' +
      '<circle class="ac-dot" id="ac-lastdot" cx="' + xs[xs.length - 1] + '" cy="' + ys[ys.length - 1] + '" r="4"/>' +
    '</svg><div class="ac-tip"></div>';
  // 悬停提示
  const svg = el.querySelector('svg');
  const tip = el.querySelector('.ac-tip');
  svg.addEventListener('mousemove', ev => {
    const r = svg.getBoundingClientRect();
    const px = (ev.clientX - r.left) / r.width * W;
    let best = 0, bd = 1e9;
    xs.forEach((x, i) => { const d = Math.abs(x - px); if (d < bd) { bd = d; best = i; } });
    const dot = el.querySelector('.ac-dot') || mkDot(svg);
    dot.setAttribute('cx', xs[best]); dot.setAttribute('cy', ys[best]);
    tip.textContent = data[best].day.slice(5) + ' · ' + fmtB(data[best].bytes);
    tip.style.left = (xs[best] / W * 100) + '%';
    tip.style.top = (ys[best] / H * 100) + '%';
    tip.classList.add('on');
  });
  svg.addEventListener('mouseleave', () => tip.classList.remove('on'));
  function mkDot(svg) {
    const c = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    c.setAttribute('class', 'ac-dot'); c.setAttribute('r', '4');
    svg.appendChild(c); return c;
  }
}

function niceMax(v) {
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

// ============================================================
// 隧道列表页: 客户端过滤 + 双视图 + 实时轮询
// ============================================================

const TUNNEL_POLL_MS = 8000;

function initTunnelsPage() {
  if (!window.PAGE || PAGE.page !== 'tunnels') return;
  const st = {
    status: PAGE.status || '',
    q: (PAGE.q || '').trim(),
    view: 'table',
    all: Array.isArray(PAGE.all) ? PAGE.all : [],
  };
  try { st.view = localStorage.getItem('on_view') || 'table'; } catch (e) {}

  const rowsEl = document.getElementById('t-rows');
  const cardsEl = document.getElementById('t-cards');
  const subEl = document.getElementById('t-sub');
  const qEl = document.getElementById('t-q');

  function match(t) {
    if (st.status === 'online' && !t.online) return false;
    if (st.status === 'offline' && t.online) return false;
    if (st.status === 'locked' && !t.locked) return false;
    if (st.q) {
      const hay = (t.name + ' ' + t.id + ' ' + (t.owner_name || '') + ' ' + (t.note || '')).toLowerCase();
      if (!hay.includes(st.q.toLowerCase())) return false;
    }
    return true;
  }

  function epsHtml(t) {
    if (!t.endpoints || !t.endpoints.length) return '<span class="muted small">—</span>';
    return t.endpoints.map(u => '<span class="copy-chip mono" data-copy="' + esc(u) + '">' + esc(u) + '</span>').join(' ');
  }
  function protosHtml(t) {
    return (t.protos || []).map(p => '<span class="chip chip-proto">' + esc(p) + '</span>').join(' ');
  }

  function render() {
    const list = st.all.filter(match);
    // 计数
    const c = { all: st.all.length, online: 0, offline: 0, locked: 0 };
    st.all.forEach(t => {
      if (t.online) c.online++; else c.offline++;
      if (t.locked) c.locked++;
    });
    document.querySelectorAll('#t-seg [data-c]').forEach(b => { b.textContent = c[b.getAttribute('data-c')]; });
    if (subEl) subEl.textContent = '共 ' + st.all.length + ' 条 · 在线 ' + c.online + (st.q ? ' · 匹配 ' + list.length + ' 条' : '');
    // 表格视图
    if (rowsEl) {
      const cols = PAGE.isAdmin ? 8 : 7;
      rowsEl.innerHTML = list.length ? list.map(t =>
        '<tr>' +
          '<td><span class="dot ' + (t.online ? 'on' : 'off') + '"></span><a class="tname" href="/t/' + esc(t.id) + '">' + esc(t.name) + '</a>' + (t.locked ? ' <svg class="ic"><use href="#i-lock"/></svg>' : '') + '</td>' +
          '<td>' + (t.online ? '<span class="badge badge-on">在线</span>' : '<span class="badge badge-off">离线</span>') + '</td>' +
          '<td>' + protosHtml(t) + '</td>' +
          '<td class="mono">' + (t.mapping_count || 0) + '</td>' +
          '<td class="cell-eps">' + epsHtml(t) + '</td>' +
          '<td class="mono muted">' + fmtB(t.week_bytes) + '</td>' +
          (PAGE.isAdmin ? '<td class="muted">' + esc(t.owner_name || '') + '</td>' : '') +
          '<td><a class="arrow" href="/t/' + esc(t.id) + '" title="详情"><svg class="ic"><use href="#i-chevron-r"/></svg></a></td>' +
        '</tr>').join('')
        : '<tr><td colspan="' + cols + '" class="empty">没有匹配的隧道' + (PAGE.isAdmin ? ' — 点击右上角「创建新隧道」' : '') + '</td></tr>';
    }
    // 卡片视图
    if (cardsEl) {
      cardsEl.innerHTML = list.length ? list.map(t =>
        '<a class="tcard card" href="/t/' + esc(t.id) + '">' +
          '<div class="tcard-head"><span class="dot ' + (t.online ? 'on' : 'off') + '"></span>' +
            '<b class="tcard-name">' + esc(t.name) + '</b>' +
            (t.locked ? '<span class="chip chip-lock"><svg class="ic"><use href="#i-lock"/></svg>锁定</span>' : '') +
            '<span class="badge ' + (t.online ? 'badge-on' : 'badge-off') + '">' + (t.online ? '在线' : '离线') + '</span></div>' +
          '<div class="tcard-eps">' + (t.endpoints && t.endpoints.length
            ? t.endpoints.map(u => '<span class="copy-chip mono" data-copy="' + esc(u) + '">' + esc(u) + '</span>').join('')
            : '<span class="muted small">公网入口待客户端上线后开通</span>') + '</div>' +
          '<div class="tcard-foot"><span>' + protosHtml(t) + '</span>' +
            '<span class="muted">' + (t.mapping_count || 0) + ' 端口 · 7 天 ' + fmtB(t.week_bytes) + (t.owner_name ? ' · ' + esc(t.owner_name) : '') + '</span></div>' +
        '</a>').join('')
        : '<div class="card empty"><p>没有匹配的隧道</p>' + (PAGE.isAdmin ? '<button class="btn btn-primary btn-sm" onclick="showCreateTunnel()">创建新隧道</button>' : '') + '</div>';
    }
  }

  function applyView(v) {
    st.view = v === 'cards' ? 'cards' : 'table';
    document.querySelectorAll('#t-view button').forEach(b => b.classList.toggle('active', b.dataset.view === st.view));
    const table = document.getElementById('t-table');
    const cards = document.getElementById('t-cards');
    if (table) table.hidden = st.view !== 'table';
    if (cards) cards.hidden = st.view !== 'cards';
    try { localStorage.setItem('on_view', st.view); } catch (e) {}
  }

  document.querySelectorAll('#t-view button').forEach(b =>
    b.addEventListener('click', () => { applyView(b.dataset.view); render(); }));
  if (qEl) qEl.addEventListener('input', () => { st.q = qEl.value.trim(); render(); });
  applyView(st.view);
  render();

  // 实时轮询 (后台标签页暂停)
  setInterval(async () => {
    if (document.hidden) return;
    try {
      const res = await api('GET', '/api/tunnels');
      if (Array.isArray(res.tunnels)) { st.all = res.tunnels; render(); }
    } catch (e) { /* 静默: 下一轮重试 */ }
  }, TUNNEL_POLL_MS);
}

// ============================================================
// 隧道详情页: 状态管线 + 运行时实时刷新
// ============================================================

function initDetailPage() {
  if (!window.PAGE || PAGE.page !== 'tunnel_detail') return;
  if (PAGE.detail) renderDetail(PAGE.detail);
  setInterval(async () => {
    if (document.hidden) return;
    try { renderDetail(await api('GET', '/api/tunnels/' + PAGE.tunnelId)); }
    catch (e) { /* 静默重试 */ }
  }, TUNNEL_POLL_MS);
}

let lastMapsSig = '', lastConnsSig = '', lastWeekSig = '';

function setText(id, v) { const el = document.getElementById(id); if (el && el.textContent !== v) el.textContent = v; }

function renderDetail(d) {
  if (!d) return;
  PAGE.mappings = d.mappings || [];
  const rt = d.runtime || {};
  const online = !!d.online || !!rt.online;
  const traffic = rt.traffic || [];
  const today = traffic.length ? traffic[traffic.length - 1].bytes : 0;
  const week = traffic.reduce((a, b) => a + (b.bytes || 0), 0);
  const conns = rt.conns || [];
  const liveEps = Object.values(rt.active || {}).filter(Boolean);

  // 副标题
  setText('td-sub', (online ? '在线' : '离线') + ' · ' + (d.mappings || []).length + ' 条映射' +
    (rt.client_version ? ' · 客户端 ' + rt.client_version + ' (' + (rt.client_os || '') + ')' : '') +
    (rt.last_seen ? ' · 最后活跃 ' + relTime(rt.last_seen) : ''));

  // 管线
  const pc = document.getElementById('pipe-client');
  const ps = document.getElementById('pipe-server');
  const pp = document.getElementById('pipe-public');
  if (pc) pc.classList.toggle('bad', !online);
  setText('pipe-client-val', online ? ((rt.client_os || '客户端') + (rt.client_version ? ' · ' + rt.client_version : '')) : '离线 / 待连接');
  if (ps) ps.classList.remove('bad');
  const l1 = document.getElementById('pipe-link1'), l2 = document.getElementById('pipe-link2');
  if (l1) l1.classList.toggle('flow', online);
  if (l2) l2.classList.toggle('flow', liveEps.length > 0);
  if (pp) pp.classList.toggle('bad', liveEps.length === 0);
  setText('pipe-public-val', liveEps.length ? liveEps.length + ' 个入口在线' : '未开通');

  // KPI
  setText('td-kpi-conns', String(conns.length));
  setText('td-kpi-today', fmtB(today));
  setText('td-kpi-week', fmtB(week));
  setText('td-kpi-seen', rt.last_seen ? relTime(rt.last_seen) : '—');
  setText('td-client', rt.client_version ? rt.client_version + ' (' + (rt.client_os || '') + ')' : '—');

  // 端口映射表 (签名变化才重绘, 避免打断点击)
  const maps = d.mappings || [];
  const sig = JSON.stringify(maps.map(m => [m.id, m.public_url, m.error, m.app_name, m.auth_inherited_shared, m.auth_override]));
  if (sig !== lastMapsSig) {
    lastMapsSig = sig;
    const tb = document.getElementById('td-maps');
    if (tb) {
      tb.innerHTML = maps.length ? maps.map(m => {
        const pub = m.public_url ? '<span class="copy-chip mono" data-copy="' + esc(m.public_url) + '">' + esc(m.public_url) + '</span>' : '<span class="muted">待开通</span>';
        const app = m.app_name
          ? '<a class="chip chip-app" href="/apps/' + esc(m.app_id || '') + '">' + esc(m.app_name) + '</a>' +
            (m.auth_override ? '<span class="badge badge-lock" title="该映射使用实例级独立凭证">独立凭证</span>'
              : (m.auth_inherited_shared ? '<span class="badge badge-warn" title="未配实例凭证, 正在继承「应用默认凭证」; 多机共用同一应用时通常只对其中一台有效">继承应用默认</span>' : ''))
          : '<span class="muted small">未关联</span>';
        const status = m.error ? '<span class="badge badge-err" title="' + esc(m.error) + '">错误</span>'
          : (m.public_url ? '<span class="badge badge-on">已开通</span>' : '<span class="badge badge-off">等待客户端</span>');
        const actions = PAGE.isAdmin
          ? '<td class="td-actions"><button class="mini" onclick="showMappingModal(\'' + esc(m.id) + '\')">编辑</button>' +
            '<button class="mini danger" onclick="delMapping(\'' + esc(m.id) + '\')">删除</button></td>'
          : '';
        return '<tr data-mid="' + esc(m.id) + '">' +
          '<td class="mono">' + esc(m.local_ip || '127.0.0.1') + ':' + esc(m.local_port) + '</td>' +
          '<td class="td-arrow">→</td><td>' + pub + '</td><td>' + app + '</td>' +
          '<td class="muted">' + esc(m.note || '') + '</td><td>' + status + '</td>' + actions + '</tr>';
      }).join('') : '<tr><td colspan="7" class="empty">暂无端口映射</td></tr>';
    }
    setText('td-map-count', maps.length ? maps.length + ' 条' : '');
  }

  // 7 天柱状图
  const wsig = JSON.stringify(traffic);
  if (wsig !== lastWeekSig) {
    lastWeekSig = wsig;
    const barEl = document.getElementById('td-week');
    if (barEl) {
      const max = Math.max(1, ...traffic.map(t => t.bytes));
      barEl.innerHTML = traffic.map(t =>
        '<div class="bar-col" title="' + esc(t.day) + ' · ' + fmtB(t.bytes) + '">' +
          '<div class="bar-wrap"><div class="bar" style="height:' + Math.round(t.bytes / max * 100) + '%"></div></div>' +
          '<div class="bar-val">' + (t.bytes ? fmtB(t.bytes) : '—') + '</div>' +
          '<div class="bar-day">' + esc(t.day.slice(5)) + '</div></div>').join('');
    }
    setText('td-week-total', '合计 ' + fmtB(week));
  }

  // 连接记录
  const csig = JSON.stringify(conns.map(c => [c.time, c.client_addr, c.public_url, c.bytes_in, c.bytes_out]));
  if (csig !== lastConnsSig) {
    lastConnsSig = csig;
    const tb = document.getElementById('td-conns');
    if (tb) {
      tb.innerHTML = conns.length ? conns.map(c =>
        '<tr><td class="muted nowrap">' + esc(fmtDateTime(c.time)) + '</td>' +
        '<td class="mono">' + esc(c.client_addr) + '</td>' +
        '<td class="mono">' + esc(c.public_url || '—') + '</td>' +
        '<td class="mono nowrap">' + fmtB(c.bytes_in) + ' <span class="muted">/</span> ' + fmtB(c.bytes_out) + '</td></tr>').join('')
        : '<tr><td colspan="4" class="empty">暂无连接记录</td></tr>';
    }
  }
}

function fmtDateTime(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return '—';
  const p = n => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
}

// ============================================================
// 详情页: 安装命令 Tab
// ============================================================

function initInstallTabs() {
  const tabs = document.getElementById('install-tabs');
  if (!tabs) return;
  tabs.querySelectorAll('button').forEach(b => b.addEventListener('click', () => {
    tabs.querySelectorAll('button').forEach(x => x.classList.toggle('active', x === b));
    document.querySelectorAll('.cmd-pane').forEach(p => { p.hidden = p.dataset.pane !== b.dataset.tab; });
  }));
}

// ============================================================
// 隧道列表页交互 (创建弹窗)
// ============================================================

function showCreateTunnel() {
  const owners = (PAGE.users || [])
    .map(u => '<option value="' + esc(u.id) + '">' + esc(u.name) + '</option>').join('');
  showModal(
    '<h3>创建新隧道</h3>' +
    '<label class="field">隧道名称<input id="f-name" placeholder="如: kb / office-ssh" required></label>' +
    '<label class="field">备注 (可选)<input id="f-note" placeholder="用途说明"></label>' +
    (PAGE.role === 'admin' && owners
      ? '<label class="field">归属用户<select id="f-owner">' + owners + '</select></label>' : '') +
    '<div class="modal-foot">' +
      '<button class="btn" onclick="hideModal()">取消</button>' +
      '<button class="btn btn-primary" onclick="submitCreateTunnel()">创建</button></div>'
  );
}

async function submitCreateTunnel() {
  const name = document.getElementById('f-name').value.trim();
  if (!name) { toast('请填写隧道名称', true); return; }
  const noteEl = document.getElementById('f-note');
  const ownerSel = document.getElementById('f-owner');
  const body = { name: name, note: noteEl ? noteEl.value.trim() : '' };
  if (ownerSel) body.owner_id = ownerSel.value;
  try {
    const res = await api('POST', '/api/tunnels', body);
    location.href = '/t/' + res.id;
  } catch (e) { toast(e.message, true); }
}

// ============================================================
// 隧道详情页操作
// ============================================================

function toggleDropdown(ev) {
  ev.stopPropagation();
  document.getElementById('tunnel-menu')?.classList.toggle('hidden');
}
document.addEventListener('click', () => {
  document.getElementById('tunnel-menu')?.classList.add('hidden');
});

function findMapping(mid) {
  return (PAGE.mappings || []).find(m => m.id === mid);
}

function showMappingModal(mid) {
  const m = mid ? findMapping(mid) : null;
  const proto = m ? m.proto : 'tcp';
  showModal(
    '<h3>' + (m ? '编辑端口' : '添加端口') + '</h3>' +
    '<label class="field">协议<select id="m-proto" onchange="onProtoChange()">' +
      '<option value="tcp" ' + (proto === 'tcp' ? 'selected' : '') + '>TCP</option>' +
      '<option value="http" ' + (proto === 'http' ? 'selected' : '') + '>HTTP</option>' +
      '<option value="https" ' + (proto === 'https' ? 'selected' : '') + '>HTTPS</option></select></label>' +
    '<label class="field">本地 IP<input id="m-ip" value="' + (m ? esc(m.local_ip) : '127.0.0.1') + '"></label>' +
    '<label class="field">本地端口<input id="m-lport" type="number" min="1" max="65535" value="' + (m ? m.local_port : '') + '" required></label>' +
    '<label class="field" id="m-rport-row">公网端口 (留空或 0 = 自动分配' + (PAGE.portRange ? ', 允许范围 ' + esc(PAGE.portRange) : '') + ')' +
      '<input id="m-rport" type="number" min="0" max="65535" value="' + (m ? m.remote_port : 0) + '"></label>' +
    '<label class="field" id="m-sub-row">子域名 (http/https 时生效)<input id="m-sub" value="' + (m ? esc(m.subdomain || '') : '') + '"></label>' +
    '<label class="field">备注<input id="m-note" value="' + (m ? esc(m.note || '') : '') + '"></label>' +
    '<div class="modal-foot">' +
      '<button class="btn" onclick="hideModal()">取消</button>' +
      '<button class="btn btn-primary" onclick="submitMapping(\'' + (mid || '') + '\')">' + (m ? '保存' : '添加') + '</button></div>'
  );
  onProtoChange();
}

function onProtoChange() {
  const proto = document.getElementById('m-proto').value;
  document.getElementById('m-rport-row').style.display = proto === 'tcp' ? '' : 'none';
  document.getElementById('m-sub-row').style.display = proto === 'tcp' ? 'none' : '';
}

async function submitMapping(mid) {
  const body = {
    proto: document.getElementById('m-proto').value,
    local_ip: document.getElementById('m-ip').value.trim() || '127.0.0.1',
    local_port: parseInt(document.getElementById('m-lport').value, 10),
    remote_port: parseInt(document.getElementById('m-rport').value || '0', 10),
    subdomain: document.getElementById('m-sub').value.trim(),
    note: document.getElementById('m-note').value.trim(),
  };
  if (!body.local_port || body.local_port < 1) { toast('请填写本地端口', true); return; }
  try {
    if (mid) await api('PATCH', '/api/mappings/' + mid, body);
    else await api('POST', '/api/tunnels/' + PAGE.tunnelId + '/mappings', body);
    location.reload();
  } catch (e) { toast(e.message, true); }
}

async function delMapping(mid) {
  if (!await confirmBox({ title: '删除端口映射', danger: true, confirmText: '删除',
    message: '确定删除该端口映射? 在线客户端会立即关闭对应公网端口。' })) return;
  try { await api('DELETE', '/api/mappings/' + mid); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function editName() {
  showModal(
    '<h3>重命名隧道</h3>' +
    '<label class="field">新名称<input id="rn-input" value="' + esc(PAGE.tunnelName || '') + '"></label>' +
    '<div class="modal-foot"><button class="btn" onclick="hideModal()">取消</button>' +
    '<button class="btn btn-primary" id="rn-ok">保存</button></div>'
  );
  const input = document.getElementById('rn-input');
  input.focus(); input.select();
  const submit = async () => {
    const name = input.value.trim();
    if (!name || name === PAGE.tunnelName) { hideModal(); return; }
    try { await api('PATCH', '/api/tunnels/' + PAGE.tunnelId, { name: name }); location.reload(); }
    catch (e) { toast(e.message, true); }
  };
  document.getElementById('rn-ok').addEventListener('click', submit);
  input.addEventListener('keydown', e => { if (e.key === 'Enter') submit(); });
}

async function toggleLock() {
  try { await api('PATCH', '/api/tunnels/' + PAGE.tunnelId, { locked: !PAGE.locked }); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function toggleAllowRemote() {
  const next = !PAGE.allowRemoteTargets;
  if (!await confirmBox({
    title: next ? '允许局域网目标?' : '限制仅本机 (127.0.0.1)?',
    danger: next, confirmText: next ? '开启' : '切换',
    message: next
      ? '开启后可将公网流量转发给内网非本机 (如 192.168.x.x) 设备。请确认你了解内网中的目标服务。'
      : '客户端将拒绝非本机回环目标的转发, 回到更安全的默认状态。'
  })) return;
  try { await api('PATCH', '/api/tunnels/' + PAGE.tunnelId, { allow_remote_targets: next }); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function delTunnel() {
  if (!await confirmBox({ title: '删除隧道', danger: true, confirmText: '删除隧道',
    message: '确定删除该隧道? 所有端口映射会被移除, 在线客户端将被断开且无法重连。' })) return;
  try { await api('DELETE', '/api/tunnels/' + PAGE.tunnelId); location.href = '/tunnels'; }
  catch (e) { toast(e.message, true); }
}

async function repairTunnel() {
  try { await api('POST', '/api/tunnels/' + PAGE.tunnelId + '/repair'); toast('已下发重建指令, 客户端将全量重建隧道'); }
  catch (e) { toast(e.message, true); }
}

async function resetKey() {
  if (!await confirmBox({ title: '重置隧道密钥', danger: true, confirmText: '重置',
    message: '确定重置密钥? 旧密钥立即失效, 已安装的客户端需要重新安装。' })) return;
  try {
    const res = await api('POST', '/api/tunnels/' + PAGE.tunnelId + '/reset-key');
    showModal(
      '<h3>新密钥已生成</h3>' +
      '<p class="muted small">旧密钥已失效, 请用新密钥重新部署客户端。</p>' +
      '<div class="cmd-row"><input class="cmd mono" id="rk-input" readonly data-copy="' + esc(res.key) + '" value="' + esc(res.key) + '" style="width:100%">' +
      '<button class="icon-btn" data-copy-btn="#rk-input" title="复制"><svg class="ic"><use href="#i-copy"/></svg></button></div>' +
      '<div class="modal-foot"><button class="btn" onclick="hideModal()">关闭</button>' +
      '<button class="btn btn-primary" onclick="copyText(document.getElementById(\'rk-input\').dataset.copy)">复制密钥</button></div>'
    );
  } catch (e) { toast(e.message, true); }
}

async function showConfig() {
  try {
    const text = await (await fetch('/api/tunnels/' + PAGE.tunnelId + '/config')).text();
    showModal(
      '<h3>配置文件信息</h3>' +
      '<div class="alert alert-err">本页含敏感信息, 请勿截图发送给他人</div>' +
      '<pre class="cfg">' + esc(text) + '</pre>' +
      '<div class="modal-foot">' +
        '<button class="btn" onclick="hideModal()">关闭</button>' +
        '<button class="btn" onclick="window.open(\'/api/tunnels/' + esc(PAGE.tunnelId) + '/config\')">下载配置文件</button>' +
        '<button class="btn btn-primary" onclick="copyText(window.__cfgText)">复制到剪贴板</button></div>'
    );
    window.__cfgText = text;
  } catch (e) { toast(e.message, true); }
}

function copyInstallCmd(id) {
  copyText(document.getElementById(id || 'install-cmd').value);
}

// ============================================================
// API 密钥页
// ============================================================

async function createKey(form) {
  const name = form.name.value.trim();
  if (!name) { toast('请填写名称', true); return false; }
  try {
    const res = await api('POST', '/api/keys', { name });
    toast('API 密钥已创建: ' + res.key.key);
    location.reload();
  } catch (e) { toast(e.message, true); }
  return false;
}

async function delKey(id, name) {
  if (!await confirmBox({ title: '撤销 API 密钥', danger: true, confirmText: '撤销',
    message: '确定撤销 API 密钥 "' + name + '"? 使用它的 AI 将立即失去访问能力。' })) return;
  try { await api('DELETE', '/api/keys/' + id); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function toggleKeyCred(id, allow) {
  try {
    await api('PATCH', '/api/keys/' + id, { can_read_cred: allow });
    toast(allow ? '已允许该 KEY 读取应用凭证 (限速 5 次/分, 全程审计)'
                : '已禁止该 KEY 读取应用凭证');
    location.reload();
  } catch (e) { toast(e.message, true); }
}

function showSkillPrompt(id) {
  const prompt = (window.KEY_PROMPTS || {})[id];
  if (!prompt) { toast('提示词不存在', true); return; }
  window.__skillPrompt = prompt;
  showModal(
    '<h3>AI 安装提示词 (SKILL)</h3>' +
    '<p class="muted small">把下面这一行提示词复制给任意 AI 助手, 它会自动下载并安装 oneNat 技能, ' +
    '之后即可查询和使用你名下的隧道资源 (只有使用权, 无创建/修改权限)。</p>' +
    '<pre class="cfg" style="white-space:pre-wrap">' + esc(prompt) + '</pre>' +
    '<div class="modal-foot"><button class="btn" onclick="hideModal()">关闭</button>' +
    '<button class="btn btn-primary" onclick="copyText(window.__skillPrompt)">复制提示词</button></div>'
  );
}

// ============================================================
// 用户管理页
// ============================================================

async function createUser(form) {
  const body = {
    username: form.username.value.trim(),
    password: form.password.value,
    role: form.role.value,
  };
  if (!body.username) { toast('请填写用户名', true); return false; }
  try { await api('POST', '/api/users', body); location.reload(); }
  catch (e) { toast(e.message, true); }
  return false;
}

async function resetUserPwd(username) {
  showModal(
    '<h3>重置密码</h3>' +
    '<p class="muted small">为用户 <b>' + esc(username) + '</b> 设置新密码 (至少 6 位)。</p>' +
    '<label class="field">新密码<input id="up-pwd" type="password" minlength="6" placeholder="••••••••"></label>' +
    '<div class="modal-foot"><button class="btn" onclick="hideModal()">取消</button>' +
    '<button class="btn btn-primary" id="up-ok">重置</button></div>'
  );
  const input = document.getElementById('up-pwd');
  input.focus();
  const submit = async () => {
    const pwd = input.value;
    if (!pwd || pwd.length < 6) { toast('密码至少 6 位', true); return; }
    const users = await (await api('GET', '/api/users')).users;
    const u = users.find(x => x.username === username);
    if (!u) { toast('用户不存在', true); return; }
    try { await api('PATCH', '/api/users/' + u.id, { password: pwd }); toast('密码已重置'); }
    catch (e) { toast(e.message, true); }
    hideModal();
  };
  document.getElementById('up-ok').addEventListener('click', submit);
  input.addEventListener('keydown', e => { if (e.key === 'Enter') submit(); });
}

async function setUserRole(id, role) {
  try { await api('PATCH', '/api/users/' + id, { role: role }); toast('角色已更新'); location.reload(); }
  catch (e) { toast(e.message, true); location.reload(); }
}

async function delUser(username) {
  if (!await confirmBox({ title: '删除用户', danger: true, confirmText: '删除',
    message: '确定删除用户 ' + username + '? 其名下隧道将保留但无归属用户。' })) return;
  const users = await (await api('GET', '/api/users')).users;
  const u = users.find(x => x.username === username);
  if (!u) { toast('用户不存在', true); return; }
  try { await api('DELETE', '/api/users/' + u.id); location.reload(); }
  catch (e) { toast(e.message, true); }
}

// ============================================================
// 密码输入小眼睛 (动态注入的 password 输入框自动补挂)
// ============================================================

function attachSecretEye(inp) {
  if (!inp || inp.dataset.eyeBound) return;
  inp.dataset.eyeBound = '1';
  const wrap = document.createElement('span');
  wrap.className = 'eye-wrap';
  inp.parentNode.insertBefore(wrap, inp);
  wrap.appendChild(inp);
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'eye-btn';
  btn.innerHTML = '<svg class="ic" style="width:15px;height:15px"><use href="#i-eye"/></svg>';
  btn.title = '显示明文';
  btn.setAttribute('aria-label', '显示明文');
  btn.addEventListener('click', () => {
    const show = inp.type === 'password';
    inp.type = show ? 'text' : 'password';
    btn.classList.toggle('eye-on', show);
    btn.title = show ? '隐藏' : '显示明文';
  });
  wrap.appendChild(btn);
}

function scanSecretEyes(root) {
  (root || document).querySelectorAll('input[type=password]').forEach(attachSecretEye);
}

// ============================================================
// 启动
// ============================================================

document.addEventListener('DOMContentLoaded', () => {
  // 主题切换按钮
  document.querySelectorAll('[data-theme-toggle]').forEach(b => b.addEventListener('click', toggleTheme));
  scanSecretEyes();
  initHomePage();
  initTunnelsPage();
  initDetailPage();
  initInstallTabs();
});

// 应用凭证/映射实例凭证等弹窗字段为动态注入, 用观察器补挂眼睛
new MutationObserver(muts => {
  for (const mu of muts) {
    for (const n of mu.addedNodes) {
      if (n.nodeType !== 1) continue;
      if (n.matches && n.matches('input[type=password]')) attachSecretEye(n);
      if (n.querySelectorAll) n.querySelectorAll('input[type=password]').forEach(attachSecretEye);
    }
  }
}).observe(document.documentElement, { childList: true, subtree: true });
