// Единая версия статики (UX5): должна совпадать с config.StaticAssetsVersion.
const ASSET_VERSION = '20260805';
const CACHE_NAME = 'gengine-' + ASSET_VERSION;
const OFFLINE_PAGE = '/offline';

const STATIC_ASSETS = [
    '/offline',
    '/static/manifest.json',
    '/static/icons/icon-192x192.png',
    '/static/icons/icon-512x512.png',
    // Версионированные URL — совпадают с тем, что запрашивает layout.html
    // (?v={{StaticVersion}} генерируется сервером из config.StaticAssetsVersion).
    '/static/css/output.css?v=' + ASSET_VERSION,
    '/static/js/app.js?v=' + ASSET_VERSION,
    '/static/js/leaflet.js',
    '/static/css/leaflet.css'
];

// Установка — кэшируем статику (только гарантированно доступные URL)
self.addEventListener('install', event => {
    event.waitUntil(
        caches.open(CACHE_NAME).then(cache =>
            Promise.allSettled(
                STATIC_ASSETS.map(url =>
                    cache.add(url).catch(err => console.warn('SW: failed to cache', url, err))
                )
            )
        )
    );
    self.skipWaiting();
});

// Активация — удаляем старые кэши
self.addEventListener('activate', event => {
    event.waitUntil(
        caches.keys().then(names =>
            Promise.all(names.filter(n => n !== CACHE_NAME).map(n => caches.delete(n)))
        ).then(() => self.clients.claim())
    );
});

// Стратегии кэширования
self.addEventListener('fetch', event => {
    // Пропускаем не-http(s) запросы (chrome-extension, devtools и т.д.)
    if (!event.request.url.startsWith('http://') && !event.request.url.startsWith('https://')) {
        return;
    }

    // Кэшируются ТОЛЬКО GET-запросы. Cache.put() не поддерживает POST/PUT/DELETE
    // и выбросит NetworkError при попытке кэшировать такие запросы.
    if (event.request.method !== 'GET') {
        return;
    }

    const url = new URL(event.request.url);

    // Пропускаем WebSocket, API, SSE, монитор (JSON polling / stream),
    // чат и логи — это живые/динамические данные, их нельзя кэшировать.
    if (url.pathname.startsWith('/ws') ||
        url.pathname.startsWith('/api/') ||
        url.pathname.startsWith('/game/sse') ||
        url.pathname.includes('/monitor') ||
        url.pathname.includes('/chat') ||
        url.pathname.includes('/logs') ||
        url.pathname.includes('/sse') ||
        url.pathname.endsWith('/stream') ||
        url.pathname.endsWith('/data')) {
        return;
    }

    // /uploads: Cache-first с fallback на сеть (изображения игр, аватары).
    // Кэширование происходит при install — в runtime только читаем из кэша.
    if (url.pathname.startsWith('/uploads/')) {
        event.respondWith(
            caches.match(event.request).then(cached => {
                if (cached) return cached;
                return fetch(event.request).catch(() => cached);
            })
        );
        return;
    }

    // HTML-страницы: Network-first (сначала сеть, кэш только как оффлайн-fallback).
    // Критично для форм с CSRF-токенами и динамическими данными — не показываем устаревший кэш.
    if (event.request.mode === 'navigate') {
        event.respondWith(
            fetch(event.request).catch(() => {
                return caches.match(event.request).then(cached => cached || caches.match(OFFLINE_PAGE));
            })
        );
        return;
    }

    // Статика: Cache First + fallback на сеть.
    // Кэширование происходит при install (STATIC_ASSETS) — в runtime только читаем из кэша.
    event.respondWith(
        caches.match(event.request).then(cached => {
            if (cached) return cached;
            return fetch(event.request).catch(() => cached);
        })
    );
});

// Кэширует ответ — не используется в runtime (кэширование только при install).
// Оставлен на будущее, если потребуется runtime-кэширование.
function cacheResponse(request, response) {
    // Только GET-запросы (Cache.put не поддерживает POST/PUT/DELETE)
    if (request.method !== 'GET') {
        return;
    }
    // Только http/https схемы
    if (!request.url.startsWith('http://') && !request.url.startsWith('https://')) {
        return;
    }
    // Не кэшируем неуспешные/неполные ответы (network errors)
    if (!response || !response.ok) {
        return;
    }
    try {
        caches.open(CACHE_NAME).then(cache => cache.put(request, response)).catch(err => {
            console.warn('SW: cache.put failed', err);
        });
    } catch (e) {
        console.warn('SW: cache put error', e);
    }
}

// Push-уведомления
self.addEventListener('push', event => {
    let data = {};
    if (event.data) {
        try {
            data = event.data.json();
        } catch (e) {
            // Не-JSON payload (простой текст) — показываем как body.
            data = { title: 'Gengine', body: event.data.text() };
        }
    }

    const options = {
        body: data.body || '',
        icon: '/static/icons/icon-192x192.png',
        badge: '/static/icons/icon-192x192.png',
        tag: data.tag || 'default',
        data: data.url ? { url: data.url } : {},
        vibrate: [200, 100, 200],
        requireInteraction: true
    };

    event.waitUntil(
        self.registration.showNotification(data.title || 'Gengine', options)
    );
});

// Клик по уведомлению
self.addEventListener('notificationclick', event => {
    event.notification.close();
    // F8: только same-origin относительные URL (начинаются с "/", но не с "//").
    // Абсолютные/протокол-относительные ссылки могли бы открывать внешний сайт
    // (фишинговый вектор), если URL уведомления станет контролируемым.
    let url = event.notification.data?.url || '/';
    if (typeof url !== 'string' || url.charAt(0) !== '/' || url.charAt(1) === '/') {
        url = '/';
    }
    event.waitUntil(
        clients.matchAll({ type: 'window' }).then(clientList => {
            for (const client of clientList) {
                if (client.url.startsWith(url) && 'focus' in client) {
                    return client.focus();
                }
            }
            if (clients.openWindow) return clients.openWindow(url);
        })
    );
});
