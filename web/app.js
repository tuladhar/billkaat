/* billkaat frontend — vanilla JS, no dependencies, works offline. */
"use strict";

const $ = (id) => document.getElementById(id);
const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

const state = {
  meta: null,
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
  locked:  () => `<div class="chip pro">pro</div>`,
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
        : "pro — one-time license, free updates";
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
    const clickable = m.locked || fs.length > 0;
    const err = st.status === "error"
      ? `<div class="check-err">${esc(st.error)}</div>` : "";

    rows.push(`
      <div class="check-row ${m.locked ? "locked" : ""} ${clickable ? "clickable" : ""}"
           data-check="${esc(m.id)}" data-locked="${m.locked ? 1 : 0}"
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
  const row = e.target.closest(".check-row");
  if (!row) return;
  if (row.dataset.locked === "1") { openLicenseModal(); return; }
  toggleFindings(row.dataset.check);
});
$("checks").addEventListener("keydown", (e) => {
  if (e.key !== "Enter" && e.key !== " ") return;
  const row = e.target.closest(".check-row");
  if (!row) return;
  e.preventDefault();
  if (row.dataset.locked === "1") { openLicenseModal(); return; }
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
      body: JSON.stringify({ region: $("region").value }),
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

  const acct = detail.scan.account_id ? ` · account ${detail.scan.account_id}` : "";
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
    return `
      <div class="history-row" data-id="${s.id}" role="button" tabindex="0">
        <span class="history-when">${esc(when)}</span>
        <span class="history-meta">${esc(s.region)}${s.account_id ? " · " + esc(s.account_id) : ""} · ${s.findings_count} findings</span>
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
  try {
    const id = await api(`/api/identity?region=${encodeURIComponent($("region").value)}`);
    $("identity").textContent = `account ${id.account}`;
    $("identity").title = id.arn;
  } catch {
    $("identity").textContent = "aws credentials not detected";
  }
}

/* ---------- license ---------- */

function renderLicenseBadge() {
  const lic = state.meta.license || {};
  const badge = $("license-badge");
  if (lic.valid) {
    badge.textContent = state.meta.pro_build ? "PRO" : "PRO LICENSE";
    badge.classList.add("pro");
  } else {
    badge.textContent = state.meta.edition.toUpperCase();
    badge.classList.remove("pro");
  }
  const lockedCount = state.meta.checks.filter((c) => c.locked).length;
  $("pro-count").textContent = lockedCount;
  $("pro-cta").hidden = lic.valid || lockedCount === 0;
}

function openLicenseModal() {
  $("license-error").hidden = true;
  $("license-ok").hidden = true;
  $("license-modal").hidden = false;
  $("license-key").focus();
}
function closeLicenseModal() { $("license-modal").hidden = true; }

$("license-badge").addEventListener("click", openLicenseModal);
$("pro-unlock").addEventListener("click", openLicenseModal);
$("lic-cancel").addEventListener("click", closeLicenseModal);
$("license-modal").addEventListener("click", (e) => {
  if (e.target === $("license-modal")) closeLicenseModal();
});

$("lic-activate").addEventListener("click", async () => {
  $("license-error").hidden = true;
  $("license-ok").hidden = true;
  try {
    const res = await api("/api/license", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key: $("license-key").value }),
    });
    const ok = $("license-ok");
    ok.textContent = res.pro_build
      ? `License valid — welcome, ${res.license.name || res.license.email}. Pro checks are unlocked.`
      : `License valid for ${res.license.email}. You're on the Community build — ` +
        `download the Pro binary from your purchase page to run the locked checks.`;
    ok.hidden = false;
    await loadMeta();
  } catch (err) {
    const el = $("license-error");
    el.textContent = err.message;
    el.hidden = false;
  }
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
  renderLicenseBadge();
  renderChecks(null);
}

(async function boot() {
  await loadMeta();
  $("region").addEventListener("change", loadIdentity);
  loadIdentity();
  const scans = await loadHistory();
  if (scans && scans.length) {
    // resume a running scan, or show the latest completed one
    attachToScan(scans[0].id);
  }
})();
