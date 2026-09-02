// oneNat dashboard front-end interactions (vanilla JS, no dependencies).
/* global PAGE */

// ---------- mobile nav helpers ----------

function toggleMobileNav(ev) {
  if (ev) ev.stopPropagation();
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('nav-backdrop');
  if (sidebar) sidebar.classList.toggle('open');
  if (backdrop) backdrop.classList.toggle('active');
}

function closeMobileNav() {
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('nav-backdrop');
  if (sidebar) sidebar.classList.remove('open');
  if (backdrop) backdrop.classList.remove('active');
}

// 点击导航链接后自动关闭移动端菜单
document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('.side-link').forEach(link => {
    link.addEventListener('click', closeMobileNav);
  });
});

// ---------- tiny helpers ----------

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

let toastTimer = null;
function toast(msg, isErr) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.classList.toggle('err', !!isErr);
  el.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add('hidden'), 2600);
}

function copyText(text) {
  // 统一入口: 先走 textarea+execCommand (HTTP 页面/局域网 IP 也能用),
  // 失败再走 clipboard API (需要 secure context)。
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

function showModal(html) {
  document.getElementById('modal-box').innerHTML = html;
  document.getElementById('modal').classList.remove('hidden');
}
function hideModal() {
  document.getElementById('modal').classList.add('hidden');
}

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

// ---------- tunnels list page ----------

function showCreateTunnel() {
  const owners = (PAGE.users || [])
    .map(u => `<option value="${esc(u.id)}">${esc(u.name)}</option>`).join('');
  showModal(`
    <h3>创建新隧道</h3>
    <label class="field">隧道名称
      <input id="f-name" placeholder="如: kb / office-ssh" required>
    </label>
    <label class="field">备注 (可选)
      <input id="f-note" placeholder="用途说明">
    </label>
    ${PAGE.role === 'admin' && owners ? `
      <label class="field">归属用户
        <select id="f-owner">${owners}</select>
      </label>` : ''}
    <div class="modal-foot">
      <button class="btn" onclick="hideModal()">取消</button>
      <button class="btn btn-primary" onclick="submitCreateTunnel()">创建</button>
    </div>`);
}

async function submitCreateTunnel() {
  const name = document.getElementById('f-name').value.trim();
  if (!name) { toast('请填写隧道名称', true); return; }
  const note = document.getElementById('f-note') ? document.getElementById('f-note').value.trim() : '';
  const ownerSel = document.getElementById('f-owner');
  const body = { name: name, note: note };
  if (ownerSel) body.owner_id = ownerSel.value;
  try {
    const res = await api('POST', '/api/tunnels', body);
    location.href = '/t/' + res.id;
  } catch (e) { toast(e.message, true); }
}

// ---------- tunnel detail page ----------

function toggleDropdown(ev) {
  ev.stopPropagation();
  document.getElementById('tunnel-menu').classList.toggle('hidden');
}
document.addEventListener('click', () => {
  const m = document.getElementById('tunnel-menu');
  if (m) m.classList.add('hidden');
});

function findMapping(mid) {
  return (PAGE.mappings || []).find(m => m.id === mid);
}

function showMappingModal(mid) {
  const m = mid ? findMapping(mid) : null;
  const proto = m ? m.proto : 'tcp';
  showModal(`
    <h3>${m ? '编辑端口' : '添加端口'}</h3>
    <label class="field">协议
      <select id="m-proto" onchange="onProtoChange()">
        <option value="tcp" ${proto === 'tcp' ? 'selected' : ''}>TCP</option>
        <option value="http" ${proto === 'http' ? 'selected' : ''}>HTTP</option>
        <option value="https" ${proto === 'https' ? 'selected' : ''}>HTTPS</option>
      </select>
    </label>
    <label class="field">本地 IP
      <input id="m-ip" value="${m ? esc(m.local_ip) : '127.0.0.1'}">
    </label>
    <label class="field">本地端口
      <input id="m-lport" type="number" min="1" max="65535" value="${m ? m.local_port : ''}" required>
    </label>
    <label class="field" id="m-rport-row">公网端口 (留空或 0 = 自动分配)
      <input id="m-rport" type="number" min="0" max="65535" value="${m ? m.remote_port : 0}">
    </label>
    <label class="field" id="m-sub-row">子域名 (http/https 时生效)
      <input id="m-sub" value="${m ? esc(m.subdomain || '') : ''}">
    </label>
    <label class="field">备注
      <input id="m-note" value="${m ? esc(m.note || '') : ''}">
    </label>
    <div class="modal-foot">
      <button class="btn" onclick="hideModal()">取消</button>
      <button class="btn btn-primary" onclick="submitMapping('${mid || ''}')">${m ? '保存' : '添加'}</button>
    </div>`);
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
  if (!confirm('确定删除该端口映射? 在线客户端会立即关闭对应公网端口。')) return;
  try { await api('DELETE', '/api/mappings/' + mid); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function editName() {
  const name = prompt('新的隧道名称', PAGE.tunnelName);
  if (!name || name.trim() === PAGE.tunnelName) return;
  try { await api('PATCH', '/api/tunnels/' + PAGE.tunnelId, { name: name.trim() }); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function toggleLock() {
  try { await api('PATCH', '/api/tunnels/' + PAGE.tunnelId, { locked: !PAGE.locked }); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function delTunnel() {
  if (!confirm('确定删除该隧道? 所有端口映射会被移除, 在线客户端将被断开且无法重连。')) return;
  try { await api('DELETE', '/api/tunnels/' + PAGE.tunnelId); location.href = '/'; }
  catch (e) { toast(e.message, true); }
}

async function repairTunnel() {
  try { await api('POST', '/api/tunnels/' + PAGE.tunnelId + '/repair'); toast('已下发重建指令, 客户端将全量重建隧道'); }
  catch (e) { toast(e.message, true); }
}

async function resetKey() {
  if (!confirm('确定重置密钥? 旧密钥立即失效, 已安装的客户端需要重新安装。')) return;
  try {
    const res = await api('POST', '/api/tunnels/' + PAGE.tunnelId + '/reset-key');
    showModal(`
      <h3>新密钥已生成</h3>
      <p class="muted small">旧密钥已失效, 请用新密钥重新部署客户端。</p>
      <input class="cmd mono" readonly value="${esc(res.key)}" style="width:100%">
      <div class="modal-foot">
        <button class="btn" onclick="hideModal()">关闭</button>
        <button class="btn btn-primary" onclick="copyText('${esc(res.key)}')">复制密钥</button>
      </div>`);
  } catch (e) { toast(e.message, true); }
}

async function showConfig() {
  try {
    const text = await (await fetch('/api/tunnels/' + PAGE.tunnelId + '/config')).text();
    showModal(`
      <h3>配置文件信息</h3>
      <div class="alert alert-err">⚠ 本页含敏感信息, 请勿截图发送给他人</div>
      <pre class="cfg">${esc(text)}</pre>
      <div class="modal-foot">
        <button class="btn" onclick="hideModal()">关闭</button>
        <button class="btn" onclick="window.open('/api/tunnels/${PAGE.tunnelId}/config')">下载配置文件</button>
        <button class="btn btn-primary" onclick="copyText(${JSON.stringify(text)})">复制到剪贴板</button>
      </div>`);
  } catch (e) { toast(e.message, true); }
}

function copyInstallCmd(id) {
  copyText(document.getElementById(id || 'install-cmd').value);
}

// ---------- api keys page ----------

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
  if (!confirm('确定撤销 API 密钥 "' + name + '"? 使用它的 AI 将立即失去访问能力。')) return;
  try { await api('DELETE', '/api/keys/' + id); location.reload(); }
  catch (e) { toast(e.message, true); }
}

function showSkillPrompt(id) {
  const prompt = (window.KEY_PROMPTS || {})[id];
  if (!prompt) { toast('提示词不存在', true); return; }
  // 提示词含引号, 不能内嵌进 onclick 属性; 存到全局变量按引用读取
  window.__skillPrompt = prompt;
  showModal(`
    <h3>AI 安装提示词 (SKILL)</h3>
    <p class="muted small">把下面这一行提示词复制给任意 AI 助手, 它会自动下载并安装 oneNat 技能,
    之后即可查询和使用你名下的隧道资源 (只有使用权, 无创建/修改权限)。</p>
    <pre class="cfg" style="white-space:pre-wrap">${esc(prompt)}</pre>
    <div class="modal-foot">
      <button class="btn" onclick="hideModal()">关闭</button>
      <button class="btn btn-primary" onclick="copyText(window.__skillPrompt)">复制提示词</button>
    </div>`);
}

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
  const pwd = prompt('为用户 ' + username + ' 设置新密码 (至少 6 位)');
  if (!pwd) return;
  const users = await (await api('GET', '/api/users')).users;
  const u = users.find(x => x.username === username);
  if (!u) { toast('用户不存在', true); return; }
  try { await api('PATCH', '/api/users/' + u.id, { password: pwd }); toast('密码已重置'); }
  catch (e) { toast(e.message, true); }
}

async function setUserRole(id, role) {
  try { await api('PATCH', '/api/users/' + id, { role: role }); toast('角色已更新'); location.reload(); }
  catch (e) { toast(e.message, true); location.reload(); }
}

async function delUser(username) {
  if (!confirm('确定删除用户 ' + username + '? 其名下隧道将保留但无归属用户。')) return;
  const users = await (await api('GET', '/api/users')).users;
  const u = users.find(x => x.username === username);
  if (!u) { toast('用户不存在', true); return; }
  try { await api('DELETE', '/api/users/' + u.id); location.reload(); }
  catch (e) { toast(e.message, true); }
}
