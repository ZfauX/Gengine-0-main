// static/js/richtext.js
// F-2 (pass 45): компактный WYSIWYG-редактор на contenteditable.
// Без внешних зависимостей; работает с любым <div class="richtext-editor">.
(function() {
    'use strict';

    // Панель инструментов создаётся рядом с редактором.
    function initRichText(editor) {
        if (!editor || editor.dataset.richtextInitialized) return;
        editor.dataset.richtextInitialized = '1';

        var toolbar = document.createElement('div');
        toolbar.className = 'rich-toolbar flex flex-wrap gap-1 p-2 mb-2 bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-700 rounded-md sticky top-0 z-10';

        function addBtn(html, title, action) {
            var b = document.createElement('button');
            b.type = 'button';
            b.innerHTML = html;
            b.title = title;
            b.setAttribute('aria-label', title);
            b.className = 'px-2 py-1 text-sm rounded hover:bg-gray-200 dark:hover:bg-gray-700 min-w-[32px]';
            b.addEventListener('click', function(e) { e.preventDefault(); action(); editor.focus(); });
            toolbar.appendChild(b);
        }

        function exec(cmd, val) {
            editor.focus();
            document.execCommand(cmd, false, val);
        }

        // ── Кнопки форматирования ──
        addBtn('<b>B</b>', 'Жирный', function() { exec('bold'); });
        addBtn('<i>I</i>', 'Курсив', function() { exec('italic'); });
        addBtn('<u>U</u>', 'Подчёркнутый', function() { exec('underline'); });
        addBtn('<s>S</s>', 'Зачёркнутый', function() { exec('strikeThrough'); });

        // Размер шрифта
        addBtn('Aa', 'Размер шрифта', function() {
            var sizes = [2, 3, 4, 5, 6];
            var cur = prompt('Размер шрифта (1-7):', '3');
            var n = parseInt(cur, 10);
            if (!isNaN(n) && n >= 1 && n <= 7) exec('fontSize', String(n));
        });

        // Цвет текста
        addBtn('🎨', 'Цвет текста', function() {
            var c = prompt('Цвет (например #ff0000 или red):', '#000000');
            if (c) exec('foreColor', c);
        });

        // Списки
        addBtn('•≡', 'Список', function() { exec('insertUnorderedList'); });
        addBtn('1≡', 'Нумерованный список', function() { exec('insertOrderedList'); });

        // Цитата / код
        addBtn('❝', 'Цитата', function() { exec('formatBlock', 'blockquote'); });
        addBtn('&lt;/&gt;', 'Код', function() { exec('formatBlock', 'pre'); });

        // Ссылка
        addBtn('🔗', 'Ссылка', function() {
            var url = prompt('URL (https://...):', 'https://');
            if (url) exec('createLink', url);
        });

        // Таблица
        addBtn('▦', 'Таблица', function() {
            var rows = prompt('Строк:', '2');
            var cols = prompt('Колонок:', '2');
            var r = parseInt(rows, 10), c2 = parseInt(cols, 10);
            if (isNaN(r) || isNaN(c2) || r < 1 || c2 < 1) return;
            var t = '<table class="w-full border-collapse"><tbody>';
            for (var i = 0; i < r; i++) {
                t += '<tr>';
                for (var j = 0; j < c2; j++) t += '<td class="border border-gray-300 dark:border-gray-600 p-1">&nbsp;</td>';
                t += '</tr>';
            }
            t += '</tbody></table><p><br></p>';
            document.execCommand('insertHTML', false, t);
        });

        // Изображение по URL
        addBtn('🖼', 'Фото', function() {
            var url = prompt('URL изображения (https://...):', 'https://');
            if (url) {
                document.execCommand('insertHTML', false,
                    '<p><img src="' + url + '" alt="" style="max-width:100%;height:auto"></p>');
            }
        });

        // Видео по URL
        addBtn('🎬', 'Видео', function() {
            var url = prompt('URL видео (https://...):', 'https://');
            if (url) {
                document.execCommand('insertHTML', false,
                    '<p><video src="' + url + '" controls style="max-width:100%"></video></p>');
            }
        });

        // Аудио по URL
        addBtn('🎵', 'Аудио', function() {
            var url = prompt('URL аудио (https://...):', 'https://');
            if (url) {
                document.execCommand('insertHTML', false,
                    '<p><audio src="' + url + '" controls></audio></p>');
            }
        });

        // Очистить форматирование
        addBtn('∅', 'Снять форматирование', function() { exec('removeFormat'); });

        // Вставить текст из textarea-синка (обновляем скрытое поле)
        editor.addEventListener('input', function() { sync(); });
        editor.addEventListener('blur', function() { sync(); });

        var syncTarget = document.getElementById(editor.dataset.richtextTarget);
        function sync() {
            if (syncTarget) syncTarget.value = editor.innerHTML;
        }

        editor.parentNode.insertBefore(toolbar, editor);
    }

    function initAll() {
        document.querySelectorAll('.richtext-editor').forEach(function(el) {
            initRichText(el);
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initAll);
    } else {
        initAll();
    }
})();
