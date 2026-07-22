// static/js/app.js
// Loading indicators, autocomplete, inline validation, toast notifications, offline detection, push subscriptions, file upload progress, auto-save drafts

document.addEventListener('DOMContentLoaded', function() {
    initToast();
    initFormLoading();
    initConfirmDialogs();
    initInlineValidation();
    initOfflineDetector();
    initFileUploadProgress();
    initAutoSaveDrafts();
    initPushSubscription();
});

// =============================================================================
// UX1: Global online/offline detector with toast notification
// =============================================================================
function initOfflineDetector() {
    function updateOnlineStatus() {
        if (!navigator.onLine) {
            showToast('Соединение потеряно. Изменения могут не сохраниться.', 'warning', 0);
        } else {
            showToast('Соединение восстановлено.', 'success', 3000);
        }
    }

    window.addEventListener('offline', updateOnlineStatus);
    window.addEventListener('online', updateOnlineStatus);
}

// =============================================================================
// Toast notification system
// =============================================================================
var TOAST_KEYS = {};
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
        toast.innerHTML = '<div class="flex items-start gap-3">' +
            '<span class="text-lg shrink-0">' + (icons[type] || icons.info) + '</span>' +
            '<div class="flex-1">' + message + '</div>' +
            '<button onclick="this.parentElement.parentElement.remove()" class="shrink-0 text-gray-400 hover:text-gray-600" aria-label="Закрыть">&times;</button>' +
            '</div>';

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
            var btn = this.querySelector('button[type="submit"]');
            if (btn && !btn.dataset.noLoading) {
                btn.disabled = true;
                btn.innerHTML = '<span class="inline-block animate-spin mr-1">⟳</span> ' + (btn.dataset.loadingText || 'Отправка...');
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
        button.addEventListener('click', function(e) {
            var message = this.getAttribute('data-confirm');
            var hasHtmx = this.hasAttribute('hx-delete') || this.hasAttribute('hx-post');
            if (hasHtmx) {
                if (!showModalConfirm(message, this)) {
                    e.preventDefault();
                }
            } else {
                if (!confirm(message)) {
                    e.preventDefault();
                    e.stopPropagation();
                }
            }
        });
    });
}

function showModalConfirm(message, element) {
    var existing = document.getElementById('confirm-modal');
    if (existing) existing.remove();

    var overlay = document.createElement('div');
    overlay.id = 'confirm-modal';
    overlay.className = 'fixed inset-0 z-[10000] flex items-center justify-center bg-black/50';
    overlay.innerHTML =
        '<div class="bg-white rounded-xl shadow-2xl p-6 max-w-md mx-4 w-full">' +
        '<div class="text-xl font-semibold mb-2">Подтверждение</div>' +
        '<p class="text-gray-600 mb-6">' + escapeHtml(message) + '</p>' +
        '<div class="flex justify-end gap-3">' +
        '<button id="confirm-cancel" class="px-4 py-2 text-gray-600 hover:text-gray-800 bg-gray-100 rounded-lg hover:bg-gray-200 transition">Отмена</button>' +
        '<button id="confirm-ok" class="px-4 py-2 bg-red-600 text-white rounded-lg hover:bg-red-700 transition">Удалить</button>' +
        '</div>' +
        '</div>';

    document.body.appendChild(overlay);

    return new Promise(function(resolve) {
        document.getElementById('confirm-cancel').addEventListener('click', function() {
            overlay.remove();
            resolve(false);
        });
        document.getElementById('confirm-ok').addEventListener('click', function() {
            overlay.remove();
            resolve(true);
        });
        overlay.addEventListener('click', function(e) {
            if (e.target === overlay) {
                overlay.remove();
                resolve(false);
            }
        });
    });
}

// =============================================================================
// UX6: File upload progress bar
// =============================================================================
function initFileUploadProgress() {
    var fileInputs = document.querySelectorAll('input[type="file"][data-progress]');
    fileInputs.forEach(function(input) {
        input.addEventListener('change', function() {
            var form = this.closest('form');
            if (!form) return;

            var progressContainer = document.getElementById(this.dataset.progress);
            if (!progressContainer) {
                progressContainer = document.createElement('div');
                progressContainer.id = this.dataset.progress;
                progressContainer.className = 'mt-2';
                this.parentElement.appendChild(progressContainer);
            }

            form.addEventListener('submit', function(e) {
                e.preventDefault();
                var file = input.files[0];
                if (!file) return;

                var xhr = new XMLHttpRequest();
                var formData = new FormData(form);
                var progressBar = progressContainer.querySelector('.progress-bar');
                var progressText = progressContainer.querySelector('.progress-text');

                if (!progressBar) {
                    progressContainer.innerHTML =
                        '<div class="w-full bg-gray-200 rounded-full h-3">' +
                        '<div class="progress-bar bg-blue-600 h-3 rounded-full transition-all duration-300" style="width:0%"></div>' +
                        '</div>' +
                        '<div class="progress-text text-sm text-gray-500 mt-1">0%</div>';
                }

                xhr.upload.addEventListener('progress', function(e) {
                    if (e.lengthComputable) {
                        var percent = Math.round((e.loaded / e.total) * 100);
                        progressContainer.querySelector('.progress-bar').style.width = percent + '%';
                        progressContainer.querySelector('.progress-text').textContent = percent + '%';
                    }
                });

                xhr.addEventListener('load', function() {
                    if (xhr.status === 200) {
                        showToast('Файл успешно загружен', 'success');
                    } else {
                        showToast('Ошибка загрузки файла', 'error');
                    }
                    progressContainer.innerHTML = '';
                });

                xhr.open(form.method || 'POST', form.action);
                xhr.send(formData);
            }, { once: true });
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
        var fields = form.querySelectorAll('input, textarea, select');

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
                showToast('Черновик восстановлен', 'info', 3000);
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
    if (!('Notification' in window) || !('serviceWorker' in navigator)) return;

    var pushBtn = document.getElementById('enable-push');
    if (!pushBtn) return;

    pushBtn.addEventListener('click', function() {
        Notification.requestPermission().then(function(permission) {
            if (permission === 'granted') {
                navigator.serviceWorker.ready.then(function(registration) {
                    return registration.pushManager.subscribe({
                        userVisibleOnly: true,
                        applicationServerKey: urlBase64ToUint8Array(pushBtn.dataset.vapidKey || '')
                    });
                }).then(function(subscription) {
                    fetch('/api/push/subscribe', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(subscription)
                    }).then(function() {
                        showToast('Уведомления включены', 'success');
                        pushBtn.style.display = 'none';
                    }).catch(function() {
                        showToast('Ошибка подписки на уведомления', 'error');
                    });
                }).catch(function(err) {
                    showToast('Ошибка: ' + err.message, 'error');
                });
            } else {
                showToast('Разрешите уведомления в настройках браузера', 'warning');
            }
        });
    });
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
    dropdown.innerHTML = '<div class="p-3 text-sm text-gray-400 text-center">Начните вводить название игры</div>';
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
                .then(function(r) { return r.json(); })
                .then(function(data) {
                    if (!data.results || data.results.length === 0) {
                        dropdown.innerHTML = '<div class="p-3 text-sm text-gray-400 text-center">Ничего не найдено</div>';
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
                        el.addEventListener('click', function() {
                            searchInput.value = this.textContent.trim();
                            dropdown.classList.add('hidden');
                            if (searchInput.form) searchInput.form.submit();
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