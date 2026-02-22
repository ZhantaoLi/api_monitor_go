// Log Viewer Alpine.js component (extracted from log_viewer.html)

function logViewer() {
    return {
        targetId: new URLSearchParams(window.location.search).get('target_id'),
        target: {},
        logs: [],
        loading: false,
        search: '',
        filterStatus: 'all',
        selectedId: null,
        activeTab: 'overview', // overview, content, trace

        async init() {
            Utils.initTheme();
            if (this.targetId) {
                this.refresh();
            }
        },

        async refresh() {
            this.loading = true;
            try {
                const [tRes, lRes] = await Promise.all([
                    Utils.authFetch(`/api/targets/${this.targetId}`),
                    Utils.authFetch(`/api/targets/${this.targetId}/logs?limit=100&scope=latest`)
                ]);

                if (tRes.ok) {
                    const resData = await tRes.json();
                    this.target = resData.item || resData;
                }

                // Handle logs
                if (lRes.ok) {
                    const data = await lRes.json();
                    this.logs = (data.items || []).map(l => ({
                        ...l,
                        tool_calls: typeof l.tool_calls === 'string' ? JSON.parse(l.tool_calls) : l.tool_calls
                    }));

                    // Select first if none selected
                    if (!this.selectedId && this.logs.length > 0) {
                        this.selectedId = this.logs[0].id;
                    }
                }
            } catch (e) {
                console.error(e);
            } finally {
                this.loading = false;
            }
        },

        get filteredLogs() {
            let items = this.logs;
            if (this.search) {
                const q = this.search.toLowerCase();
                items = items.filter(l =>
                    (l.model || '').toLowerCase().includes(q) ||
                    (l.content || '').toLowerCase().includes(q)
                );
            }
            if (this.filterStatus === 'success') items = items.filter(l => l.success);
            if (this.filterStatus === 'fail') items = items.filter(l => !l.success);
            return items;
        },

        get selectedLog() {
            return this.logs.find(l => l.id === this.selectedId);
        },

        get stats() {
            const total = this.logs.length;
            const success = this.logs.filter(l => l.success).length;
            const rate = total ? Math.round((success / total) * 100) : 0;

            const successLogs = this.logs.filter(l => l.success);
            const avg = successLogs.length
                ? (successLogs.reduce((a, b) => a + b.duration, 0) / successLogs.length).toFixed(3)
                : '0.000';
            return { successRate: rate, avgLatency: avg };
        },

        selectLog(log) {
            this.selectedId = log.id;
            this.activeTab = 'overview';
        }
    }
}
