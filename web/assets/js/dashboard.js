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
        authRole: 'visitor',

        defaultForm: {
            name: '', base_url: '', api_key: '', source_url: '',
            interval_min: 30, timeout_s: 30
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
                const permissions = data.permissions || {};
                this.authRole = permissions.role || 'visitor';
            } catch (e) {
                console.error('Failed to load targets', e);
            }
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

        async copyConfigQuick(event, t) {
            await this.copyConfig(t);
            const btn = event?.currentTarget;
            const icon = btn?.querySelector('i');
            if (!icon) return;
            const oldClass = icon.className;
            icon.className = 'ph-bold ph-check text-emerald-500';
            setTimeout(() => {
                icon.className = oldClass;
            }, 1200);
        },

        async submitForm() {
            if (!this.form.name || !this.form.base_url || !this.form.api_key) {
                this.formError = 'Please fill in required fields (Name, URL, Key)';
                return;
            }

            try {
                const payload = {
                    name: this.form.name,
                    base_url: this.form.base_url,
                    api_key: this.form.api_key,
                    source_url: this.form.source_url ?? null,
                    interval_min: this.form.interval_min,
                    timeout_s: this.form.timeout_s
                };
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
        },

        successRateValue(target) {
            if (!target) return 0;
            if (Number.isFinite(target.last_success_rate)) {
                return Math.max(0, Math.min(100, Number(target.last_success_rate)));
            }
            const total = Number(target.last_total || 0);
            const success = Number(target.last_success || 0);
            if (total <= 0) return 0;
            return Math.max(0, Math.min(100, (success * 100) / total));
        },

        successSummaryText(target) {
            const total = Number(target?.last_total || 0);
            const success = Number(target?.last_success || 0);
            const rate = this.successRateValue(target);
            const rateText = Number.isInteger(rate) ? String(rate) : rate.toFixed(1).replace(/\.0$/, '');
            return `${success} / ${total} = ${rateText}%`;
        },

        channelModelCounts(target) {
            const models = Array.isArray(target?.latest_models) ? target.latest_models : [];
            const counts = {
                openai: 0,
                anthropic: 0,
                gemini: 0,
                other: 0,
                total: models.length
            };
            for (const model of models) {
                const protocol = String(model?.protocol || '').toLowerCase();
                if (protocol.includes('openai')) {
                    counts.openai += 1;
                } else if (protocol.includes('anthropic') || protocol.includes('claude')) {
                    counts.anthropic += 1;
                } else if (protocol.includes('gemini') || protocol.includes('google')) {
                    counts.gemini += 1;
                } else {
                    counts.other += 1;
                }
            }
            return counts;
        },

        historyPointCount(history) {
            return Array.isArray(history) ? history.length : 0;
        },

        getModelHistoryPoints(history, maxPoints = 30) {
            const src = Array.isArray(history) ? history : [];
            const normalized = src.slice(-maxPoints).map(p => ({ ...p, _placeholder: false }));
            const missing = maxPoints - normalized.length;
            if (missing <= 0) return normalized;
            const placeholders = Array.from({ length: missing }, () => ({
                _placeholder: true,
                success: null,
                duration: null,
                timestamp: null,
                status_code: null,
                error: null
            }));
            return placeholders.concat(normalized);
        },

        historyBarClass(point) {
            if (!point || point._placeholder) {
                return 'bg-zinc-200/70 dark:bg-zinc-700/40';
            }
            if (point.success) {
                return 'bg-emerald-500';
            }
            if (point.status_code != null && point.status_code >= 500) {
                return 'bg-rose-500';
            }
            if (point.status_code != null && point.status_code >= 400) {
                return 'bg-amber-500';
            }
            return 'bg-amber-400';
        },

        historyStatusLabel(point) {
            if (!point || point._placeholder) return 'NO DATA';
            return point.success ? '正常' : '失败';
        },

        historyLatencyLabel(point) {
            if (!point || point._placeholder || point.duration == null) return '--';
            const ms = Math.round(Number(point.duration) * 1000);
            return `${ms} ms`;
        },

        historyCodeLabel(point) {
            if (!point || point._placeholder || point.status_code == null) return '--';
            return String(point.status_code);
        },

        historyTimeLabel(point) {
            if (!point || point._placeholder || point.timestamp == null) return '--';
            return Utils.fmtTime(point.timestamp);
        },

        nextUpdateText(target) {
            if (!target || !target.interval_min) return 'NEXT UPDATE --';
            if (!target.last_run_at) return 'NEXT UPDATE SOON';
            const now = Date.now() / 1000;
            const eta = Number(target.last_run_at) + Number(target.interval_min) * 60 - now;
            if (!Number.isFinite(eta) || eta <= 0) return 'NEXT UPDATE SOON';
            const mm = Math.floor(eta / 60);
            const ss = Math.floor(eta % 60);
            return `NEXT IN ${mm}M ${String(ss).padStart(2, '0')}S`;
        },

        // Drag and Drop Logic
        dragSourceId: null,

        get canDrag() {
            return !this.search && this.filterProtocol === 'all' && this.filterStatus === 'all';
        },

        handleDragStart(e, id) {
            if (!this.canDrag) {
                e.preventDefault();
                return;
            }
            this.dragSourceId = id;
            e.dataTransfer.effectAllowed = 'move';
            e.dataTransfer.setData('text/plain', id);

            // Set the drag image to the entire card
            const card = e.target.closest('[data-channel-card]');
            if (card) {
                // Use the card element as the drag image
                e.dataTransfer.setDragImage(card, 20, 20);
                // Defer adding transparency so the drag image (ghost) captured by the browser remains fully opaque
                requestAnimationFrame(() => {
                    card.style.opacity = '0.5';
                });
            }
        },

        handleDragEnd(e) {
            this.dragSourceId = null;
            if (e.target && e.target.closest('[data-channel-card]')) {
                e.target.closest('[data-channel-card]').style.opacity = '1';
            }
        },

        handleDragOver(e) {
            if (this.canDrag) {
                e.preventDefault(); // Necessary to allow dropping
                e.dataTransfer.dropEffect = 'move';
            }
        },

        handleDrop(e, targetId) {
            if (!this.canDrag || !this.dragSourceId || this.dragSourceId === targetId) return;

            const fromIndex = this.targets.findIndex(t => t.id === this.dragSourceId);
            const toIndex = this.targets.findIndex(t => t.id === targetId);

            if (fromIndex > -1 && toIndex > -1) {
                // Remove from old position
                const [item] = this.targets.splice(fromIndex, 1);
                // Insert at new position
                this.targets.splice(toIndex, 0, item);
            }
            this.dragSourceId = null;
        }
    }
}
