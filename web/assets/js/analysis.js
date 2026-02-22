// Analysis page logic (extracted from analysis.html)

// State
let logs = [];
let filteredLogs = [];
let sortState = { key: 'timestamp', direction: 'desc' }; // Default sort by time desc
const targetId = new URLSearchParams(window.location.search).get('target_id');

function goBackToLogs() {
    if (targetId) {
        window.location.href = `/viewer.html?target_id=${targetId}`;
    } else {
        window.location.href = '/';
    }
}

// --- Initialization ---
document.addEventListener('DOMContentLoaded', () => {
    // Theme handling
    const themeToggle = document.getElementById('themeToggle');
    const html = document.documentElement;
    const body = document.body;

    // Initialize theme from localStorage or default to dark
    const savedTheme = localStorage.getItem('theme');
    if (savedTheme === 'light') {
        html.classList.remove('dark');
        html.classList.add('light');
        body.classList.remove('bg-zinc-950', 'text-zinc-100', 'dark:bg-zinc-950', 'dark:text-zinc-100');
        body.classList.add('bg-zinc-100', 'text-zinc-900');
        themeToggle.innerHTML = '<i class="ph-bold ph-sun"></i>';
    } else {
        html.classList.remove('light');
        html.classList.add('dark');
        body.classList.add('bg-zinc-950', 'text-zinc-100');
        body.classList.remove('bg-zinc-100', 'text-zinc-900');
        themeToggle.innerHTML = '<i class="ph-bold ph-moon"></i>';
    }

    themeToggle.addEventListener('click', () => {
        const isDark = html.classList.contains('dark');
        if (isDark) {
            html.classList.remove('dark');
            html.classList.add('light');
            body.classList.remove('bg-zinc-950', 'text-zinc-100');
            body.classList.add('bg-zinc-100', 'text-zinc-900');
            themeToggle.innerHTML = '<i class="ph-bold ph-sun"></i>';
            localStorage.setItem('theme', 'light');
        } else {
            html.classList.remove('light');
            html.classList.add('dark');
            body.classList.add('bg-zinc-950', 'text-zinc-100');
            body.classList.remove('bg-zinc-100', 'text-zinc-900');
            themeToggle.innerHTML = '<i class="ph-bold ph-moon"></i>';
            localStorage.setItem('theme', 'dark');
        }
        updateChartTheme();
    });

    // Animate entry
    setTimeout(() => {
        document.getElementById('mainContainer').classList.remove('opacity-0', 'translate-y-4');
    }, 100);

    // Search & Filter
    document.getElementById('searchInput').addEventListener('input', applyFilters);
    document.getElementById('filterProtocol').addEventListener('change', applyFilters);
    document.getElementById('filterStatus').addEventListener('change', applyFilters);

    // Load Data
    if (targetId) {
        fetchLogs(targetId);
    } else {
        alert('No target ID provided');
    }
});

// --- Data Fetching ---
async function fetchLogs(targetId) {
    document.getElementById('loadingState').classList.remove('hidden');
    document.getElementById('dashboard').classList.add('hidden');

    try {
        // Fetch last 500 logs ?limit=500
        const res = await Utils.authFetch(`/api/targets/${targetId}/logs?limit=500`);
        if (!res.ok) throw new Error('Failed to fetch logs');

        const data = await res.json();
        logs = data.items.map(l => ({
            ...l,
            // Ensure numeric fields
            duration: typeof l.duration === 'number' ? l.duration : 0,
            timestamp: l.timestamp || 0,
            success: !!l.success,
            // Parse tool_calls if string
            tool_calls: typeof l.tool_calls === 'string' ? JSON.parse(l.tool_calls) : l.tool_calls
        }));

        if (logs.length > 0) {
            document.getElementById('loadingState').classList.add('hidden');
            document.getElementById('dashboard').classList.remove('hidden');
            populateFilters();
            applyFilters();
        } else {
            document.getElementById('loadingState').innerHTML = '<p>No logs found for this target.</p>';
        }
    } catch (err) {
        console.error(err);
        document.getElementById('loadingState').innerHTML = `<p class="text-red-500">Error: ${err.message}</p>`;
        document.getElementById('loadingState').classList.remove('hidden');
    }
}

function refreshData() {
    if (targetId) fetchLogs(targetId);
}

// --- Core Logic ---
function populateFilters() {
    const protocols = [...new Set(logs.map(l => l.protocol))].filter(Boolean);
    const select = document.getElementById('filterProtocol');
    select.innerHTML = '<option value="all">All Protocols</option>';
    protocols.forEach(p => {
        select.innerHTML += `<option value="${p}">${p}</option>`;
    });
}

function applyFilters() {
    const query = document.getElementById('searchInput').value.toLowerCase();
    const protocol = document.getElementById('filterProtocol').value;
    const status = document.getElementById('filterStatus').value;

    filteredLogs = logs.filter(log => {
        const matchesSearch =
            (log.model || '').toLowerCase().includes(query) ||
            (log.content || '').toLowerCase().includes(query) ||
            (log.error || '').toLowerCase().includes(query);

        const matchesProtocol = protocol === 'all' || log.protocol === protocol;

        let matchesStatus = true;
        if (status === 'success') matchesStatus = log.success;
        if (status === 'error') matchesStatus = !log.success;

        return matchesSearch && matchesProtocol && matchesStatus;
    });

    // Apply sort
    const { key, direction } = sortState;
    const dir = direction === 'asc' ? 1 : -1;

    filteredLogs.sort((a, b) => {
        if (key === 'latency') return (a.duration - b.duration) * dir;
        if (key === 'status') return (a.success === b.success ? 0 : a.success ? 1 : -1) * dir;
        if (key === 'protocol') return (a.protocol || '').localeCompare(b.protocol || '') * dir;
        if (key === 'model') return (a.model || '').localeCompare(b.model || '') * dir;
        if (key === 'timestamp') return (a.timestamp - b.timestamp) * dir;
        return 0;
    });

    updateStats();
    updateCharts();
    updateAnalytics();
    renderTable();
}

function sortTable(key) {
    if (sortState.key === key) {
        sortState.direction = sortState.direction === 'asc' ? 'desc' : 'asc';
    } else {
        sortState.key = key;
        sortState.direction = 'desc';
    }
    applyFilters(); // Re-sorts
    updateSortIcons();
}

function updateSortIcons() {
    document.querySelectorAll('th i').forEach(icon => {
        icon.className = 'ph-bold ph-caret-up-down opacity-0 group-hover:opacity-100 transition-opacity';
    });

    const ths = document.querySelectorAll('thead th');
    let targetTh = null;
    if (sortState.key === 'timestamp') targetTh = ths[1];
    if (sortState.key === 'status') targetTh = ths[2];
    if (sortState.key === 'protocol') targetTh = ths[3];
    if (sortState.key === 'model') targetTh = ths[4];
    if (sortState.key === 'latency') targetTh = ths[5];

        if (targetTh) {
        const icon = targetTh.querySelector('i');
        if (icon) {
            icon.className = sortState.direction === 'asc'
                ? 'ph-bold ph-caret-up analysis-latency-soft opacity-100'
                : 'ph-bold ph-caret-down analysis-latency-soft opacity-100';
        }
    }
}

// --- Rendering ---

function updateStats() {
    const total = filteredLogs.length;
    const success = filteredLogs.filter(l => l.success).length;
    const rate = total ? Math.round((success / total) * 100) : 0;

    const successLogs = filteredLogs.filter(l => l.success);
    const totalLatency = successLogs.reduce((acc, curr) => acc + curr.duration, 0);
    const avgLatency = successLogs.length ? (totalLatency / successLogs.length).toFixed(3) : 0;

    const protocols = new Set(filteredLogs.map(l => l.protocol)).size;

    animateValue("statTotal", parseInt(document.getElementById("statTotal").innerText) || 0, total, 1000);
    document.getElementById("statSuccessRate").innerText = `${rate}%`;
    document.getElementById("statSuccessCount").innerText = success;
    document.getElementById("statLatency").innerText = `${(avgLatency * 1000).toFixed(0)}ms`;
    document.getElementById("statProtocols").innerText = protocols;
}

function animateValue(id, start, end, duration) {
    const obj = document.getElementById(id);
    if (start === end) return;
    let startTimestamp = null;
    const step = (timestamp) => {
        if (!startTimestamp) startTimestamp = timestamp;
        const progress = Math.min((timestamp - startTimestamp) / duration, 1);
        obj.innerHTML = Math.floor(progress * (end - start) + start);
        if (progress < 1) {
            window.requestAnimationFrame(step);
        }
    };
    window.requestAnimationFrame(step);
}

// --- Charts ---
let latencyChart = null;
let statusChart = null;
let timelineChart = null;

function updateCharts() {
    const successLogs = filteredLogs.filter(l => l.success);
    const html = document.documentElement;
    const isDark = html.classList.contains('dark');
    const labelColor = isDark ? '#94a3b8' : '#475569';
    const gridColor = isDark ? 'rgba(51, 65, 85, 0.3)' : 'rgba(226, 232, 240, 0.8)';

    // --- 1. Latency Chart (Model Avg) ---
    const modelLatencyMap = {};
    successLogs.forEach(l => {
        if (!modelLatencyMap[l.model]) modelLatencyMap[l.model] = [];
        modelLatencyMap[l.model].push(l.duration * 1000);
    });

    const labels = Object.keys(modelLatencyMap);
    const dataPoints = labels.map(model => {
        const latencies = modelLatencyMap[model];
        const avg = latencies.reduce((a, b) => a + b, 0) / latencies.length;
        return avg;
    });

    const ctx1 = document.getElementById('latencyChart').getContext('2d');
    if (latencyChart) latencyChart.destroy();

    latencyChart = new Chart(ctx1, {
        type: 'bar',
        data: {
            labels: labels,
            datasets: [{
                label: 'Avg Latency (ms)',
                data: dataPoints,
                backgroundColor: 'rgba(43, 152, 232, 0.6)',
                borderColor: '#00f3ff',
                borderWidth: 1,
                borderRadius: 6
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            plugins: {
                legend: { display: false },
                tooltip: {
                    backgroundColor: isDark ? 'rgba(15, 23, 42, 0.9)' : 'rgba(255, 255, 255, 0.9)',
                    titleColor: isDark ? '#fff' : '#000',
                    bodyColor: isDark ? '#ccc' : '#444',
                    borderColor: isDark ? '#334155' : '#e2e8f0',
                    borderWidth: 1
                }
            },
            scales: {
                y: {
                    beginAtZero: true,
                    grid: { color: gridColor },
                    ticks: { color: labelColor }
                },
                x: {
                    grid: { display: false },
                    ticks: { display: false, color: labelColor }
                }
            },
            onClick: (e, activeEls) => {
                if (activeEls.length > 0) {
                    const index = activeEls[0].index;
                    const model = labels[index];
                    document.getElementById('searchInput').value = model;
                    applyFilters();
                }
            }
        }
    });

    // --- 2. Status Chart (Donut) ---
    const failCount = filteredLogs.length - filteredLogs.filter(l => l.success).length;
    const localSuccessCount = filteredLogs.filter(l => l.success).length;

    const ctx2 = document.getElementById('statusChart').getContext('2d');
    if (statusChart) statusChart.destroy();

    statusChart = new Chart(ctx2, {
        type: 'doughnut',
        data: {
            labels: ['Success', 'Failure'],
            datasets: [{
                data: [localSuccessCount, failCount],
                backgroundColor: [
                    'rgba(34, 197, 94, 0.8)',
                    'rgba(239, 68, 68, 0.8)'
                ],
                borderColor: isDark ? '#1e293b' : '#ffffff',
                borderWidth: 2
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            plugins: {
                legend: {
                    position: 'right',
                    labels: { color: labelColor, boxWidth: 12, padding: 20 }
                }
            }
        }
    });

    // --- 3. Timeline Chart (Scatter) ---
    const ctx3 = document.getElementById('timelineChart').getContext('2d');
    if (timelineChart) timelineChart.destroy();

    // Reverse for display so oldest is left if sorted desc
    const timeSorted = [...filteredLogs].sort((a, b) => a.timestamp - b.timestamp);

    const scatterData = timeSorted.map((l, i) => ({
        x: i + 1, // Sequence
        y: l.duration * 1000,
        status: l.success,
        model: l.model,
        timestamp: l.timestamp
    }));

    timelineChart = new Chart(ctx3, {
        type: 'line',
        data: {
            datasets: [{
                label: 'Latency',
                data: scatterData,
                backgroundColor: (ctx) => 'rgba(0, 243, 255, 0.1)',
                borderColor: 'rgba(43, 152, 232, 0.6)',
                borderWidth: 1,
                showLine: true,
                pointRadius: 4,
                pointHoverRadius: 6,
                pointBackgroundColor: (ctx) => {
                    const val = ctx.raw;
                    return val && val.status ? '#22c55e' : '#ef4444';
                },
                segment: {
                    borderColor: (ctx) => 'rgba(0, 243, 255, 0.3)'
                }
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            plugins: {
                legend: { display: false },
                tooltip: {
                    callbacks: {
                        label: (ctx) => {
                            const raw = ctx.raw;
                            const timeStr = new Date(raw.timestamp * 1000).toLocaleTimeString();
                            return `[${timeStr}] ${raw.model}: ${raw.y.toFixed(0)}ms (${raw.status ? 'Success' : 'Fail'})`;
                        }
                    }
                }
            },
            scales: {
                x: {
                    type: 'linear',
                    position: 'bottom',
                    title: { display: true, text: 'Request Sequence', color: labelColor },
                    grid: { color: gridColor },
                    ticks: { color: labelColor }
                },
                y: {
                    beginAtZero: true,
                    title: { display: true, text: 'Latency (ms)', color: labelColor },
                    grid: { color: gridColor },
                    ticks: { color: labelColor }
                }
            }
        }
    });
}

function updateAnalytics() {
    // --- Model Performance Table ---
    const modelStats = {};

    filteredLogs.forEach(l => {
        const model = l.model || 'Unknown';
        if (!modelStats[model]) {
            modelStats[model] = { count: 0, success: 0, totalLatency: 0, latencies: [], errors: 0 };
        }
        const s = modelStats[model];
        s.count++;
        if (l.success) {
            s.success++;
            s.totalLatency += l.duration;
            s.latencies.push(l.duration);
        } else {
            s.errors++;
        }
    });

    const tbody = document.getElementById('modelAnalyticsBody');
    tbody.innerHTML = '';

    const sortedModels = Object.keys(modelStats).sort();

    sortedModels.forEach(model => {
        const s = modelStats[model];
        const avg = s.success > 0 ? (s.totalLatency / s.success * 1000).toFixed(0) : 0;
        const rate = Math.round((s.success / s.count) * 100);

        s.latencies.sort((a, b) => a - b);
        const p95Index = Math.floor(s.success * 0.95);
        const p95 = s.success > 0 ? (s.latencies[p95Index] * 1000).toFixed(0) : 0;

        let rateColor = 'bg-green-500';
        if (rate < 80) rateColor = 'bg-yellow-500';
        if (rate < 50) rateColor = 'bg-red-500';

        const row = document.createElement('tr');
        row.className = "hover:bg-zinc-50 dark:hover:bg-white/5 transition-colors border-b border-zinc-100 dark:border-zinc-800 last:border-0";
        row.innerHTML = `
                    <td class="p-3 font-medium text-zinc-900 dark:text-white">${model}</td>
                    <td class="p-3 text-zinc-600 dark:text-zinc-400">${s.count}</td>
                    <td class="p-3">
                        <div class="flex items-center gap-2">
                            <div class="w-16 h-1.5 bg-zinc-200 dark:bg-zinc-700 rounded-full overflow-hidden">
                                <div class="h-full ${rateColor}" style="width: ${rate}%"></div>
                            </div>
                            <span class="text-xs text-zinc-600 dark:text-zinc-400">${rate}%</span>
                        </div>
                    </td>
                    <td class="p-3 font-mono analysis-latency-strong text-xs">${avg}ms</td>
                    <td class="p-3 font-mono text-purple-600 dark:text-purple-400 text-xs">${p95}ms</td>
                    <td class="p-3 text-red-500 dark:text-red-400 font-bold text-xs">${s.errors > 0 ? s.errors : '-'}</td>
                `;
        tbody.appendChild(row);
    });

    // --- Error Breakdown ---
    const errorCounts = {};
    filteredLogs.filter(l => !l.success && l.error).forEach(l => {
        const shortErr = l.error.length > 80 ? l.error.substring(0, 80) + '...' : l.error;
        errorCounts[shortErr] = (errorCounts[shortErr] || 0) + 1;
    });

    const errorList = document.getElementById('errorList');
    errorList.innerHTML = '';

    const sortedErrors = Object.entries(errorCounts).sort((a, b) => b[1] - a[1]);

    if (sortedErrors.length === 0) {
        errorList.innerHTML = '<li class="text-center text-zinc-400 italic py-4">No errors found</li>';
    } else {
        sortedErrors.forEach(([err, count]) => {
            const li = document.createElement('li');
            li.className = "flex justify-between items-start gap-2 p-2 rounded bg-red-50 dark:bg-red-500/10 border border-red-200 dark:border-red-500/20";
            li.innerHTML = `
                        <span class="text-zinc-700 dark:text-zinc-300 font-mono break-all" title="${err}">${err}</span>
                        <span class="shrink-0 bg-red-500 text-white text-[10px] px-2 py-0.5 rounded-full font-bold">${count}</span>
                    `;
            errorList.appendChild(li);
        });
    }
}

function updateChartTheme() {
    if (logs.length > 0) updateCharts();
}

// --- Table ---
function renderTable() {
    const tbody = document.getElementById('logTableBody');
    tbody.innerHTML = '';

    filteredLogs.slice(0, 500).forEach((log, idx) => {
        const row = document.createElement('tr');
        row.className = 'hover:bg-white/50 dark:hover:bg-zinc-800/30 border-b border-zinc-200/50 dark:border-zinc-700/50 transition-colors group';

        const statusIcon = log.success
            ? `<span class="inline-flex items-center justify-center w-8 h-8 rounded-full bg-green-500/20 text-green-400 glow-success"><i class="ph-bold ph-check"></i></span>`
            : `<span class="inline-flex items-center justify-center w-8 h-8 rounded-full bg-red-500/20 text-red-400 glow-error"><i class="ph-bold ph-x"></i></span>`;

        const duration = (log.duration * 1000).toFixed(0) + 'ms';
        const streamBadge = log.stream
            ? `<span class="px-2 py-0.5 rounded text-[10px] bg-blue-500/20 text-blue-400 border border-blue-500/30">STREAM</span>`
            : `<span class="px-2 py-0.5 rounded text-[10px] bg-zinc-700 text-zinc-400 border border-zinc-600">STATIC</span>`;

        const timeStr = new Date(log.timestamp * 1000).toLocaleTimeString('en-US', { hour12: false });

        // Use DB ID if available, else usage sequence
        const idDisplay = log.id || (idx + 1);

        row.innerHTML = `
                    <td class="p-4 text-center text-zinc-500 dark:text-zinc-600 font-mono text-xs">#${idDisplay}</td>
                    <td class="p-4 font-mono text-xs text-zinc-500 dark:text-zinc-400">${timeStr}</td>
                    <td class="p-4">${statusIcon}</td>
                    <td class="p-4">
                        <span class="font-bold text-zinc-800 dark:text-zinc-200">${log.protocol}</span>
                    </td>
                    <td class="p-4">
                        <div class="text-xs text-zinc-600 dark:text-zinc-400 truncate max-w-[150px]" title="${log.model}">${log.model}</div>
                        </td>
                    <td class="p-4 font-mono analysis-latency-strong">${duration}</td>
                    <td class="p-4">${streamBadge}</td>
                    <td class="p-4 text-right">
                        <button onclick="openModal(${idDisplay})" class="p-2 rounded hover:bg-zinc-200/50 dark:hover:bg-white/10 text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-white transition-colors">
                            <i class="ph-bold ph-caret-right"></i>
                        </button>
                    </td>
                `;
        tbody.appendChild(row);
    });

    document.getElementById('showingCount').innerText = `Showing ${Math.min(filteredLogs.length, 500)} of ${filteredLogs.length} records`;
}

// --- Modal ---
function openModal(id) {
    // Find logic slightly robust to numeric vs string ID
    const log = logs.find(l => l.id == id);
    if (!log) return;

    document.getElementById('modalId').innerText = `ID: ${log.id}`;
    document.getElementById('modalProtocol').innerText = log.protocol;
    document.getElementById('modalModel').innerText = log.model;
    document.getElementById('modalLatency').innerText = (log.duration * 1000).toFixed(2) + ' ms';
    document.getElementById('modalStream').innerText = log.stream ? 'True' : 'False';

    // Header status
    const header = document.getElementById('modalHeader');
    if (log.success) {
        header.className = "flex items-center justify-between p-4 rounded-xl border border-green-500/30 bg-green-500/10";
        header.innerHTML = `<div class="flex items-center gap-3"><i class="ph-fill ph-check-circle text-2xl text-green-500"></i> <span class="text-green-600 dark:text-green-400 font-bold">Success</span></div>`;
    } else {
        header.className = "flex items-center justify-between p-4 rounded-xl border border-red-500/30 bg-red-500/10";
        header.innerHTML = `<div class="flex items-center gap-3"><i class="ph-fill ph-x-circle text-2xl text-red-500"></i> <span class="text-red-600 dark:text-red-400 font-bold">Failed</span></div>`;
    }

    // Error
    const errDiv = document.getElementById('modalErrorContainer');
    if (log.error) {
        errDiv.classList.remove('hidden');
        document.getElementById('modalError').innerText = log.error;
    } else {
        errDiv.classList.add('hidden');
    }

    // Content
    document.getElementById('modalContent').innerText = log.content || "(No content)";

    // Tool Calls
    const calls = log.tool_calls || [];
    document.getElementById('modalToolCalls').innerText = JSON.stringify(calls, null, 2);

    // Show
    const modal = document.getElementById('detailModal');
    const panel = document.getElementById('modalPanel');
    modal.classList.remove('hidden');
    setTimeout(() => {
        panel.classList.remove('translate-x-full');
    }, 10);
}

function closeModal() {
    const panel = document.getElementById('modalPanel');
    panel.classList.add('translate-x-full');
    setTimeout(() => {
        document.getElementById('detailModal').classList.add('hidden');
    }, 300);
}
