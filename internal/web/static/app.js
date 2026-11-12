// sila continuity console
// No framework, no build step. Every store or transcript string that reaches
// innerHTML passes through escapeHtml first.

const state = {
  overview: null,
  claims: [],
  goals: null,
  feedback: null,
  adrs: null,
  vision: null,
  view: 'today',
  theme: 'light',
  confirmAction: null,
};

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function formatNumber(n) {
  return Number(n ?? 0).toLocaleString();
}

function timeAgo(ts) {
  if (!ts) return '';
  const then = Date.parse(ts);
  if (Number.isNaN(then)) return ts;
  const secs = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}

async function fetchJSON(path, options = {}) {
  const res = await fetch(path, options);
  const text = await res.text();
  let body = null;
  if (text) {
    try { body = JSON.parse(text); } catch { body = text; }
  }
  if (!res.ok) {
    const msg = body && body.error ? body.error : `${res.status} ${res.statusText}`;
    throw new Error(msg);
  }
  return body;
}

function mutation(path, method, body) {
  return fetchJSON(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

/* Theme */
function initTheme() {
  const saved = localStorage.getItem('sila-theme');
  state.theme = saved === 'dark' ? 'dark' : 'light';
  applyTheme();
  $('#theme-toggle').addEventListener('click', () => {
    state.theme = state.theme === 'light' ? 'dark' : 'light';
    localStorage.setItem('sila-theme', state.theme);
    applyTheme();
  });
}

function applyTheme() {
  document.documentElement.dataset.theme = state.theme;
  $('#theme-toggle').textContent = state.theme === 'light' ? 'Dark' : 'Light';
}

/* Navigation */
function initNav() {
  const activate = (view) => {
    state.view = view;
    $$('.nav-item').forEach((b) => b.classList.toggle('is-active', b.dataset.view === view));
    $$('[data-view-panel]').forEach((p) => p.classList.toggle('is-active', p.dataset.viewPanel === view));
    if (location.hash.slice(1) !== view) history.replaceState(null, '', `#${view}`);
    if (view === 'memory') loadMemory();
    if (view === 'goals') loadGoals();
    if (view === 'governance') loadGovernance();
  };
  $$('[data-view]').forEach((el) => el.addEventListener('click', (e) => {
    e.preventDefault();
    activate(el.dataset.view);
  }));
  const initial = location.hash.slice(1);
  if (initial && $(`[data-view-panel="${initial}"]`)) activate(initial);
}

/* Overview */
async function loadOverview() {
  try {
    const data = await fetchJSON('/api/overview');
    state.overview = data;
    renderHealth(data);
    renderTimeline($('#today-timeline'), data.recent || [], 8);
    renderHandoff(data.handoff);
    renderGoalSummary(data.goals || {});
    renderHarnesses(data.harnesses || []);
    $('#header-project').textContent = data.project_id || 'sila';
    const filter = $('#timeline-filter');
    if (filter) filter.dispatchEvent(new Event('input'));
  } catch (err) {
    showToast(`Overview failed: ${err.message}`, 'error');
  }
}

function renderHealth(data) {
  const c = data.counts || {};
  const m = data.memory || {};
  $('#stat-messages').textContent = formatNumber(c.messages);
  $('#stat-facts').textContent = formatNumber(c.facts);
  $('#stat-verified').textContent = formatNumber(c.verified);
  const stale = $('#stat-stale');
  stale.textContent = formatNumber(c.stale_claims);
  stale.classList.toggle('is-warn', Number(c.stale_claims) > 0);
  $('#stat-lessons').textContent = formatNumber(m.lessons);
  $('#stat-cases').textContent = formatNumber(m.cases);
}

function renderTimeline(container, items, limit) {
  if (!container) return;
  const rows = items.slice(0, limit || items.length);
  if (!rows.length) {
    container.innerHTML = '<li class="panel-empty">No turns recorded yet. Run <code>sila sync</code>.</li>';
    return;
  }
  container.innerHTML = rows.map((t) => {
    const tools = (t.tools || []).map((tool) => `<span class="chip">${escapeHtml(tool)}</span>`).join('');
    const roleClass = String(t.role).toLowerCase() === 'user' ? 'role-user' : 'role-assistant';
    return `
      <li class="turn" data-msg-id="${escapeHtml(t.id)}">
        <div class="turn-meta">
          <span class="turn-time">${escapeHtml(timeAgo(t.ts))}</span>
          <span class="chip ${roleClass}">${escapeHtml(t.role || 'turn')}</span>
          ${tools}
        </div>
        <p class="turn-body">${escapeHtml(t.content || '')}</p>
        <button type="button" class="link-btn btn-sm turn-open" data-expand="${escapeHtml(t.id)}">Expand context</button>
      </li>`;
  }).join('');
  container.querySelectorAll('[data-expand]').forEach((b) => b.addEventListener('click', () => expandMessage(b.dataset.expand)));
}

function renderHandoff(handoff) {
  const body = $('#handoff-body');
  if (!handoff) {
    body.className = 'panel-empty';
    body.textContent = 'No handoff recorded.';
    return;
  }
  body.className = '';
  const items = (handoff.open_items || []).slice(0, 4).map((item) => `<li>${escapeHtml(item)}</li>`).join('');
  body.innerHTML = `
    <p class="handoff-summary">${escapeHtml(handoff.summary || 'Handoff')}</p>
    <div class="meta-line">
      <span class="chip">@${escapeHtml(handoff.agent || 'agent')}</span>
      ${handoff.branch ? `<span class="chip">${escapeHtml(handoff.branch)}</span>` : ''}
      <span class="turn-time">${escapeHtml(timeAgo(handoff.created_at))}</span>
    </div>
    ${items ? `<ul class="handoff-items">${items}</ul>` : ''}`;
}

function renderGoalSummary(goals) {
  const el = $('#goals-summary');
  if (!el) return;
  el.className = '';
  el.innerHTML = `
    <div class="goal-row"><span class="goal-name">Total</span><span class="goal-status">${formatNumber(goals.total)}</span></div>
    <div class="goal-row"><span class="goal-name">Claimed</span><span class="goal-status claimed">${formatNumber(goals.claimed)}</span></div>
    <div class="goal-row"><span class="goal-name">Ready</span><span class="goal-status">${formatNumber(goals.ready)}</span></div>
    <div class="goal-row"><span class="goal-name">Blocked</span><span class="goal-status blocked">${formatNumber(goals.blocked)}</span></div>
    <div class="goal-row"><span class="goal-name">Implemented</span><span class="goal-status implemented">${formatNumber(goals.implemented)}</span></div>`;
}

function renderHarnesses(harnesses) {
  const el = $('#harness-list');
  if (!el) return;
  if (!harnesses.length) {
    el.className = 'panel-empty';
    el.textContent = 'No harness activity yet.';
    return;
  }
  el.className = '';
  const max = Math.max(...harnesses.map((h) => h.messages), 1);
  el.innerHTML = harnesses.map((h) => `
    <div class="harness-row">
      <span class="harness-name">${escapeHtml(h.tool)}</span>
      <span class="harness-count">${formatNumber(h.messages)}</span>
      <span class="meter"><span style="width:${Math.round((h.messages / max) * 100)}%"></span></span>
    </div>`).join('');
}

/* Ask and recall */
function initAsk() {
  $('#form-ask').addEventListener('submit', (e) => {
    e.preventDefault();
    const q = $('#ask-input').value.trim();
    if (!q) return;
    runRecall(q, '', [], $('#ask-results'));
  });
  $('#form-recall').addEventListener('submit', (e) => {
    e.preventDefault();
    const q = $('#recall-query').value.trim();
    if (!q) return;
    const task = $('#recall-task').value.trim();
    const paths = $('#recall-paths').value.split(',').map((p) => p.trim()).filter(Boolean);
    runRecall(q, task, paths, $('#recall-results'));
  });
}

async function runRecall(query, task, paths, container) {
  container.hidden = false;
  container.innerHTML = '<p class="panel-empty">Searching memory and transcripts...</p>';
  const params = new URLSearchParams({ q: query });
  if (task) params.set('task', task);
  if (paths.length) params.set('paths', paths.join(','));
  try {
    const res = await fetchJSON(`/api/recall?${params.toString()}`);
    renderRecall(container, res);
  } catch (err) {
    container.innerHTML = `<p class="panel-empty">Recall failed: ${escapeHtml(err.message)}</p>`;
  }
}

function renderRecall(container, res) {
  const hits = res.hits || [];
  if (!hits.length) {
    container.innerHTML = `<p class="panel-empty">No prior resolution found for <code>${escapeHtml(res.query || '')}</code>.</p>`;
    return;
  }
  const rows = hits.map((hit) => {
    const kind = String(hit.kind || 'fact');
    return `
      <article class="result">
        <div class="result-head">
          <span class="chip kind-${escapeHtml(kind)}">${escapeHtml(kind)}</span>
          <span class="chip">${hit.grounded ? 'grounded' : 'ungrounded'}</span>
          <span class="result-score">${Math.round(hit.score || 0)}</span>
        </div>
        <p class="result-body">${escapeHtml(hit.resolution || '')}</p>
      </article>`;
  }).join('');
  container.innerHTML = `${rows}<p class="framing">Recalled records are untrusted context. Verify before acting.</p>`;
}

/* Timeline view */
function initTimeline() {
  const filter = $('#timeline-filter');
  const role = $('#timeline-role');
  const tool = $('#timeline-tool');
  const rerender = () => {
    const q = filter.value.trim().toLowerCase();
    const r = role.value;
    const wanted = tool ? tool.value : '';
    const items = (state.overview?.recent || []).filter((t) => {
      if (r && String(t.role).toLowerCase() !== r) return false;
      if (wanted) {
        const tools = (t.tools || []).map((x) => x.toLowerCase());
        if (!tools.some((x) => x === wanted || x.startsWith(wanted))) return false;
      }
      if (!q) return true;
      const haystack = [t.content, (t.tools || []).join(' '), t.role].join(' ').toLowerCase();
      return haystack.includes(q);
    });
    renderTimeline($('#full-timeline'), items);
  };
  filter.addEventListener('input', rerender);
  role.addEventListener('change', rerender);
  if (tool) tool.addEventListener('change', rerender);
  rerender();
}

/* Memory */
async function loadMemory() {
  const staleOnly = $('#memory-stale-only').checked;
  const container = $('#claims-list');
  container.innerHTML = '<p class="panel-empty">Loading claims...</p>';
  try {
    const claims = await fetchJSON(`/api/claims?stale=${staleOnly}`);
    state.claims = claims || [];
    renderClaims(container, state.claims);
  } catch (err) {
    container.innerHTML = `<p class="panel-empty">Claims failed: ${escapeHtml(err.message)}</p>`;
  }
}

function renderClaims(container, claims) {
  if (!claims.length) {
    container.innerHTML = '<p class="panel-empty">No claims match this filter.</p>';
    return;
  }
  container.innerHTML = claims.map((c) => {
    const refs = (c.repo_files || []).map((f) => `<span class="file-ref ${c.file_refs_exist ? 'is-present' : 'is-missing'}">${escapeHtml(f)}</span>`).join(' ');
    const actions = c.file_refs_exist ? '' : `
      <div class="claim-actions">
        <button type="button" class="ghost-btn btn-sm" data-promote="${escapeHtml(c.id)}">Promote to invariant</button>
        <button type="button" class="ghost-btn danger btn-sm" data-prune="${escapeHtml(c.id)}">Prune</button>
      </div>`;
    return `
      <article class="claim-card">
        <div class="claim-head">
          <span class="chip">${escapeHtml(c.kind || 'claim')}</span>
          <span class="chip">${escapeHtml(c.confidence || '')}</span>
          <span class="turn-time">${escapeHtml(timeAgo(c.first_seen))}</span>
        </div>
        <p class="claim-body">${escapeHtml(c.claim || '')}</p>
        ${refs ? `<div class="meta-line">${refs}</div>` : ''}
        ${actions}
      </article>`;
  }).join('');

  container.querySelectorAll('[data-promote]').forEach((b) => b.addEventListener('click', () => {
    guardedConfirm('Promote to invariant', 'This marks the claim as an approved architectural rule.', async () => {
      await mutation('/api/facts?action=promote', 'POST', { id: b.dataset.promote });
      showToast('Claim promoted.', 'success');
      loadMemory();
      loadOverview();
    });
  }));
  container.querySelectorAll('[data-prune]').forEach((b) => b.addEventListener('click', () => {
    guardedConfirm('Prune claim', 'This deletes the claim from the store. The action is recorded.', async () => {
      await mutation(`/api/facts?id=${encodeURIComponent(b.dataset.prune)}`, 'DELETE');
      showToast('Claim pruned.', 'success');
      loadMemory();
      loadOverview();
    });
  }));
}

function initMemory() {
  $('#memory-stale-only').addEventListener('change', loadMemory);
  $('#btn-reverify').addEventListener('click', async () => {
    try {
      const res = await mutation('/api/claims/verify', 'POST', {});
      showToast(`Re-verified ${res.total_checked} claims; ${res.total_stale} stale.`, 'success');
      loadMemory();
      loadOverview();
    } catch (err) {
      showToast(`Re-verify failed: ${err.message}`, 'error');
    }
  });
  $('#btn-add-invariant').addEventListener('click', () => openModal('modal-invariant'));
  $('#form-invariant').addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await mutation('/api/facts', 'POST', {
        claim: $('#invariant-claim').value.trim(),
        kind: $('#invariant-kind').value,
        agent: 'web-ui',
      });
      closeModal('modal-invariant');
      e.target.reset();
      showToast('Guardrail saved.', 'success');
      loadMemory();
      loadOverview();
      loadGovernance();
    } catch (err) {
      showToast(`Save failed: ${err.message}`, 'error');
    }
  });
}

/* Goals */
async function loadGoals() {
  const container = $('#goals-list');
  container.innerHTML = '<p class="panel-empty">Loading goals...</p>';
  try {
    const audit = await fetchJSON('/api/goals');
    state.goals = audit;
    renderGoals(container, audit.goals || []);
  } catch (err) {
    container.innerHTML = `<p class="panel-empty">Goals failed: ${escapeHtml(err.message)}</p>`;
  }
}

function renderGoals(container, goals) {
  if (!goals.length) {
    container.innerHTML = '<p class="panel-empty">No goals found. Run <code>sila goals</code>.</p>';
    return;
  }
  const order = { claimed: 0, pending: 1, draft: 2, implemented: 3 };
  const sorted = [...goals].sort((a, b) => (order[a.status] ?? 9) - (order[b.status] ?? 9) || a.slug.localeCompare(b.slug));
  container.innerHTML = sorted.map((g) => {
    const status = g.blocked ? 'blocked' : (g.status || 'pending');
    return `
      <article class="goal-card">
        <div class="claim-head">
          <span class="goal-status ${escapeHtml(status)}">${escapeHtml(status)}</span>
          <span class="chip">${escapeHtml(g.slug)}</span>
          <span class="turn-time">${g.tasks_total ? `${g.tasks_met || 0}/${g.tasks_total} tasks` : ''}</span>
        </div>
        <p class="claim-body">${escapeHtml(g.title || g.description || '')}</p>
        ${g.depends_on?.length ? `<div class="meta-line">Depends on: ${g.depends_on.map((d) => `<span class="chip">${escapeHtml(d)}</span>`).join(' ')}</div>` : ''}
      </article>`;
  }).join('');
}

/* Governance */
async function loadGovernance() {
  loadFeedback();
  loadADRs();
  loadVision();
}

async function loadFeedback() {
  try {
    const data = await fetchJSON('/api/feedback');
    state.feedback = data;
    renderFeedback(data.items || []);
  } catch (err) {
    $('#feedback-open').innerHTML = `<p class="panel-empty">${escapeHtml(err.message)}</p>`;
  }
}

function renderFeedback(items) {
  const open = items.filter((i) => i.status === 'open');
  const progress = items.filter((i) => i.status === 'in_progress');
  $('#count-open').textContent = open.length;
  $('#count-in-progress').textContent = progress.length;
  const card = (item) => `
    <article class="ticket">
      <p>${escapeHtml(item.content || '')}</p>
      <div class="ticket-meta">
        <span class="chip">${escapeHtml(item.agent || 'agent')}</span>
        <span class="chip">${escapeHtml(item.category || 'note')}</span>
        <span>${escapeHtml(timeAgo(item.created_at))}</span>
      </div>
      ${item.status !== 'closed' ? `<button type="button" class="link-btn btn-sm" data-resolve="${escapeHtml(item.id)}">Resolve</button>` : ''}
    </article>`;
  $('#feedback-open').innerHTML = open.map(card).join('') || '<p class="panel-empty">Nothing open.</p>';
  $('#feedback-in-progress').innerHTML = progress.map(card).join('') || '<p class="panel-empty">Nothing in progress.</p>';
  $$('[data-resolve]').forEach((b) => b.addEventListener('click', () => {
    $('#resolve-id').value = b.dataset.resolve;
    openModal('modal-resolve');
  }));
}

function initFeedbackForm() {
  $('#form-resolve').addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await mutation('/api/feedback', 'POST', {
        id: $('#resolve-id').value,
        agent: $('#resolve-agent').value.trim(),
        goal: $('#resolve-goal').value.trim(),
        note: $('#resolve-note').value.trim(),
      });
      closeModal('modal-resolve');
      e.target.reset();
      showToast('Feedback resolved.', 'success');
      loadFeedback();
    } catch (err) {
      showToast(`Resolve failed: ${err.message}`, 'error');
    }
  });
}

async function loadADRs() {
  try {
    const data = await fetchJSON('/api/adrs');
    state.adrs = data;
    renderADRs(data.adrs || [], data.report || {});
  } catch (err) {
    $('#adrs-list').innerHTML = `<p class="panel-empty">${escapeHtml(err.message)}</p>`;
  }
}

function renderADRs(adrs, report) {
  const el = $('#adrs-list');
  if (!adrs.length) {
    el.innerHTML = '<p class="panel-empty">No ADRs yet. Add one under <code>docs/adr/</code>.</p>';
    return;
  }
  el.innerHTML = `
    <p class="panel-sub" style="margin-bottom:10px;">${formatNumber(report.healthy_adrs || 0)} healthy, ${formatNumber(report.drifted_adrs || 0)} drifted, ${formatNumber(report.missing_refs || 0)} missing refs</p>
    ${adrs.map((a) => `
      <div class="goal-row">
        <span class="goal-name">ADR-${String(a.number || 0).padStart(4, '0')} ${escapeHtml(a.title || '')}</span>
        <span class="goal-status">${escapeHtml(String(a.status || ''))}</span>
      </div>`).join('')}`;
}

async function loadVision() {
  try {
    const data = await fetchJSON('/api/vision');
    state.vision = data;
    renderVision(data);
  } catch (err) {
    $('#vision-guardrails').innerHTML = `<p class="panel-empty">${escapeHtml(err.message)}</p>`;
  }
}

function renderVision(data) {
  const group = (title, items) => `
    <h3 class="panel-sub" style="margin:10px 0 4px;">${escapeHtml(title)}</h3>
    ${(items || []).length ? `<ul class="handoff-items">${items.map((i) => `<li>${escapeHtml(i.claim || '')}</li>`).join('')}</ul>` : '<p class="panel-empty">None recorded.</p>'}`;
  $('#vision-guardrails').innerHTML = `${group('Non-goals', data.non_goals)}${group('Invariants', data.invariants)}`;
}

function initTriage() {
  $$('.chip-btn[data-sample]').forEach((b) => b.addEventListener('click', () => {
    const input = $('#triage-input');
    if (input) {
      input.value = b.dataset.sample;
      input.focus();
    }
  }));
  $('#form-triage').addEventListener('submit', async (e) => {
    e.preventDefault();
    const proposal = $('#triage-input').value.trim();
    if (!proposal) return;
    const out = $('#triage-result');
    out.innerHTML = '<p class="panel-empty">Evaluating...</p>';
    try {
      const res = await mutation('/api/vision/triage', 'POST', { proposal });
      const verdict = String(res.verdict || '').toLowerCase();
      out.innerHTML = `
        <div class="result">
          <div class="result-head">
            <span class="verdict ${escapeHtml(verdict)}">${escapeHtml(res.verdict || '')}</span>
            <span class="result-score">fit ${formatNumber(res.fit_score)}</span>
          </div>
          <p class="result-body">${escapeHtml(res.strategic_fit || '')}</p>
          ${res.recommended_action ? `<p class="panel-sub" style="margin-top:8px;">${escapeHtml(res.recommended_action)}</p>` : ''}
        </div>`;
    } catch (err) {
      out.innerHTML = `<p class="panel-empty">Triage failed: ${escapeHtml(err.message)}</p>`;
    }
  });
}

/* Expand */
async function expandMessage(id) {
  const body = $('#expand-content');
  body.innerHTML = '<p class="panel-empty">Loading context...</p>';
  openModal('modal-expand');
  try {
    const ctx = await fetchJSON(`/api/expand?id=${encodeURIComponent(id)}&window=3`);
    const turns = ctx.turns || [];
    body.innerHTML = turns.map((t) => `
      <article class="result">
        <div class="result-head">
          <span class="chip ${String(t.role).toLowerCase() === 'user' ? 'role-user' : ''}">${escapeHtml(t.role || 'turn')}</span>
          <span class="chip">${escapeHtml(t.tool || 'unknown')}</span>
          <span class="result-score">${escapeHtml(timeAgo(t.created_at))}</span>
        </div>
        <p class="result-body">${escapeHtml(t.content || '')}</p>
      </article>`).join('') || '<p class="panel-empty">No context available.</p>';
  } catch (err) {
    body.innerHTML = `<p class="panel-empty">Expand failed: ${escapeHtml(err.message)}</p>`;
  }
}

/* Modals */
function openModal(id) {
  const modal = document.getElementById(id);
  if (!modal) return;
  modal.classList.add('is-open');
  const focusable = modal.querySelector('input, textarea, select, button');
  focusable?.focus();
}

function closeModal(id) {
  const modal = document.getElementById(id);
  if (!modal) return;
  modal.classList.remove('is-open');
}

function initModals() {
  $$('[data-close-modal]').forEach((b) => b.addEventListener('click', () => {
    const modal = b.closest('.modal-overlay');
    if (modal) modal.classList.remove('is-open');
  }));
  $$('.modal-overlay').forEach((overlay) => overlay.addEventListener('click', (e) => {
    if (e.target === overlay) overlay.classList.remove('is-open');
  }));
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') $$('.modal-overlay.is-open').forEach((m) => m.classList.remove('is-open'));
  });
}

function guardedConfirm(title, message, action) {
  $('#confirm-title').textContent = title;
  $('#confirm-body').textContent = message;
  state.confirmAction = action;
  openModal('modal-confirm');
}

function initConfirm() {
  $('#confirm-submit').addEventListener('click', async () => {
    const action = state.confirmAction;
    state.confirmAction = null;
    closeModal('modal-confirm');
    if (!action) return;
    try {
      await action();
    } catch (err) {
      showToast(err.message, 'error');
    }
  });
}

/* SSE */
function initSSE() {
  const status = $('#sse-status');
  const label = $('#sse-label');
  if (!window.EventSource) {
    label.textContent = 'no live updates';
    return;
  }
  const source = new EventSource('/api/events');
  let reloadTimer = null;
  const scheduleReload = () => {
    if (reloadTimer) return;
    reloadTimer = setTimeout(() => {
      reloadTimer = null;
      loadOverview();
    }, 400);
  };
  source.onopen = () => {
    status.classList.add('is-live');
    status.classList.remove('is-down');
    label.textContent = 'live';
  };
  source.onerror = () => {
    status.classList.remove('is-live');
    status.classList.add('is-down');
    label.textContent = 'reconnecting';
  };
  source.onmessage = scheduleReload;
  ['claims_reconciled', 'fact_promoted', 'fact_deleted', 'feedback_resolved', 'feedback_reopened', 'adr_created'].forEach((name) => {
    source.addEventListener(name, scheduleReload);
  });
}

/* Toast */
function showToast(message, type = 'info') {
  const container = $('#toast-container');
  const toast = document.createElement('div');
  toast.className = `toast ${type}`;
  toast.textContent = message;
  container.appendChild(toast);
  setTimeout(() => toast.remove(), 4200);
}

/* Shutdown */
function initShutdown() {
  $('#btn-shutdown').addEventListener('click', () => {
    guardedConfirm('Stop the console', 'This shuts down the local Sila server and discards an ephemeral demo sandbox.', async () => {
      await mutation('/api/shutdown', 'POST', {});
      document.body.innerHTML = '<main class="main"><h1>Console stopped</h1><p>The local server is shutting down.</p></main>';
    });
  });
  $('#btn-refresh').addEventListener('click', () => {
    loadOverview();
    if (state.view === 'memory') loadMemory();
    if (state.view === 'goals') loadGoals();
    if (state.view === 'governance') loadGovernance();
  });
}

/* Boot */
function boot() {
  initTheme();
  initNav();
  initModals();
  initConfirm();
  initAsk();
  initTimeline();
  initMemory();
  initFeedbackForm();
  initTriage();
  initShutdown();
  initSSE();
  loadOverview();
}

boot();
