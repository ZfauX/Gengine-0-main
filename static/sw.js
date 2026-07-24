const CACHE_NAME = 'gengine-v5';
const OFFLINE_PAGE = '/offline';

const STATIC_ASSETS = [
    '/offline',
    '/static/manifest.json',
    '/static/icons/icon-192x192.png',
    '/static/icons/icon-512x512.png',
    '/static/css/output.css',
    '/static/js/app.js'
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
    const url = new URL(event.request.url);

    // Пропускаем WebSocket, API, SSE
    if (url.pathname.startsWith('/ws') || url.pathname.startsWith('/api/') ||
        url.pathname.startsWith('/game/') && url.pathname.endsWith('/sse')) {
        return;
    }

    // HTML-страницы: Stale-while-revalidate
    if (event.request.mode === 'navigate') {
        event.respondWith(
            caches.match(event.request).then(cached => {
                const fetchPromise = fetch(event.request).then(response => {
                    if (response && response.status === 200) {
                        const copy = response.clone();
                        caches.open(CACHE_NAME).then(cache => cache.put(event.request, copy));
                    }
                    return response;
                }).catch(() => cached);
                return cached || fetchPromise;
            }).catch(() => caches.match(OFFLINE_PAGE))
        );
        return;
    }

    // Статика: Cache First + фоновая синхронизация
    event.respondWith(
        caches.match(event.request).then(cached => {
            const fetchAndCache = fetch(event.request).then(response => {
                if (response && response.status === 200) {
                    caches.open(CACHE_NAME).then(cache => cache.put(event.request, response.clone()));
                }
                return response;
            });
            return cached || fetchAndCache;
        })
    );
});

// Push-уведомления
self.addEventListener('push', event => {
    let data = {};
    try {
        data = event.data.json();
    } catch (e) {
        data = { title: 'Gengine', body: event.data.text() };
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
    const url = event.notification.data?.url || '/';
    event.waitUntil(
        clients.matchAll({ type: 'window' }).then(clientList => {
            for (const client of clientList) {
                if (client.url === url && 'focus' in client) return client.focus();
            }
            if (clients.openWindow) return clients.openWindow(url);
        })
    );
});
