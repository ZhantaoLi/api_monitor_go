// Dashboard Alpine.js component (extracted from index.html)

function dashboard() {
    return {
        targets: [],
        loading: false,
        search: '',
        filterProtocol: 'all',
        filterStatus: 'all',
        modalOpen: false,
        editingId: null,
        form: {},
        formError: '',

        defaultForm: {
            name: '', base_url: '', api_key: '', source_url: '',
            interval_min: 30, timeout_s: 30,
            max_models: 0, prompt: 'What is the exact model identifier (model string) you are using for this chat/session?',
            enabled: true, verify_ssl: false, anthropic_version: '2025-09-29'
        },

        init() {
            this.loadData();
            // SSE for real-time updates
            this.connectSSE();
            // Fallback polling (60s)
            setInterval(() => this.loadData(), 60000);
        },

        connectSSE() {
            try {
                const es = Utils.createEventSource('/api/events');
                es.addEventListener('run_completed', () => this.loadData());
                es.addEventListener('target_updated', () => this.loadData());
                es.onerror = () => {
                    es.close();
                    // Reconnect after 5s
                    setTimeout(() => this.connectSSE(), 5000);
                };
            } catch (e) {
                console.warn('SSE not available, using polling', e);
            }
        },

        async loadData() {
            try {
                const res = await Utils.authFetch('/api/targets');
                const data = await res.json();
                this.targets = data.items || [];
            } catch (e) { console.error('Load failed', e); }
        },

        async refreshData() {
            this.loading = true;
            await this.loadData();
            setTimeout(() => this.loading = false, 500);
        },

        get filteredTargets() {
            let items = this.targets;

            // Filter by Status
            if (this.filterStatus !== 'all') {
                if (this.filterStatus === 'down') {
                    items = items.filter(t => ['down', 'error'].includes(t.last_status));
                } else {
                    items = items.filter(t => t.last_status === this.filterStatus);
                }
            }

            // Filter by Protocol (search in latest_models)
            if (this.filterProtocol !== 'all') {
                items = items.filter(t =>
                    t.latest_models && t.latest_models.some(m => m.protocol === this.filterProtocol)
                );
            }

            // Search (Name, URL, or Model Name)
            if (this.search) {
                const q = this.search.toLowerCase();
                items = items.filter(t =>
                    t.name.toLowerCase().includes(q) ||
                    t.base_url.toLowerCase().includes(q) ||
                    (t.latest_models && t.latest_models.some(m => m.model.toLowerCase().includes(q)))
                );
            }
            return items;
        },

        get stats() {
            const total = this.targets.length;
            // Count total models
            const models = this.targets.reduce((sum, t) => sum + (t.latest_models?.length || 0), 0);

            // Count healthy models (success=true)
            const healthy = this.targets.reduce((sum, t) => {
                const successCount = (t.latest_models || []).filter(m => m.success).length;
                return sum + successCount;
            }, 0);

            const activeTargets = this.targets.filter(t => t.enabled && t.last_total > 0);
            let rate = 0;
            if (activeTargets.length > 0) {
                const sumRate = activeTargets.reduce((s, t) => s + (t.last_success_rate || 0), 0);
                rate = Math.round(sumRate / activeTargets.length);
            }

            return { targets: total, healthy, models, rate };
        },

        // Actions
        openCreateModal() {
            this.editingId = null;
            this.form = { ...this.defaultForm };
            this.formError = '';
            this.modalOpen = true;
        },

        openEdit(t) {
            this.editingId = t.id;
            this._editOriginalUrl = t.base_url;
            this._editOriginalKey = t.api_key;
            this.form = { ...t };
            this.formError = '';
            this.modalOpen = true;
        },

        closeModal() {
            this.modalOpen = false;
        },

        async copyConfig(t) {
            const text = `${t.base_url}\n${t.api_key}`;
            try {
                await navigator.clipboard.writeText(text);
            } catch (err) {
                console.error('Failed to copy', err);
                alert('Failed to copy to clipboard');
            }
        },

        async submitForm() {
            if (!this.form.name || !this.form.base_url || !this.form.api_key) {
                this.formError = 'Please fill in required fields (Name, URL, Key)';
                return;
            }

            try {
                const payload = { ...this.form };
                let url = '/api/targets';
                let method = 'POST';

                if (this.editingId) {
                    url += `/${this.editingId}`;
                    method = 'PATCH';
                }

                const res = await Utils.authFetch(url, {
                    method,
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });

                if (!res.ok) {
                    const err = await res.json();
                    throw new Error(err.detail || 'Failed');
                }

                // Get created/updated item
                const result = await res.json();
                const newItem = result.item;

                this.closeModal();

                // If creating new target, run immediately
                if (!this.editingId && newItem && newItem.id) {
                    this.runTarget(newItem.id);
                } else if (this.editingId && (
                    this.form.base_url !== this._editOriginalUrl ||
                    this.form.api_key !== this._editOriginalKey
                )) {
                    // Key or URL changed, re-check immediately
                    this.runTarget(this.editingId);
                } else {
                    this.loadData();
                }
            } catch (e) {
                this.formError = e.message;
            }
        },

        async runTarget(id) {
            const t = this.targets.find(x => x.id === id);
            if (t && t.running) return;
            if (t) t.running = true;
            try {
                const res = await Utils.authFetch(`/api/targets/${id}/run`, { method: 'POST' });
                if (!res.ok) throw new Error('Unsuccessful');
                // Poll immediately
                setTimeout(() => this.loadData(), 1000);
            } catch (e) {
                console.error(e);
                if (t) t.running = false;
            }
        },

        async deleteTarget(id) {
            if (!confirm('Are you sure you want to delete this channel?')) return;
            try {
                const res = await Utils.authFetch(`/api/targets/${id}`, { method: 'DELETE' });
                if (!res.ok) throw new Error('Delete failed');
                await this.loadData();
            } catch (e) {
                alert('Failed to delete: ' + e.message);
            }
        },

        async toggleEnabled(t) {
            try {
                await Utils.authFetch(`/api/targets/${t.id}`, {
                    method: 'PATCH',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ enabled: !t.enabled })
                });
                this.loadData();
            } catch (e) { console.error(e); }
        },

        // Helper for rate color
        getColorForRate(rate) {
            if (!rate) return 'text-zinc-500';
            if (rate >= 90) return 'text-green-500';
            if (rate >= 50) return 'text-yellow-500';
            return 'text-red-500';
        }
    }
}
