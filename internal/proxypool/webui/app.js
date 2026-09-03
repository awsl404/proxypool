const $ = (id) => document.getElementById(id);

const titles = {
  pool: ["代理池", "查看代理状态、匿名性、延迟和来源。"],
  status: ["系统概览", "查看代理质量、出口请求、后台任务和维护状态。"],
  tasks: ["任务中心", "集中查看检测、采集、自定义抓取、调度和维护任务。"],
  import: ["批量导入", "从文本或文件导入 HTTP、HTTPS、SOCKS5 代理。"],
  collector: ["免费代理采集", "选择请求出口，抓取公开列表并验证可用性。"],
  dead: ["失效档案", "查看达到失败阈值后移出代理池的记录。"],
  custom: ["自定义抓取", "配置不同代理网站的页码和提取规则。"],
  settings: ["设置", "调整检测、采集、认证和对外服务策略。"],
  logs: ["日志", "查看运行日志、错误和关键操作记录。"],
};

const statusText = { available: "可用", unavailable: "不可用", unchecked: "未检测" };
const anonText = { anonymous: "匿名", transparent: "透明", unknown: "未知" };
const reachText = { both: "双向", domestic: "仅国内", foreign: "仅海外", none: "均不可用", unknown: "未知" };
const targetText = { both: "HTTP/HTTPS", http: "仅 HTTP", https: "仅 HTTPS", none: "均不可达", unknown: "未知" };
const boolText = { true: "启用", false: "关闭" };
const protocolOrder = ["http", "https", "socks5"];
const pageState = {
  proxy: { page: 1, pageSize: 20, total: 0, totalPages: 1 },
  source: { page: 1, pageSize: 20, total: 0, totalPages: 1 },
  dead: { page: 1, pageSize: 20, total: 0, totalPages: 1 },
  custom: { page: 1, pageSize: 20, total: 0, totalPages: 1 },
};
let activeCollectorJob = "";
let activeCustomJob = "";
let activeCustomTemplateID = 0;
let activeCustomSection = "basic";
let activeTab = "pool";
let settingsLoaded = false;
let healthPollToken = 0;
const debounceTimers = {};
const loadSeq = { proxy: 0, source: 0, dead: 0, custom: 0, logs: 0, tasks: 0 };
const defaultCustomExtractPattern = String.raw`(?:(?P<scheme>https?|socks5)://)?(?:(?P<user>[^:@\s/]+)(?::(?P<password>[^@\s/]*))?@)?(?P<host>(?:\d{1,3}\.){3}\d{1,3}|[A-Za-z0-9.-]+)\s*[:：]\s*(?P<port>\d{2,5})`;
const sampleImportText = [
  "http://127.0.0.1:8080",
  "socks5://127.0.0.1:18081",
  "http,127.0.0.1,18080,,",
  "socks5 127.0.0.1:18081",
].join("\n");

async function api(path, opts = {}) {
  opts.headers = opts.headers || {};
  if (opts.body && !(opts.body instanceof FormData)) {
    opts.headers["Content-Type"] = "application/json";
  }
  const res = await fetch(path, opts);
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) {
    throw new Error(data.error || res.statusText);
  }
  return data;
}

function showLogin() {
  $("loginUser").value = "";
  $("loginPass").value = "";
  $("loginView").hidden = false;
  $("appView").hidden = true;
  $("loginUser").focus();
}

function showApp() {
  $("loginView").hidden = true;
  $("appView").hidden = false;
  loadAll();
}

function setTab(name) {
  activeTab = name;
  document.querySelectorAll(".nav button").forEach((button) => {
    button.classList.toggle("active", button.dataset.tab === name);
  });
  Object.keys(titles).forEach((tab) => {
    $("tab-" + tab).hidden = tab !== name;
  });
  $("pageTitle").textContent = titles[name][0];
  $("pageSub").textContent = titles[name][1];
  loadAll().catch(showError);
}

async function boot() {
  bindEvents();
  try {
    await api("/api/me");
    showApp();
  } catch {
    showLogin();
  }
}

function bindEvents() {
  $("loginBtn").addEventListener("click", login);
  $("logoutBtn").addEventListener("click", logout);
  $("refreshBtn").addEventListener("click", loadAll);
  $("refreshStatusBtn").addEventListener("click", loadStatus);
  $("refreshTasksBtn").addEventListener("click", loadTasks);
  $("runMaintenanceBtn").addEventListener("click", runMaintenance);
  $("checkAllBtn").addEventListener("click", checkAllProxies);
  $("cleanupBtn").addEventListener("click", cleanupProxies);
  $("deleteFilteredBtn").addEventListener("click", deleteFilteredProxies);
  $("exportBtn").addEventListener("click", exportProxies);
  $("refreshSourcesBtn").addEventListener("click", loadSourceStats);
  $("clearSourcesBtn").addEventListener("click", clearSourceStats);
  $("refreshDeadBtn").addEventListener("click", loadDeadProxies);
  $("clearDeadBtn").addEventListener("click", clearDeadProxies);
  $("refreshLogsBtn").addEventListener("click", loadLogs);
  $("clearLogsBtn").addEventListener("click", clearLogs);
  $("sampleImportBtn").addEventListener("click", fillSampleImport);
  $("localSourceBtn").addEventListener("click", useLocalSources);
  $("addCollectSourceBtn").addEventListener("click", () => addCollectorSourceRow());
  $("syncCollectSourcesBtn").addEventListener("click", () => renderCollectorSourceEditor($("collectSources").value));
  $("collectSources").addEventListener("change", () => renderCollectorSourceEditor($("collectSources").value));
  $("importBtn").addEventListener("click", importProxies);
  $("collectBtn").addEventListener("click", collectProxies);
  $("collectStopBtn").addEventListener("click", () => stopCollectorJob(activeCollectorJob, "collectMsg"));
  $("customSampleBtn").addEventListener("click", loadCustomSample);
  $("customNewBtn").addEventListener("click", clearCustomForm);
  $("customSaveBtn").addEventListener("click", saveCustomTemplate);
  $("customPreviewBtn").addEventListener("click", previewCustomTemplate);
  $("customRunBtn").addEventListener("click", runCustomTemplate);
  $("customStopBtn").addEventListener("click", () => stopCollectorJob(activeCustomJob, "customMsg"));
  bindCustomSections();
  ["customName", "customEnabled"].forEach((id) => {
    $(id).addEventListener(id === "customName" ? "input" : "change", updateCustomCurrent);
  });
  $("saveSettingsBtn").addEventListener("click", saveSettings);
  $("closeProxyDrawerBtn").addEventListener("click", closeProxyDrawer);
  $("proxyDrawerBackdrop").addEventListener("click", closeProxyDrawer);
  bindPager("proxy", loadProxies);
  bindPager("source", loadSourceStats);
  bindPager("dead", loadDeadProxies);
  bindPager("custom", loadCustomTemplates);
  document.querySelectorAll("[data-custom-scheme]").forEach((input) => {
    input.addEventListener("change", ensureCustomSchemeSelection);
  });
  document.querySelectorAll(".nav button").forEach((button) => {
    button.addEventListener("click", () => setTab(button.dataset.tab));
  });
  ["q", "scheme", "status", "anonymity", "reachability", "targetCapability"].forEach((id) => {
    $(id).addEventListener(id === "q" ? "input" : "change", () => {
      resetPage("proxy");
      debounce("proxyFilter", () => loadProxies().catch(showError), id === "q" ? 250 : 0);
    });
  });
  ["sourceQuery", "sourceFailed"].forEach((id) => {
    $(id).addEventListener(id === "sourceQuery" ? "input" : "change", () => {
      resetPage("source");
      debounce("sourceFilter", () => loadSourceStats().catch(showError), id === "sourceQuery" ? 250 : 0);
    });
  });
  ["deadQuery", "deadDisabled"].forEach((id) => {
    $(id).addEventListener(id === "deadQuery" ? "input" : "change", () => {
      resetPage("dead");
      debounce("deadFilter", () => loadDeadProxies().catch(showError), id === "deadQuery" ? 250 : 0);
    });
  });
  ["logQuery", "logLevel", "logLines"].forEach((id) => {
    $(id).addEventListener(id === "logQuery" ? "input" : "change", () => {
      debounce("logFilter", () => loadLogs().catch(showError), id === "logQuery" ? 250 : 0);
    });
  });
}

function debounce(key, fn, delay = 250) {
  clearTimeout(debounceTimers[key]);
  debounceTimers[key] = setTimeout(fn, delay);
}

async function login() {
  $("loginMsg").textContent = "";
  try {
    await api("/api/login", {
      method: "POST",
      body: JSON.stringify({ username: $("loginUser").value, password: $("loginPass").value }),
    });
    showApp();
  } catch (err) {
    $("loginMsg").textContent = err.message;
  }
}

async function logout() {
  await api("/api/logout", { method: "POST" }).catch(() => {});
  settingsLoaded = false;
  showLogin();
}

async function loadAll() {
  await loadStats();
  if (activeTab !== "settings") {
    await loadSettingsOnce();
  }
  await loadActiveTab();
}

async function loadSettingsOnce() {
  if (settingsLoaded) return;
  await loadSettings();
}

async function loadActiveTab() {
  switch (activeTab) {
    case "status":
      await loadStatus();
      return;
    case "tasks":
      await loadTasks();
      return;
    case "pool":
      await loadProxies();
      return;
    case "collector":
      await loadSourceStats();
      return;
    case "dead":
      await loadDeadProxies();
      return;
    case "custom":
      await loadCustomTemplates();
      return;
    case "settings":
      await loadSettings();
      return;
    case "logs":
      await loadLogs();
      return;
    default:
      return;
  }
}

async function loadStats() {
  const s = await api("/api/stats");
  $("stTotal").textContent = s.total;
  $("stAvail").textContent = s.available;
  $("stBad").textContent = s.unavailable;
  $("stAnon").textContent = s.anonymous;
  $("stTrans").textContent = s.transparent;
  $("stUnknown").textContent = s.unknown;
  $("stDelay").textContent = s.average_delay + "ms";
  $("stScore").textContent = s.average_score || 0;
  $("stHigh").textContent = s.high_quality || 0;
}

async function loadStatus() {
  const box = $("statusCards");
  if (!box) return;
  try {
    const s = await api("/api/status");
    const scheduler = s.scheduler || {};
    const healthScheduler = s.health_scheduler || {};
    const maintenance = s.maintenance || {};
    const outbound = s.outbound || {};
    box.innerHTML = [
      statusCard("平均质量", s.proxy?.average_score || 0, `高质量 ${s.proxy?.high_quality || 0} 个`),
      statusCard("出口请求", outbound.total_dial || 0, `成功 ${outbound.ok_dial || 0} / 失败 ${outbound.fail_dial || 0}`),
      statusCard("检测任务", healthScheduler.running ? "运行中" : boolText[String(!!healthScheduler.enabled)], healthSchedulerSummary(healthScheduler)),
      statusCard("采集任务", s.running_jobs || 0, `任务记录 ${s.stored_jobs || 0}`),
      statusCard("数据库", formatBytes(s.db_size_bytes || 0), `日志 ${formatBytes(s.log_size_bytes || 0)}`),
      statusCard("采集源", s.source_total || 0, `失败来源 ${s.source_failing || 0}`),
      statusCard("自定义模板", s.custom_total || 0, `启用 ${s.custom_enabled || 0}`),
      statusCard("定时采集", scheduler.running ? "运行中" : boolText[String(!!scheduler.enabled)], schedulerSummary(scheduler)),
      statusCard("数据维护", maintenance.running ? "运行中" : boolText[String(!!maintenance.enabled)], maintenanceSummary(maintenance)),
    ].join("");
    $("statusMsg").textContent = statusDetail(s);
  } catch (err) {
    $("statusMsg").textContent = err.message;
  }
}

function statusCard(title, value, desc) {
  return `<article class="status-card"><span>${escapeHTML(title)}</span><strong>${escapeHTML(value)}</strong><em>${escapeHTML(desc || "")}</em></article>`;
}

async function loadTasks() {
  const seq = ++loadSeq.tasks;
  const cards = $("taskCards");
  const tbody = $("taskRows");
  if (!cards || !tbody) return;
  try {
    const data = await api("/api/tasks");
    if (seq !== loadSeq.tasks) return;
    const health = data.health_scheduler || {};
    const collector = data.collector_scheduler || {};
    const maintenance = data.maintenance || {};
    cards.innerHTML = [
      statusCard("代理池检测", health.running ? "运行中" : boolText[String(!!health.enabled)], healthSchedulerSummary(health)),
      statusCard("定时采集", collector.running ? "运行中" : boolText[String(!!collector.enabled)], schedulerSummary(collector)),
      statusCard("数据维护", maintenance.running ? "运行中" : boolText[String(!!maintenance.enabled)], maintenanceSummary(maintenance)),
      statusCard("采集任务记录", (data.collector_jobs || []).length, `更新时间 ${formatTime(data.generated_at)}`),
    ].join("");
    renderTaskRows(data.collector_jobs || []);
    $("taskMsg").textContent = taskSummary(data);
  } catch (err) {
    $("taskMsg").textContent = err.message;
  }
}

function renderTaskRows(jobs) {
  const tbody = $("taskRows");
  if (!jobs.length) {
    tbody.innerHTML = `<tr><td colspan="6" class="empty">暂无采集任务记录</td></tr>`;
    return;
  }
  tbody.innerHTML = jobs.map((job) => {
    const result = job.result || {};
    const summary = job.status === "running" || job.status === "stopping"
      ? `日志 ${job.logs?.length || 0} 行`
      : `导入 ${result.imported || 0}，重复 ${result.duplicates || 0}，验证 ${result.checked || 0}，失败 ${result.failed || 0}`;
    return `<tr>
      <td class="mono" title="${escapeAttr(job.id)}">${escapeHTML(shortID(job.id))}</td>
      <td>${taskStatusBadge(job.status)}</td>
      <td>${formatTime(job.started_at)}</td>
      <td>${formatTime(job.finished_at)}</td>
      <td>${escapeHTML(summary)}</td>
      <td><button class="secondary" data-task-log="${escapeAttr(job.id)}">日志</button></td>
    </tr>`;
  }).join("");
  tbody.querySelectorAll("[data-task-log]").forEach((button) => {
    button.addEventListener("click", () => showTaskLog(button.dataset.taskLog));
  });
}

async function showTaskLog(id) {
  try {
    const job = await api(`/api/collector/jobs/${id}`);
    setOutputText($("taskMsg"), renderJobDone(job, taskTitle(job)), true);
  } catch (err) {
    $("taskMsg").textContent = err.message;
  }
}

function taskSummary(data) {
  const health = data.health_scheduler || {};
  const collector = data.collector_scheduler || {};
  const maintenance = data.maintenance || {};
  return [
    `生成时间：${formatTime(data.generated_at)}`,
    `代理池检测：${health.running ? "运行中" : boolText[String(!!health.enabled)]}，${healthSchedulerSummary(health)}`,
    `定时采集：${collector.running ? "运行中" : boolText[String(!!collector.enabled)]}，${schedulerSummary(collector)}`,
    `数据维护：${maintenance.running ? "运行中" : boolText[String(!!maintenance.enabled)]}，${maintenanceSummary(maintenance)}`,
  ].join("\n");
}

function taskTitle(job) {
  if (job.status === "running") return "任务运行中";
  if (job.status === "stopping") return "任务停止中";
  if (job.status === "stopped") return "任务已停止";
  return "任务完成";
}

function taskStatusBadge(status) {
  const label = { running: "运行中", stopping: "停止中", stopped: "已停止", done: "完成" }[status] || status || "未知";
  const cls = status === "done" ? "available" : status === "running" ? "unchecked" : status === "stopped" ? "disabled" : "unknown";
  return `<span class="badge ${cls}">${escapeHTML(label)}</span>`;
}

function shortID(id) {
  const value = String(id || "");
  return value.length > 12 ? `${value.slice(0, 8)}...${value.slice(-4)}` : value;
}

async function loadProxies() {
  const seq = ++loadSeq.proxy;
  const params = new URLSearchParams({
    q: $("q").value,
    scheme: $("scheme").value,
    status: $("status").value,
    anonymity: $("anonymity").value,
    reachability: $("reachability").value,
    target_capability: $("targetCapability").value,
    page: pageState.proxy.page,
    page_size: readPageSize("proxy"),
  });
  const data = await api("/api/proxies?" + params.toString());
  if (seq !== loadSeq.proxy) return;
  const rows = pageItems("proxy", data);
  if (await reloadIfPageOutOfRange("proxy", rows, loadProxies)) return;
  renderProxyRows(rows);
  renderPager("proxy");
}

function renderProxyRows(rows) {
  rows = asArray(rows);
  const tbody = $("proxyRows");
  if (!rows.length) {
    tbody.innerHTML = `<tr><td colspan="12" class="empty">暂无匹配代理</td></tr>`;
    return;
  }
  const offset = (Math.max(1, pageState.proxy.page) - 1) * readPageSize("proxy");
  tbody.innerHTML = rows.map((p, index) => {
    const enabledBadge = p.enabled ? "" : ` <span class="badge disabled">已禁用</span>`;
    const label = proxyLabel(p);
    return `<tr data-label="${escapeAttr(label)}">
      <td title="数据库 ID：${p.id}">${offset + index + 1}</td>
      <td class="mono">${escapeHTML(label)}</td>
      <td>${protocolBadges(p.tags || p.scheme)}</td>
      <td>${qualityBadge(p.quality_score || 0)}</td>
      <td><span class="badge ${p.status}">${statusText[p.status] || p.status}</span>${enabledBadge}</td>
      <td><span class="badge ${p.anonymity}">${anonText[p.anonymity] || p.anonymity}</span></td>
      <td>${reachabilityBadge(p.reachability)}</td>
      <td>${targetCapabilityBadge(p.target_capability)}</td>
      <td>${p.latency_ms}ms</td>
      <td>${p.fail_count}</td>
      <td>${escapeHTML(sourceLabel(p.source))}</td>
      <td><div class="actions">
        <button class="secondary" data-action="detail" data-id="${p.id}">详情</button>
        <button class="secondary" data-action="check" data-id="${p.id}">检测</button>
        <button class="secondary" data-action="toggle" data-id="${p.id}" data-enabled="${!p.enabled}">${p.enabled ? "禁用" : "启用"}</button>
        <button class="danger" data-action="delete" data-id="${p.id}">删除</button>
      </div></td>
    </tr>`;
  }).join("");
  tbody.querySelectorAll("button").forEach((button) => {
    button.addEventListener("click", () => handleProxyAction(button));
  });
}

async function handleProxyAction(button) {
  const id = button.dataset.id;
  const action = button.dataset.action;
  button.disabled = true;
  try {
    if (action === "detail") {
      await showProxyDetail(id);
      return;
    }
    if (action === "check") {
      await api(`/api/proxies/${id}/check`, { method: "POST" });
    }
    if (action === "toggle") {
      await api(`/api/proxies/${id}`, { method: "PATCH", body: JSON.stringify({ enabled: button.dataset.enabled === "true" }) });
    }
    if (action === "delete") {
      const label = button.closest("tr")?.dataset.label || id;
      if (!confirm(`删除代理 ${label}？`)) return;
      await api(`/api/proxies/${id}`, { method: "DELETE" });
    }
    await loadAll();
  } catch (err) {
    showError(err);
  } finally {
    button.disabled = false;
  }
}

async function showProxyDetail(id) {
  const p = await api(`/api/proxies/${id}`);
  const label = proxyLabel(p);
  $("proxyDrawerSub").textContent = label;
  $("proxyDrawerBody").innerHTML = proxyDetailHTML(p);
  $("proxyDrawer").hidden = false;
  $("proxyDrawerBackdrop").hidden = false;
  document.body.classList.add("drawer-open");
  const checkBtn = $("drawerCheckBtn");
  if (checkBtn) {
    checkBtn.addEventListener("click", async () => {
      checkBtn.disabled = true;
      try {
        await api(`/api/proxies/${p.id}/check`, { method: "POST" });
        await loadAll();
        await showProxyDetail(p.id);
      } catch (err) {
        alert(err.message);
      } finally {
        checkBtn.disabled = false;
      }
    });
  }
  const copyBtn = $("drawerCopyBtn");
  if (copyBtn) {
    copyBtn.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(label);
        copyBtn.textContent = "已复制";
      } catch {
        copyBtn.textContent = "复制失败";
      }
    });
  }
}

function closeProxyDrawer() {
  $("proxyDrawer").hidden = true;
  $("proxyDrawerBackdrop").hidden = true;
  document.body.classList.remove("drawer-open");
}

function proxyDetailHTML(p) {
  const parts = qualityBreakdown(p);
  return `
    <div class="drawer-actions">
      <button id="drawerCheckBtn" class="primary">立即检测</button>
      <button id="drawerCopyBtn" class="secondary">复制地址</button>
    </div>
    <div class="detail-grid">
      ${detailItem("数据库 ID", p.id)}
      ${detailItem("状态", statusText[p.status] || p.status)}
      ${detailItem("启用", p.enabled ? "启用" : "禁用")}
      ${detailItem("协议标签", (p.tags || p.scheme || "").toUpperCase())}
      ${detailItem("匿名性", anonText[p.anonymity] || p.anonymity)}
      ${detailItem("线路", reachText[p.reachability] || p.reachability)}
      ${detailItem("目标能力", targetText[p.target_capability] || p.target_capability)}
      ${detailItem("延迟", `${p.latency_ms || 0}ms`)}
      ${detailItem("成功/失败", `${p.success_count || 0} / ${p.fail_count || 0}`)}
      ${detailItem("最近检测", formatTime(p.last_checked))}
      ${detailItem("最近使用", formatTime(p.last_used))}
      ${detailItem("创建时间", formatTime(p.created_at))}
      ${detailItem("更新时间", formatTime(p.updated_at))}
    </div>
    <section class="detail-section">
      <h3>来源</h3>
      <p>${escapeHTML(p.source || "无")}</p>
    </section>
    <section class="detail-section">
      <h3>质量分 ${p.quality_score || 0}</h3>
      <div class="score-list">
        ${parts.map((part) => `<div><span>${escapeHTML(part.label)}</span><strong class="${part.value >= 0 ? "score-plus" : "score-minus"}">${part.value >= 0 ? "+" : ""}${part.value}</strong></div>`).join("")}
      </div>
    </section>
  `;
}

function detailItem(label, value) {
  return `<div class="detail-item"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value || "-")}</strong></div>`;
}

function qualityBreakdown(p) {
  if (!p.enabled) {
    return [{ label: "代理已禁用", value: -(p.quality_score || 0) }];
  }
  const parts = [{ label: "基础分", value: 45 }];
  if (p.status === "available") parts.push({ label: "状态可用", value: 25 });
  if (p.status === "unchecked") parts.push({ label: "尚未检测", value: 5 });
  if (p.status === "unavailable") parts.push({ label: "状态不可用", value: -35 });
  const attempts = (p.success_count || 0) + (p.fail_count || 0);
  if (attempts > 0) {
    parts.push({ label: "成功率奖励", value: Math.floor((p.success_count || 0) / attempts * 20) });
    parts.push({ label: "失败率扣分", value: -Math.floor((p.fail_count || 0) / attempts * 12) });
  }
  if (p.fail_count > 0) parts.push({ label: "连续失败次数", value: -(p.fail_count || 0) * 6 });
  if (p.latency_ms > 0) {
    if (p.latency_ms <= 300) parts.push({ label: "低延迟", value: 18 });
    else if (p.latency_ms <= 800) parts.push({ label: "延迟良好", value: 12 });
    else if (p.latency_ms <= 1500) parts.push({ label: "延迟可接受", value: 6 });
    else if (p.latency_ms > 4000) parts.push({ label: "高延迟", value: -10 });
  }
  if (p.anonymity === "anonymous") parts.push({ label: "匿名代理", value: 5 });
  if (p.anonymity === "transparent") parts.push({ label: "透明代理", value: -5 });
  if (p.reachability === "both") parts.push({ label: "双向可用", value: 8 });
  if (p.reachability === "domestic" || p.reachability === "foreign") parts.push({ label: "单线路可用", value: 4 });
  if (p.reachability === "none") parts.push({ label: "线路不可达", value: -25 });
  if (p.target_capability === "both") parts.push({ label: "HTTP/HTTPS 目标", value: 8 });
  if (p.target_capability === "https") parts.push({ label: "HTTPS 目标", value: 5 });
  if (p.target_capability === "http") parts.push({ label: "HTTP 目标", value: 3 });
  if (p.target_capability === "none") parts.push({ label: "目标不可达", value: -25 });
  if (!p.last_checked) {
    parts.push({ label: "未检测时间", value: -3 });
  } else if (Date.now() - new Date(p.last_checked).getTime() > 24 * 60 * 60 * 1000) {
    parts.push({ label: "检测结果较旧", value: -6 });
  }
  return parts;
}

async function checkAllProxies() {
  const button = $("checkAllBtn");
  button.disabled = true;
  try {
    const scope = $("checkScope").value;
    const scheme = $("checkScheme").value;
    const params = new URLSearchParams({ scope, scheme });
    const r = await api(`/api/proxies/check-all?${params.toString()}`, { method: "POST" });
    $("proxyMsg").textContent = r.started
      ? `已启动后台检测任务（${checkScopeLabel(r.scope)} / ${checkSchemeLabel(r.scheme)}）。`
      : r.message;
    if (r.started) {
      pollHealthCheck();
    }
  } catch (err) {
    $("proxyMsg").textContent = err.message;
  } finally {
    button.disabled = false;
  }
}

async function pollHealthCheck() {
  const token = ++healthPollToken;
  const output = $("proxyMsg");
  for (;;) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    if (token !== healthPollToken) return;
    try {
      const data = await api("/api/tasks");
      const health = data.health_scheduler || {};
      output.textContent = [
        `代理池检测：${health.running ? "运行中" : "已完成"}`,
        health.current_task || "",
        health.last_result ? `最近结果：${health.last_result}` : "",
        health.last_error ? `错误：${health.last_error}` : "",
      ].filter(Boolean).join("\n");
      await loadStats();
      if (activeTab === "pool") await loadProxies();
      if (activeTab === "tasks") await loadTasks();
      if (activeTab === "status") await loadStatus();
      if (!health.running) {
        await loadAll();
        return;
      }
    } catch (err) {
      output.textContent = err.message;
      return;
    }
  }
}

async function cleanupProxies() {
  if (!confirm("清理所有不可用或已禁用代理？")) return;
  const button = $("cleanupBtn");
  button.disabled = true;
  try {
    const r = await api("/api/proxies/cleanup", { method: "POST" });
    alert(`已清理 ${r.deleted} 个代理。`);
    await loadAll();
  } catch (err) {
    alert(err.message);
  } finally {
    button.disabled = false;
  }
}

function proxyFilterParams() {
  return new URLSearchParams({
    q: $("q").value.trim(),
    scheme: $("scheme").value,
    status: $("status").value,
    anonymity: $("anonymity").value,
    reachability: $("reachability").value,
    target_capability: $("targetCapability").value,
  });
}

async function deleteFilteredProxies() {
  const params = proxyFilterParams();
  const hasFilter = Array.from(params.values()).some((value) => value !== "");
  const total = pageState.proxy.total || 0;
  const message = hasFilter
    ? `删除当前筛选命中的 ${total} 个代理？此操作会删除全部匹配结果，不只当前页。`
    : `当前没有任何筛选条件，将删除代理池中的全部 ${total} 个代理。确定继续？`;
  if (!confirm(message)) return;
  const button = $("deleteFilteredBtn");
  button.disabled = true;
  try {
    const r = await api("/api/proxies?" + params.toString(), { method: "DELETE" });
    alert(`已删除 ${r.deleted} 个代理。`);
    resetPage("proxy");
    await loadAll();
  } catch (err) {
    alert(err.message);
  } finally {
    button.disabled = false;
  }
}

async function exportProxies() {
  const params = proxyFilterParams();
  const res = await fetch("/api/proxies/export?" + params.toString());
  if (!res.ok) {
    alert("导出失败");
    return;
  }
  const text = await res.text();
  const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = "proxies.txt";
  link.click();
  URL.revokeObjectURL(link.href);
}

async function loadSettings() {
  const c = await api("/api/settings");
  $("setWeb").value = c.web_addr;
  $("setHTTP").value = c.http_proxy_addr;
  $("setSOCKS").value = c.socks_proxy_addr;
  $("setDomesticHealth").value = c.domestic_health_url || "http://117.72.157.151:8880/";
  $("setForeignHealth").value = c.foreign_health_url || c.health_url || "";
  $("setAnonURL").value = c.anonymity_url;
  $("setServeAnon").value = c.serve_anonymity;
  $("setServeReach").value = c.serve_reachability || "any";
  $("setServeStrategy").value = c.serve_strategy || "quality";
  $("setConcurrency").value = c.check_concurrency;
  $("setCheckScheduleEnabled").value = String(c.check_schedule_enabled !== false);
  setIntervalFields(c.check_interval_seconds || 300);
  $("setCheckScheduleScope").value = c.check_schedule_scope || "all";
  $("setCheckScheduleSchemes").value = c.check_schedule_schemes || "";
  $("setFail").value = c.fail_threshold;
  $("setTimeout").value = c.timeout_seconds;
  $("setTLSInsecure").value = String(c.tls_insecure_skip_verify === true);
  $("setCheckSuccessKeywords").value = c.check_success_keywords || "";
  $("setCheckBlockKeywords").value = c.check_block_keywords || "";
  $("setLogLevel").value = c.log_level;
  $("setLogFile").value = c.log_file;
  $("setToken").value = c.api_token;
  $("setProxyUser").value = c.proxy_username || "";
  $("setProxyPass").value = c.proxy_password || "";
  $("setUser").value = c.admin_user;
  $("setLocal").checked = c.bind_local_only;
  $("collectSources").value = c.collector_sources;
  renderCollectorSourceEditor(c.collector_sources || "");
  $("collectMode").value = c.collector_mode;
  $("collectProxy").value = c.collector_proxy;
  $("customMode").value = c.collector_mode;
  $("customProxy").value = c.collector_proxy;
  $("setCollectSources").value = c.collector_sources;
  $("setCollectMode").value = c.collector_mode;
  $("setCollectProxy").value = c.collector_proxy;
  $("setMaintenanceEnabled").value = String(c.maintenance_enabled === true);
  $("setMaintenanceInterval").value = c.maintenance_interval_seconds || 3600;
  $("setLogRetention").value = c.log_retention_days ?? 0;
  $("setSourceRetention").value = c.source_retention_days ?? 0;
  $("setBackupDir").value = c.backup_dir || "data/backups";
  $("setAutoBackup").value = String(c.auto_backup === true);
  $("setAutoVacuum").value = String(c.auto_vacuum !== false);
  $("setAutoCleanup").value = String(c.auto_cleanup_unavailable === true);
  $("setScheduleEnabled").value = String(c.schedule_enabled === true);
  $("setScheduleInterval").value = c.schedule_interval_minutes || 60;
  $("setScheduleCollector").value = String(c.schedule_run_collector !== false);
  $("setScheduleCustom").value = String(c.schedule_run_custom === true);
  $("setScheduleCheck").value = String(c.schedule_check_after_import !== false);
  $("setScheduleMaxRuns").value = c.schedule_max_runs_per_day || 24;
  $("setSchedulePause").value = c.schedule_failure_pause_minutes ?? 30;
  $("setWebAllowlist").value = c.web_allowlist || "";
  $("setWebBlocklist").value = c.web_blocklist || "";
  $("setProxyAllowlist").value = c.proxy_allowlist || "";
  $("setProxyBlocklist").value = c.proxy_blocklist || "";
  $("sideHttp").textContent = c.http_proxy_addr;
  $("sideSocks").textContent = c.socks_proxy_addr;
  settingsLoaded = true;
}

async function importProxies() {
  const button = $("importBtn");
  button.disabled = true;
  $("importMsg").textContent = "正在导入...";
  try {
    const fd = new FormData();
    fd.append("text", $("importText").value);
    if ($("importFile").files[0]) {
      fd.append("file", $("importFile").files[0]);
    }
    const r = await api("/api/proxies/import", { method: "POST", body: fd });
    const errors = r.errors || [];
    $("importMsg").textContent = `导入 ${r.imported} 个，重复 ${r.duplicates} 个，失败 ${r.failed} 个${errors.length ? "\n" + errors.join("\n") : ""}`;
    await loadAll();
  } catch (err) {
    $("importMsg").textContent = err.message;
  } finally {
    button.disabled = false;
  }
}

async function collectProxies() {
  const button = $("collectBtn");
  button.disabled = true;
  $("collectMsg").textContent = "正在采集并验证...";
  try {
    syncCollectorSourcesFromEditor();
    const r = await api("/api/collector/run", {
      method: "POST",
      body: JSON.stringify({
        mode: $("collectMode").value,
        specific_proxy: $("collectProxy").value,
        sources: $("collectSources").value,
        check: $("collectCheck").checked,
      }),
    });
    $("collectMsg").textContent = `采集任务已启动：${r.job_id}`;
    activeCollectorJob = r.job_id;
    $("collectStopBtn").hidden = false;
    await pollCollectorJob(r.job_id, "collectMsg");
    await loadAll();
  } catch (err) {
    $("collectMsg").textContent = err.message;
  } finally {
    activeCollectorJob = "";
    $("collectStopBtn").hidden = true;
    button.disabled = false;
  }
}

async function pollCollectorJob(id, targetId = "collectMsg") {
  const output = $(targetId);
  for (;;) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    const job = await api(`/api/collector/jobs/${id}`);
    if (job.status === "running" || job.status === "stopping") {
      setOutputText(output, renderJobProgress(job), true);
      continue;
    }
    if (job.status === "done") {
      setOutputText(output, renderJobDone(job, "采集任务完成"), true);
      return;
    }
    if (job.status === "stopped") {
      setOutputText(output, renderJobDone(job, "采集任务已停止"), true);
      return;
    }
    setOutputText(output, job.error || "采集任务异常结束", true);
    return;
  }
}

async function stopCollectorJob(id, targetId) {
  if (!id) return;
  const output = $(targetId);
  try {
    await api(`/api/collector/jobs/${id}/stop`, { method: "POST" });
    setOutputText(output, [output.textContent, "已发送停止请求，等待任务退出..."].filter(Boolean).join("\n"), true);
  } catch (err) {
    setOutputText(output, [output.textContent, `停止失败：${err.message}`].filter(Boolean).join("\n"), true);
  }
}

function setOutputText(output, text, scrollToBottom = false) {
  output.textContent = text;
  if (scrollToBottom) {
    output.scrollTop = output.scrollHeight;
  }
}

function renderJobProgress(job) {
  return [
    job.status === "stopping" ? `采集任务停止中：${job.id}` : `采集任务运行中：${job.id}`,
    "过程日志：",
    ...(job.logs || []),
  ].join("\n");
}

function renderJobDone(job, title) {
  const r = job.result || {};
  return [
    `${title}：${job.id}`,
    `源成功 ${r.sources_fetched || 0} 个，源失败 ${r.sources_failed || 0} 个`,
    `导入 ${r.imported || 0} 个，重复 ${r.duplicates || 0} 个，跳过 ${r.skipped || 0} 个，已验证 ${r.checked || 0} 个，失败 ${r.failed || 0} 个`,
    "过程日志：",
    ...(job.logs || []),
    r.source_errors?.length ? "源错误：\n" + r.source_errors.join("\n") : "",
    r.errors?.length ? "导入错误：\n" + r.errors.join("\n") : "",
  ].filter(Boolean).join("\n");
}

async function loadSourceStats() {
  const tbody = $("sourceRows");
  if (!tbody) return;
  const seq = ++loadSeq.source;
  const params = new URLSearchParams({
    q: $("sourceQuery").value,
    failed: $("sourceFailed").value,
    page: pageState.source.page,
    page_size: readPageSize("source"),
  });
  const data = await api("/api/collector/sources?" + params.toString());
  if (seq !== loadSeq.source) return;
  const rows = pageItems("source", data);
  if (await reloadIfPageOutOfRange("source", rows, loadSourceStats)) return;
  if (!rows.length) {
    tbody.innerHTML = `<tr><td colspan="9" class="empty">暂无采集源统计</td></tr>`;
    renderPager("source");
    return;
  }
  tbody.innerHTML = rows.map((s) => `<tr>
    <td title="${escapeAttr(s.url)}">${escapeHTML(sourceLabel(s.url))}</td>
    <td>${s.fetch_ok}</td>
    <td>${s.fetch_fail}</td>
    <td>${s.imported}</td>
    <td>${s.duplicates}</td>
    <td>${s.checked}</td>
    <td>${s.last_imported}</td>
    <td>${escapeHTML(s.last_error || "")}</td>
    <td><button class="danger" data-source-delete="${escapeAttr(s.url)}">删除</button></td>
  </tr>`).join("");
  tbody.querySelectorAll("[data-source-delete]").forEach((button) => {
    button.addEventListener("click", () => deleteSourceStat(button.dataset.sourceDelete));
  });
  renderPager("source");
}

async function deleteSourceStat(sourceURL) {
  if (!confirm(`删除该来源统计？\n${sourceURL}`)) return;
  await api(`/api/collector/sources?url=${encodeURIComponent(sourceURL)}`, { method: "DELETE" });
  await loadSourceStats();
}

async function clearSourceStats() {
  if (!confirm("清空全部采集源质量统计？")) return;
  const r = await api("/api/collector/sources", { method: "DELETE" });
  alert(`已清空 ${r.deleted || 0} 条来源统计。`);
  await loadSourceStats();
}

async function loadDeadProxies() {
  const tbody = $("deadRows");
  if (!tbody) return;
  const seq = ++loadSeq.dead;
  const params = new URLSearchParams({
    q: $("deadQuery").value,
    disabled: $("deadDisabled").value,
    page: pageState.dead.page,
    page_size: readPageSize("dead"),
  });
  const data = await api("/api/dead-proxies?" + params.toString());
  if (seq !== loadSeq.dead) return;
  const rows = pageItems("dead", data);
  if (await reloadIfPageOutOfRange("dead", rows, loadDeadProxies)) return;
  if (!rows.length) {
    tbody.innerHTML = `<tr><td colspan="9" class="empty">暂无失效代理档案</td></tr>`;
    renderPager("dead");
    return;
  }
  const offset = (Math.max(1, pageState.dead.page) - 1) * readPageSize("dead");
  tbody.innerHTML = rows.map((p, index) => {
    const label = proxyLabel(p);
    return `<tr data-label="${escapeAttr(label)}">
      <td title="数据库 ID：${p.id}">${offset + index + 1}</td>
      <td class="mono">${escapeHTML(label)}</td>
      <td>${protocolBadges(p.tags || p.scheme)}</td>
      <td>${p.fail_count || 0}</td>
      <td><span class="badge ${p.disabled ? "disabled" : "unavailable"}">${p.disabled ? "已关闭" : "正在跳过"}</span></td>
      <td>${escapeHTML(sourceLabel(p.source))}</td>
      <td>${formatTime(p.last_seen)}</td>
      <td>${escapeHTML(p.last_error || "")}</td>
      <td><div class="actions">
        <button class="secondary" data-dead="toggle" data-id="${p.id}" data-disabled="${!p.disabled}">${p.disabled ? "启用跳过" : "关闭跳过"}</button>
        <button class="danger" data-dead="delete" data-id="${p.id}">删除</button>
      </div></td>
    </tr>`;
  }).join("");
  tbody.querySelectorAll("button").forEach((button) => button.addEventListener("click", () => handleDeadAction(button)));
  renderPager("dead");
}

async function handleDeadAction(button) {
  const id = button.dataset.id;
  const action = button.dataset.dead;
  button.disabled = true;
  try {
    if (action === "toggle") {
      await api(`/api/dead-proxies/${id}`, { method: "PATCH", body: JSON.stringify({ disabled: button.dataset.disabled === "true" }) });
    }
    if (action === "delete") {
      const label = button.closest("tr")?.dataset.label || id;
      if (!confirm(`删除失效档案 ${label}？`)) return;
      await api(`/api/dead-proxies/${id}`, { method: "DELETE" });
    }
    await loadDeadProxies();
  } catch (err) {
    showError(err);
  } finally {
    button.disabled = false;
  }
}

async function clearDeadProxies() {
  if (!confirm("清空全部失效代理档案？")) return;
  const r = await api("/api/dead-proxies", { method: "DELETE" });
  alert(`已清空 ${r.deleted || 0} 条失效档案。`);
  await loadDeadProxies();
}

async function loadCustomTemplates() {
  const list = $("customRows");
  if (!list) return;
  const seq = ++loadSeq.custom;
  const params = new URLSearchParams({
    page: pageState.custom.page,
    page_size: readPageSize("custom"),
  });
  const data = await api("/api/custom-scrapers?" + params.toString());
  if (seq !== loadSeq.custom) return;
  const rows = pageItems("custom", data);
  if (await reloadIfPageOutOfRange("custom", rows, loadCustomTemplates)) return;
  if (!rows.length) {
    list.innerHTML = `<div class="empty custom-template-empty">暂无自定义抓取模板</div>`;
    renderPager("custom");
    return;
  }
  list.innerHTML = rows.map(renderCustomTemplateCard).join("");
  list.querySelectorAll("button[data-custom]").forEach((button) => button.addEventListener("click", () => handleCustomAction(button)));
  renderPager("custom");
  refreshCustomTemplateSelection();
}

function renderCustomTemplateCard(tpl) {
  const id = Number(tpl.id || 0);
  const active = id && id === activeCustomTemplateID ? " active" : "";
  const disabled = tpl.enabled === false ? ` <span class="badge disabled">禁用</span>` : "";
  const name = tpl.name || `模板 ${id}`;
  const url = tpl.url_template || "未配置 URL";
  const lastRun = tpl.last_run || "未运行";
  const lastResult = tpl.last_result || "暂无结果";
  return `<article class="custom-template-item${active}" data-template-id="${id}" role="listitem">
    <div class="custom-template-main">
      <div class="custom-template-title"><strong>${escapeHTML(name)}</strong>${disabled}</div>
      <div class="custom-template-url" title="${escapeAttr(url)}">${escapeHTML(url)}</div>
      <div class="custom-template-meta">
        <span>${escapeHTML(customPageLabel(tpl))}</span>
        <span class="custom-template-protocols">${protocolBadges(tpl.default_scheme || "http")}</span>
      </div>
      <div class="custom-template-last" title="${escapeAttr(lastResult)}">
        <span>${escapeHTML(lastRun)}</span>
        <span>${escapeHTML(lastResult)}</span>
      </div>
    </div>
    <div class="custom-template-actions">
      <button class="secondary" data-custom="edit" data-id="${id}">编辑</button>
      <button class="secondary" data-custom="run" data-id="${id}">运行</button>
      <button class="danger" data-custom="delete" data-id="${id}">删除</button>
    </div>
  </article>`;
}

async function handleCustomAction(button) {
  const id = Number(button.dataset.id || 0);
  const action = button.dataset.custom;
  if (action === "edit") {
    await loadCustomTemplateIntoForm(id, "basic");
    return;
  }
  if (action === "run") {
    await loadCustomTemplateIntoForm(id, "result");
    await runCustomTemplate(id);
    return;
  }
  if (action === "delete") {
    if (!confirm(`删除自定义模板 ${id}？`)) return;
    await api(`/api/custom-scrapers/${id}`, { method: "DELETE" });
    if (Number($("customId").value || 0) === id) {
      clearCustomForm();
    }
    await loadCustomTemplates();
  }
}

async function loadCustomTemplateIntoForm(id, section) {
  const tpl = await api(`/api/custom-scrapers/${id}`);
  fillCustomForm(tpl);
  setCustomSection(section || "basic");
  focusCustomEditor();
}

function bindCustomSections() {
  document.querySelectorAll("[data-custom-section]").forEach((button) => {
    button.addEventListener("click", () => setCustomSection(button.dataset.customSection));
  });
  setCustomSection(activeCustomSection);
  updateCustomCurrent();
}

function setCustomSection(name) {
  activeCustomSection = name || "basic";
  document.querySelectorAll("[data-custom-section]").forEach((button) => {
    const active = button.dataset.customSection === activeCustomSection;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });
  document.querySelectorAll("[data-custom-panel]").forEach((panel) => {
    panel.hidden = panel.dataset.customPanel !== activeCustomSection;
  });
}

function updateCustomCurrent() {
  const id = Number($("customId")?.value || 0);
  const enabled = $("customEnabled")?.value !== "false";
  const name = $("customName")?.value.trim() || (id ? `模板 ${id}` : "新建模板");
  const title = $("customCurrentName");
  const meta = $("customCurrentMeta");
  if (title) title.textContent = name;
  if (meta) meta.textContent = id ? `ID ${id} · ${enabled ? "启用" : "禁用"}` : "未保存";
}

function refreshCustomTemplateSelection() {
  const id = Number($("customId")?.value || activeCustomTemplateID || 0);
  document.querySelectorAll("[data-template-id]").forEach((item) => {
    const active = Number(item.dataset.templateId || 0) === id;
    item.classList.toggle("active", active);
    item.setAttribute("aria-current", active ? "true" : "false");
  });
}

function focusCustomEditor() {
  const editor = $("customEditor");
  if (!editor || window.innerWidth > 760) return;
  editor.scrollIntoView({ behavior: "smooth", block: "start" });
}

async function loadCustomSample() {
  const tpl = await api("/api/custom-scrapers/sample");
  tpl.id = "";
  fillCustomForm(tpl);
  setCustomSection("basic");
  $("customMsg").textContent = "已载入示例模板，可按目标站点页面结构调整正则。";
}

function clearCustomForm() {
  activeCustomTemplateID = 0;
  fillCustomForm({
    id: "",
    name: "",
    enabled: true,
    url_template: "",
    request_method: "GET",
    request_headers: "",
    request_body: "",
    page_mode: "range",
    page_start: 1,
    page_end: 1,
    page_size: 0,
    max_pages: 50,
    request_timeout_seconds: 0,
    concurrency: 1,
    retry_count: 0,
    stop_after_failures: 0,
    page_pattern: "",
    page_group: "page",
    default_scheme: "http",
    response_type: "text",
    extract_pattern: defaultCustomExtractPattern,
    scheme_group: "scheme",
    host_group: "host",
    port_group: "port",
    user_group: "user",
    password_group: "password",
    notes: "",
  });
  setCustomSection("basic");
  $("customMsg").textContent = "";
}

function fillCustomForm(tpl) {
  activeCustomTemplateID = Number(tpl.id || 0);
  $("customId").value = tpl.id || "";
  $("customName").value = tpl.name || "";
  $("customEnabled").value = String(tpl.enabled !== false);
  $("customURL").value = tpl.url_template || "";
  $("customMethod").value = tpl.request_method || "GET";
  $("customHeaders").value = tpl.request_headers || "";
  $("customBody").value = tpl.request_body || "";
  $("customPageMode").value = tpl.page_mode || "range";
  $("customPageStart").value = tpl.page_start || 1;
  $("customPageEnd").value = tpl.page_end || 1;
  $("customPageSizeParam").value = tpl.page_size || "";
  $("customMaxPages").value = tpl.max_pages || 50;
  $("customTimeout").value = tpl.request_timeout_seconds || 0;
  $("customConcurrency").value = tpl.concurrency || 1;
  $("customRetry").value = tpl.retry_count || 0;
  $("customStopFailures").value = tpl.stop_after_failures || 0;
  $("customPagePattern").value = tpl.page_pattern || "";
  $("customPageGroup").value = tpl.page_group || "page";
  setCustomSchemes(tpl.default_scheme || "http");
  $("customPattern").value = tpl.extract_pattern || "";
  $("customSchemeGroup").value = tpl.scheme_group || "";
  $("customHostGroup").value = tpl.host_group || "host";
  $("customPortGroup").value = tpl.port_group || "port";
  $("customUserGroup").value = tpl.user_group || "";
  $("customPassGroup").value = tpl.password_group || "";
  $("customNotes").value = tpl.notes || "";
  updateCustomCurrent();
  refreshCustomTemplateSelection();
}

function readCustomForm() {
  return {
    id: Number($("customId").value || 0),
    name: $("customName").value,
    enabled: $("customEnabled").value === "true",
    url_template: $("customURL").value,
    request_method: $("customMethod").value,
    request_headers: $("customHeaders").value,
    request_body: $("customBody").value,
    page_mode: $("customPageMode").value,
    page_start: Number($("customPageStart").value || 1),
    page_end: Number($("customPageEnd").value || 1),
    page_size: Number($("customPageSizeParam").value || 0),
    max_pages: Number($("customMaxPages").value || 50),
    request_timeout_seconds: Number($("customTimeout").value || 0),
    concurrency: Number($("customConcurrency").value || 1),
    retry_count: Number($("customRetry").value || 0),
    stop_after_failures: Number($("customStopFailures").value || 0),
    page_pattern: $("customPagePattern").value,
    page_group: $("customPageGroup").value,
    default_scheme: readCustomSchemes(),
    response_type: "text",
    json_list_path: "",
    json_proxy_path: "",
    json_scheme_path: "",
    json_host_path: "",
    json_port_path: "",
    json_user_path: "",
    json_password_path: "",
    extract_pattern: $("customPattern").value,
    scheme_group: $("customSchemeGroup").value,
    host_group: $("customHostGroup").value,
    port_group: $("customPortGroup").value,
    user_group: $("customUserGroup").value,
    password_group: $("customPassGroup").value,
    notes: $("customNotes").value,
  };
}

async function saveCustomTemplate() {
  const tpl = readCustomForm();
  const method = tpl.id ? "PUT" : "POST";
  const path = tpl.id ? `/api/custom-scrapers/${tpl.id}` : "/api/custom-scrapers";
  const saved = await api(path, { method, body: JSON.stringify(tpl) });
  fillCustomForm(saved);
  $("customMsg").textContent = `已保存模板：${saved.name}`;
  setCustomSection("result");
  await loadCustomTemplates();
}

async function previewCustomTemplate() {
  const tpl = readCustomForm();
  setCustomSection("result");
  const options = customRequestOptions();
  const preview = tpl.id
    ? await api(`/api/custom-scrapers/${tpl.id}/preview`, {
        method: "POST",
        body: JSON.stringify(options),
      })
    : await api("/api/custom-scrapers/preview", {
        method: "POST",
        body: JSON.stringify({ template: tpl, ...options }),
      });
  $("customMsg").textContent = [
    preview.error ? `错误：${preview.error}` : "",
    `URL：${(preview.urls || []).join(", ")}`,
    `匹配示例：${(preview.matches || []).map(proxyLabel).join("\n") || "无"}`,
  ].filter(Boolean).join("\n");
}

async function runCustomTemplate(id) {
  const tplID = id || Number($("customId").value || 0);
  setCustomSection("result");
  if (!tplID) {
    $("customMsg").textContent = "请先保存模板。";
    return;
  }
  const r = await api(`/api/custom-scrapers/${tplID}/run`, {
    method: "POST",
    body: JSON.stringify(customRequestOptions()),
  });
  $("customMsg").textContent = `自定义抓取任务已启动：${r.job_id}`;
  activeCustomJob = r.job_id;
  $("customStopBtn").hidden = false;
  try {
    await pollCollectorJob(r.job_id, "customMsg");
    await loadAll();
  } finally {
    activeCustomJob = "";
    $("customStopBtn").hidden = true;
  }
}

function customRequestOptions() {
  return {
    mode: $("customMode").value,
    specific_proxy: $("customProxy").value,
    check: $("customCheck").checked,
  };
}

function customPageLabel(tpl) {
  if (tpl.page_mode === "auto") {
    return `全部页，从 ${tpl.page_start || 1} 开始，最多 ${tpl.max_pages || 50} 页，并发 ${tpl.concurrency || 1}`;
  }
  return `${tpl.page_start || 1}-${tpl.page_end || tpl.page_start || 1}，并发 ${tpl.concurrency || 1}`;
}

function protocolBadges(tags) {
  const items = tagItems(tags);
  if (!items.length) return "";
  return items.map((tag) => `<span class="badge protocol">${escapeHTML(tag)}</span>`).join(" ");
}

function reachabilityBadge(value) {
  const key = ["both", "domestic", "foreign", "none", "unknown"].includes(value) ? value : "unknown";
  return `<span class="badge reach ${key}">${reachText[key]}</span>`;
}

function targetCapabilityBadge(value) {
  const key = ["both", "http", "https", "none", "unknown"].includes(value) ? value : "unknown";
  return `<span class="badge target ${key}">${targetText[key]}</span>`;
}

function qualityBadge(score) {
  const n = Number(score || 0);
  const level = n >= 70 ? "good" : n >= 40 ? "mid" : "low";
  return `<span class="badge quality ${level}" title="质量分：${n}/100">${n}</span>`;
}

function tagItems(tags) {
  const seen = new Set();
  String(tags || "")
    .split(/[\s,|;]+/)
    .map((item) => item.trim().toLowerCase())
    .filter(Boolean)
    .forEach((tag) => {
      if (tag === "auto" || tag === "all") {
        protocolOrder.forEach((protocol) => seen.add(protocol));
        return;
      }
      if (protocolOrder.includes(tag) || tag === "unknown") {
        seen.add(tag);
      }
    });
  return [...protocolOrder, "unknown"].filter((tag) => seen.has(tag));
}

function setCustomSchemes(value) {
  const selected = new Set(protocolItems(value));
  document.querySelectorAll("[data-custom-scheme]").forEach((input) => {
    input.checked = selected.has(input.value);
  });
  ensureCustomSchemeSelection();
}

function readCustomSchemes() {
  const selected = Array.from(document.querySelectorAll("[data-custom-scheme]:checked"))
    .map((input) => input.value)
    .filter((value) => protocolOrder.includes(value));
  return selected.length ? selected.join(",") : "http";
}

function protocolItems(value) {
  const seen = new Set();
  String(value || "")
    .split(/[\s,|;]+/)
    .map((item) => item.trim().toLowerCase())
    .filter(Boolean)
    .forEach((item) => {
      if (item === "auto" || item === "all") {
        protocolOrder.forEach((protocol) => seen.add(protocol));
        return;
      }
      if (protocolOrder.includes(item)) {
        seen.add(item);
      }
    });
  const items = protocolOrder.filter((item) => seen.has(item));
  return items.length ? items : ["http"];
}

function ensureCustomSchemeSelection(event) {
  const checked = document.querySelectorAll("[data-custom-scheme]:checked");
  if (!checked.length) {
    const target = event?.target?.matches?.("[data-custom-scheme]") ? event.target : document.querySelector("[data-custom-scheme][value='http']");
    if (target) target.checked = true;
  }
}

function bindPager(key, loader) {
  const pageSize = $(`${key}PageSize`);
  const prev = $(`${key}PrevPage`);
  const next = $(`${key}NextPage`);
  if (pageSize) {
    pageSize.addEventListener("change", () => {
      pageState[key].pageSize = readPageSize(key);
      resetPage(key);
      loader().catch(showError);
    });
  }
  if (prev) {
    prev.addEventListener("click", () => {
      if (pageState[key].page <= 1) return;
      pageState[key].page -= 1;
      loader().catch(showError);
    });
  }
  if (next) {
    next.addEventListener("click", () => {
      if (pageState[key].page >= pageState[key].totalPages) return;
      pageState[key].page += 1;
      loader().catch(showError);
    });
  }
}

function readPageSize(key) {
  const value = Number($(`${key}PageSize`)?.value || pageState[key].pageSize || 20);
  return [10, 20, 50, 100].includes(value) ? value : 20;
}

function resetPage(key) {
  pageState[key].page = 1;
}

function pageItems(key, data) {
  if (Array.isArray(data)) {
    pageState[key] = { page: 1, pageSize: data.length || readPageSize(key), total: data.length, totalPages: 1 };
    return data;
  }
  const items = asArray(data.items);
  pageState[key] = {
    page: Number(data.page || 1),
    pageSize: Number(data.page_size || readPageSize(key)),
    total: Number(data.total || 0),
    totalPages: Math.max(1, Number(data.total_pages || 1)),
  };
  return items;
}

async function reloadIfPageOutOfRange(key, rows, loader) {
  if (!rows.length && pageState[key].total > 0 && pageState[key].page > pageState[key].totalPages) {
    pageState[key].page = pageState[key].totalPages;
    await loader();
    return true;
  }
  return false;
}

function renderPager(key) {
  const state = pageState[key];
  const info = $(`${key}PageInfo`);
  const pageSize = $(`${key}PageSize`);
  const prev = $(`${key}PrevPage`);
  const next = $(`${key}NextPage`);
  if (info) {
    info.textContent = `第 ${state.page}/${state.totalPages} 页，共 ${state.total} 条`;
  }
  if (pageSize) {
    pageSize.value = String(state.pageSize);
  }
  if (prev) prev.disabled = state.page <= 1;
  if (next) next.disabled = state.page >= state.totalPages;
}

function fillSampleImport() {
  $("importText").value = sampleImportText;
  $("importMsg").textContent = "已载入本地示例。示例用于离线体验导入流程，未必代表可用公网代理。";
}

function useLocalSources() {
  $("collectSources").value = [
    `http|${location.origin}/samples/http.txt`,
    `socks5|${location.origin}/samples/socks5.txt`,
  ].join("\n");
  renderCollectorSourceEditor($("collectSources").value);
  $("collectMode").value = "direct";
  $("collectCheck").checked = false;
  $("collectMsg").textContent = "已切换为本地示例源，并关闭立即验证，适合离线试用采集导入流程。";
}

function parseCollectorSourcesText(text) {
  return String(text || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("#"))
    .map((line) => {
      const idx = line.indexOf("|");
      if (idx > 0) {
        return { scheme: line.slice(0, idx).trim(), url: line.slice(idx + 1).trim() };
      }
      return { scheme: inferSourceScheme(line), url: line };
    });
}

function renderCollectorSourceEditor(text) {
  const tbody = $("collectSourceRows");
  if (!tbody) return;
  const rows = parseCollectorSourcesText(text);
  if (!rows.length) {
    tbody.innerHTML = `<tr><td colspan="3" class="empty">暂无来源，点击新增来源</td></tr>`;
    return;
  }
  tbody.innerHTML = rows.map((row, index) => collectorSourceRowHTML(row, index)).join("");
  bindCollectorSourceEditor();
}

function collectorSourceRowHTML(row, index) {
  return `<tr data-source-index="${index}">
    <td>
      <select data-source-scheme>
        ${["http", "https", "socks5", "http,socks5", "auto"].map((value) => `<option value="${value}" ${normalizeSourceScheme(row.scheme) === value ? "selected" : ""}>${value.toUpperCase()}</option>`).join("")}
      </select>
    </td>
    <td><input data-source-url value="${escapeAttr(row.url)}" placeholder="https://example.com/proxies.txt"></td>
    <td><div class="actions">
      <button class="secondary" data-source-move="-1">上移</button>
      <button class="secondary" data-source-move="1">下移</button>
      <button class="danger" data-source-remove>删除</button>
    </div></td>
  </tr>`;
}

function bindCollectorSourceEditor() {
  $("collectSourceRows").querySelectorAll("input, select").forEach((input) => {
    input.addEventListener("input", syncCollectorSourcesFromEditor);
    input.addEventListener("change", syncCollectorSourcesFromEditor);
  });
  $("collectSourceRows").querySelectorAll("[data-source-remove]").forEach((button) => {
    button.addEventListener("click", () => {
      button.closest("tr").remove();
      syncCollectorSourcesFromEditor();
      renderCollectorSourceEditor($("collectSources").value);
    });
  });
  $("collectSourceRows").querySelectorAll("[data-source-move]").forEach((button) => {
    button.addEventListener("click", () => {
      const tr = button.closest("tr");
      const dir = Number(button.dataset.sourceMove);
      const sibling = dir < 0 ? tr.previousElementSibling : tr.nextElementSibling;
      if (!sibling) return;
      if (dir < 0) {
        tr.parentElement.insertBefore(tr, sibling);
      } else {
        tr.parentElement.insertBefore(sibling, tr);
      }
      syncCollectorSourcesFromEditor();
      renderCollectorSourceEditor($("collectSources").value);
    });
  });
}

function addCollectorSourceRow() {
  const rows = parseCollectorSourcesText($("collectSources").value);
  rows.push({ scheme: "http", url: "" });
  $("collectSources").value = rows.map(formatCollectorSourceRow).join("\n");
  renderCollectorSourceEditor($("collectSources").value);
}

function syncCollectorSourcesFromEditor() {
  const tbody = $("collectSourceRows");
  if (!tbody) return;
  const rows = Array.from(tbody.querySelectorAll("tr"))
    .map((tr) => ({
      scheme: tr.querySelector("[data-source-scheme]")?.value || "http",
      url: tr.querySelector("[data-source-url]")?.value.trim() || "",
    }))
    .filter((row) => row.url);
  $("collectSources").value = rows.map(formatCollectorSourceRow).join("\n");
}

function formatCollectorSourceRow(row) {
  const scheme = normalizeSourceScheme(row.scheme);
  return `${scheme}|${row.url}`;
}

function normalizeSourceScheme(value) {
  const raw = String(value || "").toLowerCase().trim();
  if (raw === "https" || raw === "socks5" || raw === "http,socks5" || raw === "auto") return raw;
  return "http";
}

function inferSourceScheme(url) {
  let lower = String(url || "").toLowerCase();
  try {
    lower = new URL(url).pathname.toLowerCase();
  } catch {}
  if (lower.includes("socks5")) return "socks5";
  if (lower.includes("https")) return "https";
  return "http";
}

async function saveSettings() {
  const button = $("saveSettingsBtn");
  button.disabled = true;
  $("settingsMsg").textContent = "正在保存...";
  const body = {
    web_addr: $("setWeb").value,
    http_proxy_addr: $("setHTTP").value,
    socks_proxy_addr: $("setSOCKS").value,
    health_url: $("setForeignHealth").value,
    domestic_health_url: $("setDomesticHealth").value,
    foreign_health_url: $("setForeignHealth").value,
    anonymity_url: $("setAnonURL").value,
    serve_anonymity: $("setServeAnon").value,
    serve_reachability: $("setServeReach").value,
    serve_strategy: $("setServeStrategy").value,
    collector_sources: $("setCollectSources").value,
    collector_mode: $("setCollectMode").value,
    collector_proxy: $("setCollectProxy").value,
    check_concurrency: Number($("setConcurrency").value),
    check_interval_seconds: readIntervalSeconds(),
    check_schedule_enabled: $("setCheckScheduleEnabled").value === "true",
    check_schedule_scope: $("setCheckScheduleScope").value,
    check_schedule_schemes: $("setCheckScheduleSchemes").value,
    fail_threshold: Number($("setFail").value),
    timeout_seconds: Number($("setTimeout").value),
    tls_insecure_skip_verify: $("setTLSInsecure").value === "true",
    check_success_keywords: $("setCheckSuccessKeywords").value,
    check_block_keywords: $("setCheckBlockKeywords").value,
    api_token: $("setToken").value,
    proxy_username: $("setProxyUser").value,
    proxy_password: $("setProxyPass").value,
    admin_user: $("setUser").value,
    admin_password: $("setPass").value,
    bind_local_only: $("setLocal").checked,
    web_allowlist: $("setWebAllowlist").value,
    web_blocklist: $("setWebBlocklist").value,
    proxy_allowlist: $("setProxyAllowlist").value,
    proxy_blocklist: $("setProxyBlocklist").value,
    log_level: $("setLogLevel").value,
    log_file: $("setLogFile").value,
    maintenance_enabled: $("setMaintenanceEnabled").value === "true",
    maintenance_interval_seconds: Number($("setMaintenanceInterval").value),
    log_retention_days: Number($("setLogRetention").value),
    source_retention_days: Number($("setSourceRetention").value),
    backup_dir: $("setBackupDir").value,
    auto_backup: $("setAutoBackup").value === "true",
    auto_vacuum: $("setAutoVacuum").value === "true",
    auto_cleanup_unavailable: $("setAutoCleanup").value === "true",
    schedule_enabled: $("setScheduleEnabled").value === "true",
    schedule_interval_minutes: Number($("setScheduleInterval").value),
    schedule_run_collector: $("setScheduleCollector").value === "true",
    schedule_run_custom: $("setScheduleCustom").value === "true",
    schedule_check_after_import: $("setScheduleCheck").value === "true",
    schedule_max_runs_per_day: Number($("setScheduleMaxRuns").value),
    schedule_failure_pause_minutes: Number($("setSchedulePause").value),
  };
  try {
    await api("/api/settings", { method: "PUT", body: JSON.stringify(body) });
    $("settingsMsg").textContent = "已保存。监听地址变更需要重启服务后生效。";
    await loadSettings();
  } catch (err) {
    $("settingsMsg").textContent = err.message;
  } finally {
    button.disabled = false;
  }
}

async function runMaintenance() {
  if (!confirm("立即执行数据维护？可能会清理不可用代理、裁剪日志、备份并压缩数据库。")) return;
  const button = $("runMaintenanceBtn");
  button.disabled = true;
  $("statusMsg").textContent = "正在执行维护...";
  try {
    const r = await api("/api/maintenance/run", { method: "POST" });
    const result = r.result || {};
    $("statusMsg").textContent = [
      r.started ? "维护已完成。" : "维护任务已在运行。",
      `清理不可用代理：${result.deleted_unavailable || 0}`,
      `清理来源统计：${result.deleted_sources || 0}`,
      `裁剪日志行数：${result.log_lines_removed || 0}`,
      `数据库压缩：${result.vacuumed ? "已执行" : "未执行"}`,
      result.backup_path ? `备份文件：${result.backup_path}` : "",
      result.errors?.length ? "错误：\n" + result.errors.join("\n") : "",
    ].filter(Boolean).join("\n");
    await loadAll();
  } catch (err) {
    $("statusMsg").textContent = err.message;
  } finally {
    button.disabled = false;
  }
}

function setIntervalFields(seconds) {
  const units = [
    ["days", 86400],
    ["hours", 3600],
    ["minutes", 60],
    ["seconds", 1],
  ];
  let value = Math.max(10, Number(seconds || 300));
  let unit = "seconds";
  for (const [candidate, factor] of units) {
    if (value >= factor && value % factor === 0) {
      unit = candidate;
      value = value / factor;
      break;
    }
  }
  $("setIntervalValue").value = value;
  $("setIntervalUnit").value = unit;
}

function readIntervalSeconds() {
  const value = Math.max(1, Number($("setIntervalValue").value || 1));
  const factor = {
    seconds: 1,
    minutes: 60,
    hours: 3600,
    days: 86400,
  }[$("setIntervalUnit").value] || 60;
  return Math.max(10, Math.round(value * factor));
}

function healthSchedulerSummary(s) {
  const parts = [];
  if (s.current_task) parts.push(s.current_task);
  if (s.next_run) parts.push(`下次 ${formatTime(s.next_run)}`);
  parts.push(`间隔 ${formatDuration(s.interval_seconds || 0)}`);
  parts.push(`${checkScopeLabel(s.scope)} / ${checkSchemeLabel(s.schemes)}`);
  if (s.last_result) parts.push(s.last_result);
  if (s.last_error) parts.push(s.last_error);
  return parts.join("，");
}

function schedulerSummary(s) {
  const parts = [];
  if (s.current_task) parts.push(s.current_task);
  if (s.next_run) parts.push(`下次 ${formatTime(s.next_run)}`);
  if (s.pause_until) parts.push(`暂停到 ${formatTime(s.pause_until)}`);
  parts.push(`今日 ${s.runs_today || 0} 次`);
  if (s.last_error) parts.push(s.last_error);
  return parts.join("，");
}

function maintenanceSummary(m) {
  const parts = [];
  if (m.next_run) parts.push(`下次 ${formatTime(m.next_run)}`);
  if (m.last_run) parts.push(`上次 ${formatTime(m.last_run)}`);
  const r = m.last_result;
  if (r) parts.push(`清理 ${r.deleted_unavailable || 0} 个代理`);
  return parts.join("，") || "暂无记录";
}

function statusDetail(s) {
  const scheduler = s.scheduler || {};
  const healthScheduler = s.health_scheduler || {};
  const maintenance = s.maintenance || {};
  const outbound = s.outbound || {};
  return [
    `生成时间：${formatTime(s.generated_at)}`,
    `代理：总数 ${s.proxy?.total || 0}，可用 ${s.proxy?.available || 0}，平均延迟 ${s.proxy?.average_delay || 0}ms，平均质量 ${s.proxy?.average_score || 0}`,
    `出口请求：总计 ${outbound.total_dial || 0}，成功 ${outbound.ok_dial || 0}，失败 ${outbound.fail_dial || 0}${outbound.last_error ? "，最近错误 " + outbound.last_error : ""}`,
    `检测任务：${healthScheduler.enabled ? "启用" : "关闭"}，${healthSchedulerSummary(healthScheduler)}`,
    `采集任务：运行中 ${s.running_jobs || 0}，任务记录 ${s.stored_jobs || 0}`,
    `定时采集：${scheduler.enabled ? "启用" : "关闭"}，${schedulerSummary(scheduler)}`,
    `数据维护：${maintenance.enabled ? "启用" : "关闭"}，${maintenanceSummary(maintenance)}`,
    `存储：数据库 ${formatBytes(s.db_size_bytes || 0)}，日志 ${formatBytes(s.log_size_bytes || 0)}`,
  ].join("\n");
}

function formatDuration(seconds) {
  const n = Number(seconds || 0);
  if (n <= 0) return "未设置";
  if (n % 86400 === 0) return `${n / 86400} 天`;
  if (n % 3600 === 0) return `${n / 3600} 小时`;
  if (n % 60 === 0) return `${n / 60} 分钟`;
  return `${n} 秒`;
}

function formatBytes(bytes) {
  const n = Number(bytes || 0);
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function formatTime(value) {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return String(value);
  return d.toLocaleString("zh-CN", { hour12: false });
}

function proxyLabel(p) {
  const auth = p.username ? `${p.username}:${p.password || ""}@` : "";
  return `${p.scheme}://${auth}${p.host}:${p.port}`;
}

function sourceLabel(source) {
  if (!source) return "";
  try {
    return new URL(source).host;
  } catch {
    return source;
  }
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function escapeAttr(value) {
  return escapeHTML(value);
}

function asArray(value) {
  return Array.isArray(value) ? value : [];
}

function checkScopeLabel(scope) {
  return {
    all: "全部启用代理",
    unavailable: "仅不可用",
    unchecked: "仅未检测",
    available: "仅可用",
  }[scope] || "全部启用代理";
}

function checkSchemeLabel(scheme) {
  const value = String(scheme || "").trim();
  if (!value) return "全部协议";
  return value.split(",").filter(Boolean).map((item) => item.toUpperCase()).join("/");
}

async function loadLogs() {
  const output = $("logOutput");
  if (!output) return;
  const seq = ++loadSeq.logs;
  try {
    const params = new URLSearchParams({
      lines: $("logLines").value || "200",
      q: $("logQuery").value,
      level: $("logLevel").value,
    });
    const r = await api(`/api/logs?${params.toString()}`);
    if (seq !== loadSeq.logs) return;
    output.textContent = (r.lines || []).join("\n") || "暂无日志";
  } catch (err) {
    if (seq !== loadSeq.logs) return;
    output.textContent = err.message;
  }
}

async function clearLogs() {
  if (!confirm("清空当前日志文件？")) return;
  try {
    await api("/api/logs", { method: "DELETE" });
    await loadLogs();
  } catch (err) {
    $("logOutput").textContent = err.message;
  }
}

function showError(err) {
  console.error(err);
}

if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  });
}

boot();
