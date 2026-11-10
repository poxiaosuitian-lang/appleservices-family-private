'use strict';
const $ = id => document.getElementById(id), token = document.querySelector('meta[name="app-token"]').content;
let loginMode = 'reconnect', loginAccount = '', current = null, busy = false, privateCode = '', privateCodeKind = '', hideAt = 0, codeTotalMs = 60000, lastState = '', lastQuery = '', lastRefresh = '', lastRequest = '', noticeTimer, selectedAccount = localStorage.getItem('selected-account') || '', familyInfo = null, familyAccount = '', familyCheckedAt = '', familyStale = false, storageAccount = '', storageCheckedAt = '', planAccount = '', planCheckedAt = '', poolOrganizerAccount = localStorage.getItem('family-organizer-account') || '', poolOrganizerName = localStorage.getItem('family-organizer-name') || '', purchaseAction = '', purchaseLabel = '', hmeInfo = null, hmeAccount = '', hmeCandidate = '', hmeFilter = 'active', hmeVisible = 100, hmeEditing = '', activationInfo = null, activationAccount = '', autoRefreshScheduled = false;
let storeAuthReady = false, storeAuthAccount = '', storeAuthPID = 0, storeAuthRefresh = '';
let giftPreparation = '', giftOwner = '', giftPID = 0;

const themeMedia = window.matchMedia?.('(prefers-color-scheme: dark)');
let themePref = localStorage.getItem('appleservices:theme') || 'light';
function applyTheme(pref) {
  themePref = ['light', 'dark', 'auto'].includes(pref) ? pref : 'light';
  const resolved = themePref === 'auto' ? (themeMedia?.matches ? 'dark' : 'light') : themePref;
  document.documentElement.dataset.theme = resolved;
  document.documentElement.dataset.themePref = themePref;
  const btn = $('themeToggle');
  if (btn) {
    const label = themePref === 'auto' ? '外观：跟随系统（点击切换浅色）' : themePref === 'dark' ? '外观：深色模式（点击切换跟随系统）' : '外观：浅色模式（点击切换深色）';
    btn.title = label;
    btn.setAttribute('aria-label', label);
  }
}
applyTheme(themePref);
themeMedia?.addEventListener?.('change', () => { if (themePref === 'auto') applyTheme('auto'); });
$('themeToggle')?.addEventListener('click', () => {
  const next = themePref === 'light' ? 'dark' : themePref === 'dark' ? 'auto' : 'light';
  try { localStorage.setItem('appleservices:theme', next); } catch {}
  applyTheme(next);
});

const autoRefreshState = new Map();
const autoRefreshOwner = (globalThis.crypto?.randomUUID?.() || Math.random().toString(36).slice(2));
function autoRefreshLeaseKey(account) { return 'appleservices:auto-refresh:' + account; }
function autoRefreshAttemptKey(key) { return 'appleservices:auto-attempt:' + key; }
function acquireAutoRefreshLease(account) {
  try {
    const key = autoRefreshLeaseKey(account), now = Date.now();
    let lease = {};
    try { lease = JSON.parse(localStorage.getItem(key) || '{}'); } catch {}
    if (lease.owner && lease.owner !== autoRefreshOwner && Number(lease.expires) > now) return false;
    localStorage.setItem(key, JSON.stringify({ owner: autoRefreshOwner, expires: now + 150000 }));
    return JSON.parse(localStorage.getItem(key) || '{}').owner === autoRefreshOwner;
  } catch { return true; }
}
function renewAutoRefreshLease(account) {
  try {
    const key = autoRefreshLeaseKey(account), lease = JSON.parse(localStorage.getItem(key) || '{}');
    if (lease.owner === autoRefreshOwner) localStorage.setItem(key, JSON.stringify({ owner: autoRefreshOwner, expires: Date.now() + 150000 }));
  } catch {}
}
function releaseAutoRefreshLease(account) {
  try {
    const key = autoRefreshLeaseKey(account), lease = JSON.parse(localStorage.getItem(key) || '{}');
    if (lease.owner === autoRefreshOwner) localStorage.removeItem(key);
  } catch {}
}
function autoRefreshAttemptStatus(key) {
  try { const value = JSON.parse(localStorage.getItem(autoRefreshAttemptKey(key)) || '{}'); return value.status || ''; } catch { return ''; }
}
function autoRefreshAttempted(key) { return !!autoRefreshAttemptStatus(key); }
function markAutoRefreshAttempt(key, status = 'refreshing') {
  try { localStorage.setItem(autoRefreshAttemptKey(key), JSON.stringify({ status, owner: autoRefreshOwner, at: Date.now() })); } catch {}
}
function clearAutoRefreshAttempt(key) { try { localStorage.removeItem(autoRefreshAttemptKey(key)); } catch {} }

const time = value => {
  if (!value || value.startsWith('0001')) return '—';
  return new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', timeZone: 'Asia/Singapore', hour12: false }).format(new Date(value));
};
async function api(path, data) {
  const suffix = path === 'view' && !selectedAccount ? '' : (selectedAccount ? '?account=' + encodeURIComponent(selectedAccount) : '');
  const response = await fetch('/api/' + path + suffix, {
    method: data === undefined ? 'GET' : 'POST',
    headers: { 'X-App-Token': token, 'Content-Type': 'application/json' },
    body: data === undefined ? undefined : JSON.stringify(data),
    cache: 'no-store'
  });
  if (!response.ok) throw new Error(response.status === 403 ? '请刷新面板后重试' : '操作未完成，请稍后重试');
  return response.json();
}
function notice(message, error = false) {
  $('notice').textContent = message;
  $('notice').classList.toggle('error', error);
  $('notice').hidden = false;
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => { $('notice').hidden = true; }, 6500);
}
function activity(text, when = new Date().toISOString()) {
  const list = $('activityList');
  list.querySelector('.empty-activity')?.remove();
  const entry = document.createElement('div');
  entry.className = 'activity-entry';
  const label = document.createElement('span');
  label.textContent = text;
  const stamp = document.createElement('time');
  stamp.textContent = time(when);
  entry.append(label, stamp);
  list.prepend(entry);
  while (list.children.length > 5) list.lastElementChild.remove();
}

function renderCodeSlots(chars) {
  const box = $('codeDisplay');
  box.replaceChildren();
  for (let i = 0; i < 6; i++) {
    if (i === 3) {
      const sep = document.createElement('span');
      sep.className = 'otp-sep';
      box.append(sep);
    }
    const slot = document.createElement('span');
    slot.className = 'otp-digit';
    slot.textContent = chars[i] || '•';
    box.append(slot);
  }
}
function clearCode() {
  privateCode = '';
  hideAt = 0;
  $('codeDisplay').classList.remove('shown');
  renderCodeSlots(['•', '•', '•', '•', '•', '•']);
  $('codeTimerFill')?.setAttribute('width', '0');
  $('codeDetail').textContent = current?.code_ready ? '点击按钮显示六位验证码' : '登录后可生成六位验证码';
  $('copyButton').disabled = true;
}
function showCode(result) {
  privateCode = result.code;
  privateCodeKind = result.code_kind || '';
  hideAt = new Date(result.hide_at).getTime();
  if (!Number.isFinite(hideAt)) hideAt = Date.now() + 60000;
  codeTotalMs = Math.max(1000, hideAt - Date.now());
  renderCodeSlots(String(privateCode).padEnd(6, '•').slice(0, 6).split(''));
  $('codeDisplay').classList.add('shown');
  $('copyButton').disabled = false;
  tickCode();
  if (result.message) notice(result.message);
}
function tickCode() {
  if (!privateCode) return;
  const remainingMs = Math.max(0, hideAt - Date.now());
  const seconds = Math.ceil(remainingMs / 1000);
  if (!seconds) { clearCode(); return; }
  const ratio = Math.min(1, Math.max(0, remainingMs / codeTotalMs));
  $('codeTimerFill')?.setAttribute('width', String(Math.round(ratio * 1000)));
  $('codeDetail').textContent = (privateCodeKind === 'circle' ? '官方客户端配对码 · ' : '') + seconds + ' 秒后自动隐藏';
}

function deviceIcon(device) {
  const label = (device.model + ' ' + device.name).toLowerCase();
  if (label.includes('windows')) {
    return '<svg viewBox="0 0 40 40" fill="none" aria-hidden="true"><rect x="6" y="7" width="12.5" height="12.5" rx="2.5" fill="#3b82f6"/><rect x="21.5" y="7" width="12.5" height="12.5" rx="2.5" fill="#2563eb"/><rect x="6" y="21.5" width="12.5" height="12.5" rx="2.5" fill="#60a5fa"/><rect x="21.5" y="21.5" width="12.5" height="12.5" rx="2.5" fill="#1d4ed8"/></svg>';
  }
  if (label.includes('iphone') || label.includes('ipad')) {
    return '<svg viewBox="0 0 40 40" fill="none" aria-hidden="true"><rect x="11" y="4" width="18" height="32" rx="4.5" fill="#1e293b"/><rect x="13" y="6.5" width="14" height="27" rx="2.8" fill="#dbeafe"/><path d="M13 24L27 10v23.5H13z" fill="#bfdbfe"/><rect x="17.5" y="8" width="5" height="1.8" rx="0.9" fill="#1e293b"/></svg>';
  }
  return '<svg viewBox="0 0 40 40" fill="none" aria-hidden="true"><rect x="6.5" y="7" width="27" height="19" rx="3" fill="#1e293b"/><rect x="8.8" y="9.2" width="22.4" height="14.6" rx="1.6" fill="#dbeafe"/><path d="M8.8 23.8L31.2 9.2v14.6z" fill="#bfdbfe"/><path d="M3.5 27.5h33l-2.4 3.5H5.9z" fill="#94a3b8"/><rect x="16" y="27.5" width="8" height="1.2" rx="0.6" fill="#64748b"/></svg>';
}

function renderDevices(devices, status = current?.status || {}) {
  const grid = $('deviceGrid');
  grid.replaceChildren();
  if (!devices?.length) {
    const empty = document.createElement('div');
    empty.className = 'empty-state';
    const state = status?.state || '';
    if (state === 'device_identity_refresh_required' || state === 'device_query_failed') {
      empty.textContent = '设备列表读取失败，请重新连接当前账号后再试';
    } else if (state === 'refresh_failed') {
      empty.textContent = '登录续期暂未完成，设备列表暂不可用';
    } else if (status?.running) {
      empty.textContent = '正在同步设备列表…';
    } else {
      empty.textContent = '登录后，你的设备会显示在这里';
    }
    grid.append(empty);
    return;
  }
  for (const device of [...devices].sort((a, b) => Number(b.installation) - Number(a.installation))) {
    const card = document.createElement('article');
    card.className = 'device-card' + (device.installation ? ' installation' : '');
    const icon = document.createElement('div');
    icon.className = 'device-icon';
    icon.innerHTML = deviceIcon(device);
    const info = document.createElement('div');
    info.className = 'device-info';
    const name = document.createElement('h3');
    name.textContent = device.name || 'Apple 设备';
    const model = document.createElement('p');
    model.textContent = device.model || '已关联设备';
    info.append(name, model);
    const tags = document.createElement('div');
    tags.className = 'device-tags';
    if (device.installation) {
      const tag = document.createElement('span');
      tag.className = 'device-tag blue';
      tag.textContent = '本机登记';
      tags.append(tag);
    }
    if (device.trusted === true) {
      const tag = document.createElement('span');
      tag.className = 'device-tag green';
      tag.textContent = '受信任';
      tags.append(tag);
    }
    if (device.current && !device.installation) {
      const tag = document.createElement('span');
      tag.className = 'device-tag';
      tag.textContent = '当前认证设备';
      tags.append(tag);
    }
    if (tags.children.length) info.append(tags);
    card.append(icon, info);
    grid.append(card);
  }
}

function gib(value) { return value ? (value / 1073741824).toLocaleString('zh-CN', { maximumFractionDigits: 0 }) + 'GB' : '—'; }
function autoRefreshKey(kind, checkedAt) { return selectedAccount + '|' + kind + '|' + (checkedAt || 'none'); }
function cacheLabel(kind, checkedAt, stale) {
  if (!stale) return '已保存 · ' + (checkedAt ? time(checkedAt) : '刚刚');
  const key = autoRefreshKey(kind, checkedAt), state = autoRefreshState.get(key) || autoRefreshAttemptStatus(key);
  if (state === 'refreshing') return '正在后台核验 Apple…';
  if (state === 'failed') return '自动核验失败 · ' + (checkedAt ? time(checkedAt) : '刚刚');
  return '等待后台核验 · ' + (checkedAt ? time(checkedAt) : '刚刚');
}

function renderStorage(info, { checkedAt = '', stale = false } = {}) {
  storageAccount = selectedAccount;
  storageCheckedAt = checkedAt || '';
  const note = $('storageCacheNote'), visual = $('storageVisual');
  if (!info) {
    $('storageSummary').textContent = '登录后查询当前容量和使用情况。';
    note.textContent = '尚无本地状态';
    note.classList.remove('stale');
    if (visual) visual.hidden = true;
    return;
  }
  const format = x => (x / 1073741824).toLocaleString('zh-CN', { maximumFractionDigits: 2 }) + ' GB';
  $('storageSummary').textContent = '已用 ' + format(info.used_bytes) + ' / ' + format(info.total_bytes) + ' · 可用 ' + format(info.available_bytes);
  note.textContent = cacheLabel('storage', checkedAt, stale);
  note.classList.toggle('stale', !!stale);
  if (visual) {
    visual.hidden = false;
    const ratio = info.total_bytes > 0 ? Math.min(1, Math.max(0, info.used_bytes / info.total_bytes)) : 0;
    const width = ratio > 0 ? Math.max(18, Math.round(ratio * 1000)) : 0;
    $('storageBarFill')?.setAttribute('width', String(width));
    visual.classList.toggle('high-usage', ratio >= 0.85);
    $('storageUsedStat').textContent = format(info.used_bytes);
    $('storageAvailStat').textContent = format(info.available_bytes);
    $('storageTotalStat').textContent = format(info.total_bytes);
    $('storagePercentStat').textContent = (ratio * 100).toLocaleString('zh-CN', { maximumFractionDigits: 1 }) + '%';
  }
}

function updateFamilyCacheMeta(checkedAt = '', stale = false, hasInfo = !!familyInfo) {
  familyCheckedAt = checkedAt || '';
  familyStale = !!stale;
  const shell = $('familyCard');
  shell.classList.toggle('cache-stale', familyStale);
  if (!hasInfo) {
    $('familyCacheNote').textContent = '尚无本地状态';
    $('familyRefreshButton').textContent = '读取 Apple 状态';
    return;
  }
  const key = autoRefreshKey('family', familyCheckedAt), state = autoRefreshState.get(key) || autoRefreshAttemptStatus(key);
  $('familyCacheNote').textContent = cacheLabel('family', familyCheckedAt, familyStale);
  $('familyRefreshButton').textContent = state === 'refreshing' ? '正在核验…' : '重新核验 Apple';
}

function renderFamily(info, { checkedAt = '', stale = false, animate = true } = {}) {
  familyInfo = info || null;
  familyAccount = selectedAccount;
  const shell = $('familyCard'), card = $('familyStatus'), metrics = $('familyMetrics'), map = $('familyMap'), controls = $('familyControls'), removeControls = $('familyRemoveControls');
  card.className = 'family-status';
  updateFamilyCacheMeta(checkedAt, stale, !!info);
  if (!info) {
    $('familySummary').textContent = '尚未读取家庭状态';
    $('familyDetail').textContent = '操作前会实时核对成员、组织者和共享储存状态。';
    $('familyStatusChip').textContent = '待读取';
    metrics.hidden = true; map.hidden = true; controls.hidden = true; removeControls.hidden = true;
    return;
  }
  if (info.organizer_account) {
    poolOrganizerAccount = info.organizer_account;
    poolOrganizerName = info.organizer_name || info.organizer_account;
    localStorage.setItem('family-organizer-account', poolOrganizerAccount);
    localStorage.setItem('family-organizer-name', poolOrganizerName);
  }
  metrics.hidden = false;
  controls.hidden = !!info.member;
  removeControls.hidden = !info.organizer;
  map.hidden = !info.member;
  if (info.member) {
    card.classList.add(info.organizer ? 'organizer' : 'member');
    $('familySummary').textContent = info.organizer ? '当前账号是家庭母号' : '已加入家庭 · ' + info.members + ' 位成员';
    $('familyDetail').textContent = info.organizer ? '组织者身份已由 Apple 实时回读确认。' : '当前账号是普通成员，服务端已确认 self=1。';
    $('familyStatusChip').textContent = info.organizer ? '母号' : '家庭成员';
  } else if (info.eligibility_known && info.eligible) {
    card.classList.add('eligible');
    $('familySummary').textContent = '尚未加入家庭 · 当前可加入';
    $('familyDetail').textContent = '选择池内母号后提交；系统会双端回读，不以单次 HTTP 响应判定成功。';
    $('familyStatusChip').textContent = '可加入';
  } else {
    $('familySummary').textContent = '当前没有可确认的家庭成员资格';
    $('familyDetail').textContent = info.eligibility_known ? 'Apple 当前未确认可加入家庭。' : 'Apple 没有返回明确的家庭资格。';
    $('familyStatusChip').textContent = '未确认';
  }
  $('familyMemberCount').textContent = info.members || '0';
  $('familyRole').textContent = info.organizer ? '组织者' : info.member ? '成员' : '未加入';
  $('familyStorageState').textContent = !info.storage_known ? '未返回' : info.storage_participating && info.storage_available && info.storage_enabled ? '已启用' : info.storage_available ? '可加入' : '未共享';
  $('familyStorageBytes').textContent = gib(info.storage_bytes);
  if (info.member) {
    const selected = current?.accounts?.find(account => account.id === selectedAccount);
    const currentName = selected?.name || selectedAccount || '当前账号';
    $('familyOrganizerName').textContent = info.organizer_name || poolOrganizerName || '池外组织者';
    $('familyCurrentName').textContent = currentName;
    $('familyCurrentAvatar').textContent = (currentName || 'A').slice(0, 1).toUpperCase();
    map.classList.toggle('self-organizer', !!info.organizer);
  }
  const sharing = [];
  sharing.push(info.purchase_sharing_known ? (info.purchase_sharing_enabled ? '购买共享已开启' : '购买共享已关闭') : '未请求购买共享');
  sharing.push(info.location_sharing_known ? (info.location_sharing_enabled ? '位置共享已开启' : '位置共享已关闭') : '未请求位置共享');
  $('familySafety').textContent = sharing.join(' · ') + '。家庭验证码只在 Apple 挑战出现后由当前账号自己的本地 Mac 身份生成。';
  if (animate) {
    shell.classList.remove('family-updated');
    requestAnimationFrame(() => shell.classList.add('family-updated'));
    window.setTimeout(() => shell.classList.remove('family-updated'), 700);
  }
}

function resetHME() {
  hmeInfo = null; hmeAccount = ''; hmeCandidate = ''; hmeVisible = 100; hmeEditing = '';
  $('hmeCandidatePanel').hidden = true;
  $('hmeReachablePanel').hidden = true;
  $('hmeReachableCodeForm').hidden = true;
  $('hmeReachableEmail').value = '';
  $('hmeReachableCode').value = '';
  $('hmeSearch').value = '';
  renderHME(null);
}

function renderActivation(view) {
  const activated = !!view?.icloud_activated, ready = !!view?.activation_ready && !activated, card = $('activationCard');
  if (activationAccount && activationAccount !== selectedAccount) { activationInfo = null; activationAccount = ''; }
  card.hidden = !ready;
  if (!ready) return;
  $('activationState').textContent = activationInfo?.required ? '需要同意条款' : '待检查';
  $('activationSummary').textContent = activationInfo?.required ? 'Apple 要求先接受当前条款；确认后后台会自动生成本账号 NAC、完成 delegate 并读取配额。' : '账号验证码会话已保存；检查 Apple 当前条款后可在此继续激活。';
  const versions = activationInfo?.required ? '地区 ' + activationInfo.region + ' · iCloud ' + activationInfo.icloud_version + ' · 媒体服务 ' + (activationInfo.media_version || '无更新') : '';
  $('activationVersions').textContent = versions;
  $('activationVersions').hidden = !versions;
  for (const [id, url] of [['activationICloudTerms', activationInfo?.icloud_terms_url], ['activationMediaTerms', activationInfo?.media_terms_url]]) {
    const link = $(id);
    link.hidden = !url;
    if (url) link.href = url;
  }
  $('activationCheckButton').hidden = !!activationInfo?.required;
  $('activationCheckButton').disabled = busy;
  $('activationAcceptButton').hidden = !activationInfo?.required;
  $('activationAcceptButton').disabled = busy;
}

function hmeDate(value) {
  if (!value) return '时间未知';
  const stamp = Number(value);
  const date = new Date(stamp < 100000000000 ? stamp * 1000 : stamp);
  return Number.isNaN(date.getTime()) ? '时间未知' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date);
}
function showHMECandidate(candidate) {
  hmeCandidate = candidate || '';
  $('hmeCandidateAddress').textContent = hmeCandidate || '—';
  $('hmeCandidatePanel').hidden = !hmeCandidate;
  if (hmeCandidate) {
    $('hmeCreateLabel').value = '';
    $('hmeCreateNote').value = '';
    $('hmeCreateLabel').focus();
  }
}
function hmeForwardOptions(info) {
  const values = [info?.selected_forward_to, ...(info?.forward_to_emails || [])].map(value => (value || '').trim()).filter(Boolean);
  return values.filter((value, index) => values.findIndex(item => item.toLowerCase() === value.toLowerCase()) === index);
}
function renderHMEForwarding(info) {
  const panel = $('hmeForwardSettings'), select = $('hmeForwardSelect'), selected = (info?.selected_forward_to || '').trim(), options = hmeForwardOptions(info);
  panel.hidden = !info;
  $('hmeReachablePanel').hidden = !info;
  select.replaceChildren();
  $('hmeForwardCurrent').textContent = selected || 'Apple 未返回当前目标';
  for (const address of options) {
    const option = document.createElement('option');
    option.value = address;
    option.textContent = address + (address.toLowerCase() === selected.toLowerCase() ? ' · 当前' : '');
    select.append(option);
  }
  if (selected) select.value = selected;
  const hasAlternative = options.some(value => value.toLowerCase() !== selected.toLowerCase());
  $('hmeForwardHint').textContent = hasAlternative ? '所有隐私邮箱使用同一个目标；保存后会实时回读确认。' : '当前账号没有其他已验证邮箱；可以在下方直接协议添加并验证。';
  select.disabled = !hasAlternative;
  $('hmeForwardButton').disabled = busy || !hasAlternative || !selected || select.value.toLowerCase() === selected.toLowerCase();
}
function renderHME(info) {
  hmeInfo = info || null;
  hmeAccount = selectedAccount;
  renderHMEForwarding(info);
  const list = $('hmeList');
  list.replaceChildren();
  $('hmeActiveCount').textContent = info?.active_count ?? '—';
  $('hmeInactiveCount').textContent = info?.inactive_count ?? '—';
  if (!info) {
    $('hmeSummary').textContent = '读取后可按标签、备注或邮箱搜索。';
    const empty = document.createElement('div');
    empty.className = 'hme-empty';
    empty.textContent = '尚未读取当前子号的地址';
    list.append(empty);
    $('hmeMoreButton').hidden = true;
    return;
  }
  const query = $('hmeSearch').value.trim().toLowerCase();
  const aliases = (info.aliases || []).filter(item => (hmeFilter === 'all' || (hmeFilter === 'active') === !!item.active) && (!query || [item.email, item.label, item.note, item.origin].some(value => (value || '').toLowerCase().includes(query))));
  $('hmeSummary').textContent = '共 ' + (info.aliases?.length || 0) + ' 个地址 · ' + (aliases.length === info.aliases?.length ? '显示全部' : '当前筛选 ' + aliases.length + ' 个');
  if (!aliases.length) {
    const empty = document.createElement('div');
    empty.className = 'hme-empty';
    empty.textContent = query ? '没有匹配的地址' : '当前分类没有地址';
    list.append(empty);
  }
  for (const item of aliases.slice(0, hmeVisible)) {
    const row = document.createElement('article');
    row.className = 'hme-row' + (item.active ? '' : ' inactive');
    const identity = document.createElement('div');
    identity.className = 'hme-identity';
    const email = document.createElement('strong');
    email.textContent = item.email;
    const meta = document.createElement('small');
    meta.textContent = (item.label || '无标签') + ' · ' + hmeDate(item.create_timestamp);
    identity.append(email, meta);
    if (item.note) {
      const note = document.createElement('p');
      note.textContent = item.note;
      identity.append(note);
    }
    const forward = document.createElement('div');
    forward.className = 'hme-forward';
    const state = document.createElement('span');
    state.className = 'hme-state ' + (item.active ? 'active' : 'inactive');
    state.textContent = item.active ? '转发中' : '已停用';
    const destination = document.createElement('small');
    destination.textContent = item.forward_to_email ? '转发至 ' + item.forward_to_email : (item.origin || 'iCloud+');
    forward.append(state, destination);
    const actions = document.createElement('div');
    actions.className = 'hme-actions';
    const copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'icon-button';
    copy.title = '复制邮箱';
    copy.setAttribute('aria-label', '复制 ' + item.email);
    copy.textContent = '⧉';
    copy.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(item.email); notice('邮箱地址已复制'); }
      catch { notice('复制失败', true); }
    });
    const edit = document.createElement('button');
    edit.type = 'button';
    edit.className = 'text-button';
    edit.textContent = '编辑';
    edit.addEventListener('click', () => {
      hmeEditing = item.email;
      $('hmeEditAddress').textContent = item.email;
      $('hmeEditLabel').value = item.label || '';
      $('hmeEditNote').value = item.note || '';
      $('hmeEditDialog').showModal();
      $('hmeEditLabel').focus();
    });
    actions.append(copy, edit);
    if (item.active) {
      const deactivate = document.createElement('button');
      deactivate.type = 'button';
      deactivate.className = 'text-button danger-text';
      deactivate.textContent = '停用';
      deactivate.addEventListener('click', () => {
        if (confirm('停用 ' + item.email + '？停用后将停止转发邮件，但可以恢复。')) action('hme-deactivate', { code: item.email });
      });
      actions.append(deactivate);
    } else {
      const reactivate = document.createElement('button');
      reactivate.type = 'button';
      reactivate.className = 'text-button';
      reactivate.textContent = '恢复';
      reactivate.addEventListener('click', () => action('hme-reactivate', { code: item.email }));
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'text-button danger-text';
      remove.textContent = '永久删除';
      remove.addEventListener('click', () => {
        if (confirm('永久删除 ' + item.email + '？删除后无法恢复，也不能再使用这个地址。')) action('hme-delete', { code: item.email });
      });
      actions.append(reactivate, remove);
    }
    row.append(identity, forward, actions);
    list.append(row);
  }
  $('hmeMoreButton').hidden = aliases.length <= hmeVisible;
}

function renderOrganizerOptions(accounts, selected) {
  const select = $('familyOrganizer'), previous = select.value;
  select.replaceChildren();
  const placeholder = document.createElement('option');
  placeholder.value = '';
  placeholder.textContent = '选择母号';
  select.append(placeholder);
  for (const account of accounts) {
    if (account.id === selected || !account.icloud_activated) continue;
    const option = document.createElement('option');
    option.value = account.id;
    option.textContent = (account.id === poolOrganizerAccount ? '★ 家庭母号 · ' : '') + (account.name || account.id) + ' · ' + account.id;
    select.append(option);
  }
  if ([...select.options].some(option => option.value === previous)) select.value = previous;
  else if ([...select.options].some(option => option.value === poolOrganizerAccount)) select.value = poolOrganizerAccount;
}
function renderFamilyMemberOptions(accounts, organizerID) {
  const select = $('familyRemoveMember'), previous = select.value;
  select.replaceChildren();
  const placeholder = document.createElement('option');
  placeholder.value = '';
  placeholder.textContent = '选择家庭成员';
  select.append(placeholder);
  for (const account of accounts) {
    if (account.id === organizerID || !account.family_member || account.family_organizer || account.family_stale || account.family_organizer_account !== organizerID) continue;
    const option = document.createElement('option');
    option.value = account.id;
    option.textContent = (account.name || account.id) + ' · ' + account.id;
    select.append(option);
  }
  if ([...select.options].some(option => option.value === previous)) select.value = previous;
}

function renderAccounts(view) {
  const list = $('accountList');
  if (!list) return;
  const accounts = view.accounts || [];
  const cachedOrganizers = accounts.filter(account => account.family_organizer);
  if (cachedOrganizers.length === 1) {
    poolOrganizerAccount = cachedOrganizers[0].id;
    poolOrganizerName = cachedOrganizers[0].name || cachedOrganizers[0].id;
    localStorage.setItem('family-organizer-account', poolOrganizerAccount);
    localStorage.setItem('family-organizer-name', poolOrganizerName);
  }
  renderOrganizerOptions(accounts, view.selected);
  renderFamilyMemberOptions(accounts, view.selected);
  list.replaceChildren();
  for (const account of accounts) {
    const organizer = account.family_organizer || account.id === poolOrganizerAccount;
    const item = document.createElement('button');
    item.type = 'button';
    item.className = 'account-item' + (account.id === view.selected ? ' active' : '') + (organizer ? ' family-organizer' : '') + (organizer && account.family_stale ? ' stale-family' : '');
    const avatar = document.createElement('span');
    avatar.className = 'account-avatar';
    avatar.textContent = organizer ? '★' : (account.name || '?').slice(0, 1).toUpperCase();
    const copy = document.createElement('span');
    copy.className = 'account-item-copy';
    const strong = document.createElement('strong');
    strong.textContent = account.name || account.id;
    if (organizer) {
      const role = document.createElement('span');
      role.className = 'account-role';
      role.textContent = account.family_stale ? '母号待核验' : '母号';
      strong.append(role);
    }
    const small = document.createElement('small');
    const state = account.running ? (account.authenticated ? '在线' : '连接中') : account.icloud_activated ? 'iCloud 已激活' : '已停止';
    small.textContent = organizer ? '家庭组织者 · ' + state : account.running && account.authenticated ? '在线工作区' : state;
    copy.append(strong, small);
    const dot = document.createElement('i');
    dot.className = account.running && account.authenticated ? 'on' : account.running ? 'waiting' : account.icloud_activated ? 'activated' : '';
    item.append(avatar, copy, dot);
    item.addEventListener('click', () => {
      selectedAccount = account.id;
      localStorage.setItem('selected-account', account.id);
      familyInfo = null; familyAccount = ''; familyCheckedAt = ''; familyStale = false;
      storageAccount = ''; storageCheckedAt = ''; planAccount = ''; planCheckedAt = '';
      purchaseAction = ''; purchaseLabel = ''; activationInfo = null; activationAccount = '';
      $('purchaseButton').textContent = '开通 iCloud+ · 50GB';
      renderFamily(null);
      renderStorage(null);
      renderPlans(null);
      resetHME();
      clearCode();
      poll();
    });
    list.append(item);
  }
}

function openLogin(mode) {
  if (mode === 'reconnect' && current?.accounts?.length === 0) mode = 'add';
  loginMode = mode;
  loginAccount = selectedAccount;
  $('appleID').value = mode === 'reconnect' ? (current?.login_account || '') : '';
  $('appleID').readOnly = mode === 'reconnect' && !!$('appleID').value;
  $('password').value = '';
  $('rememberPassword').checked = mode === 'reconnect' && !!current?.status?.password_remembered;
  $('twofactor').value = 'trusted-device';
  $('loginDialog').querySelector('h2').textContent = mode === 'add' ? '添加 Apple 账户' : '重新连接当前账号';
  $('loginDialog').showModal();
  ($('appleID').readOnly ? $('password') : $('appleID')).focus();
}

function scheduleAutoRefresh(view, activated) {
  if (busy || autoRefreshScheduled || !activated) return;
  const candidates = [];
  if (view.family && view.family_stale) candidates.push({ kind: 'family', action: 'family-info', checkedAt: view.family_checked_at });
  if (view.storage && view.storage_stale) candidates.push({ kind: 'storage', action: 'storage-info', checkedAt: view.storage_checked_at });
  if (view.storage_plan && view.storage_plan_stale) candidates.push({ kind: 'plan', action: 'storage-plan', checkedAt: view.storage_plan_checked_at });
  if (!candidates.length) { releaseAutoRefreshLease(selectedAccount); return; }
  const target = candidates.find(item => { const key = autoRefreshKey(item.kind, item.checkedAt); return !autoRefreshState.has(key) && !autoRefreshAttempted(key); });
  if (!target) { releaseAutoRefreshLease(selectedAccount); return; }
  const account = selectedAccount, key = autoRefreshKey(target.kind, target.checkedAt);
  if (!acquireAutoRefreshLease(account)) return;
  markAutoRefreshAttempt(key);
  autoRefreshState.set(key, 'refreshing');
  autoRefreshScheduled = true;
  if (target.kind === 'family') updateFamilyCacheMeta(view.family_checked_at, true, true);
  if (target.kind === 'storage') renderStorage(view.storage, { checkedAt: view.storage_checked_at, stale: true });
  if (target.kind === 'plan') renderPlans(view.storage_plan, { checkedAt: view.storage_plan_checked_at, stale: true });
  window.setTimeout(() => {
    autoRefreshScheduled = false;
    if (account !== selectedAccount || busy) {
      autoRefreshState.delete(key);
      clearAutoRefreshAttempt(key);
      releaseAutoRefreshLease(account);
      return;
    }
    renewAutoRefreshLease(account);
    action(target.action, {}, { silent: true, automatic: true, autoKey: key });
  }, 80);
}

function hasPaidStorage(view) { return !!view?.storage && !view.storage_stale && view.storage.paid_bytes > 0; }

function render(view) {
  current = view;
  selectedAccount = view.selected || selectedAccount;
  if (view.accounts) renderAccounts(view);
  if (view.family) {
    const changed = familyAccount !== selectedAccount || !familyInfo || familyCheckedAt !== view.family_checked_at;
    if (changed) renderFamily(view.family, { checkedAt: view.family_checked_at, stale: view.family_stale, animate: false });
    else updateFamilyCacheMeta(view.family_checked_at, view.family_stale, true);
  } else if (familyAccount === selectedAccount && familyInfo) renderFamily(null);
  if (view.storage) {
    if (storageAccount !== selectedAccount || storageCheckedAt !== view.storage_checked_at) renderStorage(view.storage, { checkedAt: view.storage_checked_at, stale: view.storage_stale });
  } else if (storageAccount === selectedAccount && storageCheckedAt) renderStorage(null);
  if (view.storage_plan) {
    if (planAccount !== selectedAccount || planCheckedAt !== view.storage_plan_checked_at) renderPlans(view.storage_plan, { checkedAt: view.storage_plan_checked_at, stale: view.storage_plan_stale });
  } else if (planAccount === selectedAccount && planCheckedAt) renderPlans(null);
  renderActivation(view);

  const status = view.status || {}, online = status.running && status.authenticated, activated = !!view.icloud_activated, waiting = status.verification_required, running = status.running || view.launching, starting = running && !online && !waiting;
  const sessionCard = $('sessionCard');
  if (sessionCard) {
    sessionCard.classList.toggle('is-online', !!online);
    sessionCard.classList.toggle('is-waiting', !!(waiting || starting));
  }

  if (storeAuthAccount !== selectedAccount || storeAuthPID !== (status.pid || 0) || !online) {
    $('purchaseResult').hidden = true;
    $('purchaseResult').textContent = '';
  }
  if (storeAuthAccount !== selectedAccount || storeAuthPID !== (status.pid || 0) || (typeof view.store_session_ready !== 'boolean' && storeAuthRefresh !== (status.last_refresh_at || '')) || !online) {
    storeAuthReady = false;
    storeAuthAccount = selectedAccount;
    storeAuthPID = status.pid || 0;
    storeAuthRefresh = status.last_refresh_at || '';
    $('storeAuthSummary').textContent = '购买会话尚未验证 · 点击验证购买会话后继续';
  }
  if (typeof view.store_session_ready === 'boolean') {
    const wasReady = storeAuthReady;
    storeAuthReady = !!view.store_session_ready && !!online && !!view.control_ready;
    storeAuthRefresh = status.last_refresh_at || '';
    if (storeAuthReady) $('storeAuthSummary').textContent = '购买会话已保留 · 同一账号登录续期后继续使用 · Apple 仍可能要求重新验证';
    else if (wasReady) $('storeAuthSummary').textContent = '购买会话不可用，请重新验证购买会话。';
  }

  if (giftOwner !== selectedAccount || giftPID !== (status.pid || 0) || !online) {
    giftPreparation = '';
    $('giftCode').value = '';
    $('giftResult').textContent = '先检查当前账号的兑换会话；会话检查不会提交兑换。';
    giftOwner = selectedAccount;
    giftPID = status.pid || 0;
  }
  $('giftPrepareButton').disabled = busy || !online || !view.control_ready || !view.gift_card_available;
  $('giftCode').disabled = busy || !giftPreparation;
  $('giftRedeemButton').disabled = busy || !online || !view.control_ready || !giftPreparation || !/^X[A-Z0-9]{15}$/.test($('giftCode').value.trim().toUpperCase());
  if (online && !view.gift_card_available) $('giftResult').textContent = '请重新连接当前账号加载礼品卡兑换功能。';

  const titles = {
    initializing: '正在初始化本机身份',
    signing_in: '正在登录 Apple 账户',
    starting: '正在建立后台连接',
    refresh_failed: '登录刷新暂未完成',
    verification_required: '需要重新验证账户',
    awaiting_sms: '完成短信验证',
    awaiting_existing_sms: '输入已有短信验证码',
    awaiting_device_code: '输入设备验证码',
    awaiting_account_validation: '验证 Apple 账户',
    awaiting_pairing: '等待设备配对',
    awaiting_pairing_code: '输入设备配对码',
    verifying_pairing: '正在验证设备配对'
  };
  $('sessionTitle').textContent = online ? (status.push_issue === 'reconnecting' ? '登录正常 · 推送重连中' : status.state === 'refresh_failed' ? '已登录 · 续期暂未完成' : '登录持续在线') : (titles[status.state] || (status.state === 'failed' && !activated ? '后台异常停止' : starting ? '正在连接 Apple' : waiting ? '需要短信验证' : activated ? 'iCloud 已激活' : '连接你的 Apple 账户'));
  $('sessionDescription').textContent = online ? (status.push_issue === 'reconnecting' ? '账户会话仍保留，后台正在恢复推送连接。' : '设备身份已保留，后台会自动保持登录。') : waiting ? (status.state === 'awaiting_account_validation' ? '请使用受信任 Apple 设备收到的验证码完成账户验证。' : '请使用绑定手机号收到的验证码完成登录。') : starting ? '正在恢复这台电脑的设备身份，请稍等。' : activated ? 'Apple 已接受该账号的 iCloud 会话，可直接读取配额与可购方案。' : status.state === 'failed' ? '后台运行出错，设备身份和已保存的会话仍保留。请重新连接。' : '登录后，即可查看设备和生成本机验证码。';

  const pairingStates = {
    awaiting_approval: '收到官方客户端配对请求，请在登录请求中允许。',
    awaiting_client_proof: '等待官方客户端校验配对验证码。',
    code_exchanged: '配对证明已通过，已交换本机验证码；等待官方客户端结果。',
    exchange_complete_no_voucher: '验证码交换已完成。本机没有加密数据的信任凭据；官方验证结果仍需核实。',
    request_rejected: '配对请求参数未通过校验，尚未批准。',
    exchange_failed: '配对交换未完成，账户会话保留。',
    interrupted: '推送连接变化，原配对事务已终止，请重新发起验证。'
  };
  const capabilities = [];
  if (status.push_issue === 'token_changed') capabilities.push('推送凭据已变化，请停止后台后重新连接，以更新设备登记。');
  if (status.pairing_state) capabilities.push(pairingStates[status.pairing_state] || '正在处理客户端配对。');
  else if (status.pairing_unsupported) capabilities.push('后台是旧版本，尚不支持客户端配对，请更新并重启后台。');
  $('capabilityNotice').hidden = !capabilities.length;
  $('capabilityNotice').textContent = capabilities.join(' ');

  $('statusBadge').className = 'status-badge' + (online ? ' online' : waiting || starting ? ' waiting' : '');
  $('statusBadge').replaceChildren();
  const dot = document.createElement('i');
  $('statusBadge').append(dot, document.createTextNode(online ? '在线' : waiting ? '待验证' : starting ? '连接中' : '未连接'));

  $('authState').textContent = online ? '已认证' : activated ? 'iCloud 已激活' : waiting ? '等待验证' : starting ? '认证中' : '未登录';
  $('pushState').textContent = status.push_connected ? '已连接' : status.push_issue === 'reconnecting' ? '重连中' : '未连接';
  $('renewState').textContent = online ? '每 10 分钟' : activated ? '按需验证' : '等待登录';
  for (const [id, active] of [['authDot', online], ['pushDot', status.push_connected], ['renewDot', online]]) $(id).classList.toggle('active', !!active);

  $('accountTag').hidden = !view.account;
  $('accountTag').textContent = view.account;
  $('sidebarAccount').replaceChildren(document.createTextNode(view.account || '本机账户'));
  const sub = document.createElement('small');
  sub.textContent = 'Windows 工作空间';
  $('sidebarAccount').append(sub);

  $('startButton').hidden = running;
  $('startButton').textContent = status.state === 'failed' ? '重新连接' : '连接账户';
  $('startButton').disabled = busy || starting;
  $('renewButton').hidden = !online;
  $('renewButton').disabled = busy || !view.control_ready;
  $('stopButton').hidden = !status.running;
  $('stopButton').disabled = busy;

  $('sessionHint').textContent = online ? '上次续期 ' + time(status.last_refresh_at) : activated ? '已保存 Apple 验收会话' : '登录身份会在本机保留';
  $('deviceCount').textContent = status.device_count || '—';
  $('navCount').textContent = status.device_count || '—';

  const storageRefreshing = autoRefreshAttemptStatus(autoRefreshKey('storage', view.storage_checked_at)) === 'refreshing', planRefreshing = autoRefreshAttemptStatus(autoRefreshKey('plan', view.storage_plan_checked_at)) === 'refreshing';
  $('codeButton').disabled = busy || !view.code_ready || !online;
  $('devicesButton').disabled = busy || !online || !view.control_ready;
  $('storageButton').disabled = busy || storageRefreshing || (!activated && (!online || !view.control_ready));
  $('planButton').disabled = busy || planRefreshing || (!activated && (!online || !view.control_ready));
  $('purchaseButton').disabled = busy || !online || !view.control_ready || !purchaseAction || hasPaidStorage(view);
  $('paymentCheckButton').disabled = busy || !online || !view.control_ready;
  $('storeAuthButton').disabled = busy || !online || !view.control_ready;

  if (hasPaidStorage(view)) {
    $('purchaseButton').textContent = 'iCloud+ 已开通';
    $('planSummary').textContent = 'Apple 已核验付费容量 ' + gib(view.storage.paid_bytes) + '，无需再次开通。';
  } else if (purchaseAction) {
    $('purchaseButton').textContent = '开通 50GB · ' + purchaseLabel;
  }

  const cloudHint = hasPaidStorage(view) ? 'iCloud+ 已开通 · 付费容量 ' + gib(view.storage.paid_bytes) + '。可查询储存空间更新使用情况。' : !online ? '请先在概览中连接账户并完成验证。已有激活会话可查询容量和方案，开通订阅仍需后台在线。' : !view.control_ready ? '账户已登录，正在等待后台控制连接。' : busy ? '正在处理当前操作，请稍候。' : !purchaseAction ? '请先读取可购方案；仅支持已核验的 50GB 币种与价格。' : purchaseAction === 'storage-skill-purchase-us' && !storeAuthReady ? '点击开通将先自动检查购买会话、报价和现有订阅，通过后再由你确认扣款。' : '报价已核验，可确认开通；Apple 可能要求验证购买会话。';
  $('icloudAccessHint').textContent = cloudHint;
  $('purchaseAvailability').textContent = cloudHint;
  $('icloudLoginLink').hidden = online;
  $('purchaseButton').title = $('purchaseButton').disabled ? cloudHint : '确认开通 iCloud+ 50GB';

  if (familyAccount && familyAccount !== selectedAccount) renderFamily(null);
  const selectedFamily = familyAccount === selectedAccount ? familyInfo : null, familyRefreshing = autoRefreshAttemptStatus(autoRefreshKey('family', view.family_checked_at)) === 'refreshing';
  $('familyRefreshButton').disabled = busy || familyRefreshing || !activated;
  $('familyOrganizer').disabled = busy || !online || !!selectedFamily?.member || familyStale;
  $('familyAddButton').disabled = busy || !online || !view.control_ready || !!selectedFamily?.member || familyStale || !$('familyOrganizer').value;
  $('familyJoinStorageButton').disabled = busy || !activated || familyStale || !selectedFamily?.member || !selectedFamily?.storage_known || !selectedFamily?.storage_available || selectedFamily?.storage_participating;
  $('familyRemoveMember').disabled = busy || familyStale || !selectedFamily?.organizer;
  $('familyRemoveButton').disabled = busy || familyStale || !selectedFamily?.organizer || !$('familyRemoveMember').value;

  const hmeEligible = !!selectedFamily?.member && !selectedFamily?.organizer && selectedFamily?.storage_known && selectedFamily?.storage_participating && selectedFamily?.storage_available && selectedFamily?.storage_enabled;
  $('hmeCard').hidden = !hmeEligible;
  const hmeReady = hmeEligible && !familyStale && online && view.control_ready && view.hide_my_email_available;
  $('hmeListButton').disabled = busy || !hmeReady;
  $('hmeGenerateButton').disabled = busy || !hmeReady;
  $('hmeReserveButton').disabled = busy || !hmeReady || !hmeCandidate || !$('hmeCreateLabel').value.trim();
  if (hmeEligible && !view.hide_my_email_available && !hmeInfo) $('hmeSummary').textContent = online ? '当前后台版本不支持隐私邮箱，请重新连接此账号完成升级。' : '重新连接此账号后可读取隐私邮箱。';
  if (hmeAccount && hmeAccount !== selectedAccount) resetHME();

  const forwardChoice = $('hmeForwardSelect').value, forwardCurrent = hmeInfo?.selected_forward_to || '';
  $('hmeForwardButton').disabled = busy || !hmeReady || !forwardChoice || forwardChoice.toLowerCase() === forwardCurrent.toLowerCase();
  $('hmeReachableEmail').disabled = busy || !hmeReady || !$('hmeReachableCodeForm').hidden;
  $('hmeReachableStartButton').disabled = busy || !hmeReady || !$('hmeReachableEmail').value.trim() || !$('hmeReachableCodeForm').hidden;
  $('hmeReachableCode').disabled = busy;
  $('hmeReachableCodeForm').querySelector('button').disabled = busy;

  $('smsPanel').hidden = !['awaiting_sms', 'awaiting_existing_sms', 'awaiting_device_code', 'awaiting_account_validation'].includes(status.state);
  const smsCode = ['awaiting_sms', 'awaiting_existing_sms'].includes(status.state), deviceCode = !smsCode;
  $('smsPanel').querySelector('h3').textContent = deviceCode ? '输入受信任设备上的验证码' : '输入短信验证码';
  $('smsPanel').querySelector('p').textContent = deviceCode ? '请在已登录该账号的 Apple 设备上获取六位验证码。' : status.state === 'awaiting_existing_sms' ? '提交已经收到的短信码；本次不会再次发送短信。' : '验证码已发送到账户绑定的手机号。';
  $('resendButton').hidden = status.state !== 'awaiting_sms';
  $('resendButton').disabled = busy;
  $('smsForm').querySelector('button[type="submit"]').disabled = busy;

  $('pairingPanel').hidden = !['awaiting_pairing_code', 'verifying_pairing'].includes(status.state);
  $('pairingCode').disabled = busy || status.state === 'verifying_pairing';
  $('pairingForm').querySelector('button').disabled = busy || status.state === 'verifying_pairing';

  $('deviceUpdate').textContent = status.last_device_query_at && !status.last_device_query_at.startsWith('0001') ? '最近同步 ' + time(status.last_device_query_at) + ' · ' + status.device_count + ' 台设备' : '设备列表与 Apple 账户同步';
  renderDevices(view.devices);

  const pending = view.pending;
  $('requests').classList.toggle('has-pending', !!pending);
  const reqSymbol = $('requestContent')?.querySelector('.request-symbol');
  if (reqSymbol) reqSymbol.textContent = pending ? '!' : '✓';
  $('requestDot').hidden = !pending;
  $('requestTag').textContent = pending ? '待处理' : '等待请求';
  $('requestTag').classList.toggle('pending', !!pending);
  $('requestTitle').textContent = pending ? (pending.kind === 'circle' ? '官方客户端配对验证' : '收到新的 Apple 登录请求') : '没有待处理的登录请求';
  $('requestText').textContent = pending ? (pending.kind === 'circle' ? '允许后会显示配对码，请输入到发起验证的官方客户端。' : '请确认这是你正在进行的登录。') : '收到新的请求时，它会第一时间出现在这里。';
  $('requestActions').hidden = !pending;
  $('allowButton').disabled = busy;
  $('denyButton').disabled = busy;

  $('lastUpdated').textContent = running ? '后台运行中 · ' + time(status.updated_at) : '后台未运行';
  if (!privateCode) $('codeDetail').textContent = view.code_ready ? '点击按钮显示六位验证码' : '登录后可生成六位验证码';
  if (online && lastState !== status.state) activity('后台登录已连接');
  if (!online && lastState === 'online') { activity('后台登录状态已变化'); clearCode(); }
  lastState = status.state;
  if (status.last_device_query_at && !status.last_device_query_at.startsWith('0001') && lastQuery !== status.last_device_query_at) {
    activity('设备列表已同步 · ' + status.device_count + ' 台', status.last_device_query_at);
    lastQuery = status.last_device_query_at;
  }
  if (status.refresh_count && lastRefresh !== status.last_refresh_at) {
    activity('登录续期成功', status.last_refresh_at);
    lastRefresh = status.last_refresh_at;
  }
  if (pending && lastRequest !== pending.id) {
    activity('收到新的登录请求', pending.received_at);
    lastRequest = pending.id;
  }
  if (view.message && !running) $('sessionDescription').textContent = view.message;
  scheduleAutoRefresh(view, activated);
}

let polling = false;
async function poll() {
  if (polling) return;
  polling = true;
  const requested = selectedAccount;
  try {
    const view = await api('view');
    if (requested === selectedAccount) render(view);
  } catch (error) {
    $('lastUpdated').textContent = '面板连接暂时中断';
  } finally {
    polling = false;
  }
}

function renderPlans(info, { checkedAt = '', stale = false } = {}) {
  planAccount = selectedAccount;
  planCheckedAt = checkedAt || '';
  const list = $('planList'), note = $('planCacheNote');
  list.replaceChildren();
  purchaseAction = '';
  purchaseLabel = '';
  $('purchaseButton').textContent = '开通 iCloud+ · 50GB';
  if (!info?.plans?.length) {
    $('planSummary').textContent = info ? 'Apple 没有返回可购方案。' : '先读取 Apple 实时报价；仅已验证的币种与价格会启用购买按钮。';
    note.textContent = info ? cacheLabel('plan', checkedAt, stale) : '尚无本地状态';
    note.classList.toggle('stale', !!info && !!stale);
    return;
  }
  note.textContent = cacheLabel('plan', checkedAt, stale);
  note.classList.toggle('stale', !!stale);
  let target = null;
  for (const plan of info.plans) {
    const is50GB = plan.bytes === 50 * 1024 * 1024 * 1024 && plan.available;
    const isVerified = is50GB && ((plan.currency === 'CNY' && plan.price_minor === 600) || (plan.currency === 'USD' && plan.price_minor === 99));
    const row = document.createElement('div');
    row.className = 'plan-row' + (isVerified ? ' verified' : '');
    const left = document.createElement('div');
    left.className = 'plan-title';
    const name = document.createElement('strong');
    name.textContent = (plan.bytes ? gib(plan.bytes) : '未知容量') + (plan.period ? ' · ' + plan.period : '');
    left.append(name);
    if (isVerified) {
      const badge = document.createElement('small');
      badge.className = 'plan-badge';
      badge.textContent = '支持开通';
      left.append(badge);
    }
    const price = document.createElement('span');
    price.className = 'plan-price';
    price.textContent = plan.currency ? (plan.currency + ' ' + (plan.price_minor / 100).toFixed(2) + '/月') : '价格待 Apple 返回';
    row.append(left, price);
    list.append(row);
    if (is50GB) target = plan;
  }
  if (!stale && target?.currency === 'CNY' && target.price_minor === 600) {
    purchaseAction = 'storage-purchase';
    purchaseLabel = 'CNY 6.00/月';
  } else if (target?.currency === 'USD' && target.price_minor === 99) {
    purchaseAction = 'storage-skill-purchase-us';
    purchaseLabel = 'USD 0.99/月';
  }
  if (stale) {
    $('planSummary').textContent = '显示上次保存的报价；点击检查并开通后，会先重新核验 Apple 实时报价。';
    if (purchaseAction === 'storage-skill-purchase-us') $('purchaseButton').textContent = '检查并开通 50GB';
  } else if (purchaseAction) {
    $('purchaseButton').textContent = '开通 50GB · ' + purchaseLabel;
    $('planSummary').textContent = '已核对 50GB · ' + purchaseLabel + '。点击购买仍会再次确认。';
  } else {
    $('planSummary').textContent = 'Apple 返回了方案，但没有匹配已验证的 50GB 币种与价格；购买保持禁用。';
  }
}

function purchaseOutcomeMessage(result) {
  if (!result.ok) {
    if (result.stage === 'purchase-store-session') return '购买会话已失效或随登录续期重置，请重新验证购买会话；未提交购买。';
    if (['payment-confirmation', 'billing-confirmation'].includes(result.apple_hint)) return 'Apple 返回付款或账单相关提示，请检查该账号的可用付款方式或 Apple 账户余额。可在官方客户端兑换同地区礼品卡，到账后点击“付款准备完成，重新检查”；此提示不能单独确定错误原因。';
    if (result.stage === 'purchase-skill-buy') return 'Apple 未接受此次购买。HTTP 200 仅表示收到响应，不代表已开通；具体原因请查看下方 Apple 错误码。若尚未添加付款方式，请先完成付款准备。';
    return result.message || '购买结果尚未确认，请先核对订阅和容量，不要重复提交。';
  }
  const purchase = result.purchase;
  if (purchase?.state === 'completed' && purchase.quota_verified) return '订阅和付费容量已核验，iCloud+ 已开通。';
  if (purchase?.state === 'authentication-required') return 'Apple 要求重新验证购买会话，订阅尚未完成。';
  if (['handoff', 'confirmation-required'].includes(purchase?.state)) return 'Apple 要求进一步确认，请在官方客户端处理并核对订阅；当前尚未确认开通。';
  return '购买结果尚未确认，请先核对订阅与容量；不要重复提交购买。';
}

async function action(name, extra = {}, options = {}) {
  if (busy) return;
  if (name.startsWith('hme-') && familyStale) {
    try {
      busy = true; document.body.classList.add('busy'); if (current) render(current);
      const refreshed = await api('action', { action: 'family-info' });
      if (!refreshed.ok || !refreshed.family?.member || refreshed.family.organizer) throw new Error(refreshed.message || '子号家庭状态核验未完成');
      const checkedAt = new Date().toISOString();
      if (current) { current.family = refreshed.family; current.family_checked_at = checkedAt; current.family_stale = false; }
      renderFamily(refreshed.family, { checkedAt, stale: false });
    } catch (error) {
      notice(error.message, true);
      return;
    } finally {
      busy = false; document.body.classList.remove('busy'); if (current) render(current);
    }
  }
  const actionAccount = selectedAccount, actionPID = current?.status?.pid || 0, actionRefresh = current?.status?.last_refresh_at || '';
  const familyAction = name.startsWith('family-'), hmeAction = name.startsWith('hme-');
  busy = true;
  document.body.classList.add('busy');
  if (familyAction) $('familyCard').classList.add('is-loading');
  if (hmeAction) $('hmeCard').classList.add('is-loading');
  if (current) render(current);
  try {
    const result = await api('action', { action: name, ...extra });
    if (result.family) {
      const checkedAt = new Date().toISOString();
      if (current) { current.family = result.family; current.family_checked_at = checkedAt; current.family_stale = false; }
      renderFamily(result.family, { checkedAt, stale: false });
    }
    if (name === 'storage-purchase-prepare-us' && actionAccount === selectedAccount) {
      $('purchaseResult').textContent = (result.message || '购买预检查未完成，未提交购买') + (result.stage ? ' · ' + result.stage : '') + (result.http_status ? ' · HTTP ' + result.http_status : '') + (result.apple_code ? ' · Apple 错误码：' + result.apple_code : '');
      $('purchaseResult').hidden = false;
    }
    if (name.startsWith('gift-') && actionAccount === selectedAccount && actionPID === (current?.status?.pid || 0)) {
      const gift = result.gift_card;
      if (name === 'gift-prepare' && result.ok && gift?.ready) {
        giftPreparation = gift.preparation_id || '';
        $('giftCode').placeholder = '16 位充值礼品卡代码';
      }
      $('giftResult').textContent = (result.message || '兑换结果未确认') + (gift?.state === 'completed' ? ' · 入账 ' + gift.credited + ' · 余额 ' + gift.balance : '') + (result.apple_code ? ' · Apple 错误码 ' + result.apple_code : '');
    }
    const purchaseRequest = ['storage-purchase', 'storage-skill-purchase-us'].includes(name);
    if (purchaseRequest && actionAccount === selectedAccount) {
      result.message = purchaseOutcomeMessage(result);
      const code = result.apple_code ? ' · Apple 错误码：' + result.apple_code : '';
      $('purchaseResult').textContent = result.message + code;
      $('purchaseResult').hidden = false;
      if (name === 'storage-skill-purchase-us' && !(result.purchase?.state === 'completed' && result.purchase?.quota_verified)) result.ok = false;
    }
    if (!result.ok) {
      const detail = (result.stage ? ' · ' + result.stage : '') + (result.http_status ? ' · HTTP ' + result.http_status : '') + (result.apple_code ? ' · Apple 错误码：' + result.apple_code : '') + (result.apple_hint ? ' · ' + result.apple_hint : '');
      if (name === 'devices' && result.stage === 'device_identity_refresh_required') {
        notice(result.message || '设备管理登录态需要补齐', true);
        openLogin('reconnect');
        return;
      }
      if (name === 'validate-account' && result.stage === 'reconnect-required') {
        notice(result.message || '请重新连接当前账号', true);
        openLogin('reconnect');
        return;
      }
      if (name === 'hme-reachable-start' && result.stage === 'reconnect-required') {
        notice(result.message || '请重新连接当前子号', true);
        openLogin('reconnect');
        return;
      }
      if (result.stage === 'purchase-store-session') {
        storeAuthReady = false;
        $('storeAuthSummary').textContent = '购买会话不可用，请重新验证购买会话。';
      }
      if (name === 'storage-skill-auth-local') {
        storeAuthReady = false;
        $('storeAuthSummary').textContent = (result.message || '购买会话验证未完成') + detail;
      }
      if (purchaseRequest || name === 'storage-purchase-prepare-us') $('planSummary').textContent = (result.message || '购买请求未完成') + detail;
      throw new Error((result.message || '操作未完成') + detail);
    }
    if (result.activation) {
      activationInfo = result.activation;
      activationAccount = selectedAccount;
      if (current && result.activation.completed) { current.icloud_activated = true; current.activation_ready = false; }
      renderActivation(current);
    }
    if (result.store_authentication?.ready && actionAccount === selectedAccount && actionPID === (current?.status?.pid || 0) && actionRefresh === (current?.status?.last_refresh_at || '')) {
      storeAuthReady = true;
      storeAuthAccount = selectedAccount;
      $('storeAuthSummary').textContent = '购买会话认证通过 · 当前后台会话可用 · 临时会话不会持久化';
    }
    if (result.storage) {
      const checkedAt = new Date().toISOString();
      if (current) { current.storage = result.storage; current.storage_checked_at = checkedAt; current.storage_stale = false; }
      renderStorage(result.storage, { checkedAt, stale: false });
    }
    if (result.storage_plan) {
      const checkedAt = new Date().toISOString();
      if (current) { current.storage_plan = result.storage_plan; current.storage_plan_checked_at = checkedAt; current.storage_plan_stale = false; }
      renderPlans(result.storage_plan, { checkedAt, stale: false });
    }
    if (result.hide_my_email) renderHME(result.hide_my_email);
    if (result.stage === 'hme-reachable-code') { $('hmeReachableCodeForm').hidden = false; $('hmeReachableEmail').readOnly = true; $('hmeReachableCode').focus(); }
    if (['hme-reachable-completed', 'hme-reachable-added'].includes(result.stage)) { $('hmeReachableCodeForm').hidden = true; $('hmeReachableEmail').readOnly = false; $('hmeReachableCode').value = ''; }
    if (result.hme_candidate) showHMECandidate(result.hme_candidate);
    if (name === 'hme-reserve') showHMECandidate('');
    if (result.family) {
      if (name === 'family-remove-member') activity('家庭成员已移出并完成双边核验');
      else if (result.family.member) activity('家庭成员状态已确认');
      if (result.family.storage_participating) activity('家庭共享储存已启用');
    }
    if (result.purchase) {
      $('planSummary').textContent = purchaseOutcomeMessage(result);
    }
    if (name === 'family-add-self' && !result.family) {
      const readback = await api('action', { action: 'family-info' });
      if (!readback.ok || !readback.family?.member || readback.family.self !== 1) throw new Error(readback.message || '家庭加入后的目标账号回读未完成');
      result.family = readback.family;
      let checkedAt = new Date().toISOString();
      if (current) { current.family = result.family; current.family_checked_at = checkedAt; current.family_stale = false; }
      renderFamily(result.family, { checkedAt, stale: false });
      if (result.family.storage_known && result.family.storage_available && !result.family.storage_participating) {
        const joined = await api('action', { action: 'family-join-storage' });
        if (!joined.ok || !joined.family?.storage_participating || !joined.family?.storage_enabled) throw new Error(joined.message || '已加入家庭，但共享储存尚未生效');
        result.family = joined.family;
        checkedAt = new Date().toISOString();
        if (current) { current.family = result.family; current.family_checked_at = checkedAt; current.family_stale = false; }
        renderFamily(result.family, { checkedAt, stale: false });
        result.message = '池内账号已加入家庭并启用共享 iCloud+ 储存';
      }
    }
    if (options.autoKey) autoRefreshState.delete(options.autoKey);
    if (result.code) showCode(result);
    else if (!options.silent) notice(result.message || '操作已完成');
    await poll();
    return result;
  } catch (error) {
    if (options.autoKey) { autoRefreshState.set(options.autoKey, 'failed'); markAutoRefreshAttempt(options.autoKey, 'failed'); }
    if (!options.silent) notice(error.message, true);
  } finally {
    busy = false;
    document.body.classList.remove('busy');
    if (familyAction) $('familyCard').classList.remove('is-loading');
    if (hmeAction) $('hmeCard').classList.remove('is-loading');
    if (current) render(current);
  }
}

$('reloadView').addEventListener('click', poll);
$('codeButton').addEventListener('click', () => action('code'));
$('devicesButton').addEventListener('click', () => action('devices'));
$('renewButton').addEventListener('click', () => action('validate-account'));
$('storageButton').addEventListener('click', () => action('storage-info'));
$('activationCheckButton').addEventListener('click', () => action('activation-plan'));
$('activationAcceptButton').addEventListener('click', () => {
  if (!activationInfo?.required) return;
  const tuple = activationInfo.region + ':' + activationInfo.icloud_version + ':' + (activationInfo.media_version || '');
  if (!confirm('确认接受 ' + activationInfo.region + ' 区 iCloud ' + activationInfo.icloud_version + ' 与媒体服务 ' + (activationInfo.media_version || '无更新') + ' 条款，并继续生成本账号 NAC 完成激活？')) return;
  action('activation-accept', { code: tuple });
});
$('planButton').addEventListener('click', () => action('storage-plan'));
$('familyRefreshButton').addEventListener('click', () => action('family-info'));
$('familyOrganizer').addEventListener('change', () => { if (current) render(current); });
$('familyRemoveMember').addEventListener('change', () => { if (current) render(current); });
$('familyAddButton').addEventListener('click', () => {
  const organizer = $('familyOrganizer').value;
  if (!organizer) return;
  const label = $('familyOrganizer').selectedOptions[0]?.textContent || organizer;
  if (!confirm('将当前账号加入 ' + label + ' 的家庭，并在可用时启用共享 iCloud+？购买共享和位置共享保持关闭。')) return;
  action('family-add-self', { code: organizer });
});
$('familyJoinStorageButton').addEventListener('click', () => action('family-join-storage'));
$('familyRemoveButton').addEventListener('click', () => {
  const target = $('familyRemoveMember').value;
  if (!target) return;
  const label = $('familyRemoveMember').selectedOptions[0]?.textContent || target;
  if (!confirm('确认将 ' + label + ' 移出当前家庭？该账号会立即失去共享 iCloud+，之后仍可重新拉入。购买共享和位置共享不会开启。')) return;
  action('family-remove-member', { code: target });
});
$('hmeListButton').addEventListener('click', () => action('hme-list'));
$('hmeGenerateButton').addEventListener('click', () => action('hme-generate'));
$('hmeForwardSelect').addEventListener('change', () => {
  const choice = $('hmeForwardSelect').value, currentTarget = hmeInfo?.selected_forward_to || '';
  $('hmeForwardButton').disabled = busy || !choice || choice.toLowerCase() === currentTarget.toLowerCase();
});
$('hmeForwardButton').addEventListener('click', () => {
  const target = $('hmeForwardSelect').value, currentTarget = hmeInfo?.selected_forward_to || '';
  if (!target || target.toLowerCase() === currentTarget.toLowerCase()) return;
  if (!confirm('确认将当前账号的所有隐私邮箱统一转发到 ' + target + '？')) return;
  action('hme-forward', { code: target });
});
$('hmeReachableEmail').addEventListener('input', () => { if (current) render(current); });
$('hmeReachableStartButton').addEventListener('click', () => {
  const address = $('hmeReachableEmail').value.trim();
  if (!address) return;
  if (!confirm('确认通过 Apple 账户协议向 ' + address + ' 发送验证邮件？验证成功后将自动把当前子号的所有隐私邮箱切换到该目标。')) return;
  action('hme-reachable-start', { code: address });
});
$('hmeReachableCodeForm').addEventListener('submit', event => {
  event.preventDefault();
  const code = $('hmeReachableCode').value.trim();
  $('hmeReachableCode').value = '';
  if (code) action('hme-reachable-verify', { code });
});
$('hmeCreateLabel').addEventListener('input', () => { if (current) render(current); });
$('hmeCancelCandidate').addEventListener('click', () => showHMECandidate(''));
$('hmeReserveButton').addEventListener('click', () => {
  const label = $('hmeCreateLabel').value.trim(), note = $('hmeCreateNote').value.trim();
  if (!hmeCandidate || !label) return;
  if (!confirm('确认创建 ' + hmeCandidate + ' 并开始转发邮件？')) return;
  action('hme-reserve', { code: hmeCandidate, label, note });
});
$('hmeSearch').addEventListener('input', () => { hmeVisible = 100; renderHME(hmeInfo); });
for (const button of document.querySelectorAll('[data-hme-filter]')) {
  button.addEventListener('click', () => {
    hmeFilter = button.dataset.hmeFilter;
    hmeVisible = 100;
    for (const item of document.querySelectorAll('[data-hme-filter]')) item.classList.toggle('active', item === button);
    renderHME(hmeInfo);
  });
}
$('hmeMoreButton').addEventListener('click', () => { hmeVisible += 100; renderHME(hmeInfo); });
$('closeHMEEdit').addEventListener('click', () => { $('hmeEditDialog').close(); hmeEditing = ''; });
$('hmeEditDialog').addEventListener('close', () => { hmeEditing = ''; });
$('hmeEditForm').addEventListener('submit', event => {
  event.preventDefault();
  const email = hmeEditing, label = $('hmeEditLabel').value.trim(), note = $('hmeEditNote').value.trim();
  if (!email || !label) return;
  $('hmeEditDialog').close();
  action('hme-update', { code: email, label, note });
});
$('giftPrepareButton').addEventListener('click', () => {
  if (busy) return;
  giftPreparation = '';
  $('giftCode').value = '';
  $('giftResult').textContent = '正在验证当前账号的兑换会话，尚未提交兑换…';
  action('gift-prepare');
});
$('giftCode').addEventListener('input', () => { if (current) render(current); });
$('giftRedeemForm').addEventListener('submit', async event => {
  event.preventDefault();
  if (busy || !giftPreparation) return;
  const code = $('giftCode').value.trim().toUpperCase();
  if (!/^X[A-Z0-9]{15}$/.test(code)) return;
  if (!confirm('确认将这张充值礼品卡兑换到 ' + (current?.login_account || current?.account || '当前账号') + '？兑换码将被消耗；本次只提交兑换，不会自动购买订阅。')) return;
  const preparation = giftPreparation;
  giftPreparation = '';
  $('giftCode').value = '';
  $('giftResult').textContent = '已发起单次兑换，正在等待 Apple 结果。结果不明时先核对官方余额，不要重复提交。';
  await action('gift-redeem', { code, request_id: preparation });
});
$('paymentCheckButton').addEventListener('click', async () => {
  if (busy) return;
  const account = selectedAccount;
  const quota = await action('storage-info');
  if (!quota?.ok || selectedAccount !== account) return;
  if (hasPaidStorage(current)) { notice('付费容量已核验，iCloud+ 已开通。'); return; }
  if (!current?.purchase_preparation_available) { notice('请重新连接账号加载新版购买预检查。', true); return; }
  const prepared = await action('storage-purchase-prepare-us');
  if (prepared?.ok && selectedAccount === account) notice('预检查通过。余额请在 Apple 官方客户端核对；点击开通并确认后才会提交订阅。');
});
$('purchaseButton').addEventListener('click', async () => {
  if (busy || !purchaseAction || !purchaseLabel || hasPaidStorage(current)) return;
  const account = selectedAccount, requestedAction = purchaseAction;
  if (requestedAction === 'storage-skill-purchase-us') {
    if (!current?.purchase_preparation_available) { notice('购买链路已更新，请在此重新连接账号加载新版后台。', true); openLogin('reconnect'); return; }
    $('purchaseResult').hidden = false;
    $('purchaseResult').textContent = '正在检查当前容量、实时报价、购买会话和现有订阅；尚未提交购买…';
    const prepared = await action('storage-purchase-prepare-us');
    if (!prepared?.ok || selectedAccount !== account || purchaseAction !== requestedAction) return;
  }
  if (!confirm('确认购买 iCloud+ 50GB（' + purchaseLabel + '）？这是会产生真实扣款并自动续费的订阅。')) return;
  action(requestedAction);
});
$('storeAuthButton').addEventListener('click', () => {
  if (busy) return;
  storeAuthReady = false;
  $('storeAuthSummary').textContent = '正在验证购买会话，Apple 如要求验证码将由当前本机身份完成，请稍候…';
  action('storage-skill-auth-local');
});
$('addAccountButton').addEventListener('click', () => openLogin('add'));
$('addAccountButtonSecondary').addEventListener('click', () => openLogin('add'));
$('removeAccountButton').addEventListener('click', async () => {
  if (busy) return;
  if (!selectedAccount) { notice('当前是内置账号，先在左侧选择要移除的账号', true); return; }
  if (!confirm('从列表移除该账号？设备身份仍保留在磁盘上，可重新添加。')) return;
  busy = true;
  try {
    const result = await api('accounts/remove', { id: selectedAccount });
    if (!result.ok) throw new Error(result.message);
    notice(result.message);
    selectedAccount = '';
    localStorage.removeItem('selected-account');
  } catch (error) {
    notice(error.message, true);
  } finally {
    busy = false;
    await poll();
  }
});
$('copyButton').addEventListener('click', async () => {
  if (!privateCode) return;
  try { await navigator.clipboard.writeText(privateCode); notice('验证码已复制'); }
  catch { notice('复制失败，请手动输入屏幕上的验证码', true); }
});
$('allowButton').addEventListener('click', () => { if (current?.pending) action('allow', { request_id: current.pending.id }); });
$('denyButton').addEventListener('click', () => { if (current?.pending) action('deny', { request_id: current.pending.id }); });
$('startButton').addEventListener('click', () => openLogin('reconnect'));
$('closeLogin').addEventListener('click', () => { $('password').value = ''; $('loginDialog').close(); });
$('loginDialog').addEventListener('close', () => { $('password').value = ''; });
$('loginForm').addEventListener('submit', async event => {
  event.preventDefault();
  if (busy) return;
  busy = true;
  const creds = { apple_id: $('appleID').value.trim(), password: $('password').value, twofactor: $('twofactor').value, remember_password: $('rememberPassword').checked };
  $('password').value = '';
  try {
    if (loginMode === 'reconnect' && loginAccount !== selectedAccount) throw new Error('当前账号已切换，请重新打开连接窗口');
    const result = await api(loginMode === 'add' ? 'accounts/add' : 'start', creds);
    if (!result.ok) throw new Error(result.message);
    $('loginDialog').close();
    notice(result.message);
    await poll();
  } catch (error) {
    notice(error.message, true);
    $('password').focus();
  } finally {
    busy = false;
    creds.password = '';
    await poll();
  }
});
$('smsForm').addEventListener('submit', event => {
  event.preventDefault();
  const code = $('smsCode').value;
  $('smsCode').value = '';
  action(current?.status?.state === 'awaiting_account_validation' ? 'submit-account-code' : 'submit-sms', { code });
});
$('resendButton').addEventListener('click', () => action('resend-sms'));
$('pairingForm').addEventListener('submit', event => {
  event.preventDefault();
  const code = $('pairingCode').value;
  $('pairingCode').value = '';
  action('submit-pairing', { code });
});
$('stopButton').addEventListener('click', () => { $('stopDialog').showModal(); });
$('cancelStop').addEventListener('click', () => { $('stopDialog').close(); });
$('confirmStop').addEventListener('click', async () => {
  if (busy) return;
  busy = true;
  $('stopDialog').close();
  try {
    const result = await api('stop', { account: selectedAccount });
    if (!result.ok) throw new Error(result.message);
    clearCode();
    notice(result.message);
    await poll();
  } catch (error) {
    notice(error.message, true);
  } finally {
    busy = false;
    await poll();
  }
});

function applyRoute() {
  const route = (location.hash || '#overview').slice(1);
  const page = ['overview', 'icloud', 'devices', 'requests'].includes(route) ? route : 'overview';
  const titles = {
    icloud: ['iCloud+', '查询云端储存容量，核验报价并开通可用的订阅方案。', '订阅与云端容量'],
    overview: ['设备与登录', '在一处管理设备身份、后台守护状态、家庭共享与安全验证码。', '你的 Apple 工作空间'],
    devices: ['我的设备', '查看和刷新当前账号关联的受信任硬件与本机虚拟身份。', '已关联硬件与身份'],
    requests: ['登录请求', '实时审批 Apple 设备发来的登录确认与官方客户端配对。', '双重认证审批']
  };
  document.querySelector('main').className = 'view-' + page;
  document.querySelector('.page-header h1').textContent = titles[page][0];
  document.querySelector('.page-header p').textContent = titles[page][1];
  if ($('pageEyebrow')) $('pageEyebrow').textContent = titles[page][2];
  for (const item of document.querySelectorAll('nav a')) {
    const selected = item.getAttribute('href') === '#' + page;
    item.classList.toggle('selected', selected);
    if (selected) item.setAttribute('aria-current', 'page');
    else item.removeAttribute('aria-current');
  }
}
for (const link of document.querySelectorAll('nav a')) link.addEventListener('click', applyRoute);
window.addEventListener('hashchange', applyRoute);
applyRoute();
requestAnimationFrame(() => requestAnimationFrame(() => document.body.classList.add('ui-ready')));
poll();
setInterval(poll, 2500);
setInterval(tickCode, 250);
window.addEventListener('pagehide', clearCode);
