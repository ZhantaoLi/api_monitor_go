(function () {
    const dom = {
        byId(id) {
            return document.getElementById(id);
        }
    };

    function showAlert(type, message) {
        const box = dom.byId('admin-alert') || dom.byId('login-message');
        if (!box) return;
        box.classList.remove('hidden');
        box.textContent = message || '';

        box.classList.remove(
            'bg-emerald-100', 'text-emerald-700', 'border', 'border-emerald-200',
            'bg-rose-100', 'text-rose-700', 'border-rose-200',
            'bg-amber-100', 'text-amber-700', 'border-amber-200'
        );

        if (type === 'success') {
            box.classList.add('bg-emerald-100', 'text-emerald-700', 'border', 'border-emerald-200');
            return;
        }
        if (type === 'warning') {
            box.classList.add('bg-amber-100', 'text-amber-700', 'border', 'border-amber-200');
            return;
        }
        box.classList.add('bg-rose-100', 'text-rose-700', 'border', 'border-rose-200');
    }

    function clearAlert() {
        const box = dom.byId('admin-alert') || dom.byId('login-message');
        if (!box) return;
        box.classList.add('hidden');
        box.textContent = '';
    }

    async function apiJSON(url, options = {}, redirectOnUnauthorized = true) {
        const headers = {
            ...(options.headers || {})
        };
        if (options.body && !headers['Content-Type']) {
            headers['Content-Type'] = 'application/json';
        }

        const res = await fetch(url, {
            ...options,
            headers,
            credentials: 'same-origin'
        });

        let data = {};
        try {
            data = await res.json();
        } catch (err) {
            data = {};
        }

        if (res.status === 401 && redirectOnUnauthorized) {
            window.location.href = '/admin/login';
            throw new Error('admin login required');
        }

        if (!res.ok) {
            throw new Error(data.detail || `HTTP ${res.status}`);
        }
        return data;
    }

    function parseIntStrict(raw, def) {
        const n = Number.parseInt(String(raw ?? ''), 10);
        if (Number.isNaN(n)) return def;
        return n;
    }

    function togglePasswordInput(input, iconEl) {
        if (!input || !iconEl) return;
        const hidden = input.type === 'password';
        input.type = hidden ? 'text' : 'password';
        iconEl.classList.remove('ph-eye', 'ph-eye-slash');
        iconEl.classList.add(hidden ? 'ph-eye-slash' : 'ph-eye');
    }

    function randomToken(prefix = '', bytes = 16) {
        const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';
        const arr = new Uint8Array(bytes);
        if (window.crypto && window.crypto.getRandomValues) {
            window.crypto.getRandomValues(arr);
        } else {
            for (let i = 0; i < arr.length; i++) {
                arr[i] = Math.floor(Math.random() * 256);
            }
        }
        let out = prefix;
        for (let i = 0; i < arr.length; i++) {
            out += chars[arr[i] % chars.length];
        }
        return out;
    }

    function esc(text) {
        return String(text ?? '')
            .replaceAll('&', '&amp;')
            .replaceAll('<', '&lt;')
            .replaceAll('>', '&gt;')
            .replaceAll('"', '&quot;')
            .replaceAll("'", '&#39;');
    }

    const AdminPage = {
        item: null,
        channels: [],
        editingChannelId: null,

        bindThemeToggle() {
            const btn = dom.byId('theme-toggle-btn');
            if (!btn) return;
            btn.addEventListener('click', () => Utils.toggleTheme());
        },

        bindVisibilityToggle(buttonId, inputId) {
            const btn = dom.byId(buttonId);
            const input = dom.byId(inputId);
            if (!btn || !input) return;
            const icon = btn.querySelector('i');
            btn.addEventListener('click', () => togglePasswordInput(input, icon));
        },

        bindChannelModalControls() {
            const closeBtn = dom.byId('channel-advanced-close-btn');
            const cancelBtn = dom.byId('channel-advanced-cancel-btn');
            const saveBtn = dom.byId('channel-advanced-save-btn');
            const backdrop = dom.byId('channel-advanced-backdrop');
            const tbody = dom.byId('admin-channel-list-body');

            if (closeBtn) closeBtn.addEventListener('click', () => this.closeChannelAdvancedModal());
            if (cancelBtn) cancelBtn.addEventListener('click', () => this.closeChannelAdvancedModal());
            if (backdrop) backdrop.addEventListener('click', () => this.closeChannelAdvancedModal());
            if (saveBtn) saveBtn.addEventListener('click', () => this.saveChannelAdvanced());
            if (tbody) {
                tbody.addEventListener('click', (e) => {
                    const btn = e.target.closest('[data-action="edit-advanced"]');
                    if (!btn) return;
                    const id = parseIntStrict(btn.dataset.id, 0);
                    if (id > 0) {
                        this.openChannelAdvancedModal(id);
                    }
                });
            }

            window.addEventListener('keydown', (e) => {
                if (e.key === 'Escape') {
                    this.closeChannelAdvancedModal();
                }
            });
        },

        async initLogin() {
            this.bindThemeToggle();
            this.bindVisibilityToggle('toggle-password-btn', 'admin-password');

            const form = dom.byId('admin-login-form');
            const submitBtn = dom.byId('login-submit-btn');
            const passwordInput = dom.byId('admin-password');
            if (!form || !submitBtn || !passwordInput) return;

            clearAlert();

            form.addEventListener('submit', async (e) => {
                e.preventDefault();
                clearAlert();
                submitBtn.disabled = true;
                submitBtn.textContent = 'Signing in...';

                try {
                    await apiJSON('/api/admin/login', {
                        method: 'POST',
                        body: JSON.stringify({
                            password: passwordInput.value || ''
                        })
                    }, false);
                    showAlert('success', 'Login successful, redirecting...');
                    setTimeout(() => {
                        window.location.href = '/admin.html';
                    }, 250);
                } catch (err) {
                    showAlert('error', err.message || 'Login failed');
                } finally {
                    submitBtn.disabled = false;
                    submitBtn.textContent = 'Sign In';
                }
            });
        },

        async initPanel() {
            this.bindThemeToggle();
            this.bindVisibilityToggle('toggle-api-token-btn', 'api-monitor-token');
            this.bindVisibilityToggle('toggle-admin-password-btn', 'admin-panel-password');
            this.bindVisibilityToggle('toggle-proxy-token-btn', 'proxy-master-token');
            this.bindChannelModalControls();

            const saveGeneralBtn = dom.byId('save-general-btn');
            const logoutBtn = dom.byId('logout-btn');
            const randomProxyTokenBtn = dom.byId('random-proxy-token-btn');
            const refreshChannelsBtn = dom.byId('refresh-channels-btn');

            if (saveGeneralBtn) {
                saveGeneralBtn.addEventListener('click', () => this.saveGeneral());
            }
            if (randomProxyTokenBtn) {
                randomProxyTokenBtn.addEventListener('click', () => {
                    const input = dom.byId('proxy-master-token');
                    if (input) input.value = randomToken('sk-', 32);
                });
            }
            if (refreshChannelsBtn) {
                refreshChannelsBtn.addEventListener('click', () => this.loadChannels());
            }
            if (logoutBtn) {
                logoutBtn.addEventListener('click', async () => {
                    try {
                        await apiJSON('/api/admin/logout', { method: 'POST' }, false);
                    } catch (err) {
                        // ignore logout error
                    }
                    window.location.href = '/admin/login';
                });
            }

            await Promise.all([this.loadSettings(), this.loadChannels()]);
        },

        setBusy(buttonIds, busy, busyTextMap = {}) {
            for (const id of buttonIds) {
                const btn = dom.byId(id);
                if (!btn) continue;
                btn.disabled = !!busy;
                if (busy && busyTextMap[id]) {
                    btn.dataset.oldText = btn.textContent;
                    btn.textContent = busyTextMap[id];
                } else if (!busy && btn.dataset.oldText) {
                    btn.textContent = btn.dataset.oldText;
                    delete btn.dataset.oldText;
                }
            }
        },

        applySettings(item) {
            this.item = item || null;
            if (!this.item) return;

            const apiMonitorTokenInput = dom.byId('api-monitor-token');
            const adminPasswordInput = dom.byId('admin-panel-password');
            const proxyInput = dom.byId('proxy-master-token');
            const cleanupEnabledInput = dom.byId('log-cleanup-enabled');
            const cleanupSizeInput = dom.byId('log-max-size-mb');

            if (apiMonitorTokenInput) apiMonitorTokenInput.value = this.item.api_monitor_token || '';
            if (adminPasswordInput) adminPasswordInput.value = this.item.admin_panel_password || '';
            if (proxyInput) proxyInput.value = this.item.proxy_master_token || '';
            if (cleanupEnabledInput) cleanupEnabledInput.checked = !!this.item.log_cleanup_enabled;
            if (cleanupSizeInput) cleanupSizeInput.value = this.item.log_max_size_mb ?? 500;
        },

        collectGeneralPayload() {
            const apiMonitorToken = String(dom.byId('api-monitor-token')?.value || '').trim();
            const adminPanelPassword = String(dom.byId('admin-panel-password')?.value || '').trim();
            const token = String(dom.byId('proxy-master-token')?.value || '').trim();
            const cleanupEnabled = !!dom.byId('log-cleanup-enabled')?.checked;
            const maxMB = parseIntStrict(dom.byId('log-max-size-mb')?.value, 500);

            if (!apiMonitorToken || apiMonitorToken.length > 256) {
                throw new Error('api_monitor_token must be 1-256 chars');
            }
            if (!adminPanelPassword || adminPanelPassword.length > 256) {
                throw new Error('admin_panel_password must be 1-256 chars');
            }
            if (maxMB < 0 || maxMB > 102400) {
                throw new Error('log_max_size_mb must be between 0 and 102400');
            }

            return {
                api_monitor_token: apiMonitorToken,
                admin_panel_password: adminPanelPassword,
                proxy_master_token: token,
                log_cleanup_enabled: cleanupEnabled,
                log_max_size_mb: maxMB
            };
        },

        async saveGeneral() {
            clearAlert();
            const buttonId = 'save-general-btn';

            try {
                const payload = this.collectGeneralPayload();
                this.setBusy([buttonId], true, {
                    [buttonId]: 'Saving...'
                });

                const data = await apiJSON('/api/admin/settings', {
                    method: 'PATCH',
                    body: JSON.stringify(payload)
                });

                this.applySettings(data.item || null);
                showAlert('success', 'Global settings updated.');
            } catch (err) {
                showAlert('error', err.message || 'Failed to update global settings');
            } finally {
                this.setBusy([buttonId], false);
            }
        },

        async loadSettings() {
            try {
                const data = await apiJSON('/api/admin/settings');
                this.applySettings(data.item || null);
            } catch (err) {
                showAlert('error', err.message || 'Failed to load settings');
            }
        },

        renderChannels() {
            const tbody = dom.byId('admin-channel-list-body');
            const empty = dom.byId('admin-channel-empty');
            if (!tbody) return;

            tbody.innerHTML = '';
            if (!Array.isArray(this.channels) || this.channels.length === 0) {
                if (empty) empty.classList.remove('hidden');
                return;
            }
            if (empty) empty.classList.add('hidden');

            for (const ch of this.channels) {
                const tr = document.createElement('tr');
                tr.className = 'hover:bg-zinc-50 dark:hover:bg-zinc-800/40 transition-colors';
                tr.innerHTML = `
                    <td class="px-4 py-3">
                        <div class="font-semibold text-zinc-800 dark:text-zinc-100">${esc(ch.name || '')}</div>
                        <div class="text-xs text-zinc-500 mt-1">${ch.enabled ? 'Enabled' : 'Disabled'}</div>
                    </td>
                    <td class="px-4 py-3">
                        <div class="font-mono text-xs text-zinc-600 dark:text-zinc-300 truncate max-w-[320px]" title="${esc(ch.base_url || '')}">
                            ${esc(ch.base_url || '')}
                        </div>
                    </td>
                    <td class="px-4 py-3 text-zinc-600 dark:text-zinc-300">
                        ${esc(ch.interval_min ?? '--')} min
                    </td>
                    <td class="px-4 py-3 text-zinc-600 dark:text-zinc-300">
                        ${esc(ch.max_models ?? 0)}
                    </td>
                    <td class="px-4 py-3 text-right">
                        <button type="button" data-action="edit-advanced" data-id="${esc(ch.id)}"
                            class="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-semibold transition-colors">
                            <i class="ph-bold ph-sliders"></i>
                            Edit Advanced
                        </button>
                    </td>
                `;
                tbody.appendChild(tr);
            }
        },

        async loadChannels() {
            clearAlert();
            this.setBusy(['refresh-channels-btn'], true, { 'refresh-channels-btn': 'Loading...' });
            try {
                const data = await apiJSON('/api/admin/channels');
                this.channels = Array.isArray(data.items) ? data.items : [];
                this.renderChannels();
            } catch (err) {
                showAlert('error', err.message || 'Failed to load channels');
            } finally {
                this.setBusy(['refresh-channels-btn'], false);
            }
        },

        openChannelAdvancedModal(channelID) {
            const channel = this.channels.find((c) => Number(c.id) === Number(channelID));
            if (!channel) return;

            this.editingChannelId = Number(channel.id);
            const modal = dom.byId('channel-advanced-modal');
            const meta = dom.byId('channel-advanced-meta');

            if (meta) {
                meta.textContent = `${channel.name || ''}  |  ${channel.base_url || ''}`;
            }
            const maxModelsInput = dom.byId('channel-advanced-max-models');
            const promptInput = dom.byId('channel-advanced-prompt');
            const verifySSLInput = dom.byId('channel-advanced-verify-ssl');
            const anthropicVersionInput = dom.byId('channel-advanced-anthropic-version');

            if (maxModelsInput) maxModelsInput.value = channel.max_models ?? 0;
            if (promptInput) promptInput.value = channel.prompt || '';
            if (verifySSLInput) verifySSLInput.checked = !!channel.verify_ssl;
            if (anthropicVersionInput) anthropicVersionInput.value = channel.anthropic_version || '';

            this.setChannelAdvancedError('');
            if (modal) {
                modal.classList.remove('hidden');
                modal.classList.add('flex');
            }
        },

        closeChannelAdvancedModal() {
            const modal = dom.byId('channel-advanced-modal');
            if (modal) {
                modal.classList.add('hidden');
                modal.classList.remove('flex');
            }
            this.editingChannelId = null;
            this.setChannelAdvancedError('');
        },

        setChannelAdvancedError(message) {
            const box = dom.byId('channel-advanced-error');
            if (!box) return;
            if (!message) {
                box.classList.add('hidden');
                box.textContent = '';
                return;
            }
            box.classList.remove('hidden');
            box.textContent = message;
        },

        collectChannelAdvancedPayload() {
            const maxModels = parseIntStrict(dom.byId('channel-advanced-max-models')?.value, 0);
            const prompt = String(dom.byId('channel-advanced-prompt')?.value || '').trim();
            const verifySSL = !!dom.byId('channel-advanced-verify-ssl')?.checked;
            const anthropicVersion = String(dom.byId('channel-advanced-anthropic-version')?.value || '').trim();

            if (maxModels < 0 || maxModels > 5000) {
                throw new Error('max_models must be between 0 and 5000');
            }
            if (!prompt || prompt.length > 4000) {
                throw new Error('prompt must be 1-4000 chars');
            }
            if (anthropicVersion.length < 4 || anthropicVersion.length > 64) {
                throw new Error('anthropic_version must be 4-64 chars');
            }

            return {
                max_models: maxModels,
                prompt,
                verify_ssl: verifySSL,
                anthropic_version: anthropicVersion
            };
        },

        async saveChannelAdvanced() {
            const id = this.editingChannelId;
            if (!id) return;

            let payload;
            try {
                payload = this.collectChannelAdvancedPayload();
                this.setChannelAdvancedError('');
            } catch (err) {
                this.setChannelAdvancedError(err.message || 'Invalid advanced settings');
                return;
            }

            const saveButtonId = 'channel-advanced-save-btn';
            this.setBusy([saveButtonId], true, { [saveButtonId]: 'Saving...' });
            try {
                const data = await apiJSON(`/api/admin/channels/${id}/advanced`, {
                    method: 'PATCH',
                    body: JSON.stringify(payload)
                });
                const item = data.item || null;
                if (item) {
                    this.channels = this.channels.map((ch) => (Number(ch.id) === Number(item.id) ? item : ch));
                    this.renderChannels();
                } else {
                    await this.loadChannels();
                }
                this.closeChannelAdvancedModal();
                showAlert('success', 'Channel advanced settings updated.');
            } catch (err) {
                const msg = err.message || 'Failed to update advanced settings';
                this.setChannelAdvancedError(msg);
                showAlert('error', msg);
            } finally {
                this.setBusy([saveButtonId], false);
            }
        }
    };

    document.addEventListener('DOMContentLoaded', () => {
        const page = document.body?.dataset?.page;
        if (page === 'admin-login') {
            AdminPage.initLogin();
            return;
        }
        if (page === 'admin-panel') {
            AdminPage.initPanel();
        }
    });
})();
