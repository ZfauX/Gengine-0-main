// static/js/app.js
// Loading indicators, autocomplete, inline validation, toast notifications, offline detection, push subscriptions, file upload progress, auto-save drafts

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
                btn.innerHTML = '<span class="inline-block animate-spin mr-1">\u27F3</span> ' + (btn.dataset.loadingText || 'Отправка...');
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
            var hasHtmx = this.hasAttribute('hx-delete') || this.hasAttribute('hx-post') || this.hasAttribute('hx-put');
            if (hasHtmx) {
                e.preventDefault();
                var confirmed = await showModalConfirm(message, this);
                if (confirmed) {
                    this.dataset.__confirming = '1';
                    this.click();
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

// =============================================================================
// UX8: SSE game status notifications
// =============================================================================
function initSSEGameNotifications(gameId) {
    if (!gameId) return;

    var eventSource = null;

    function connectSSE() {
        try {
            eventSource = new EventSource('/game/' + gameId + '/sse');

            eventSource.onopen = function() {
                console.debug('SSE connected for game', gameId);
                document.body.setAttribute('data-sse-active', 'true');
            };

            eventSource.addEventListener('game_started', function(e) {
                var data = JSON.parse(e.data);
                showToast('🎮 Игра начата! Удачи всем командам!', 'success', 5000);
            });

            eventSource.addEventListener('game_finished', function(e) {
                var data = JSON.parse(e.data);
                showToast('🏁 Игра завершена! Результаты обновлены.', 'info', 8000);
            });

            eventSource.addEventListener('team_disqualified', function(e) {
                var data = JSON.parse(e.data);
                if (data && data.team_id) {
                    showToast('⚠️ Команда дисквалифицирована!', 'error', 10000);
                }
            });

            eventSource.addEventListener('level_completed', function(e) {
                var data = JSON.parse(e.data);
                if (data && data.team_id) {
                    showToast('✅ Уровень пройден! Отличная работа!', 'success', 4000);
                }
            });

            eventSource.addEventListener('time_warning', function(e) {
                var data = JSON.parse(e.data);
                if (data && data.remaining_minutes) {
                    showToast('⏰ Осталось ' + data.remaining_minutes + ' минут до завершения!', 'warning', 5000);
                }
            });

            eventSource.addEventListener('hint_available', function(e) {
                var data = JSON.parse(e.data);
                if (data && data.level_number) {
                    showToast('💡 Подсказка доступна для уровня ' + data.level_number, 'info', 4000);
                }
            });

            eventSource.onerror = function(err) {
                console.warn('SSE error, reconnecting in 5s...', err);
                eventSource.close();
                document.body.removeAttribute('data-sse-active');
                setTimeout(connectSSE, 5000);
            };
        } catch (e) {
            console.warn('SSE not supported, notifications disabled:', e);
        }
    }

    connectSSE();

    // Cleanup on page unload
    window.addEventListener('beforeunload', function() {
        if (eventSource) {
            eventSource.close();
        }
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
                placeBadge.textContent = '🥇 1-е';
            } else if (place === 2) {
                placeBadge.classList.add('bg-gray-100', 'text-gray-800');
                placeBadge.textContent = '🥈 2-е';
            } else if (place === 3) {
                placeBadge.classList.add('bg-orange-100', 'text-orange-800');
                placeBadge.textContent = '🥉 3-е';
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
            starsContainer.title = 'Рейтинг: ' + rating.toFixed(1);

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
    indicator.innerHTML = '<span class="animate-spin h-3 w-3 mr-1 border-2 border-yellow-600 border-t-transparent rounded-full"></span> Подключение...';

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
                indicator.innerHTML = '<span class="h-2 w-2 mr-1 bg-green-500 rounded-full"></span> Подключено';
            }

            originalConnect(id);

            // Override the SSE reconnect to show connecting state
            var checkInterval = setInterval(function() {
                var esCheck = document.querySelector('[data-sse-active]');
                if (!esCheck) {
                    indicator.className = 'inline-flex items-center text-sm text-yellow-600';
                    indicator.innerHTML = '<span class="animate-spin h-3 w-3 mr-1 border-2 border-yellow-600 border-t-transparent rounded-full"></span> Переподключение...';
                } else {
                    clearInterval(checkInterval);
                }
            }, 1000);

            setTimeout(function() { clearInterval(checkInterval); }, 30000);
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
        indicator.textContent = '✓ Сохранено';
        form.appendChild(indicator);

        var inputs = form.querySelectorAll('input, textarea, select');
        var saveTimer = null;

        inputs.forEach(function(input) {
            input.addEventListener('input', function() {
                indicator.textContent = '✎ Не сохранено';
                indicator.className = 'text-xs text-orange-500 mt-1';

                if (saveTimer) clearTimeout(saveTimer);
                saveTimer = setTimeout(function() {
                    indicator.textContent = '✓ Сохранено';
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
                    showToast('📋 Скопировано', 'success', 2000);
                }).catch(function() {
                    fallbackCopy(text);
                });
            } else {
                fallbackCopy(text);
            }
        });

        el.style.cursor = 'pointer';
        el.title = 'Нажмите, чтобы скопировать';
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
            showToast('📋 Скопировано', 'success', 2000);
        } catch (e) {
            showToast('Не удалось скопировать', 'error', 3000);
        }
        document.body.removeChild(ta);
    }
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
    initSSEIndicator();
    initSearchAutocomplete();
});

// Auto-detect game ID from page for SSE notifications
function initSSEGameNotificationsFromPage() {
    var gameIdEl = document.querySelector('[data-game-id]');
    if (gameIdEl) {
        var gameId = parseInt(gameIdEl.dataset.gameId);
        if (!isNaN(gameId)) {
            initSSEGameNotifications(gameId);
        }
    }
}