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
// 「实例凭证」区: 同一应用被多条映射指向不同机器实例时 (如两台 DSH,
// apiKey 各异), 可在映射级覆盖应用默认凭证; 凭证跟实例走。

  function injectCredSection(mid) {
    const m = mid ? (window.PAGE.mappings || []).find(x => x.id === mid) : null;
    const appSel = document.getElementById('m-app');
    const anchor = appSel || document.getElementById('m-note');
    if (!anchor) return;
    const hasOverride = !!(m && m.auth_override);
    const t = (m && m.auth_type) || 'bearer';
    anchor.insertAdjacentHTML('afterend', `
      <label class="field">实例凭证
        <select id="m-auth-mode" onchange="onAuthModeChange()">
          <option value="inherit" ${hasOverride ? '' : 'selected'}>继承应用默认凭证</option>
          <option value="override" ${hasOverride ? 'selected' : ''}>实例独立凭证 (覆盖应用默认)</option>
        </select>
      </label>
      <div id="m-auth-fields" class="${hasOverride ? '' : 'hidden'}">
        <label class="field">认证类型
          <select id="m-auth-type">
            <option value="bearer" ${t === 'bearer' ? 'selected' : ''}>Bearer / API Key</option>
            <option value="basic" ${t === 'basic' ? 'selected' : ''}>Basic (用户名+密码)</option>
            <option value="header" ${t === 'header' ? 'selected' : ''}>自定义 Header</option>
          </select>
        </label>
        <label class="field">用户名 (basic 必填)
          <input id="m-auth-user" autocomplete="off">
        </label>
        <label class="field">密码
          <input id="m-auth-pass" type="password" autocomplete="new-password">
        </label>
        <label class="field">API Key
          <input id="m-auth-ak" type="password" autocomplete="new-password">
        </label>
        <div class="muted small" style="margin:-4px 0 8px">密钥加密存储, 仅经 /api/v1/mappings/:id/credentials 限速读取 (全程审计)。应用默认凭证更新时, 与旧/新值相同的实例覆盖会自动回归继承; 真正的实例差异凭证保留不动</div>
      </div>`);
  }

  window.onAuthModeChange = function () {
    const f = document.getElementById('m-auth-fields');
    if (f) f.classList.toggle('hidden', document.getElementById('m-auth-mode').value !== 'override');
  };

  // 计算提交时的 auth 补丁: undefined = 不触碰; {auth_type:"none"} = 清除覆盖
  function buildAuthPatch(mid) {
    const mode = document.getElementById('m-auth-mode');
    if (!mode) return undefined;
    const m = mid ? (window.PAGE.mappings || []).find(x => x.id === mid) : null;
    if (mode.value === 'inherit') {
      return m && m.auth_override ? { auth_type: 'none' } : undefined;
    }
    const auth = {
      auth_type: document.getElementById('m-auth-type').value,
      username: document.getElementById('m-auth-user').value.trim(),
      password: document.getElementById('m-auth-pass').value,
      api_key: document.getElementById('m-auth-ak').value,
    };
    const allBlank = !auth.username && !auth.password && !auth.api_key;
    if (allBlank && m && m.auth_override) return undefined; // 编辑时全留空 = 保持已存覆盖
    return auth;
  }

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
        injectCredSection(mid);
      };
    }
    const origSubmit = window.submitMapping;
    if (origSubmit) {
      window.submitMapping = function (mid) {
        const sel = document.getElementById('m-app');
        if (sel && !sel.value) { toast('请选择关联应用', true); return; }
        if (!sel) return origSubmit(mid);
        const authPatch = buildAuthPatch(mid);
        const origApi = window.api;
        window.api = async function (method, url, body) {
          if (body && typeof body === 'object') {
            body.app_id = sel.value;
            if (authPatch !== undefined) body.auth = authPatch;
          }
          return origApi(method, url, body);
        };
        try { return origSubmit(mid); }
        finally { window.api = origApi; }
      };
    }
  });
})();

// ---------- 配置导入/导出 (管理员) ----------
// 导出: 应用(含凭证明文+技能)+隧道(含映射与实例级凭证覆盖) 打包为 JSON;
// 导入: 按名称匹配, skip=跳过同名 / update=更新同名; 永不删除既有数据。

async function exportConfig() {
  try {
    const withCred = confirm('导出文件将包含访问凭证明文 (用于跨环境导入)。\n\n'
      + '确定 = 包含凭证 (推荐, 完整迁移)\n取消 = 仅导出不含凭证的结构配置');
    const res = await fetch('/api/config/export?credentials=' + (withCred ? '1' : '0'), { credentials: 'same-origin' });
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || ('HTTP ' + res.status));
    const blob = await res.blob();
    const cd = res.headers.get('Content-Disposition') || '';
    const m = cd.match(/filename="([^"]+)"/);
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = m ? m[1] : 'onenat-config.json';
    a.click();
    URL.revokeObjectURL(a.href);
    toast('配置已导出' + (withCred ? ' (含凭证, 请妥善保管)' : ' (不含凭证)'));
  } catch (e) { toast('导出失败: ' + e.message, true); }
}

function showImportConfig() {
  showModal(`
    <h3>导入配置</h3>
    <label class="field">选择导出文件 (.json)
      <input type="file" id="cfg-file" accept=".json,application/json">
    </label>
    <label class="field">冲突处理
      <select id="cfg-mode">
        <option value="skip">跳过同名 (只补缺失, 推荐)</option>
        <option value="update">更新同名 (覆盖元数据与凭证)</option>
      </select>
    </label>
    <div class="hint small">导入按名称匹配应用与隧道: 缺失的创建 (隧道 KEY 重新生成), 已有的按上面策略处理; 不会删除任何现有配置。导入者成为新资源的归属人。</div>
    <div class="modal-foot">
      <button class="btn" onclick="hideModal()">取消</button>
      <button class="btn btn-primary" onclick="submitImportConfig()">导入</button>
    </div>`);
}

async function submitImportConfig() {
  const f = document.getElementById('cfg-file').files[0];
  if (!f) { toast('请选择配置文件', true); return; }
  const mode = document.getElementById('cfg-mode').value;
  let config;
  try { config = JSON.parse(await f.text()); }
  catch (e) { toast('文件不是有效的 JSON: ' + e.message, true); return; }
  if (config.format !== 'onenat-config') { toast('不是 oneNat 配置导出文件', true); return; }
  if (config.include_credentials &&
      !confirm('该文件包含凭证明文, 导入后将按本机密钥重新加密保存。继续?')) return;
  try {
    const res = await api('POST', '/api/config/import', { config, mode });
    hideModal();
    const fail = (res.mappings_failed || []).length;
    showModal(`
      <h3>导入完成</h3>
      <table class="tbl">
        <tr><td>应用</td><td>新建 ${res.apps_created} · 更新 ${res.apps_updated} · 跳过 ${res.apps_skipped}</td></tr>
        <tr><td>技能文件</td><td>新增 ${res.skills_added}</td></tr>
        <tr><td>隧道</td><td>新建 ${res.tunnels_created} · 更新 ${res.tunnels_updated} · 跳过 ${res.tunnels_skipped}</td></tr>
        <tr><td>端口映射</td><td>新建 ${res.mappings_created}${fail ? ` · 失败 ${fail}` : ''}</td></tr>
      </table>
      ${fail ? `<div class="alert alert-err" style="margin-top:8px">${res.mappings_failed.map(esc).join('<br>')}</div>` : ''}
      <div class="modal-foot"><button class="btn btn-primary" onclick="hideModal();location.reload()">好的</button></div>`);
  } catch (e) { toast('导入失败: ' + e.message, true); }
}
