// static/js/app.js
// Loading indicators for forms

document.addEventListener('DOMContentLoaded', function() {
    // Add loading state to all forms with data-loading attribute
    const forms = document.querySelectorAll('form[data-loading]');
    forms.forEach(form => {
        form.addEventListener('submit', function(e) {
            const submitButton = this.querySelector('button[type="submit"], input[type="submit"]');
            if (submitButton) {
                submitButton.disabled = true;
                submitButton.textContent = 'Отправка...';
                submitButton.classList.add('opacity-50', 'cursor-not-allowed');
            }
        });
    });

    // Confirm dialog for dangerous actions
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
});
