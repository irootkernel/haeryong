// The trusted host owns this client. It has no message-based management proxy,
// credential getter, artifact bridge or URL credential transport.
export const storageKey = 'haeryong.owner-session.v1';
export class TokenClient {
  #token = '';
  #remembered = false;
  #changed;
  constructor(changed) {
    this.#changed = changed;
    addEventListener('storage', event => {
      if (event.key !== storageKey) return;
      if (!event.newValue && this.#remembered) this.clear('Remembered access was removed; connect again.');
      else if (event.newValue && !this.#token) { this.#token = event.newValue; this.#remembered = true; void this.restore(); }
    });
  }
  get connected() { return this.#token !== ''; }
  clear(message = 'Connect device') {
    const previous = this.#token;
    this.#token = ''; this.#remembered = false;
    try { if (localStorage.getItem(storageKey) === previous) localStorage.removeItem(storageKey); } catch {}
    this.#changed({connected: false, message});
  }
  async accept(token, remember) {
    this.#token = token; this.#remembered = false;
    let message = 'Connected in memory only. Reload or close requires pairing again.';
    try {
      localStorage.removeItem(storageKey);
      if (remember) { localStorage.setItem(storageKey, token); this.#remembered = true; message = 'Connected and remembered'; }
    } catch { message = 'Connected in memory only: storage unavailable. Reload or close requires pairing again.'; }
    await this.check(message);
  }
  async restore() {
    if (!this.#token) {
      try { this.#token = localStorage.getItem(storageKey) || ''; this.#remembered = this.#token !== ''; }
      catch { this.#changed({connected: false, message: 'Storage unavailable; memory-only access is available.'}); }
    }
    if (this.#token) await this.check(this.#remembered ? 'Remembered connection' : 'Connected in memory only');
  }
  async check(message) {
    try { const session = await this.request('/api/v1/session'); this.#changed({connected: true, message, session}); }
    catch (error) { if (this.#token) this.#changed({connected: true, message: 'Service unavailable; credential retained. Retry connection.', error}); }
  }
  async request(path, options = {}) {
    if (!path.startsWith('/api/v1/') || path.startsWith('//')) throw new Error('Invalid API route');
    const credential = this.#token;
    const headers = new Headers(options.headers);
    if (credential) headers.set('Authorization', 'Bearer ' + credential);
    const response = await fetch(path, {...options, headers, credentials: 'omit', redirect: 'error'});
    if (credential !== this.#token) throw new Error('Previous session response ignored');
    if (!response.ok) {
      if (response.status === 401 && credential) this.clear('Device access stopped; connect again.');
      const error = new Error('Request failed: HTTP ' + response.status);
      error.status = response.status; error.retryAfter = Number(response.headers.get('Retry-After')) || 0;
      throw error;
    }
    if (options.stream) return response;
    return response.status === 204 ? null : (await response.json()).data;
  }
}

export function safeReturn(value) {
  if (!value || !value.startsWith('/') || value.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(value)) return '/';
  try {
    const url = new URL(value, location.origin);
    if (url.origin !== location.origin || url.username || url.password) return '/';
    const path = url.pathname;
    const id = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}';
    const known = ['/', '/connect', '/library', '/projects', '/maintenance', '/trash', '/settings'].includes(path)
      || new RegExp('^/projects/' + id + '(?:/sources)?$').test(path)
      || new RegExp('^/documents/' + id + '/versions(?:/' + id + ')?$').test(path)
      || new RegExp('^/maintenance/jobs/' + id + '$').test(path)
      || /^\/settings\/(general|connection|sessions|projects|ai|maintenance|storage|retention|backup|about)$/.test(path);
    const queryKeys = new Set(['q', 'project_id', 'topic_id', 'lifecycle', 'source_basis', 'page', 'page_query', 'page_fragment', 'context_ref']);
    for (const [key, value] of url.searchParams) {
      if (!queryKeys.has(key) || /[\u0000-\u001f\u007f]/.test(value)) return '/';
      if (key === 'page' && (value.startsWith('/') || value.includes('\\') || value.split('/').includes('..'))) return '/';
    }
    return known ? path + url.search + url.hash : '/';
  } catch { return '/'; }
}

const state = document.querySelector('#state');
const privateView = document.querySelector('#private');
const owner = document.querySelector('#owner');
const connect = document.querySelector('#connect');
let pending = null, timer = null, events = null;
const client = new TokenClient(value => {
  state.textContent = value.message;
  owner.hidden = !value.connected; connect.hidden = value.connected;
  if (!value.connected) { privateView.textContent = ''; document.querySelector('#devices').replaceChildren(); events?.abort(); events = null; if (!pending) connect.querySelector('button').disabled = false; }
  if (value.session) {
    const target = new URL(location.href).searchParams.get('return');
    if (target !== null) history.replaceState(null, '', safeReturn(target));
    void refreshDevices();
    if (!events) void watchEvents();
  }
});
async function jsonPost(path, body, headers = {}) {
  return client.request(path, {method: 'POST', headers: {'Content-Type': 'application/json', ...headers}, body: JSON.stringify(body)});
}
async function refreshDevices() {
  try {
    const devices = await client.request('/api/v1/sessions');
    const list = document.querySelector('#devices'); list.replaceChildren();
    for (const device of devices.items) {
      const item = document.createElement('li'); item.textContent = device.device_label + ' (' + device.state + ')';
      const button = document.createElement('button'); button.textContent = 'Revoke ' + device.device_label; button.dataset.sessionId = device.session_id;
      button.onclick = async () => { try { await client.request('/api/v1/sessions/' + encodeURIComponent(device.session_id), {method: 'DELETE'}); await refreshDevices(); } catch (error) { state.textContent = error.message; } };
      item.append(button); list.append(item);
    }
  } catch (error) { if (client.connected) document.querySelector('#result').textContent = error.message; }
}
async function watchEvents() {
  const controller = new AbortController(); events = controller;
  try {
    const response = await client.request('/api/v1/events', {stream: true, signal: controller.signal});
    const reader = response.body.getReader();
    while (!(await reader.read()).done) {}
    if (client.connected) await client.restore();
  } catch (error) { if (!controller.signal.aborted && client.connected) await client.restore(); }
  finally { if (events === controller) events = null; }
}
function schedule() { clearTimeout(timer); if (pending && !document.hidden) timer = setTimeout(poll, 2100); }
async function poll() {
  const current = pending;
  if (!current || document.hidden) return;
  try {
    const result = await jsonPost('/api/v1/auth/pairings/' + encodeURIComponent(current.pairing_id) + '/poll', {polling_secret: current.polling_secret});
    if (pending !== current) return;
    if (result.token) {
      pending = null; document.querySelector('#comparison').textContent = '';
      await client.accept(result.token, current.remember);
    } else if (result.state === 'pending') schedule();
    else { pending = null; document.querySelector('#comparison').textContent = ''; state.textContent = 'Pairing ' + result.state + '. Connect device again.'; connect.querySelector('button').disabled = false; }
  } catch (error) {
    if (pending !== current) return;
    if (error.status === 429) { clearTimeout(timer); timer = setTimeout(poll, Math.max(2100, error.retryAfter * 1000)); return; }
    pending = null; document.querySelector('#comparison').textContent = ''; connect.querySelector('button').disabled = false;
    state.textContent = 'Connection was not confirmed. Connect device again; single-use issuance is not repeated automatically.';
  }
}
connect.onsubmit = async event => {
  event.preventDefault(); if (pending || client.connected) return;
  const button = connect.querySelector('button'); button.disabled = true;
  try {
    pending = await jsonPost('/api/v1/auth/pairings', {device_label: document.querySelector('#label').value});
    pending.remember = document.querySelector('#remember').checked;
    document.querySelector('#comparison').textContent = 'Compare code ' + pending.code + ' for request ' + pending.pairing_id;
    state.textContent = 'Waiting for owner approval'; schedule();
  } catch (error) { state.textContent = error.message; button.disabled = false; }
};
document.querySelector('#approve').onsubmit = async event => {
  event.preventDefault();
  const id = document.querySelector('#request-id').value, code = document.querySelector('#code').value;
  try {
    const pairings = await client.request('/api/v1/auth/pairings');
    const request = pairings.items.find(value => value.pairing_id === id);
    if (!request) throw new Error('Pending request unavailable');
    await jsonPost('/api/v1/auth/pairings/' + encodeURIComponent(id) + '/' + event.submitter.value,
      {code, origin: request.origin}, {'If-Match': request.etag, 'Idempotency-Key': crypto.randomUUID()});
    document.querySelector('#decision-result').textContent = 'Exact request ' + event.submitter.value + ' saved';
    document.querySelector('#request-id').value = ''; document.querySelector('#code').value = '';
  } catch (error) { document.querySelector('#decision-result').textContent = error.message; }
};
document.querySelector('#logout').onclick = async () => { try { await client.request('/api/v1/session', {method: 'DELETE'}); client.clear('Logged out'); } catch (error) { state.textContent = error.message; } };
document.querySelector('#retry').onclick = () => void client.restore();
document.querySelector('#refresh').onclick = () => void refreshDevices();
document.querySelector('#read').onclick = async () => { try { const value = await client.request('/api/v1/status'); privateView.textContent = JSON.stringify(value); } catch (error) { if (client.connected) state.textContent = error.message; } };
document.addEventListener('visibilitychange', () => { if (!document.hidden) { schedule(); if (client.connected) void client.restore(); } });
setInterval(() => { if (client.connected) void client.restore(); }, 3000);
void client.restore();
