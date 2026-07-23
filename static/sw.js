const CACHE_NAME = 'gengine-v3';
const ASSETS_TO_CACHE = [
    '/',
    '/dashboard',
    '/games',
    '/calendar',
    '/static/manifest.json',
    '/static/icons/icon-192x192.png',
    '/static/icons/icon-512x512.png',
    '/static/css/output.css',
    '/static/js/app.js'
];

// Установка Service Worker
self.addEventListener('install', event => {
    event.waitUntil(
        caches.open(CACHE_NAME).then(cache => cache.addAll(ASSETS_TO_CACHE))
    );
    self.skipWaiting();
});

// Активация — удаляем старые кэши
self.addEventListener('activate', event => {
    event.waitUntil(
        caches.keys().then(names =>
            Promise.all(names.filter(n => n !== CACHE_NAME).map(n => caches.delete(n)))
        )
    );
    self.clients.claim();
});

// Offline page
const OFFLINE_PAGE = '/offline';

// Стратегия кэширования
self.addEventListener('fetch', event => {
    const url = new URL(event.request.url);

    // Игнорируем WebSocket и API
    if (url.pathname.startsWith('/ws') || url.pathname.startsWith('/chat/ws') ||
        url.pathname.startsWith('/monitor/ws') || url.pathname.startsWith('/api/')) {
        return;
    }

	// HTML-страницы: только сеть (не кешировать)
	if (event.request.mode === 'navigate') {
		event.respondWith(
			fetch(event.request).catch(() => {
				return caches.match(event.request).then(cached => {
					return cached || caches.match(OFFLINE_PAGE);
				});
			})
		);
		return;
	}

    // Статика: Cache First
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
