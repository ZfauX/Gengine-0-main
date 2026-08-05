// static/js/app.js
// Loading indicators, autocomplete, inline validation, toast notifications, offline detection, push subscriptions, file upload progress, auto-save drafts

// =============================================================================
// i18n helper: read translated strings from <body> data attributes
// =============================================================================
function tI18n(key, fallback) {
    var body = document.body;
    if (!body) return fallback || key;
    var val = body.getAttribute('data-i18n-' + key);
    return (val != null && val !== '') ? val : (fallback || key);
}

// =============================================================================
// UX1: Global online/offline detector with toast notification
// =============================================================================
function initOfflineDetector() {
    function updateOnlineStatus() {
        var offlineMsg = tI18n('offline', 'Соединение потеряно. Изменения могут не сохраниться.');
        var onlineMsg = tI18n('online', 'Соединение восстановлено.');
        if (!navigator.onLine) {
            showToast(offlineMsg, 'warning', 0);
        } else {
            // Убираем «вечный» warning-тост и показываем восстановление.
            removeWarningToasts();
            showToast(onlineMsg, 'success', 3000);
        }
    }

    window.addEventListener('offline', updateOnlineStatus);
    window.addEventListener('online', updateOnlineStatus);
}

// Удаляет «вечные» warning-тосты (duration=0), чтобы они не копились при флапах сети.
function removeWarningToasts() {
    var container = document.getElementById('toast-container');
    if (!container) return;
    var toasts = container.querySelectorAll('.toast-warning');
    toasts.forEach(function(t) {
        t.style.transition = 'opacity .3s';
        t.style.opacity = '0';
        setTimeout(function() { t.remove(); }, 350);
    });
}

// =============================================================================
// Toast notification system
// =============================================================================
function initToast() {
    var container = document.getElementById('toast-container');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toast-container';
        container.className = 'fixed top-4 right-4 z-[10050] space-y-2';
        document.body.appendChild(container);
    }

    window.showToast = function(message, type, duration) {
        type = type || 'info';
        duration = duration || 4000;

        var icons = {
            success: '✅',
            error: '❌',
            info: 'ℹ️',
            warning: '⚠️'
        };

        var toast = document.createElement('div');
        toast.className = 'toast toast-' + type + ' transition-all duration-300 ease-in-out';
        var closeBtn = document.createElement('button');
        closeBtn.className = 'shrink-0 text-gray-400 hover:text-gray-600';
        closeBtn.setAttribute('aria-label', tI18n('close', 'Закрыть'));
        closeBtn.innerHTML = '&times;';
        closeBtn.addEventListener('click', function() { toast.remove(); });
        toast.innerHTML = '<div class="flex items-start gap-3">' +
            '<span class="text-lg shrink-0">' + (icons[type] || icons.info) + '</span>' +
            '<div class="flex-1">' + escapeHtml(message) + '</div>' +
            '</div>';
        toast.appendChild(closeBtn);

        container.appendChild(toast);

        if (duration > 0) {
            setTimeout(function() {
                toast.style.opacity = '0';
                toast.style.transform = 'translateX(100%)';
                setTimeout(function() {
                    if (toast.parentElement) toast.parentElement.removeChild(toast);
                }, 300);
            }, duration);
        }
    };
}

// =============================================================================
// UX2: Loading indicators for all forms — all mutating forms get spinner
// =============================================================================
function initFormLoading() {
    var forms = document.querySelectorAll('form');
    forms.forEach(function(form) {
        form.addEventListener('submit', function(e) {
            // Формы с кастомным подтверждением (data-confirm-form) могут быть отменены
            // document-обработчиком — не блокируем кнопку заранее, иначе после «Отмена»
            // она останется залипшей со спиннером.
            if (form.hasAttribute('data-confirm-form')) return;
            var btn = this.querySelector('button[type="submit"]');
            if (btn && !btn.dataset.noLoading) {
                btn.disabled = true;
                btn.innerHTML = '<span class="inline-block animate-spin mr-1">\u27F3</span> ' + (btn.dataset.loadingText || tI18n('sending'));
                btn.classList.add('opacity-70', 'cursor-not-allowed');
            }
        });
    });
}

// =============================================================================
// UX3: Modal confirm dialog for dangerous actions (replaces native confirm())
// =============================================================================
function initConfirmDialogs() {
    var confirmButtons = document.querySelectorAll('[data-confirm]');
    confirmButtons.forEach(function(button) {
        button.addEventListener('click', async function(e) {
            var message = this.getAttribute('data-confirm');
            if (this.dataset.__confirming) {
                delete this.dataset.__confirming;
                return;
            }
            e.preventDefault();
            var confirmed = await showModalConfirm(message, this);
            if (confirmed) {
                this.dataset.__confirming = '1';
                this.click();
            }
        });
    });

    // Handle data-confirm-form for form submissions (async modal instead of native confirm)
    document.addEventListener('submit', async function(e) {
        var form = e.target.closest('[data-confirm-form]');
        if (!form) return;
        e.preventDefault();
        var message = form.getAttribute('data-confirm-form');
        var confirmed = await showModalConfirm(message, form);
        if (confirmed) {
            // Remove the attribute to prevent loop, then submit
            form.removeAttribute('data-confirm-form');
            form.submit();
        }
    });
}

function showModalConfirm(message, element) {
    var existing = document.getElementById('confirm-modal');
    if (existing) existing.remove();

    var resolvePromise;
    var promise = new Promise(function(resolve) {
        resolvePromise = resolve;
    });

    var body = document.body;
    var title = body.getAttribute('data-i18n-confirm-title') || 'Подтверждение';
    var cancelText = body.getAttribute('data-i18n-confirm-cancel') || 'Отмена';
    // Per-action OK label (UX2): элемент может задать data-confirm-ok,
    // иначе — глобальный текст (по умолчанию «Удалить» для деструктивных действий).
    var okText = (element && element.dataset && element.dataset.confirmOk)
        ? element.dataset.confirmOk
        : (body.getAttribute('data-i18n-confirm-ok') || 'Удалить');

    var overlay = document.createElement('div');
    overlay.id = 'confirm-modal';
    overlay.className = 'fixed inset-0 z-[10000] flex items-center justify-center bg-black/50';
    // A11y (UX4): роль диалога для скринридеров + aria-labelledby на заголовок.
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-modal', 'true');
    overlay.setAttribute('aria-labelledby', 'confirm-modal-title');
    overlay.innerHTML =
        '<div class="bg-white rounded-xl shadow-2xl p-6 max-w-md mx-4 w-full">' +
        '<div id="confirm-modal-title" class="text-xl font-semibold mb-2">' + title + '</div>' +
        '<p class="text-gray-600 mb-6">' + escapeHtml(message) + '</p>' +
        '<div class="flex justify-end gap-3">' +
        '<button id="confirm-cancel" class="px-4 py-2 text-gray-600 hover:text-gray-800 bg-gray-100 rounded-lg hover:bg-gray-200 transition">' + cancelText + '</button>' +
        '<button id="confirm-ok" class="px-4 py-2 bg-red-600 text-white rounded-lg hover:bg-red-700 transition">' + okText + '</button>' +
        '</div>' +
        '</div>';

    document.body.appendChild(overlay);

    // A11y: возвращаем фокус на элемент, вызвавший модалку (UX9).
    var trigger = (element && typeof element.focus === 'function') ? element : document.activeElement;

    function finish(result) {
        overlay.remove();
        if (trigger && typeof trigger.focus === 'function') {
            try { trigger.focus(); } catch (_) {}
        }
        resolvePromise(result);
    }

    // Focus trap — Tab/Shift+Tab cycle within modal
    var focusableSelector = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';
    var focusableElements = overlay.querySelectorAll(focusableSelector);
    var firstFocusable = focusableElements[0];
    var lastFocusable = focusableElements[focusableElements.length - 1];

    overlay.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') {
            finish(false);
            return;
        }
        if (e.key === 'Tab') {
            if (e.shiftKey) {
                if (document.activeElement === firstFocusable) {
                    e.preventDefault();
                    if (lastFocusable) lastFocusable.focus();
                }
            } else {
                if (document.activeElement === lastFocusable) {
                    e.preventDefault();
                    if (firstFocusable) firstFocusable.focus();
                }
            }
        }
    });

    // Move focus to first focusable element in modal
    if (firstFocusable) {
        firstFocusable.focus();
    }

    document.getElementById('confirm-cancel').addEventListener('click', function() {
        finish(false);
    });
    document.getElementById('confirm-ok').addEventListener('click', function() {
        finish(true);
    });
    overlay.addEventListener('click', function(e) {
        if (e.target === overlay) {
            finish(false);
        }
    });

    return promise;
}


// =============================================================================
// UX15: Unified reconnecting WebSocket client
// Единый механизм соединения/переподключения для чатов и мониторинга.
// Usage:
//   const ws = window.createReconnectingWebSocket(url, {
//       onMessage: function(e) { ... },
//       onStatus: function(name, detail) { /* 'connecting'|'connected'|'reconnecting'|'error'|'failed'|'closed' */ },
//       maxAttempts: 5, baseDelay: 1000, maxDelay: 30000
//   });
//   ws.send(data); ws.isOpen(); ws.close();
// =============================================================================
window.createReconnectingWebSocket = function(url, opts) {
    opts = opts || {};
    var maxAttempts = opts.maxAttempts || 5;
    var baseDelay = opts.baseDelay || 1000;
    var maxDelay = opts.maxDelay || 30000;
    var onMessage = opts.onMessage || function() {};
    var onStatus = opts.onStatus || function() {};
    var onFinalClose = opts.onFinalClose || function() {};

    var socket = null;
    var attempts = 0;
    var reconnectTimer = null;
    var manuallyClosed = false;

    function connect() {
        if (manuallyClosed) return;
        onStatus('connecting');
        socket = new WebSocket(url);
        socket.onopen = function() {
            attempts = 0;
            onStatus('connected');
        };
        socket.onmessage = function(e) { onMessage(e, socket); };
        socket.onerror = function() { onStatus('error'); };
        socket.onclose = function(event) {
            if (manuallyClosed) {
                onStatus('closed');
                onFinalClose();
                return;
            }
            if (attempts >= maxAttempts) {
                onStatus('failed');
                onFinalClose();
                return;
            }
            var delay = Math.min(maxDelay, baseDelay * Math.pow(2, attempts));
            attempts++;
            onStatus('reconnecting', { attempt: attempts, delay: delay });
            if (reconnectTimer) clearTimeout(reconnectTimer);
            reconnectTimer = setTimeout(connect, delay);
        };
    }

    connect();

    return {
        send: function(data) {
            if (socket && socket.readyState === WebSocket.OPEN) socket.send(data);
        },
        isOpen: function() {
            return socket && socket.readyState === WebSocket.OPEN;
        },
        close: function() {
            manuallyClosed = true;
            if (reconnectTimer) clearTimeout(reconnectTimer);
            if (socket) {
                socket.onclose = null;
                try { socket.close(); } catch (_) {}
            }
            onStatus('closed');
        }
    };
};

// =============================================================================
// Push-подписка (Web Push) — кнопки #enable-push / #disable-push в профиле.
// Бэкенд: /api/push/vapid-public-key, /api/push/subscribe, /api/push/unsubscribe.
// =============================================================================
function initPushSubscription() {
    var enableBtn = document.getElementById('enable-push');
    var disableBtn = document.getElementById('disable-push');
    var statusEl = document.getElementById('push-status');
    if (!enableBtn) return;

    function setStatus(msg) {
        if (statusEl) statusEl.textContent = msg;
    }

    function urlBase64ToUint8Array(base64String) {
        var padding = '='.repeat((4 - base64String.length % 4) % 4);
        var base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
        var raw = window.atob(base64);
        var arr = new Uint8Array(raw.length);
        for (var i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i);
        return arr;
    }

    enableBtn.addEventListener('click', async function() {
        try {
            if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
                setStatus(tI18n('push.not_supported', 'Push не поддерживается этим браузером'));
                return;
            }
            var reg = await navigator.serviceWorker.register('/sw.js', { scope: '/' });
            var vapid = enableBtn.dataset.vapidKey;
            if (!vapid) {
                var resp = await fetch('/api/push/vapid-public-key', { credentials: 'same-origin' });
                var data = await resp.json();
                vapid = data.public_key;
            }
            if (!vapid) {
                setStatus(tI18n('push.error', 'Ошибка: ') + 'Не удалось получить VAPID-ключ');
                return;
            }
            var sub = await reg.pushManager.subscribe({
                userVisibleOnly: true,
                applicationServerKey: urlBase64ToUint8Array(vapid)
            });
            var saveResp = await fetch('/api/push/subscribe', {
                method: 'POST',
                credentials: 'same-origin',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(sub.toJSON())
            });
            if (!saveResp.ok) throw new Error('HTTP ' + saveResp.status);
            setStatus(tI18n('push.enabled', 'Push-уведомления включены'));
            enableBtn.classList.add('hidden');
            if (disableBtn) disableBtn.classList.remove('hidden');
        } catch (e) {
            setStatus(tI18n('push.error', 'Ошибка: ') + e.message);
        }
    });

    if (disableBtn) disableBtn.addEventListener('click', async function() {
        try {
            var reg = await navigator.serviceWorker.getRegistration();
            if (reg) {
                var sub = await reg.pushManager.getSubscription();
                if (sub) {
                    await fetch('/api/push/unsubscribe', {
                        method: 'POST',
                        credentials: 'same-origin',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ endpoint: sub.endpoint })
                    });
                    await sub.unsubscribe();
                }
            }
            setStatus(tI18n('push.disabled', 'Push-уведомления отключены'));
            enableBtn.classList.remove('hidden');
            disableBtn.classList.add('hidden');
        } catch (e) {
            setStatus(tI18n('push.error', 'Ошибка: ') + e.message);
        }
    });
}

// =============================================================================
// Init on DOM ready
// =============================================================================
document.addEventListener('DOMContentLoaded', function() {
    initToast();
    initFormLoading();
    initConfirmDialogs();
    initOfflineDetector();
    initPushSubscription();
});
