// Sila Decision Cockpit - Frontend Controller

let appState = {
  activeTab: 'healing',
  status: null,
  theme: localStorage.getItem('sila-theme') || 'light',
  pendingConfirm: null
};

// --- DOM References ---
const sseIndicator = document.getElementById('sse-indicator');
const sseLabel = document.getElementById('sse-label');
const projectBadge = document.getElementById('header-project-badge');
const themeToggle = document.getElementById('theme-toggle');

// Metrics
const statMessages = document.getElementById('stat-messages');
const statFacts = document.getElementById('stat-facts');
const statVerified = document.getElementById('stat-verified');
const statStale = document.getElementById('stat-stale');
const statProjects = document.getElementById('stat-projects');
const statFreelist = document.getElementById('stat-freelist');

// --- Initialization ---
document.addEventListener('DOMContentLoaded', () => {
  initTheme();
  initTabs();
  initSSE();
  initModals();
  loadAllData();

  document.getElementById('btn-refresh').addEventListener('click', () => {
    loadAllData();
    showToast('Refreshing all cockpit data...', 'info');
  });

  themeToggle.addEventListener('click', toggleTheme);
  document.getElementById('chk-stale-only').addEventListener('change', loadClaims);

  document.getElementById('btn-reverify-disk').addEventListener('click', () => {
    guardedConfirm(
      'Re-verify Claims on Disk',
      'Scan all referenced file paths against actual disk files and refresh grounding indicators?',
      async () => {
        try {
          const res = await fetch('/api/claims/verify', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: '{}'
          });
          if (!res.ok) throw new Error(await res.text());
          const data = await res.json();
          showToast(
            `✔ Re-verification Complete: ${data.total_checked} checked, ${data.total_grounded ?? '-'} grounded, ${data.total_stale ?? '-'} stale (Recovered: ${data.recovered ?? 0}, Newly Stale: ${data.newly_stale ?? 0})`,
            'success'
          );
          loadAllData();
        } catch (err) {
          showToast('Re-verify failed: ' + err.message, 'error');
        }
      }
    );
  });

  document.getElementById('btn-add-invariant').addEventListener('click', () => {
    openModal('modal-add-invariant');
  });

  document.getElementById('form-add-invariant').addEventListener('submit', async (e) => {
    e.preventDefault();
    const claim = document.getElementById('invariant-claim').value.trim();
    const kind = document.getElementById('invariant-kind').value;
    if (!claim) return;

    try {
      const res = await fetch('/api/facts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ claim, kind, agent: 'web-cockpit' })
      });
      if (!res.ok) throw new Error(await res.text());
      closeModal('modal-add-invariant');
      document.getElementById('invariant-claim').value = '';
      showToast('New architectural invariant recorded.', 'success');
      loadAllData();
    } catch (err) {
      showToast('Save invariant failed: ' + err.message, 'error');
    }
  });

  document.getElementById('form-resolve-ticket').addEventListener('submit', async (e) => {
    e.preventDefault();
    const id = document.getElementById('resolve-ticket-id').value;
    const agent = document.getElementById('resolve-agent').value;
    const goal = document.getElementById('resolve-goal').value;
    const note = document.getElementById('resolve-note').value;

    try {
      const res = await fetch('/api/feedback', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, agent, goal, note })
      });
      if (!res.ok) throw new Error(await res.text());
      closeModal('modal-resolve');
      showToast(`Ticket ${id} resolved.`, 'success');
      loadFeedback();
    } catch (err) {
      showToast('Resolve failed: ' + err.message, 'error');
    }
  });

  document.getElementById('form-triage').addEventListener('submit', async (e) => {
    e.preventDefault();
    const proposal = document.getElementById('input-proposal').value.trim();
    if (!proposal) return;
    triageProposal(proposal);
  });

  document.getElementById('form-search').addEventListener('submit', async (e) => {
    e.preventDefault();
    const q = document.getElementById('input-search-q').value.trim();
    const tool = document.getElementById('select-search-tool').value;
    if (!q) return;
    searchTranscripts(q, tool);
  });

  // Safe Server Shutdown Handler
  document.getElementById('btn-shutdown')?.addEventListener('click', confirmShutdown);

  window.addEventListener('keydown', (e) => {
    if (e.key === 'q' || e.key === 'Q') {
      const tag = document.activeElement?.tagName?.toLowerCase();
      if (tag === 'input' || tag === 'textarea' || document.activeElement?.isContentEditable) {
        return;
      }
      confirmShutdown();
    }
  });

  // Example Proposal Chips
  document.querySelectorAll('.chip-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const sample = btn.getAttribute('data-sample');
      if (sample) {
        const input = document.getElementById('input-proposal');
        input.value = sample;
        triageProposal(sample);
      }
    });
  });
});

function confirmShutdown() {
  guardedConfirm(
    'Safely Stop Sila Web Cockpit?',
    'This will gracefully terminate the local web server and release the SQLite lock and network port. Press OK to shut down.',
    async () => {
      try {
        const res = await fetch('/api/shutdown', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: '{}'
        });
        if (!res.ok) throw new Error(await res.text());
        showToast('Sila web cockpit stopped cleanly.', 'success');
        setTimeout(() => {
          document.body.innerHTML = `
            <div style="display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;font-family:var(--font-main);text-align:center;padding:24px;">
              <h2 style="color:var(--text-bright);margin-bottom:12px;">✔ Sila Web Cockpit Stopped</h2>
              <p style="color:var(--text-secondary);max-width:480px;line-height:1.5;">The local server has terminated safely and released all system resources. You may now close this browser tab or restart from terminal using <code>sila ui</code>.</p>
            </div>
          `;
        }, 400);
      } catch (err) {
        showToast('Shutdown failed: ' + err.message, 'error');
      }
    }
  );
}

// --- Theme Management ---
function initTheme() {
  document.documentElement.setAttribute('data-theme', appState.theme);
  updateThemeButtonText();
}

function toggleTheme() {
  appState.theme = appState.theme === 'light' ? 'dark' : 'light';
  localStorage.setItem('sila-theme', appState.theme);
  document.documentElement.setAttribute('data-theme', appState.theme);
  updateThemeButtonText();
}

function updateThemeButtonText() {
  themeToggle.textContent = appState.theme === 'light' ? '🌙 Dark' : '☀️ Light';
}

// --- Toast Notifications ---
function showToast(message, type = 'info') {
  const container = document.getElementById('toast-container');
  const toast = document.createElement('div');
  toast.className = `toast toast-${type}`;
  toast.textContent = message;
  container.appendChild(toast);
  setTimeout(() => {
    toast.style.opacity = '0';
    toast.style.transform = 'translateY(10px)';
    toast.style.transition = 'all 0.3s ease';
    setTimeout(() => toast.remove(), 300);
  }, 4000);
}

// --- Tabs Controller ---
function initTabs() {
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
      document.querySelectorAll('.tab-panel').forEach(p => p.classList.remove('active'));

      btn.classList.add('active');
      const tabId = btn.getAttribute('data-tab');
      appState.activeTab = tabId;
      document.getElementById(`panel-${tabId}`).classList.add('active');
    });
  });
}

// --- SSE Real-time Updates ---
function initSSE() {
  const evtSource = new EventSource('/api/events');

  evtSource.onopen = () => {
    sseIndicator.className = 'sse-dot connected';
    sseLabel.textContent = 'Live SSE';
  };

  evtSource.onerror = () => {
    sseIndicator.className = 'sse-dot';
    sseLabel.textContent = 'Reconnecting...';
  };

  evtSource.addEventListener('claims_reconciled', () => {
    loadClaims();
    loadStatus();
  });

  evtSource.addEventListener('feedback_resolved', () => {
    loadFeedback();
  });

  evtSource.addEventListener('feedback_reopened', () => {
    loadFeedback();
  });

  evtSource.addEventListener('fact_created', () => {
    loadClaims();
    loadStatus();
  });

  evtSource.addEventListener('fact_promoted', () => {
    loadClaims();
    loadStatus();
  });

  evtSource.addEventListener('fact_deleted', () => {
    loadClaims();
    loadStatus();
  });

  evtSource.addEventListener('adr_created', () => {
    loadADRs();
  });
}

// --- Load Data ---
async function loadAllData() {
  await Promise.all([
    loadStatus(),
    loadClaims(),
    loadFeedback(),
    loadADRs(),
    loadVision(),
    loadSwarmAndGoals()
  ]);
}

async function loadStatus() {
  try {
    const res = await fetch('/api/status');
    const data = await res.json();
    appState.status = data;

    projectBadge.textContent = data.project_id || 'sila';
    statMessages.textContent = data.messages?.toLocaleString() ?? '0';
    statFacts.textContent = data.facts?.toLocaleString() ?? '0';
    statVerified.textContent = data.verified?.toLocaleString() ?? '0';
    statStale.textContent = data.stale_claims?.toLocaleString() ?? '0';
    statProjects.textContent = data.projects?.toLocaleString() ?? '1';
    statFreelist.textContent = data.freelist_pages != null ? `${data.freelist_pages} pgs` : '-';
  } catch (err) {
    console.error('loadStatus error:', err);
  }
}

// --- 1. Knowledge Healing & Claims ---
async function loadClaims() {
  const staleOnly = document.getElementById('chk-stale-only').checked;
  const container = document.getElementById('claims-list');
  try {
    const res = await fetch(`/api/claims?stale=${staleOnly}`);
    const claims = await res.json();
    container.innerHTML = '';

    if (!claims || claims.length === 0) {
      container.innerHTML = `
        <div class="card" style="grid-column: 1 / -1;">
          <div class="card-recommendation recommended-clean">
            <h5>✔ All Claims Grounded</h5>
            <p>Zero stale claims detected on disk for active project scope. Run <code>sila sync</code> to ingest new agent sessions or add explicit architectural invariants.</p>
          </div>
        </div>
      `;
      return;
    }

    claims.forEach(c => {
      const card = document.createElement('div');
      card.className = 'card';

      const isInvariant = c.kind === 'invariant' || c.kind === 'nongoal' || c.kind === 'milestone';
      const isStale = !c.file_refs_exist && !isInvariant;

      const badgeClass = c.file_refs_exist ? 'badge-green' : (isInvariant ? 'badge-purple' : 'badge-red');
      const badgeText = c.file_refs_exist ? 'Grounded' : (isInvariant ? 'Invariant' : 'Stale Ref');

      let filesHtml = '';
      if (c.repo_files && c.repo_files.length > 0) {
        filesHtml = `<ul class="file-list">${c.repo_files.map(f => `
          <li class="file-item ${c.file_refs_exist ? 'file-present' : 'file-missing'}">
            ${c.file_refs_exist ? '✔' : '✖'} <code>${escapeHtml(f)}</code>
          </li>`).join('')}</ul>`;
      }

      // Actionable Recommendation Box
      let recHtml = '';
      if (isStale) {
        recHtml = `
          <div class="card-recommendation">
            <h5>💡 Recommended Decision & Action</h5>
            <p>Referenced files were not found on disk. If this claim represents a conceptual project rule rather than a transient file, promote it to an Invariant. If obsolete, prune it.</p>
            <div class="card-actions">
              <button class="btn btn-primary btn-sm btn-promote-invariant" data-id="${c.id}">⚡ Promote to Invariant</button>
              <button class="btn btn-danger btn-sm btn-prune-claim" data-id="${c.id}">🗑️ Prune</button>
              <button class="btn btn-sm btn-copy-cli" data-cmd="sila dream query &quot;${escapeHtml(c.claim.slice(0, 32))}&quot; --stale" title="Copy CLI search command">📋 Copy CLI: <code>sila dream query</code></button>
            </div>
          </div>
        `;
      } else if (isInvariant) {
        recHtml = `
          <div class="card-recommendation recommended-clean">
            <h5>✔ Durable Invariant</h5>
            <p>Recognized as an invariant rule. Invariant validity is conceptual and does not decay with physical file moves.</p>
            <div class="card-actions">
              <button class="btn btn-sm btn-copy-cli" data-cmd="sila memo &quot;${escapeHtml(c.claim)}&quot;" title="Copy memo directive command">📋 Copy CLI: <code>sila memo</code></button>
            </div>
          </div>
        `;
      }

      card.innerHTML = `
        <div class="card-header">
          <span class="card-title">${escapeHtml(c.claim)}</span>
          <span class="badge ${badgeClass}">${badgeText}</span>
        </div>
        <div class="card-meta">
          <span>Project: <strong>${escapeHtml(c.project_id)}</strong></span>
          ${c.tools?.length ? `<span>Tools: ${c.tools.join(', ')}</span>` : ''}
          ${c.confidence ? `<span>Confidence: ${escapeHtml(c.confidence)}</span>` : ''}
        </div>
        ${filesHtml}
        ${recHtml}
      `;

      // Wire action buttons
      card.querySelector('.btn-promote-invariant')?.addEventListener('click', () => {
        guardedConfirm(
          'Promote Claim to Invariant',
          `Elevate this claim to a permanent architectural invariant? It will no longer be treated as stale.\n\n"${c.claim}"`,
          async () => {
            try {
              const res = await fetch('/api/facts?action=promote', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ id: c.id })
              });
              if (!res.ok) throw new Error(await res.text());
              showToast('Claim successfully promoted to invariant.', 'success');
              loadClaims();
              loadStatus();
            } catch (err) {
              showToast('Promotion failed: ' + err.message, 'error');
            }
          }
        );
      });

      card.querySelector('.btn-prune-claim')?.addEventListener('click', () => {
        guardedConfirm(
          'Prune Obsolete Claim',
          `Remove this stale claim from the knowledge store?\n\n"${c.claim}"`,
          async () => {
            try {
              const res = await fetch(`/api/facts?id=${encodeURIComponent(c.id)}`, {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json' }
              });
              if (!res.ok) throw new Error(await res.text());
              showToast('Stale claim pruned.', 'success');
              loadClaims();
              loadStatus();
            } catch (err) {
              showToast('Prune failed: ' + err.message, 'error');
            }
          }
        );
      });

      card.querySelector('.btn-copy-cli')?.addEventListener('click', (e) => {
        const cmd = e.currentTarget.getAttribute('data-cmd');
        navigator.clipboard.writeText(cmd);
        showToast(`Copied CLI command: ${cmd}`, 'info');
      });

      container.appendChild(card);
    });
  } catch (err) {
    container.innerHTML = `<div class="card"><p style="color:var(--red);">Error loading claims: ${err.message}</p></div>`;
  }
}

// --- 2. Feedback Board ---
async function loadFeedback() {
  const cols = {
    open: document.getElementById('col-feedback-open'),
    in_progress: document.getElementById('col-feedback-in_progress'),
    closed: document.getElementById('col-feedback-closed'),
    wontfix: document.getElementById('col-feedback-wontfix')
  };

  Object.values(cols).forEach(c => c.innerHTML = '');

  try {
    const res = await fetch('/api/feedback');
    const data = await res.json();
    const items = data.items || [];
    const stats = data.stats || {};

    document.getElementById('badge-count-open').textContent = stats.open || 0;
    document.getElementById('badge-count-in_progress').textContent = stats.in_progress || 0;
    document.getElementById('badge-count-closed').textContent = stats.closed || 0;
    document.getElementById('badge-count-wontfix').textContent = stats.wontfix || 0;

    items.forEach(item => {
      const col = cols[item.status] || cols.open;
      const card = document.createElement('div');
      card.className = 'card';
      card.style.marginBottom = '12px';

      let actionsHtml = '';
      if (item.status === 'open' || item.status === 'in_progress') {
        actionsHtml = `
          <div class="card-recommendation">
            <h5>💡 Recommended Action</h5>
            <p>Investigate this agent friction. Click Resolve to document the fix and attribute it to an active goal.</p>
            <div class="card-actions">
              <button class="btn btn-primary btn-sm btn-resolve" data-id="${item.id}">Resolve Ticket</button>
              <button class="btn btn-sm btn-copy-cli" data-cmd="sila feedback resolve ${item.id}" title="Copy CLI resolve command">📋 Copy CLI: <code>sila feedback resolve</code></button>
            </div>
          </div>
        `;
      } else {
        actionsHtml = `
          <div class="card-recommendation recommended-clean">
            <h5>✔ Resolved Ticket</h5>
            <p>${escapeHtml(item.resolution_note || 'Closed')}</p>
            <div class="card-actions">
              <button class="btn btn-sm btn-reopen" data-id="${item.id}">Reopen</button>
            </div>
          </div>
        `;
      }

      card.innerHTML = `
        <div class="card-header">
          <span class="card-title" style="font-size:13px;">${escapeHtml(item.content || item.message)}</span>
        </div>
        <div class="card-meta">
          <span>Agent: <strong>${escapeHtml(item.agent || 'unknown')}</strong></span>
          <span>Category: ${escapeHtml(item.category || 'general')}</span>
          <span>Status: <strong class="badge badge-${item.status === 'closed' ? 'green' : (item.status === 'in_progress' ? 'yellow' : 'purple')}">${escapeHtml(item.status)}</strong></span>
        </div>
        ${actionsHtml}
      `;

      card.querySelector('.btn-resolve')?.addEventListener('click', () => {
        document.getElementById('resolve-ticket-id').value = item.id;
        openModal('modal-resolve');
      });

      card.querySelector('.btn-reopen')?.addEventListener('click', () => {
        guardedConfirm('Reopen Feedback Ticket', `Reopen ticket ${item.id}?`, async () => {
          try {
            const res = await fetch('/api/feedback?action=reopen', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ id: item.id })
            });
            if (!res.ok) throw new Error(await res.text());
            showToast(`Ticket ${item.id} reopened.`, 'info');
            loadFeedback();
          } catch (err) {
            showToast('Reopen failed: ' + err.message, 'error');
          }
        });
      });

      card.querySelector('.btn-copy-cli')?.addEventListener('click', (e) => {
        const cmd = e.currentTarget.getAttribute('data-cmd');
        navigator.clipboard.writeText(cmd);
        showToast(`Copied CLI command: ${cmd}`, 'info');
      });

      col.appendChild(card);
    });
  } catch (err) {
    console.error('loadFeedback error:', err);
  }
}

// --- 3. ADR Studio ---
async function loadADRs() {
  const container = document.getElementById('adrs-list');
  try {
    const res = await fetch('/api/adrs');
    const data = await res.json();
    container.innerHTML = '';

    const adrs = data.adrs || [];
    if (adrs.length === 0) {
      container.innerHTML = `
        <div class="card" style="grid-column: 1 / -1;">
          <div class="card-recommendation">
            <h5>💡 No Architecture Decision Records Found</h5>
            <p>Run <code>sila adr new &quot;&lt;title&gt;&quot;</code> or promote a memo from Knowledge Healing to create your first MADR record in <code>docs/adr/</code>.</p>
          </div>
        </div>
      `;
      return;
    }

    adrs.forEach(adr => {
      const card = document.createElement('div');
      card.className = 'card';

      card.innerHTML = `
        <div class="card-header">
          <span class="card-title">ADR-${String(adr.number).padStart(4, '0')}: ${escapeHtml(adr.title)}</span>
          <span class="badge ${adr.status === 'Accepted' ? 'badge-green' : 'badge-yellow'}">${adr.status}</span>
        </div>
        <div class="card-meta">
          <span>Date: ${escapeHtml(adr.date || '-')}</span>
          <span>Deciders: <strong>${escapeHtml(adr.deciders || 'unattributed')}</strong></span>
        </div>
        <div class="card-body">
          <code>${escapeHtml(adr.path)}</code>
        </div>
        ${adr.references?.length ? `<ul class="file-list">${adr.references.map(r => `<li class="file-item"><code>${escapeHtml(r)}</code></li>`).join('')}</ul>` : ''}
        <div class="card-recommendation recommended-clean">
          <h5>💡 Grounding Guidance</h5>
          <p>Run <code>sila adr doctor</code> to verify references against disk or edit <code>${escapeHtml(adr.path)}</code> directly.</p>
        </div>
      `;
      container.appendChild(card);
    });
  } catch (err) {
    container.innerHTML = `<div class="card"><p style="color:var(--red);">Error loading ADRs: ${err.message}</p></div>`;
  }
}

// --- 4. Strategic Vision & Guardrails ---
async function loadVision() {
  const nongoalsList = document.getElementById('list-nongoals');
  const invariantsList = document.getElementById('list-invariants');
  const nongoalsCount = document.getElementById('badge-nongoals-count');
  const invariantsCount = document.getElementById('badge-invariants-count');

  if (!nongoalsList || !invariantsList) return;

  try {
    const res = await fetch('/api/vision');
    const data = await res.json();

    const nonGoals = data.non_goals || [];
    const invariants = data.invariants || [];

    if (nongoalsCount) nongoalsCount.textContent = nonGoals.length;
    if (invariantsCount) invariantsCount.textContent = invariants.length;

    if (nonGoals.length === 0) {
      nongoalsList.innerHTML = `
        <li class="guardrail-item card-meta">
          No explicit non-goals recorded yet. Add one from CLI:
          <br><code>sila vision add-nongoal "&lt;negative-constraint&gt;"</code>
        </li>
      `;
    } else {
      nongoalsList.innerHTML = nonGoals.map(ng => `
        <li class="guardrail-item">
          <strong>⛔</strong> ${escapeHtml(ng.claim)}
        </li>
      `).join('');
    }

    if (invariants.length === 0) {
      invariantsList.innerHTML = `
        <li class="guardrail-item card-meta">
          No architectural invariants recorded yet. Add one from CLI:
          <br><code>sila memo "&lt;invariant-directive&gt;"</code>
        </li>
      `;
    } else {
      invariantsList.innerHTML = invariants.map(inv => `
        <li class="guardrail-item">
          <strong>🏛️</strong> ${escapeHtml(inv.claim)}
        </li>
      `).join('');
    }
  } catch (err) {
    nongoalsList.innerHTML = `<li class="guardrail-item card-meta">Failed to load non-goals: ${err.message}</li>`;
    invariantsList.innerHTML = `<li class="guardrail-item card-meta">Failed to load invariants: ${err.message}</li>`;
  }
}

// Autonomous Proposal Triage Simulator
async function triageProposal(proposal) {
  const container = document.getElementById('triage-result-container');
  container.style.display = 'block';
  container.innerHTML = '<div class="card"><p>Simulating proposal tournament against project non-goals and invariants...</p></div>';

  try {
    const res = await fetch('/api/vision/triage', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ proposal })
    });
    const data = await res.json();

    const isApproved = data.verdict === 'APPROVED';
    const isRejected = data.verdict === 'REJECTED';
    const badgeClass = isApproved ? 'badge-green' : (isRejected ? 'badge-red' : 'badge-yellow');
    const borderLeftColor = isApproved ? 'var(--green)' : (isRejected ? 'var(--red)' : 'var(--yellow)');

    let conflictsHtml = '';
    if (data.direct_conflicts && data.direct_conflicts.length > 0) {
      conflictsHtml = `
        <div style="margin-top:14px; padding:10px 14px; background:var(--red-bg); border-radius:6px; border:1px solid rgba(220,38,38,0.3);">
          <strong style="color:var(--red); font-size:12.5px;">⛔ Direct Architectural Conflicts:</strong>
          <ul style="margin-left:18px; margin-top:4px; color:var(--red); font-size:12px; line-height:1.5;">
            ${data.direct_conflicts.map(c => `<li>${escapeHtml(c)}</li>`).join('')}
          </ul>
        </div>
      `;
    }

    let nongoalsHtml = '';
    if (data.violated_nongoals && data.violated_nongoals.length > 0) {
      nongoalsHtml = `
        <div style="margin-top:10px; padding:10px 14px; background:var(--red-bg); border-radius:6px; border:1px solid rgba(220,38,38,0.3);">
          <strong style="color:var(--red); font-size:12.5px;">⚠️ Violated Non-Goals:</strong>
          <ul style="margin-left:18px; margin-top:4px; color:var(--red); font-size:12px; line-height:1.5;">
            ${data.violated_nongoals.map(ng => `<li>${escapeHtml(ng)}</li>`).join('')}
          </ul>
        </div>
      `;
    }

    container.innerHTML = `
      <div class="card" style="border-left: 5px solid ${borderLeftColor}; margin-bottom: 24px;">
        <div class="card-header">
          <span class="card-title">Verdict: ${escapeHtml(data.verdict)}</span>
          <span class="badge ${badgeClass}">${escapeHtml(data.verdict)}</span>
        </div>
        <div class="card-body">
          <div style="margin-bottom: 12px;">
            <div style="display:flex; justify-content:space-between; font-size:12px; margin-bottom:4px;">
              <span><strong>Strategic Alignment Fit Score:</strong></span>
              <span><strong>${data.fit_score}/100</strong></span>
            </div>
            <div style="background:var(--border); height:8px; border-radius:4px; overflow:hidden;">
              <div style="background:${borderLeftColor}; height:100%; width:${Math.min(100, Math.max(0, data.fit_score))}%; transition:width 0.3s ease;"></div>
            </div>
          </div>

          <p style="margin-top:8px;"><strong>Strategic Fit:</strong> ${escapeHtml(data.strategic_fit || '-')}</p>
          <p style="margin-top:8px;"><strong>💡 Actionable Guidance:</strong> ${escapeHtml(data.recommended_action || '-')}</p>
          
          ${conflictsHtml}
          ${nongoalsHtml}
        </div>
      </div>
    `;
  } catch (err) {
    container.innerHTML = `<div class="card"><p style="color:var(--red);">Triage evaluation failed: ${err.message}</p></div>`;
  }
}

// --- 5. Swarm & Goals ---
async function loadSwarmAndGoals() {
  const crewContainer = document.getElementById('crew-list');
  const goalsContainer = document.getElementById('goals-list');

  // Load Crew
  try {
    const res = await fetch('/api/crew');
    const crewmates = await res.json();
    crewContainer.innerHTML = '';

    if (!crewmates || crewmates.length === 0) {
      crewContainer.innerHTML = `
        <div class="card" style="grid-column: 1 / -1;">
          <div class="card-recommendation recommended-clean">
            <h5>💡 No Active Swarm Crewmates</h5>
            <p>Spawn an agent crewmate in a disposable worktree: <code>sila crew spawn claude &lt;goal-slug&gt;</code>.</p>
          </div>
        </div>
      `;
    } else {
      crewmates.forEach(cm => {
        const card = document.createElement('div');
        card.className = 'card';
        card.innerHTML = `
          <div class="card-header">
            <span class="card-title">${escapeHtml(cm.harness)} · ${escapeHtml(cm.id)}</span>
            <span class="badge badge-green">${escapeHtml(cm.status)}</span>
          </div>
          <div class="card-meta">
            <span>Goal: <strong>${escapeHtml(cm.goal_slug)}</strong></span>
            <span>PID: ${cm.pid}</span>
            <span>Tmux: <code>${escapeHtml(cm.tmux_session)}</code></span>
          </div>
        `;
        crewContainer.appendChild(card);
      });
    }
  } catch (err) {
    crewContainer.innerHTML = `<div class="card"><p class="card-meta">Crew status unavailable.</p></div>`;
  }

  // Load Goals
  try {
    const res = await fetch('/api/goals');
    const audit = await res.json();
    goalsContainer.innerHTML = '';

    const goals = audit.goals || [];
    if (goals.length === 0) {
      goalsContainer.innerHTML = `<div class="card"><p class="card-meta">No goals defined in goals/.</p></div>`;
      return;
    }

    goals.forEach(g => {
      const card = document.createElement('div');
      card.className = 'card';

      const isImplemented = g.status === 'implemented';
      const isClaimed = g.status === 'claimed' || !!g.claim;
      const isBlocked = !!g.blocked;
      const isReopened = !!g.reopened;

      let badgeClass = 'badge-yellow';
      let badgeText = '🟡 Ready to Pick';
      if (isReopened) {
        badgeClass = 'badge-yellow';
        badgeText = '🔁 Reopened';
      } else if (isImplemented) {
        badgeClass = 'badge-green';
        badgeText = '✔ Implemented';
      } else if (isClaimed) {
        badgeClass = 'badge-purple';
        badgeText = `⚡ Claimed (${g.claim?.agent || 'agent'})`;
      } else if (isBlocked) {
        badgeClass = 'badge-red';
        badgeText = '⛔ Blocked';
      }

      const tasksMet = g.tasks_met ?? g.tasks_completed ?? (isImplemented ? (g.tasks_total || 0) : 0);
      const tasksTotal = g.tasks_total || 0;

      let recHtml = '';
      if (!isImplemented && !isClaimed && !isBlocked) {
        recHtml = `
          <div class="card-recommendation">
            <h5>💡 Recommended Next Step</h5>
            <p>Unblocked and ready to claim. Launch with: <code>sila goals claim ${escapeHtml(g.slug)} --agent=me</code></p>
            <div class="card-actions">
              <button class="btn btn-sm btn-copy-cli" data-cmd="sila goals claim ${escapeHtml(g.slug)} --agent=me" title="Copy CLI claim command">📋 Copy CLI: <code>sila goals claim</code></button>
            </div>
          </div>
        `;
      } else if (isBlocked) {
        recHtml = `
          <div class="card-recommendation">
            <h5>⛔ Dependency Blocker</h5>
            <p>${escapeHtml(g.blocked_reason || 'Waiting on prerequisites: ' + (g.unmet_dependencies || []).join(', '))}</p>
          </div>
        `;
      } else if (isImplemented) {
        recHtml = `
          <div class="card-recommendation recommended-clean">
            <h5>✔ Delivered & Verified</h5>
            <p>Goal committed with all ${tasksTotal} tasks satisfied.${g.commit ? ` Commit: <code>${escapeHtml(g.commit)}</code>` : ''}</p>
          </div>
        `;
      }

      card.innerHTML = `
        <div class="card-header">
          <span class="card-title">${escapeHtml(g.title || g.slug)}</span>
          <span class="badge ${badgeClass}">${badgeText}</span>
        </div>
        <div class="card-meta">
          <span>Slug: <code>${escapeHtml(g.slug)}</code></span>
          <span>Shape: ${g.shape === 'scout' ? '🔭 Scout' : '🚢 Ship'}</span>
          <span>Tasks: <strong>${tasksMet}/${tasksTotal}</strong></span>
        </div>
        ${recHtml}
      `;

      card.querySelector('.btn-copy-cli')?.addEventListener('click', (e) => {
        const cmd = e.currentTarget.getAttribute('data-cmd');
        navigator.clipboard.writeText(cmd);
        showToast(`Copied CLI command: ${cmd}`, 'info');
      });

      goalsContainer.appendChild(card);
    });
  } catch (err) {
    goalsContainer.innerHTML = `<div class="card"><p class="card-meta">Goals matrix unavailable.</p></div>`;
  }
}

// --- 6. Omni-Search & Conversation Unrolling ---
async function searchTranscripts(q, tool) {
  const container = document.getElementById('search-results');
  container.innerHTML = '<div class="card"><p>Searching transcripts across AI CLI tools...</p></div>';

  try {
    let url = `/api/search?q=${encodeURIComponent(q)}`;
    if (tool) url += `&tool=${encodeURIComponent(tool)}`;
    const res = await fetch(url);
    const results = await res.json();
    container.innerHTML = '';

    if (!results || results.length === 0) {
      container.innerHTML = `
        <div class="card" style="grid-column: 1 / -1;">
          <div class="card-recommendation">
            <h5>💡 No Matches Found</h5>
            <p>No conversation turns matched "${escapeHtml(q)}". Try broader search terms or clear the tool filter.</p>
          </div>
        </div>
      `;
      return;
    }

    results.forEach(msg => {
      const card = document.createElement('div');
      card.className = 'card';

      const toolName = msg.tool || (msg.tools && msg.tools[0]) || 'unknown';
      const timestamp = msg.created_at || msg.timestamp || '';
      const textContent = msg.content || '';

      card.innerHTML = `
        <div class="card-header">
          <span class="badge badge-purple">${escapeHtml(toolName)} · ${escapeHtml(msg.role || 'turn')}</span>
          <span class="card-meta" style="margin-bottom:0;">${escapeHtml(timestamp)}</span>
        </div>
        <div class="card-body" style="white-space: pre-wrap; max-height: 140px; overflow-y: auto; font-family: var(--font-mono); font-size: 12px; background: var(--bg-tertiary); padding: 8px 10px; border-radius: 6px;">
${escapeHtml(textContent)}
        </div>
        <div class="card-recommendation recommended-clean" style="margin-top: 8px; padding: 6px 10px;">
          <div class="card-actions" style="justify-content: space-between; align-items: center;">
            <span style="font-size: 11.5px; color: var(--text-secondary);">Message ID: <code>${msg.id}</code></span>
            <button class="btn btn-primary btn-sm btn-expand" data-id="${msg.id}">🔍 Expand Context (±3 Turns)</button>
          </div>
        </div>
      `;

      card.querySelector('.btn-expand').addEventListener('click', () => expandContext(msg.id));
      container.appendChild(card);
    });
  } catch (err) {
    container.innerHTML = `<div class="card"><p style="color:var(--red);">Search error: ${err.message}</p></div>`;
  }
}

async function expandContext(msgId) {
  const contentEl = document.getElementById('expand-content');
  contentEl.innerHTML = '<p>Loading surrounding conversation turns...</p>';
  openModal('modal-expand');

  try {
    const res = await fetch(`/api/expand?id=${msgId}&window=3`);
    const data = await res.json();
    contentEl.innerHTML = '';

    const turns = data.turns || [];
    if (turns.length === 0) {
      contentEl.innerHTML = '<p class="card-meta">No context turns found for this message.</p>';
      return;
    }

    turns.forEach(t => {
      const turnEl = document.createElement('div');
      turnEl.className = `transcript-turn ${t.is_target ? 'highlight' : ''}`;
      turnEl.innerHTML = `
        <div class="role">${escapeHtml(t.tool || 'tool')} · ${escapeHtml(t.role)} ${t.is_target ? '<span class="badge badge-green" style="font-size:10px; margin-left:6px;">Target Message</span>' : ''} <span style="float:right; color:var(--text-secondary); font-size:11px;">${escapeHtml(t.created_at || '')}</span></div>
        <div style="white-space: pre-wrap; font-family: var(--font-mono); font-size: 12px; margin-top: 6px;">${escapeHtml(t.content)}</div>
      `;
      contentEl.appendChild(turnEl);
    });
  } catch (err) {
    contentEl.innerHTML = `<p style="color:var(--red);">Expand failed: ${err.message}</p>`;
  }
}

// --- Guarded Confirmation Modal Helper ---
function guardedConfirm(title, message, onConfirm) {
  document.getElementById('modal-confirm-title').textContent = title;
  document.getElementById('modal-confirm-body').textContent = message;
  appState.pendingConfirm = onConfirm;
  openModal('modal-confirm');
}

function initModals() {
  document.querySelectorAll('.modal-close').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.target.closest('.modal-overlay').classList.remove('open');
    });
  });

  document.getElementById('modal-confirm-cancel').addEventListener('click', () => {
    closeModal('modal-confirm');
    appState.pendingConfirm = null;
  });

  document.getElementById('modal-confirm-submit').addEventListener('click', async () => {
    if (appState.pendingConfirm) {
      const fn = appState.pendingConfirm;
      appState.pendingConfirm = null;
      closeModal('modal-confirm');
      await fn();
    }
  });
}

function openModal(id) {
  document.getElementById(id).classList.add('open');
}

function closeModal(id) {
  document.getElementById(id).classList.remove('open');
}

function escapeHtml(str) {
  if (!str) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}
