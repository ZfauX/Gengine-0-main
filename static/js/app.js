// static/js/app.js
// Loading indicators, autocomplete, inline validation, toast notifications

document.addEventListener('DOMContentLoaded', function() {
    // Toast notification system
    initToast();

    // Add loading state to all forms with data-loading attribute
    initFormLoading();

    // Confirm dialog for dangerous actions
    initConfirmDialogs();

    // Inline validation
    initInlineValidation();
});

// Toast notification system
function initToast() {
    // Создаём контейнер для toast-уведомлений
    let container = document.getElementById('toast-container');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toast-container';
        container.className = 'fixed top-4 right-4 z-[9999] space-y-2';
        document.body.appendChild(container);
    }

    // Глобальная функция showToast
    window.showToast = function(message, type, duration) {
        type = type || 'info';
        duration = duration || 4000;

        const icons = {
            success: '✅',
            error: '❌',
            info: 'ℹ️',
            warning: '⚠️'
        };

        const toast = document.createElement('div');
        toast.className = 'toast toast-' + type;
        toast.innerHTML = '<div class="flex items-start gap-3">' +
            '<span class="text-lg shrink-0">' + (icons[type] || icons.info) + '</span>' +
            '<div class="flex-1">' + message + '</div>' +
            '<button onclick="this.parentElement.parentElement.remove()" class="shrink-0 text-gray-400 hover:text-gray-600" aria-label="Закрыть">&times;</button>' +
            '</div>';

        container.appendChild(toast);

        // Автоудаление
        setTimeout(function() {
            toast.style.opacity = '0';
            toast.style.transform = 'translateX(100%)';
            setTimeout(function() {
                if (toast.parentElement) toast.parentElement.removeChild(toast);
            }, 300);
        }, duration);
    };
}

// Loading indicators for forms — все мутирующие формы получают spinner
function initFormLoading() {
    const forms = document.querySelectorAll('form[method="POST"], form[method="post"]');
    forms.forEach(form => {
        form.addEventListener('submit', function(e) {
            const btn = this.querySelector('button[type="submit"]');
            if (btn && !btn.dataset.noLoading) {
                btn.disabled = true;
                btn.innerHTML = '<span class="inline-block animate-spin mr-1">⟳</span> Отправка...';
                btn.classList.add('opacity-70', 'cursor-not-allowed');
            }
        });
    });
}

// Confirm dialog for dangerous actions
function initConfirmDialogs() {
    const confirmButtons = document.querySelectorAll('[data-confirm]');
    confirmButtons.forEach(button => {
        button.addEventListener('click', function(e) {
            const message = this.getAttribute('data-confirm');
            if (!confirm(message)) {
                e.preventDefault();
                e.stopPropagation();
            }
        });
    });
}

// Search autocomplete — показывает выпадающий список с играми при вводе
function initSearchAutocomplete() {
    const searchInput = document.getElementById('search');
    if (!searchInput) return;

    const container = searchInput.parentElement;
    if (!container) return;

    // Создаём выпадающий список
    const dropdown = document.createElement('div');
    dropdown.id = 'searchDropdown';
    dropdown.className = 'absolute z-50 mt-1 w-full bg-white rounded-lg shadow-lg border border-gray-200 max-h-60 overflow-y-auto hidden';
    dropdown.innerHTML = '<div class="p-3 text-sm text-gray-400 text-center">Начните вводить название игры</div>';
    container.style.position = 'relative';
    container.appendChild(dropdown);

    let debounceTimer = null;
    let selectedIndex = -1;

    searchInput.addEventListener('input', function() {
        clearTimeout(debounceTimer);
        const query = this.value.trim();

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

                    let html = '';
                    data.results.forEach(function(item, index) {
                        html += '<a href="/games/' + item.id + '" class="block px-3 py-2 hover:bg-blue-50 transition cursor-pointer search-item" data-index="' + index + '" data-id="' + item.id + '">' +
                                '<span class="font-medium text-gray-800">' + escapeHtml(item.name) + '</span>' +
                                '</a>';
                    });
                    dropdown.innerHTML = html;
                    dropdown.classList.remove('hidden');
                    selectedIndex = -1;

                    // Обработчики кликов
                    dropdown.querySelectorAll('.search-item').forEach(function(el) {
                        el.addEventListener('click', function() {
                            searchInput.value = this.textContent.trim();
                            dropdown.classList.add('hidden');
                            searchInput.form.submit();
                        });
                    });
                })
                .catch(function() {
                    dropdown.classList.add('hidden');
                });
        }, 250);
    });

    // Keyboard navigation
    searchInput.addEventListener('keydown', function(e) {
        const items = dropdown.querySelectorAll('.search-item');
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

    // Закрытие при клике вне
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

    function escapeHtml(text) {
        const map = {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;'};
        return String(text).replace(/[&<>"']/g, function(m) { return map[m]; });
    }
}

// Inline validation для форм
function initInlineValidation() {
    const forms = document.querySelectorAll('form[data-inline-validation]');
    forms.forEach(form => {
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
        const errorEl = document.getElementById('error-' + field.id);
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
