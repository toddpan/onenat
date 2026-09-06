// oneNat 应用管理前端逻辑 (vanilla JS, 无依赖)。
// 被 apps.html / app_detail.html / tunnel_detail.html 引用。

/* global PAGE, api, toast, showModal, hideModal, esc, copyText */

// ---------- 常量 ----------

const APP_TYPES = [
  ['ssh', 'SSH / TCP 服务'],
  ['http-api', 'HTTP API 接口'],
  ['web', 'Web 应用'],
  ['database', '数据库'],
  ['custom', '自定义'],
];

// ---------- 应用 CRUD ----------

function appAuthFields(prefix, authType, a) {
  a = a || {};
  const v = k => (a[k] != null ? esc(a[k]) : '');
  return `
    <label class="field">认证方式
      <select id="${prefix}-authtype" onchange="onAuthTypeChange('${prefix}')">
        <option value="none" ${authType === 'none' ? 'selected' : ''}>无认证</option>
        <option value="basic" ${authType === 'basic' ? 'selected' : ''}>Basic (用户名+密码)</option>
        <option value="bearer" ${authType === 'bearer' ? 'selected' : ''}>Bearer / API KEY</option>
        <option value="header" ${authType === 'header' ? 'selected' : ''}>自定义请求头</option>
        <option value="custom" ${authType === 'custom' ? 'selected' : ''}>其他 (技能里说明)</option>
      </select>
    </label>
    <label class="field auth-f auth-basic auth-header auth-custom">用户名
      <input id="${prefix}-username" value="${v('username')}" placeholder="应用登录用户名">
    </label>
    <label class="field auth-f auth-basic">密码
      <input id="${prefix}-password" type="password" placeholder="${a.has_password ? '已设置 — 留空保持不变' : '应用登录密码'}">
    </label>
    <label class="field auth-f auth-bearer">应用 API KEY
      <input id="${prefix}-apikey" type="password" placeholder="${a.has_api_key ? '已设置 — 留空保持不变' : '上游系统 API KEY'}">
    </label>`;
}

function onAuthTypeChange(prefix) {
  const t = document.getElementById(prefix + '-authtype').value;
  document.querySelectorAll('.auth-f').forEach(el => {
    let show = false;
    if (t === 'basic') show = el.classList.contains('auth-basic');
    else if (t === 'bearer') show = el.classList.contains('auth-bearer');
    else if (t === 'header') show = el.classList.contains('auth-header') || el.classList.contains('auth-basic');
    else if (t === 'custom') show = el.classList.contains('auth-custom') || el.classList.contains('auth-basic');
    el.style.display = show ? '' : 'none';
  });
}

function showCreateApp() {
  const typeOpts = APP_TYPES.map(([v, n]) => `<option value="${v}">${n}</option>`).join('');
  showModal(`
    <h3>注册新应用</h3>
    <label class="field">应用名称
      <input id="ap-name" placeholder="如: KB API / 生产 SSH" required>
    </label>
    <label class="field">应用类型
      <select id="ap-type">${typeOpts}</select>
    </label>
    <label class="field">一句话描述 (会展示给 AI)
      <input id="ap-desc" placeholder="如: 仓储/AGV 系统 REST 接口">
    </label>
    <label class="field">内网原始地址 (可选, 纯备注)
      <input id="ap-url" placeholder="如: http://192.168.30.164/kb">
    </label>
    ${appAuthFields('ap', 'none')}
    <div class="modal-foot">
      <button class="btn" onclick="hideModal()">取消</button>
      <button class="btn btn-primary" onclick="submitCreateApp()">注册</button>
    </div>`);
  onAuthTypeChange('ap');
}

async function submitCreateApp() {
  const body = {
    name: document.getElementById('ap-name').value.trim(),
    type: document.getElementById('ap-type').value,
    description: document.getElementById('ap-desc').value.trim(),
    internal_url: document.getElementById('ap-url').value.trim(),
    auth_type: document.getElementById('ap-authtype').value,
    username: (document.getElementById('ap-username') || {}).value || '',
    password: (document.getElementById('ap-password') || {}).value || '',
    api_key: (document.getElementById('ap-apikey') || {}).value || '',
  };
  if (!body.name) { toast('请填写应用名称', true); return; }
  try {
    const res = await api('POST', '/api/apps', body);
    toast('应用已注册, 已生成技能骨架');
    location.href = '/apps/' + res.app.id;
  } catch (e) { toast(e.message, true); }
}

async function editApp(id) {
  try {
    const res = await api('GET', '/api/apps/' + id);
    const a = res.app;
    showModal(`
      <h3>编辑应用</h3>
      <label class="field">应用名称
        <input id="ea-name" value="${esc(a.name)}" required>
      </label>
      <label class="field">应用类型
        <select id="ea-type">${APP_TYPES.map(([v, n]) => `<option value="${v}" ${a.type === v ? 'selected' : ''}>${n}</option>`).join('')}</select>
      </label>
      <label class="field">描述
        <input id="ea-desc" value="${esc(a.description)}">
      </label>
      <label class="field">内网原始地址
        <input id="ea-url" value="${esc(a.internal_url)}">
      </label>
      <div class="modal-foot">
        <button class="btn" onclick="hideModal()">取消</button>
        <button class="btn btn-primary" onclick="submitEditApp('${id}')">保存</button>
      </div>`);
  } catch (e) { toast(e.message, true); }
}

async function submitEditApp(id) {
  const body = {
    name: document.getElementById('ea-name').value.trim(),
    type: document.getElementById('ea-type').value,
    description: document.getElementById('ea-desc').value.trim(),
    internal_url: document.getElementById('ea-url').value.trim(),
  };
  if (!body.name) { toast('请填写应用名称', true); return; }
  try { await api('PATCH', '/api/apps/' + id, body); toast('已保存'); location.reload(); }
  catch (e) { toast(e.message, true); }
}

// ---------- 凭证 ----------

async function editAppCredential(id) {
  try {
    const res = await api('GET', '/api/apps/' + id);
    const a = res.app;
    showModal(`
      <h3>修改访问凭证</h3>
      ${appAuthFields('cr', a.auth_type, { username: a.username, has_password: a.has_password, has_api_key: a.has_api_key })}
      <div class="hint small">修改会被记录到审计日志。留空密码/KEY 表示保持不变。</div>
      <div class="modal-foot">
        <button class="btn" onclick="hideModal()">取消</button>
        <button class="btn btn-primary" onclick="submitAppCredential('${id}')">保存</button>
      </div>`);
    onAuthTypeChange('cr');
  } catch (e) { toast(e.message, true); }
}

async function submitAppCredential(id) {
  const body = {
    auth_type: document.getElementById('cr-authtype').value,
    username: (document.getElementById('cr-username') || {}).value || '',
    password: (document.getElementById('cr-password') || {}).value || '',
    api_key: (document.getElementById('cr-apikey') || {}).value || '',
  };
  try { await api('PUT', '/api/apps/' + id + '/credential', body); toast('凭证已更新'); location.reload(); }
  catch (e) { toast(e.message, true); }
}

async function revealAppCredential(id) {
  if (!confirm('查看明文凭证? 此操作会记录到审计日志。')) return;
  try {
    const res = await api('POST', '/api/apps/' + id + '/reveal');
    const rows = [];
    let i = 0;
    [['用户名', res.username], ['密码', res.password], ['API KEY', res.api_key]].forEach(([k, v]) => {
      if (!v) return;
      rows.push(`<tr><td>${k}</td><td class="mono" id="rv-${i}">${esc(v)}</td>
        <td><a class="mini" onclick="copyText(document.getElementById('rv-${i}').textContent)">复制</a></td></tr>`);
      i++;
    });
    showModal(`
      <h3>凭证明文</h3>
      <div class="alert alert-err">⚠ 请勿截图/外发; 本次查看已记录审计日志</div>
      <table class="tbl">${rows.join('')}</table>
      <div class="modal-foot"><button class="btn" onclick="hideModal()">关闭</button></div>`);
  } catch (e) { toast(e.message, true); }
}

async function deleteApp(id, name) {
  if (!confirm('确定删除应用 "' + name + '"? 其技能文件会被一并删除。')) return;
  try {
    await api('DELETE', '/api/apps/' + id);
    toast('应用已删除');
    location.href = '/apps';
  } catch (e) {
    if (confirm(e.message + '\n\n强制解绑所有端口映射并删除?')) {
      try { await api('DELETE', '/api/apps/' + id + '?force=1'); toast('应用已删除(映射已解绑)'); location.href = '/apps'; }
      catch (e2) { toast(e2.message, true); }
    }
  }
}

// ---------- 技能文件 ----------

function uploadSkill(appId) {
  showModal(`
    <h3>上传技能文件</h3>
    <label class="field">选择文件 (.md .txt .yaml .yml .json, ≤512KB)
      <input type="file" id="sk-file" accept=".md,.txt,.yaml,.yml,.json">
    </label>
    <label class="field">文件名 (留空用上传文件名)
      <input id="sk-name" placeholder="如: kb-api.md">
    </label>
    <div class="modal-foot">
      <button class="btn" onclick="hideModal()">取消</button>
      <button class="btn btn-primary" onclick="submitUploadSkill('${appId}')">上传</button>
    </div>`);
}

async function submitUploadSkill(appId) {
  const f = document.getElementById('sk-file').files[0];
  if (!f) { toast('请选择文件', true); return; }
  if (f.size > 512 * 1024) { toast('文件超过 512KB', true); return; }
  const fd = new FormData();
  fd.append('file', f);
  const customName = document.getElementById('sk-name').value.trim();
  if (customName) fd.append('name', customName);
  try {
    const resp = await fetch('/api/apps/' + appId + '/skills', { method: 'POST', body: fd });
    if (!resp.ok) {
      const data = await resp.json().catch(() => ({}));
      throw new Error(data.error || ('HTTP ' + resp.status));
    }
    toast('技能文件已上传');
    location.reload();
  } catch (e) { toast(e.message, true); }
}

function newSkill(appId) {
  showModal(`
    <h3>新建技能文件</h3>
    <label class="field">文件名
      <input id="ns-name" placeholder="如: usage.md">
    </label>
    <label class="field">内容 (Markdown)
      <textarea class="skill-editor" id="ns-content" placeholder="# 应用使用说明&#10;&#10;## 连接方式&#10;..."></textarea>
    </label>
    <div class="modal-foot">
      <button class="btn" onclick="hideModal()">取消</button>
      <button class="btn btn-primary" onclick="submitNewSkill('${appId}')">创建</button>
    </div>`);
}

async function submitNewSkill(appId) {
  const name = document.getElementById('ns-name').value.trim();
  const content = document.getElementById('ns-content').value;
  if (!name) { toast('请填写文件名', true); return; }
  if (!content.trim()) { toast('内容不能为空', true); return; }
  try {
    await api('POST', '/api/apps/' + appId + '/skills', { name, content });
    toast('技能文件已创建');
    location.reload();
  } catch (e) { toast(e.message, true); }
}

async function editSkill(appId, sid, name) {
  try {
    const resp = await fetch(`/api/apps/${appId}/skills/${sid}/download`);
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const content = await resp.text();
    window.__editSkill = { appId, sid };
    showModal(`
      <h3>编辑 ${esc(name)}</h3>
      <textarea class="skill-editor" id="es-content">${esc(content)}</textarea>
      <div class="modal-foot">
        <button class="btn" onclick="hideModal()">取消</button>
        <button class="btn btn-primary" onclick="submitEditSkill()">保存</button>
      </div>`);
  } catch (e) { toast('读取技能内容失败: ' + e.message, true); }
}

async function submitEditSkill() {
  const st = window.__editSkill || {};
  if (!st.appId) return;
  try {
    await api('PUT', `/api/apps/${st.appId}/skills/${st.sid}`, { content: document.getElementById('es-content').value });
    toast('已保存');
    location.reload();
  } catch (e) { toast(e.message, true); }
}

async function deleteSkill(appId, sid, name) {
  if (!confirm('确定删除技能文件 "' + name + '"?')) return;
  try { await api('DELETE', `/api/apps/${appId}/skills/${sid}`); toast('已删除'); location.reload(); }
  catch (e) { toast(e.message, true); }
}

// ---------- 应用 AI 提示词 ----------

async function showAppAIPrompt() {
  try {
    const res = await api('GET', '/api/keys');
    const keys = res.keys || [];
    if (!keys.length) {
      toast('请先在「API 密钥」页创建一个 KEY', true);
      return;
    }
    const opts = keys.map(k => `<option value="${esc(k.key)}">${esc(k.name)} (${esc(k.key.slice(0, 12))}…)</option>`).join('');
    showModal(`
      <h3>AI 接入提示词</h3>
      <label class="field">选择 API KEY
        <select id="pk-key" onchange="renderAppPrompt()">${opts}</select>
      </label>
      <pre class="cfg" style="white-space:pre-wrap" id="pk-prompt"></pre>
      <div class="modal-foot">
        <button class="btn" onclick="hideModal()">关闭</button>
        <button class="btn btn-primary" onclick="copyText(document.getElementById('pk-prompt').textContent)">复制提示词</button>
      </div>`);
    renderAppPrompt();
  } catch (e) { toast(e.message, true); }
}

function renderAppPrompt() {
  const key = document.getElementById('pk-key').value;
  const tpl = (window.PAGE.promptTemplate || '')
    .replace('<BASE_URL>', window.PAGE.base || location.origin)
    .replace('<API_KEY>', key);
  document.getElementById('pk-prompt').textContent = tpl;
}

// ---------- 映射表单「关联应用」注入 ----------
// 包装 app.js 的 showMappingModal / submitMapping: 插入必选应用下拉,
// 并把选中的 app_id 注入提交的 body。

(function () {
  function injectAppSelect(mid) {
    const apps = (window.PAGE && window.PAGE.apps) || [];
    const note = document.getElementById('m-note');
    if (!note) return;
    if (!apps.length) {
      note.insertAdjacentHTML('afterend',
        `<div class="alert alert-err" style="margin-top:8px">⚠ 还没有可关联的应用 — 请先到 <a href="/apps">应用管理</a> 注册应用</div>`);
      return;
    }
    const m = mid ? (window.PAGE.mappings || []).find(x => x.id === mid) : null;
    const opts = apps.map(a =>
      `<option value="${esc(a.id)}" ${m && m.app_id === a.id ? 'selected' : ''}>${esc(a.name)} (${esc(a.type)})</option>`).join('');
    note.insertAdjacentHTML('beforebegin',
      `<label class="field">关联应用 (端口背后的系统, 必选)
        <select id="m-app">${opts}</select>
      </label>`);
  }

  // 等原 app.js 的 showMappingModal 定义完成后再包装
  window.addEventListener('DOMContentLoaded', () => {
    const origShow = window.showMappingModal;
    if (origShow) {
      window.showMappingModal = function (mid) {
        origShow(mid);
        injectAppSelect(mid);
      };
    }
    const origSubmit = window.submitMapping;
    if (origSubmit) {
      window.submitMapping = function (mid) {
        const sel = document.getElementById('m-app');
        if (sel && !sel.value) { toast('请选择关联应用', true); return; }
        if (!sel) return origSubmit(mid);
        const origApi = window.api;
        window.api = async function (method, url, body) {
          if (body && typeof body === 'object') body.app_id = sel.value;
          return origApi(method, url, body);
        };
        try { return origSubmit(mid); }
        finally { window.api = origApi; }
      };
    }
  });
})();
