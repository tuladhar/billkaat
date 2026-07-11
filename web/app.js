/* billkaat frontend — vanilla JS, no dependencies, works offline. */
"use strict";

const $ = (id) => document.getElementById(id);
const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

const state = {
  meta: null,
  accounts: [],
  authMode: null,
  scanId: null,
  pollTimer: null,
  openFindings: new Set(), // check ids whose findings are expanded
  meterShown: 0,
  meterTarget: 0,
  meterRAF: null,
};

/* ---------- helpers ---------- */

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

function money(v, decimals = 2) {
  return "$" + Number(v || 0).toLocaleString("en-US", {
    minimumFractionDigits: decimals, maximumFractionDigits: decimals,
  });
}

async function api(path, opts) {
  const res = await fetch(path, opts);
  const body = await res.json().catch(() => ({}));
  if (res.status === 401 && !path.startsWith("/api/auth/")) {
    showAuth("login");
  }
  if (!res.ok) throw new Error(body.error || res.statusText);
  return body;
}

/* ---------- savings meter (the signature) ---------- */

function setMeter(target) {
  state.meterTarget = target;
  const el = $("meter");
  const apply = (v) => {
    el.textContent = money(v, v >= 1000 ? 0 : 2);
    el.parentElement.classList.toggle("has-savings", state.meterTarget > 0);
  };
  if (reducedMotion) { state.meterShown = target; apply(target); return; }
  if (state.meterRAF) cancelAnimationFrame(state.meterRAF);
  const step = () => {
    const diff = state.meterTarget - state.meterShown;
    if (Math.abs(diff) < 0.5) {
      state.meterShown = state.meterTarget;
      apply(state.meterShown);
      state.meterRAF = null;
      return;
    }
    state.meterShown += diff * 0.12;
    apply(state.meterShown);
    state.meterRAF = requestAnimationFrame(step);
  };
  state.meterRAF = requestAnimationFrame(step);
}

/* ---------- checks ledger ---------- */

const CHIP = {
  pending: () => `<div class="chip">·</div>`,
  running: () => `<div class="chip running"><span class="spinner"></span>run</div>`,
  passed:  () => `<div class="chip passed">pass</div>`,
  flagged: (n) => `<div class="chip flagged">${n} found</div>`,
  locked:  () => `<div class="chip soon">soon</div>`,
  error:   () => `<div class="chip error">err</div>`,
};

function renderChecks(detail) {
  const byId = {};
  const findingsByCheck = {};
  if (detail) {
    for (const c of detail.checks) byId[c.check_id] = c;
    for (const f of detail.findings) {
      (findingsByCheck[f.check_id] ||= []).push(f);
    }
  }

  const rows = [];
  let lastTier = null;
  for (const m of state.meta.checks) {
    if (m.tier !== lastTier) {
      lastTier = m.tier;
      const label = m.tier === "free"
        ? "included in every build"
        : "coming in a future update";
      rows.push(`<div class="tier-rule microlabel">${label}</div>`);
    }
    const st = byId[m.id] || { status: m.locked ? "locked" : "idle" };
    const chip =
      m.locked ? CHIP.locked()
      : st.status === "running" ? CHIP.running()
      : st.status === "passed" ? CHIP.passed()
      : st.status === "flagged" ? CHIP.flagged(st.findings_count)
      : st.status === "error" ? CHIP.error()
      : st.status === "pending" ? CHIP.pending()
      : `<div class="chip">—</div>`;

    const savings = st.savings > 0
      ? `<div class="check-savings">${money(st.savings)}</div>`
      : `<div class="check-savings zero">${m.locked ? "◇" : "—"}</div>`;

    const fs = findingsByCheck[m.id] || [];
    const clickable = fs.length > 0;
    const err = st.status === "error"
      ? `<div class="check-err">${esc(st.error)}</div>` : "";

    rows.push(`
      <div class="check-row ${m.locked ? "locked" : ""} ${clickable ? "clickable" : ""}"
           data-check="${esc(m.id)}"
           role="${clickable ? "button" : ""}" ${clickable ? 'tabindex="0"' : ""}>
        ${chip}
        <div class="check-main">
          <div class="check-name">${esc(m.name)}</div>
          <div class="check-desc">${esc(m.description)}</div>
          ${err}
        </div>
        ${savings}
      </div>`);

    if (fs.length && state.openFindings.has(m.id)) {
      rows.push(`<div class="findings">` + fs.map(renderFinding).join("") + `</div>`);
    }
  }
  $("checks").innerHTML = rows.join("");

  const free = state.meta.checks.filter((c) => c.tier === "free").length;
  $("ledger-count").textContent =
    `${free} free / ${state.meta.checks.length} total`;
}

function renderFinding(f) {
  return `
    <div class="finding">
      <div>
        <span class="sev sev-${esc(f.severity)}">${esc(f.severity)}</span>
        <span class="finding-res">${esc(f.resource_id)}</span>
        <div class="finding-title">${esc(f.title)}</div>
        <div class="finding-detail">${esc(f.detail)}</div>
        <div class="finding-rec">${esc(f.recommendation)}</div>
      </div>
      <div class="finding-savings">${f.monthly_savings_usd > 0 ? money(f.monthly_savings_usd) + "/mo" : ""}</div>
    </div>`;
}

$("checks").addEventListener("click", (e) => {
  const row = e.target.closest(".check-row.clickable");
  if (!row) return;
  toggleFindings(row.dataset.check);
});
$("checks").addEventListener("keydown", (e) => {
  if (e.key !== "Enter" && e.key !== " ") return;
  const row = e.target.closest(".check-row.clickable");
  if (!row) return;
  e.preventDefault();
  toggleFindings(row.dataset.check);
});

function toggleFindings(checkId) {
  if (state.openFindings.has(checkId)) state.openFindings.delete(checkId);
  else state.openFindings.add(checkId);
  refreshScan();
}

/* ---------- scanning ---------- */

async function startScan() {
  $("scan-error").hidden = true;
  $("run").disabled = true;
  try {
    const body = await api("/api/scan", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        region: $("region").value,
        account_id: Number($("account").value),
      }),
    });
    attachToScan(body.scan_id);
  } catch (err) {
    // 409 returns the running scan id — just attach to it.
    $("run").disabled = false;
    showScanError(err.message);
  }
}

function attachToScan(id) {
  state.scanId = id;
  state.openFindings = new Set();
  $("run").disabled = true;
  $("export").hidden = true;
  $("meter-sub").textContent = "Scanning…";
  setMeter(0);
  if (state.pollTimer) clearInterval(state.pollTimer);
  state.pollTimer = setInterval(refreshScan, 1200);
  refreshScan();
}

async function refreshScan() {
  if (!state.scanId) return;
  let detail;
  try {
    detail = await api(`/api/scan/${state.scanId}`);
  } catch { return; }

  renderChecks(detail);

  const running = detail.scan.status === "running";
  const liveTotal = detail.checks.reduce((s, c) => s + (c.savings || 0), 0);
  setMeter(running ? liveTotal : detail.scan.total_savings);

  if (running) {
    const done = detail.checks.filter((c) =>
      ["passed", "flagged", "error", "locked"].includes(c.status)).length;
    $("meter-sub").textContent =
      `Scanning ${detail.scan.region} — ${done}/${detail.checks.length} checks complete`;
    return;
  }

  // finished
  if (state.pollTimer) { clearInterval(state.pollTimer); state.pollTimer = null; }
  $("run").disabled = false;

  if (detail.scan.status === "failed") {
    $("meter-sub").textContent = "Scan failed.";
    showScanError(detail.scan.error);
    return;
  }

  const acct = detail.scan.account_label
    ? ` · ${detail.scan.account_label}${detail.scan.account_id ? " (" + detail.scan.account_id + ")" : ""}`
    : detail.scan.account_id ? ` · account ${detail.scan.account_id}` : "";
  $("meter-sub").textContent =
    `${detail.scan.findings_count} findings in ${detail.scan.region}${acct} — click a row for details.`;
  $("export").hidden = detail.scan.findings_count === 0;
  loadHistory();
}

function showScanError(msg) {
  const el = $("scan-error");
  el.textContent = msg;
  el.hidden = false;
}

$("run").addEventListener("click", startScan);
$("export").addEventListener("click", () => {
  if (state.scanId) window.location = `/api/scan/${state.scanId}/export.csv`;
});

/* ---------- history ---------- */

async function loadHistory() {
  let scans;
  try { scans = await api("/api/scans"); } catch { return; }
  if (!scans.length) {
    $("history").innerHTML = `<div class="history-empty">No scans yet. Your first one takes under a minute.</div>`;
    return null;
  }
  $("history").innerHTML = scans.map((s) => {
    const when = new Date(s.started_at).toLocaleString();
    const right = s.status === "failed"
      ? `<span class="history-failed">failed</span>`
      : s.status === "running"
        ? `<span class="history-meta">running…</span>`
        : `<span class="history-savings">${money(s.total_savings)}/mo</span>`;
    const label = s.account_label ? esc(s.account_label) : (s.account_id ? esc(s.account_id) : "");
    return `
      <div class="history-row" data-id="${s.id}" role="button" tabindex="0">
        <span class="history-when">${esc(when)}</span>
        <span class="history-meta">${esc(s.region)}${label ? " · " + label : ""} · ${s.findings_count} findings</span>
        ${right}
      </div>`;
  }).join("");
  return scans;
}

$("history").addEventListener("click", (e) => {
  const row = e.target.closest(".history-row");
  if (row) attachToScan(Number(row.dataset.id));
});
$("history").addEventListener("keydown", (e) => {
  if (e.key !== "Enter") return;
  const row = e.target.closest(".history-row");
  if (row) attachToScan(Number(row.dataset.id));
});

/* ---------- identity ---------- */

async function loadIdentity() {
  const accountId = $("account").value;
  if (!accountId) { $("identity").textContent = ""; return; }
  try {
    const id = await api(`/api/identity?region=${encodeURIComponent($("region").value)}` +
      `&account=${encodeURIComponent(accountId)}`);
    $("identity").textContent = `account ${id.account}`;
    $("identity").title = id.arn;
  } catch {
    $("identity").textContent = "could not verify credentials for this account";
  }
}

/* ---------- AWS accounts ---------- */

function renderAccountPicker() {
  const sel = $("account");
  const prev = sel.value;
  sel.innerHTML = "";
  if (!state.accounts.length) {
    const o = document.createElement("option");
    o.value = "";
    o.textContent = "no accounts added yet";
    sel.appendChild(o);
    sel.disabled = true;
    $("run").disabled = true;
    return;
  }
  sel.disabled = false;
  for (const a of state.accounts) {
    const o = document.createElement("option");
    o.value = a.id;
    o.textContent = a.name + (a.account_id ? ` (${a.account_id})` : "");
    sel.appendChild(o);
  }
  sel.value = state.accounts.some((a) => String(a.id) === prev) ? prev : state.accounts[0].id;
  $("run").disabled = false;
}

async function loadAccounts() {
  state.accounts = await api("/api/accounts");
  renderAccountPicker();
}

function renderAccountsList() {
  const el = $("accounts-list");
  if (!state.accounts.length) {
    el.innerHTML = `<div class="history-empty">No AWS accounts yet — add one below.</div>`;
    return;
  }
  el.innerHTML = state.accounts.map((a) => `
    <div class="account-row" data-id="${a.id}">
      <div>
        <div class="check-name">${esc(a.name)}</div>
        <div class="check-desc">${esc(a.account_id || "—")} · key ${esc(a.access_key_id)}</div>
      </div>
      <button class="btn btn-ghost btn-small acct-delete" type="button" data-id="${a.id}">Remove</button>
    </div>`).join("");
}

async function openAccountsModal() {
  $("acct-error").hidden = true;
  $("acct-name").value = "";
  $("acct-account-id").value = "";
  $("acct-access-key").value = "";
  $("acct-secret-key").value = "";
  renderAccountsList();
  if (!$("iam-policy-text").textContent) {
    try {
      const res = await api("/api/iam-policy");
      $("iam-policy-text").textContent = res.policy;
    } catch { /* shown blank, non-fatal */ }
  }
  $("accounts-modal").hidden = false;
}
function closeAccountsModal() { $("accounts-modal").hidden = true; }

$("manage-accounts").addEventListener("click", openAccountsModal);
$("acct-cancel").addEventListener("click", closeAccountsModal);
$("accounts-modal").addEventListener("click", (e) => {
  if (e.target === $("accounts-modal")) closeAccountsModal();
});

$("accounts-list").addEventListener("click", async (e) => {
  const btn = e.target.closest(".acct-delete");
  if (!btn) return;
  try {
    await api(`/api/accounts/${btn.dataset.id}`, { method: "DELETE" });
    await loadAccounts();
    renderAccountsList();
    loadIdentity();
  } catch (err) {
    const el = $("acct-error");
    el.textContent = err.message;
    el.hidden = false;
  }
});

$("copy-policy").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText($("iam-policy-text").textContent);
    const btn = $("copy-policy");
    const prev = btn.textContent;
    btn.textContent = "Copied";
    setTimeout(() => { btn.textContent = prev; }, 1200);
  } catch { /* clipboard permission denied — nothing to fall back to here */ }
});

$("acct-add").addEventListener("click", async () => {
  $("acct-error").hidden = true;
  try {
    await api("/api/accounts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: $("acct-name").value,
        account_id: $("acct-account-id").value,
        access_key_id: $("acct-access-key").value,
        secret_access_key: $("acct-secret-key").value,
      }),
    });
    await loadAccounts();
    renderAccountsList();
    $("acct-name").value = "";
    $("acct-account-id").value = "";
    $("acct-access-key").value = "";
    $("acct-secret-key").value = "";
    loadIdentity();
  } catch (err) {
    const el = $("acct-error");
    el.textContent = err.message;
    el.hidden = false;
  }
});

/* ---------- auth ---------- */

function showAuth(mode) {
  state.authMode = mode;
  $("app-view").hidden = true;
  $("auth-view").hidden = false;
  const setup = mode === "setup";
  $("auth-title").textContent = setup ? "Create your login" : "Log in";
  $("auth-sub").hidden = !setup;
  $("auth-confirm-label").hidden = !setup;
  $("auth-confirm").hidden = !setup;
  $("auth-submit").textContent = setup ? "Create login" : "Log in";
  $("auth-error").hidden = true;
  $("auth-password").value = "";
  $("auth-confirm").value = "";
}

async function submitAuth() {
  $("auth-error").hidden = true;
  const username = $("auth-username").value.trim();
  const password = $("auth-password").value;
  if (state.authMode === "setup" && password !== $("auth-confirm").value) {
    $("auth-error").textContent = "passwords don't match";
    $("auth-error").hidden = false;
    return;
  }
  const path = state.authMode === "setup" ? "/api/auth/setup" : "/api/auth/login";
  try {
    await api(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    $("auth-view").hidden = true;
    await startApp();
  } catch (err) {
    $("auth-error").textContent = err.message;
    $("auth-error").hidden = false;
  }
}

$("auth-submit").addEventListener("click", submitAuth);
$("auth-confirm").addEventListener("keydown", (e) => { if (e.key === "Enter") submitAuth(); });
$("auth-password").addEventListener("keydown", (e) => { if (e.key === "Enter" && state.authMode !== "setup") submitAuth(); });

$("logout").addEventListener("click", async () => {
  try { await api("/api/auth/logout", { method: "POST" }); } catch { /* logging out anyway */ }
  location.reload();
});

/* ---------- boot ---------- */

async function loadMeta() {
  state.meta = await api("/api/meta");
  const sel = $("region");
  if (!sel.options.length) {
    for (const r of state.meta.regions) {
      const o = document.createElement("option");
      o.value = o.textContent = r;
      sel.appendChild(o);
    }
    sel.value = state.meta.default_region;
  }
  renderChecks(null);
}

async function startApp() {
  $("app-view").hidden = false;
  await loadMeta();
  await loadAccounts();
  $("region").addEventListener("change", loadIdentity);
  $("account").addEventListener("change", loadIdentity);
  loadIdentity();
  const scans = await loadHistory();
  if (scans && scans.length) {
    // resume a running scan, or show the latest completed one
    attachToScan(scans[0].id);
  }
}

(async function boot() {
  const status = await api("/api/auth/status");
  if (status.setup_required) { showAuth("setup"); return; }
  if (!status.authenticated) { showAuth("login"); return; }
  await startApp();
})();
