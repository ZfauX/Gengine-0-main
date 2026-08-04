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
        container.className = 'fixed top-4 right-4 z-[9999] space-y-2';
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
        closeBtn.className = 'shrink-0 text-gray-400 hover:text-gray-600';        closeBtn.setAttribute('aria-label', tI18n('confirm-cancel') || 'Закрыть');
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
    var okText = body.getAttribute('data-i18n-confirm-ok') || 'Удалить';

    var overlay = document.createElement('div');
    overlay.id = 'confirm-modal';
    overlay.className = 'fixed inset-0 z-[10000] flex items-center justify-center bg-black/50';
    overlay.innerHTML =
        '<div class="bg-white rounded-xl shadow-2xl p-6 max-w-md mx-4 w-full">' +
        '<div class="text-xl font-semibold mb-2">' + title + '</div>' +
        '<p class="text-gray-600 mb-6">' + escapeHtml(message) + '</p>' +
        '<div class="flex justify-end gap-3">' +
        '<button id="confirm-cancel" class="px-4 py-2 text-gray-600 hover:text-gray-800 bg-gray-100 rounded-lg hover:bg-gray-200 transition">' + cancelText + '</button>' +
        '<button id="confirm-ok" class="px-4 py-2 bg-red-600 text-white rounded-lg hover:bg-red-700 transition">' + okText + '</button>' +
        '</div>' +
        '</div>';

    document.body.appendChild(overlay);

    // Focus trap — Tab/Shift+Tab cycle within modal
    var focusableSelector = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';
    var focusableElements = overlay.querySelectorAll(focusableSelector);
    var firstFocusable = focusableElements[0];
    var lastFocusable = focusableElements[focusableElements.length - 1];

    overlay.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') {
            overlay.remove();
            resolvePromise(false);
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
        overlay.remove();
        resolvePromise(false);
    });
    document.getElementById('confirm-ok').addEventListener('click', function() {
        overlay.remove();
        resolvePromise(true);
    });
    overlay.addEventListener('click', function(e) {
        if (e.target === overlay) {
            overlay.remove();
            resolvePromise(false);
        }
    });

    return promise;
}

// =============================================================================
// UX6: File upload progress bar + image preview
// =============================================================================
function initFileUploadProgress() {
    // Track active upload per form
    var activeUploads = {};

    var fileInputs = document.querySelectorAll('input[type="file"][data-progress]');
    fileInputs.forEach(function(input) {
        var form = input.closest('form');
        if (!form) return;

        // Guard: only initialize each form once
        if (form.dataset.progressInitialized) return;
        form.dataset.progressInitialized = 'true';

        input.addEventListener('change', function() {
            var file = this.files[0];
            if (!file) return;

            // Image preview
            var previewContainer = document.getElementById(this.dataset.previewId);
            if (previewContainer && file.type.startsWith('image/')) {
                var reader = new FileReader();
                reader.onload = function(e) {
                    previewContainer.innerHTML = '<img src="' + e.target.result + '" class="max-h-48 rounded-lg shadow-md mt-2" alt="preview">';
                };
                reader.readAsDataURL(file);
            }

            var progressContainer = document.getElementById(this.dataset.progress);
            if (!progressContainer) {
                progressContainer = document.createElement('div');
                progressContainer.id = this.dataset.progress;
                progressContainer.className = 'mt-2';
                this.parentElement.appendChild(progressContainer);
            }
        });

        // Attach submit listener ONCE (outside change handler)
        form.addEventListener('submit', function(e) {
            var file = input.files[0];
            if (!file) return;

            // Prevent duplicate submissions
            var uploadKey = form.id || 'upload';
            if (activeUploads[uploadKey]) {
                showToast(tI18n('loading-already'), 'warning');
                e.preventDefault();
                return;
            }

            e.preventDefault();

            var xhr = new XMLHttpRequest();
            var formData = new FormData(form);
            var progressContainer = document.getElementById(input.dataset.progress);
            if (!progressContainer) {
                progressContainer = document.createElement('div');
                progressContainer.id = input.dataset.progress;
                progressContainer.className = 'mt-2';
                input.parentElement.appendChild(progressContainer);
            }

            var progressBar = progressContainer.querySelector('.progress-bar');
            var progressText = progressContainer.querySelector('.progress-text');

            if (!progressBar) {
                progressContainer.innerHTML =
                    '<div class="w-full bg-gray-200 rounded-full h-3">' +
                    '<div class="progress-bar bg-blue-600 h-3 rounded-full transition-all duration-300" style="width:0%"></div>' +
                    '</div>' +
                    '<div class="progress-text text-sm text-gray-500 mt-1">0%</div>';
            }

            // Disable submit button
            var submitBtn = form.querySelector('[type="submit"]');
            if (submitBtn) submitBtn.disabled = true;

            xhr.upload.addEventListener('progress', function(e) {
                if (e.lengthComputable) {
                    var percent = Math.round((e.loaded / e.total) * 100);
                    progressContainer.querySelector('.progress-bar').style.width = percent + '%';
                    progressContainer.querySelector('.progress-text').textContent = percent + '%';
                }
            });

            xhr.addEventListener('load', function() {
                if (xhr.status === 200) {
                    showToast(tI18n('file-uploaded'), 'success');
                } else {
                    showToast(tI18n('file-upload-error'), 'error');
                }
                progressContainer.innerHTML = '';
            });

            xhr.addEventListener('loadend', function() {
                // Re-enable submit button
                if (submitBtn) submitBtn.disabled = false;
                // Clear active upload flag
                delete activeUploads[uploadKey];
            });

            // Track active upload
            activeUploads[uploadKey] = true;

            xhr.open(form.method || 'POST', form.action);
            xhr.send(formData);
        });
    });
}

// =============================================================================
// UX7: Auto-save drafts to localStorage
// =============================================================================
function initAutoSaveDrafts() {
    var draftForms = document.querySelectorAll('[data-autosave]');
    draftForms.forEach(function(form) {
        var key = form.dataset.autosave;
        var fields = form.querySelectorAll('input:not([type="password"]):not([type="hidden"]):not([data-nosave]), textarea:not([data-nosave]), select:not([data-nosave])');

        // Restore draft on page load
        var draft = localStorage.getItem(key);
        if (draft) {
            try {
                var data = JSON.parse(draft);
                fields.forEach(function(field) {
                    if (field.name && data[field.name] !== undefined) {
                        field.value = data[field.name];
                    }
                });
                showToast(tI18n('draft-restored'), 'info', 3000);
            } catch (e) {
                localStorage.removeItem(key);
            }
        }

        // Save draft every 30 seconds
        var timer = setInterval(function() {
            var data = {};
            fields.forEach(function(field) {
                if (field.name) data[field.name] = field.value;
            });
            localStorage.setItem(key, JSON.stringify(data));
        }, 30000);

        // Clear draft on successful submit
        form.addEventListener('submit', function() {
            clearInterval(timer);
            localStorage.removeItem(key);
        });
    });
}

// =============================================================================
// UX5: Web Push subscription
// =============================================================================
function initPushSubscription() {
    var enableBtn = document.getElementById('enable-push');
    var disableBtn = document.getElementById('disable-push');
    var statusEl = document.getElementById('push-status');
    // enableBtn обязателен; disable-push и push-status могут отсутствовать
    // (например, на странице /settings/notifications) — работаем и без них.
    if (!enableBtn) return;

    function disableEnable(reason) {
        showDisabled(reason);
        enableBtn.disabled = true;
        enableBtn.classList.add('opacity-50', 'cursor-not-allowed');
    }

    // 1. Проверка поддержки Push API.
    // На не-secure origin (http://<IP>) Notification/ServiceWorker/PushManager недоступны —
    // раньше кнопка молча ничего не делала, теперь даём понятное сообщение.
    if (!('Notification' in window) || !('serviceWorker' in navigator) || !('PushManager' in window)) {
        disableEnable(tI18n('push-unsupported'));
        return;
    }

    // 2. Web Push работает только в secure context (HTTPS или localhost).
    if (window.isSecureContext === false) {
        disableEnable(tI18n('push-https-required'));
        return;
    }

    // 3. Нет VAPID-ключа на сервере — push не настроен.
    if (!enableBtn.dataset.vapidKey) {
        disableEnable(tI18n('push-misconfigured'));
        return;
    }

    function showEnabled() {
        enableBtn.classList.add('hidden');
        if (disableBtn) disableBtn.classList.remove('hidden');
        if (statusEl) {
            statusEl.textContent = tI18n('push-active');
            statusEl.className = 'text-xs text-green-600 dark:text-green-400 mt-2';
        }
    }

    function showDisabled(reason) {
        enableBtn.classList.remove('hidden');
        if (disableBtn) disableBtn.classList.add('hidden');
        if (statusEl) {
            statusEl.textContent = reason || tI18n('push-enable-hint');
            statusEl.className = 'text-xs text-gray-500 dark:text-gray-400 mt-2';
        }
    }

    // При загрузке — проверяем Notification.permission
    if (Notification.permission === 'granted') {
        showEnabled();
    } else {
        showDisabled();
    }

    enableBtn.addEventListener('click', function() {
        Notification.requestPermission().then(function(permission) {
            if (permission === 'granted') {
                showEnabled();
                // Подписка через SW (в фоне, UI уже обновлён)
                navigator.serviceWorker.ready.then(function(registration) {
                    return registration.pushManager.subscribe({
                        userVisibleOnly: true,
                        applicationServerKey: urlBase64ToUint8Array(enableBtn.dataset.vapidKey || '')
                    });
                }).then(function(subscription) {
                    fetch('/api/push/subscribe', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(subscription)
                    }).catch(function(e) {
                        console.warn('Push subscribe save failed:', e);
                        showToast(tI18n('push-save-failed'), 'error');
                    });
                }).catch(function(err) {
                    console.warn('Push SW subscribe failed:', err);
                });
            } else {
                showDisabled(tI18n('push-allow-browser'));
            }
        });
    });

    if (disableBtn) {
        disableBtn.addEventListener('click', function() {
            showDisabled(tI18n('push-disabled'));
            navigator.serviceWorker.ready.then(function(registration) {
                return registration.pushManager.getSubscription();
            }).then(function(subscription) {
                if (!subscription) return;
                var endpoint = subscription.endpoint;
                subscription.unsubscribe().then(function() {
                    fetch('/api/push/unsubscribe', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ endpoint: endpoint })
                    }).catch(function(e) { console.warn('Push unsubscribe save failed:', e); });
                });
            }).catch(function(err) {
                console.warn('Push unsubscribe failed:', err);
            });
        });
    }
}

// =============================================================================
// Helpers
// =============================================================================
function urlBase64ToUint8Array(base64String) {
    var padding = '='.repeat((4 - base64String.length % 4) % 4);
    var base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    var rawData = window.atob(base64);
    var output = new Uint8Array(rawData.length);
    for (var i = 0; i < rawData.length; ++i) {
        output[i] = rawData.charCodeAt(i);
    }
    return output;
}

function escapeHtml(text) {
    var map = {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;'};
    return String(text).replace(/[&<>"']/g, function(m) { return map[m]; });
}

// =============================================================================
// Search autocomplete — shows dropdown with games on input
// =============================================================================
function initSearchAutocomplete() {
    var searchInput = document.getElementById('search');
    if (!searchInput) return;

    var container = searchInput.parentElement;
    if (!container) return;

    var dropdown = document.createElement('div');
    dropdown.id = 'searchDropdown';
    dropdown.className = 'absolute z-50 mt-1 w-full bg-white rounded-lg shadow-lg border border-gray-200 max-h-60 overflow-y-auto hidden';
    dropdown.innerHTML = '<div class="p-3 text-sm text-gray-400 text-center">' + tI18n('start-typing-hint') + '</div>';
    container.style.position = 'relative';
    container.appendChild(dropdown);

    var debounceTimer = null;
    var selectedIndex = -1;

    searchInput.addEventListener('input', function() {
        clearTimeout(debounceTimer);
        var query = this.value.trim();

        if (query.length < 2) {
            dropdown.classList.add('hidden');
            return;
        }

        debounceTimer = setTimeout(function() {
            fetch('/api/search/games?q=' + encodeURIComponent(query))
                .then(function(r) {
                    if (!r.ok) { throw new Error('HTTP ' + r.status); }
                    return r.json();
                })
                .then(function(data) {
                    if (!data.results || data.results.length === 0) {
                        dropdown.innerHTML = '<div class="p-3 text-sm text-gray-400 text-center">' + tI18n('nothing-found') + '</div>';
                        dropdown.classList.remove('hidden');
                        return;
                    }

                    var html = '';
                    data.results.forEach(function(item, index) {
                        html += '<a href="/games/' + item.id + '" class="block px-3 py-2 hover:bg-blue-50 transition cursor-pointer search-item" data-index="' + index + '" data-id="' + item.id + '">' +
                                '<span class="font-medium text-gray-800">' + escapeHtml(item.name) + '</span>' +
                                '</a>';
                    });
                    dropdown.innerHTML = html;
                    dropdown.classList.remove('hidden');
                    selectedIndex = -1;

                    dropdown.querySelectorAll('.search-item').forEach(function(el) {
                        el.addEventListener('click', function(e) {
                            e.preventDefault();
                            var url = this.getAttribute('data-url') || this.getAttribute('href');
                            if (url) {
                                window.location.href = url;
                            }
                        });
                    });
                })
                .catch(function() {
                    dropdown.classList.add('hidden');
                });
        }, 250);
    });

    searchInput.addEventListener('keydown', function(e) {
        var items = dropdown.querySelectorAll('.search-item');
        if (!items.length) return;

        if (e.key === 'ArrowDown') {
            e.preventDefault();
            selectedIndex = Math.min(selectedIndex + 1, items.length - 1);
            updateSelection(items);
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            selectedIndex = Math.max(selectedIndex - 1, 0);
            updateSelection(items);
        } else if (e.key === 'Enter' && selectedIndex >= 0) {
            e.preventDefault();
            items[selectedIndex].click();
        } else if (e.key === 'Escape') {
            dropdown.classList.add('hidden');
        }
    });

    document.addEventListener('click', function(e) {
        if (!container.contains(e.target)) {
            dropdown.classList.add('hidden');
        }
    });

    function updateSelection(items) {
        items.forEach(function(el, i) {
            el.classList.toggle('bg-blue-50', i === selectedIndex);
        });
    }
}

// =============================================================================
// Inline validation for forms
// =============================================================================
function initInlineValidation() {
    var forms = document.querySelectorAll('form[data-inline-validation]');
    forms.forEach(function(form) {
        form.querySelectorAll('input[required], textarea[required]').forEach(function(input) {
            input.addEventListener('blur', function() {
                validateField(this);
            });
            input.addEventListener('input', function() {
                if (this.classList.contains('border-red-500')) {
                    validateField(this);
                }
            });
        });
    });

    function validateField(field) {
        var errorEl = document.getElementById('error-' + field.id);
        if (!field.checkValidity()) {
            field.classList.add('border-red-500', 'focus:border-red-500', 'focus:ring-red-500');
            field.classList.remove('border-gray-300', 'focus:border-blue-500', 'focus:ring-blue-500');
            if (errorEl) errorEl.textContent = field.validationMessage;
            return false;
        } else {
            field.classList.remove('border-red-500', 'focus:border-red-500', 'focus:ring-red-500');
            field.classList.add('border-gray-300', 'focus:border-blue-500', 'focus:ring-blue-500');
            if (errorEl) errorEl.textContent = '';
            return true;
        }
    }
}

// =============================================================================
// UX8: SSE game status notifications
// =============================================================================

function initSSEGameNotifications(gameId) {
    if (!gameId) return;

    var eventSource = null;
    var reconnectTimer = null;      // scoped per instance (not global)
    var reconnectAttempts = 0;
    var lastEventId = null;         // track for reconnect (Last-Event-ID)
    var MAX_RECONNECT_ATTEMPTS = 5;
    var BASE_RECONNECT_DELAY = 2000;
    var MAX_RECONNECT_DELAY = 30000;

    function connectSSE() {
        // Закрываем старый EventSource, если был
        if (eventSource) {
            eventSource.close();
        }

        // Отменяем старый reconnect timer
        if (reconnectTimer) {
            clearTimeout(reconnectTimer);
            reconnectTimer = null;
        }

        try {
            // Append lastEventId for event replay on reconnect
            var url = '/game/sse/' + gameId + (lastEventId ? '?lastEventId=' + encodeURIComponent(lastEventId) : '');
            eventSource = new EventSource(url);

            eventSource.onopen = function() {
                console.debug('SSE connected for game', gameId);
                reconnectAttempts = 0;
                document.body.setAttribute('data-sse-active', 'true');
            };

            function parseSSEData(e) {
                // Browser populates e.lastEventId from the SSE "id:" field
                if (e && e.lastEventId) lastEventId = e.lastEventId;
                try { return JSON.parse(e.data); } catch (_) { return null; }
            }

            eventSource.addEventListener('game_started', function(e) {
                var data = parseSSEData(e);
                if (data) showToast(tI18n('game-started'), 'success', 5000);
            });

            eventSource.addEventListener('game_finished', function(e) {
                var data = parseSSEData(e);
                if (data) showToast(tI18n('game-finished'), 'info', 8000);
            });

            eventSource.addEventListener('team_disqualified', function(e) {
                var data = parseSSEData(e);
                if (data && data.team_id) {
                    showToast(tI18n('team-disqualified'), 'error', 10000);
                }
            });

            eventSource.addEventListener('level_completed', function(e) {
                var data = parseSSEData(e);
                if (data && data.team_id) {
                    showToast(tI18n('level-passed'), 'success', 4000);
                }
            });

            eventSource.addEventListener('time_warning', function(e) {
                var data = parseSSEData(e);
                if (data && data.remaining_minutes) {
                    showToast(tI18n('time-left') + data.remaining_minutes + ' ' + (data.remaining_minutes === 1 ? tI18n('minute-unit') : tI18n('minutes-unit')) + ' ' + tI18n('until-finish'), 'warning', 5000);
                }
            });

            eventSource.addEventListener('hint_available', function(e) {
                var data = parseSSEData(e);
                if (data && data.level_number) {
                    showToast(tI18n('hint-available') + data.level_number, 'info', 4000);
                }
            });

            eventSource.onerror = function(err) {
                eventSource.close();
                document.body.removeAttribute('data-sse-active');
                if (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
                    console.warn('SSE: max reconnect attempts reached, stopping');
                    return;
                }
                reconnectAttempts++;
                var delay = Math.min(MAX_RECONNECT_DELAY, BASE_RECONNECT_DELAY * Math.pow(2, reconnectAttempts - 1));
                console.warn('SSE error, reconnecting in ' + Math.round(delay / 1000) + 's (attempt ' + reconnectAttempts + ')', err);
                reconnectTimer = setTimeout(connectSSE, delay);
            };
        } catch (e) {
            console.warn('SSE not supported, notifications disabled:', e);
        }
    }

    connectSSE();

    // Cleanup on page unload
    window.addEventListener('beforeunload', function() {
        if (reconnectTimer) clearTimeout(reconnectTimer);
        if (eventSource) eventSource.close();
    });
}

// =============================================================================
// UX9: Team rating indicators in lobby
// =============================================================================
function initTeamRatingIndicators() {
    var teamRows = document.querySelectorAll('.team-row');
    if (!teamRows.length) return;

    teamRows.forEach(function(row) {
        var placeEl = row.querySelector('[data-place]');
        var ratingEl = row.querySelector('[data-rating]');
        var scoreEl = row.querySelector('[data-score]');

        if (!placeEl && !ratingEl && !scoreEl) return;

        var place = placeEl ? parseInt(placeEl.textContent) || 0 : 0;
        var rating = ratingEl ? parseFloat(ratingEl.textContent) || 0 : 0;
        var score = scoreEl ? parseInt(scoreEl.textContent) || 0 : 0;

        // Place indicator
        if (place > 0) {
            var placeBadge = document.createElement('span');
            placeBadge.className = 'inline-flex items-center px-2 py-0.5 rounded text-xs font-medium';
            if (place === 1) {
                placeBadge.classList.add('bg-yellow-100', 'text-yellow-800');
                placeBadge.textContent = tI18n('rank-1');
            } else if (place === 2) {
                placeBadge.classList.add('bg-gray-100', 'text-gray-800');
                placeBadge.textContent = tI18n('rank-2');
            } else if (place === 3) {
                placeBadge.classList.add('bg-orange-100', 'text-orange-800');
                placeBadge.textContent = tI18n('rank-3');
            } else {
                placeBadge.classList.add('bg-blue-100', 'text-blue-800');
                placeBadge.textContent = '#' + place;
            }

            var placeContainer = row.querySelector('.team-place-indicator');
            if (!placeContainer) {
                placeContainer = document.createElement('span');
                placeContainer.className = 'team-place-indicator ml-2';
                row.insertBefore(placeContainer, row.firstChild);
            }
            placeContainer.innerHTML = '';
            placeContainer.appendChild(placeBadge);
        }

        // Rating stars
        if (rating > 0) {
            var starsContainer = document.createElement('span');
            starsContainer.className = 'team-rating-indicator ml-2 flex items-center';
            var fullStars = Math.floor(rating / 20);
            var hasHalfStar = (rating % 20) >= 10;
            var starHTML = '';
            for (var i = 0; i < fullStars; i++) {
                starHTML += '⭐';
            }
            if (hasHalfStar) {
                starHTML += '🌤️';
            }
            starsContainer.textContent = starHTML;
            starsContainer.title = tI18n('rating') + rating.toFixed(1);

            var ratingContainer = row.querySelector('.team-rating-container');
            if (!ratingContainer) {
                ratingContainer = document.createElement('span');
                ratingContainer.className = 'team-rating-container ml-2';
                row.insertBefore(ratingContainer, row.firstChild);
            }
            ratingContainer.innerHTML = '';
            ratingContainer.appendChild(starsContainer);
        }

        // Score highlight for top teams
        if (score > 0 && place <= 3) {
            row.classList.add('bg-blue-50');
        }
    });
}

// =============================================================================
// UX10: SSE connection loading indicator
// =============================================================================
function initSSEIndicator() {
    var indicator = document.getElementById('sse-status');
    if (!indicator) return;

    var gameId = indicator.dataset.sseGameId;
    if (!gameId) return;

    indicator.className = 'inline-flex items-center text-sm text-yellow-600';
    indicator.innerHTML = '<span class="animate-spin h-3 w-3 mr-1 border-2 border-yellow-600 border-t-transparent rounded-full"></span> ' + tI18n('connecting');

    // The SSE function will update the indicator on connect/error
    var originalConnect = window.initSSEGameNotifications;
    if (originalConnect) {
        window.initSSEGameNotifications = function(id) {
            var es = null;
            var origOnOpen = null;
            var origOnError = null;

            // Patch EventSource to detect connection state changes
            var origEventSource = EventSource;
            if (parseInt(id) === parseInt(gameId)) {
                indicator.className = 'inline-flex items-center text-sm text-green-600';
                indicator.innerHTML = '<span class="h-2 w-2 mr-1 bg-green-500 rounded-full"></span> ' + tI18n('connected');
            }

            originalConnect(id);

            // Override the SSE reconnect to show connecting state
            if (window._sseCheckInterval) {
                clearInterval(window._sseCheckInterval);
                window._sseCheckInterval = null;
            }
            window._sseCheckInterval = setInterval(function() {
                var esCheck = document.querySelector('[data-sse-active]');
                if (!esCheck) {
                    indicator.className = 'inline-flex items-center text-sm text-yellow-600';
                    indicator.innerHTML = '<span class="animate-spin h-3 w-3 mr-1 border-2 border-yellow-600 border-t-transparent rounded-full"></span> ' + tI18n('reconnecting');
                } else {
                    if (window._sseCheckInterval) {
                        clearInterval(window._sseCheckInterval);
                        window._sseCheckInterval = null;
                    }
                }
            }, 1000);

            setTimeout(function() {
                if (window._sseCheckInterval) {
                    clearInterval(window._sseCheckInterval);
                    window._sseCheckInterval = null;
                }
            }, 30000);
        };
    }
}

// =============================================================================
// UX12: Save state indicator for admin forms
// =============================================================================
function initAutoSaveIndicator() {
    var forms = document.querySelectorAll('[data-autosave]');
    if (!forms.length) return;

    forms.forEach(function(form) {
        var indicator = document.createElement('div');
        indicator.className = 'text-xs text-gray-400 mt-1';
        indicator.textContent = tI18n('saved');
        form.appendChild(indicator);

        var inputs = form.querySelectorAll('input, textarea, select');
        var saveTimer = null;

        inputs.forEach(function(input) {
            input.addEventListener('input', function() {
                indicator.textContent = tI18n('not-saved');
                indicator.className = 'text-xs text-orange-500 mt-1';

                if (saveTimer) clearTimeout(saveTimer);
                saveTimer = setTimeout(function() {
                    indicator.textContent = tI18n('saved');
                    indicator.className = 'text-xs text-gray-400 mt-1';
                }, 2000);
            });
        });
    });
}

// =============================================================================
// UX13: Copy code to clipboard on click
// =============================================================================
function initCodeCopy() {
    var codeBlocks = document.querySelectorAll('[data-copy]');
    if (!codeBlocks.length) return;

    codeBlocks.forEach(function(el) {
        el.addEventListener('click', function() {
            var text = el.getAttribute('data-copy') || el.textContent;
            if (navigator.clipboard && navigator.clipboard.writeText) {
                navigator.clipboard.writeText(text).then(function() {
                    showToast(tI18n('copied'), 'success', 2000);
                }).catch(function() {
                    fallbackCopy(text);
                });
            } else {
                fallbackCopy(text);
            }
        });

        el.style.cursor = 'pointer';
        el.title = tI18n('click-to-copy');
    });

    function fallbackCopy(text) {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.left = '-9999px';
        document.body.appendChild(ta);
        ta.select();
        try {
            document.execCommand('copy');
            showToast(tI18n('copied'), 'success', 2000);
        } catch (e) {
            showToast(tI18n('copy-failed'), 'error', 3000);
        }
        document.body.removeChild(ta);
    }
}

// =============================================================================
// HTMX loading indicator — спиннер на кнопках при hx-запросах
// =============================================================================
function initHTMXLoading() {
    document.addEventListener('htmx:beforeRequest', function(e) {
        var btn = e.detail.elt.querySelector('button[type="submit"]') || e.detail.elt;
        if (btn && btn.tagName === 'BUTTON' && !btn.dataset.noLoading) {
            btn.disabled = true;
            btn.dataset.originalText = btn.innerHTML;
            btn.innerHTML = '<span class="inline-block animate-spin mr-1">\u27F3</span> ' + (btn.dataset.loadingText || tI18n('sending'));
            btn.classList.add('opacity-70', 'cursor-not-allowed');
        }
    });
    document.addEventListener('htmx:afterRequest', function(e) {
        var btn = e.detail.elt.querySelector('button[type="submit"]') || e.detail.elt;
        if (btn && btn.tagName === 'BUTTON') {
            btn.disabled = false;
            btn.innerHTML = btn.dataset.originalText || btn.textContent;
            btn.classList.remove('opacity-70', 'cursor-not-allowed');
        }
    });
}

// =============================================================================
// Initialize on DOM ready
// =============================================================================
document.addEventListener('DOMContentLoaded', function() {
    initToast();
    initFormLoading();
    initConfirmDialogs();
    initInlineValidation();
    initOfflineDetector();
    initFileUploadProgress();
    initAutoSaveDrafts();
    initPushSubscription();
    initSSEGameNotificationsFromPage();
    initTeamRatingIndicators();
    initCodeCopy();
    initAutoSaveIndicator();
    initSearchAutocomplete();
    initHTMXLoading();
});

// Auto-detect game ID from page for SSE notifications.
// Uses data-sse-game-id (only on gameplay pages) so monitor/chat/logs pages,
// which already use WebSocket, don't open a duplicate SSE connection.
function initSSEGameNotificationsFromPage() {
    var gameIdEl = document.querySelector('[data-sse-game-id]');
    if (gameIdEl) {
        var gameId = parseInt(gameIdEl.dataset.sseGameId);
        if (!isNaN(gameId)) {
            initSSEGameNotifications(gameId);
        }
    }
}