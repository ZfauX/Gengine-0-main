/** @type {import('tailwindcss').Config} */
module.exports = {
  // Пути ко всем файлам, где используются классы Tailwind
  content: [
    "./internal/domain/**/templates/*.html",
    "./internal/domain/*/templates/*.html",
    "./static/**/*.js",
    "./static/**/*.html",
  ],

  // Режим тёмной темы (используем класс 'dark' на html-элементе)
  darkMode: 'class',

  theme: {
    extend: {
      // Кастомные цвета в фирменном стиле Encounter
      colors: {
        primary: {
          50: '#eff6ff',
          100: '#dbeafe',
          200: '#bfdbfe',
          300: '#93c5fd',
          400: '#60a5fa',
          500: '#3b82f6',
          600: '#2563eb',
          700: '#1d4ed8',
          800: '#1e40af',
          900: '#1e3a8a',
        },
        brand: {
          blue: '#2563eb',
          dark: '#1e293b',
          light: '#f8fafc',
        },
      },

      // Шрифты: Inter — современный, читаемый
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['JetBrains Mono', 'monospace'],
      },

      // Кастомные отступы для карточек и контейнеров
      spacing: {
        '18': '4.5rem',
        '22': '5.5rem',
      },

      // Кастомные тени
      boxShadow: {
        'soft': '0 2px 4px rgba(0,0,0,0.05), 0 1px 2px rgba(0,0,0,0.1)',
        'card': '0 4px 6px -1px rgba(0,0,0,0.1), 0 2px 4px -2px rgba(0,0,0,0.1)',
        'hover': '0 10px 15px -3px rgba(0,0,0,0.1), 0 4px 6px -4px rgba(0,0,0,0.1)',
      },

      // Брейкпоинты для адаптивности (уже есть, но можно переопределить)
      screens: {
        'xs': '475px',
      },

      // Кастомная анимация
      animation: {
        'fade-in': 'fadeIn 0.2s ease-in-out',
        'spin-slow': 'spin 3s linear infinite',
      },
      keyframes: {
        fadeIn: {
          from: { opacity: '0', transform: 'translateY(4px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
      },
    },
  },

  // Плагины для улучшенной стилизации форм, текста и пропорций
  plugins: [
    require('@tailwindcss/forms'),
    require('@tailwindcss/typography'),
    require('@tailwindcss/aspect-ratio'),
  ],

  // Безопасный список классов, которые генерируются динамически (например, через JS)
  // Если вы используете динамические цвета (bg-${color}-500), добавьте их сюда:
  // safelist: [
  //   'bg-blue-500', 'bg-red-500', 'bg-green-500', 'bg-yellow-500',
  //   'text-blue-500', 'text-red-500', 'text-green-500', 'text-yellow-500',
  //   'border-blue-500', 'border-red-500', 'border-green-500', 'border-yellow-500',
  // ],
};