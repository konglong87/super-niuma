const API = '/api/super/v1';
const MARKER = { 'X-Super-Niuma-Request': '1' };
const STATUS = { recorded: '已接单 · 未执行', cancelled: '已取消' };
const byId = (id) => document.getElementById(id);
const state = { orders: [], selected: null, pending: null, loading: false, ready: false };
const dateFormat = new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });

function notice(message, error = false) {
 const target = byId('notice');
 target.textContent = message; target.classList.toggle('error', error); target.hidden = !message;
}
async function request(path, options = {}) {
 const controller = new AbortController();
 const timeout = setTimeout(() => controller.abort(), 10000);
 try {
  const response = await fetch(`${API}${path}`, { ...options, signal: controller.signal, headers: { ...MARKER, ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers } });
  const payload = await response.json();
  if (!response.ok) throw new Error(payload?.error?.message || '请求失败，请稍后重试。');
  return payload;
 } finally { clearTimeout(timeout); }
}
function node(tag, className, text) {
 const item = document.createElement(tag); if (className) item.className = className;
 if (text !== undefined) item.textContent = text; return item;
}
function renderOrders() {
 byId('count-all').textContent = state.orders.length;
 byId('count-recorded').textContent = state.orders.filter((item) => item.status === 'recorded').length;
 byId('count-cancelled').textContent = state.orders.filter((item) => item.status === 'cancelled').length;
 byId('list-count').textContent = `${state.orders.length} 张 · 最近记录`;
 const list = byId('orders'); list.replaceChildren();
 if (!state.orders.length) { list.append(node('p', 'empty', '还没有工单。\n从左侧登记一个目标，并说明怎样算完成。')); return; }
 for (const order of state.orders) {
  const row = node('button', `order-row${state.selected?.id === order.id ? ' selected' : ''}`); row.type = 'button'; row.dataset.id = order.id;
  const top = node('div', 'order-top'); top.append(node('span', 'order-title', order.title), node('span', `badge ${order.status}`, STATUS[order.status] || order.status));
  const meta = node('p', 'order-meta'); meta.append(node('span', '', `#${order.id.slice(0, 8)}`), node('time', '', dateFormat.format(new Date(order.created_at))));
  row.append(top, node('p', 'order-preview', order.objective), meta);
  row.addEventListener('click', () => showDetail(order.id)); list.append(row);
 }
}
async function refresh({ silent = false } = {}) {
 if (state.loading) return;
 state.loading = true; byId('refresh').disabled = true;
 try {
  const [status, orders] = await Promise.all([request('/status'), request('/work-orders')]);
  if (status.execution_enabled !== false || status.mode !== 'intake_only') throw new Error('服务模式与当前界面不一致，请停止操作并检查版本。');
  state.ready = true; state.orders = orders.items || []; byId('service-status').textContent = '接单服务在线'; renderOrders();
  if (state.selected) {
   const selected = state.orders.find((item) => item.id === state.selected.id);
   if (selected) renderDetail(selected);
   else renderDetail(await request(`/work-orders/${encodeURIComponent(state.selected.id)}`));
  }
  if (!silent) notice('列表已更新。工单尚未进入自动执行。');
 } catch (error) {
  state.ready = false; byId('service-status').textContent = '服务不可用'; notice(`无法更新工单：${error.message}`, true);
 } finally { state.loading = false; byId('refresh').disabled = false; }
}
function renderDetail(order) {
 state.selected = order; byId('detail').hidden = false;
 byId('detail-title').textContent = order.title; byId('detail-id').textContent = `工单 ${order.id}`;
 byId('detail-objective').textContent = order.objective;
 byId('detail-workspace').textContent = order.workspace || '未指定（不授权执行）';
 byId('detail-state').textContent = STATUS[order.status] || order.status;
 byId('detail-criteria').replaceChildren(...order.acceptance_criteria.map((text) => node('li', '', text)));
 byId('cancel-order').hidden = order.status !== 'recorded';
}
async function showDetail(id) {
 try {
  renderDetail(await request(`/work-orders/${encodeURIComponent(id)}`)); renderOrders();
  byId('detail').scrollIntoView({ behavior: 'auto', block: 'start' });
 } catch (error) { notice(`无法读取详情：${error.message}`, true); }
}
byId('intake-form').addEventListener('submit', async (event) => {
 event.preventDefault();
 if (!state.ready) { notice('接单服务尚未就绪，请先刷新后重试。', true); return; }
 const payload = { title: byId('title').value.trim(), objective: byId('objective').value.trim(), acceptance_criteria: byId('criteria').value.split('\n').map((line) => line.trim()).filter(Boolean), workspace: byId('workspace').value.trim() };
 if (!payload.title || !payload.objective || !payload.acceptance_criteria.length) { notice('请填写标题、目标和至少一条验收条件。', true); return; }
 const body = JSON.stringify(payload);
 if (!state.pending || state.pending.body !== body) state.pending = { key: crypto.randomUUID(), body };
 const button = byId('submit-order'); button.disabled = true; button.textContent = '正在登记…';
 try {
  const order = await request('/work-orders', { method: 'POST', body, headers: { 'Idempotency-Key': state.pending.key } });
  state.pending = null; byId('intake-form').reset();
  await refresh({ silent: true }); renderDetail(order); renderOrders();
  notice('工单已保存。当前只接单，不会自动执行。');
 } catch (error) { notice(`登记未确认：${error.message} 保持内容不变重试，不会重复创建。`, true); }
 finally { button.disabled = false; button.replaceChildren(document.createTextNode('登记工单 '), node('span', '', '↗')); }
});
byId('refresh').addEventListener('click', () => refresh());
byId('close-detail').addEventListener('click', () => { state.selected = null; byId('detail').hidden = true; renderOrders(); });
byId('cancel-order').addEventListener('click', () => byId('cancel-dialog').showModal());
byId('keep-order').addEventListener('click', () => byId('cancel-dialog').close());
byId('confirm-cancel').addEventListener('click', async () => {
 if (!state.selected) return;
 const button = byId('confirm-cancel'); button.disabled = true;
 try {
  const order = await request(`/work-orders/${encodeURIComponent(state.selected.id)}/cancel`, { method: 'POST', body: '{}' });
  byId('cancel-dialog').close(); await refresh({ silent: true }); renderDetail(order); renderOrders(); notice('工单已取消，记录已保留。');
 } catch (error) { byId('cancel-dialog').close(); notice(`取消未确认：${error.message}`, true); }
 finally { button.disabled = false; }
});
refresh({ silent: true });
