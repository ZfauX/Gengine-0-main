// static/sw.js

const CACHE_NAME = 'encounter-v2';
const ASSETS_TO_CACHE = [
    '/',
    '/dashboard',
    '/games',
    '/calendar',
    '/static/manifest.json',
    '/static/icons/icon-192x192.png',
    '/static/icons/icon-512x512.png',
    '/static/css/app.css',      // если есть отдельный файл стилей
    '/static/js/app.js'         // если есть отдельный скрипт
];

// Установка Service Worker – кэшируем статические ресурсы
self.addEventListener('install', event => {
    event.waitUntil(
        caches.open(CACHE_NAME).then(cache => {
            console.log('Service Worker: кэширую ресурсы');
            return cache.addAll(ASSETS_TO_CACHE);
        })
    );
    // Активируем новый SW сразу, не дожидаясь закрытия старых вкладок
    self.skipWaiting();
});

// Активация – удаляем старые кэши
self.addEventListener('activate', event => {
    event.waitUntil(
        caches.keys().then(cacheNames => {
            return Promise.all(
                cacheNames.filter(name => name !== CACHE_NAME)
                    .map(name => {
                        console.log('Service Worker: удаляю старый кэш', name);
                        return caches.delete(name);
                    })
            );
        })
    );
    // Захватываем все вкладки под новый SW
    self.clients.claim();
});

// Стратегия: сначала кэш, потом сеть (Cache First)
self.addEventListener('fetch', event => {
    // Игнорируем запросы к API и WebSocket
    const url = new URL(event.request.url);
    if (url.pathname.startsWith('/api/') ||
        url.pathname.startsWith('/ws') ||
        url.pathname.startsWith('/chat/ws') ||
        url.pathname.startsWith('/monitor/ws')) {
        return;
    }

    event.respondWith(
        caches.match(event.request).then(cachedResponse => {
            if (cachedResponse) {
                return cachedResponse;
            }
            return fetch(event.request).then(networkResponse => {
                // Кэшируем новые страницы для будущего оффлайн-доступа
                if (networkResponse && networkResponse.status === 200) {
                    const clonedResponse = networkResponse.clone();
                    caches.open(CACHE_NAME).then(cache => {
                        cache.put(event.request, clonedResponse);
                    });
                }
                return networkResponse;
            });
        }).catch(() => {
            // Если ресурс не в кэше и сеть недоступна, показываем оффлайн-страницу
            if (event.request.mode === 'navigate') {
                return caches.match('/');
            }
            return new Response('Нет соединения', { status: 503 });
        })
    );
});

// Push-уведомления
self.addEventListener('push', event => {
    let data = { title: 'Encounter Engine', body: 'Новое уведомление' };
    if (event.data) {
        try {
            data = event.data.json();
        } catch (e) {
            data.body = event.data.text();
        }
    }
    const options = {
        body: data.body,
        icon: '/static/icons/icon-192x192.png',
        badge: '/static/icons/icon-192x192.png',
        data: data.url || '/',
        requireInteraction: false
    };
    event.waitUntil(self.registration.showNotification(data.title, options));
});

// Клик по уведомлению – открываем соответствующую страницу
self.addEventListener('notificationclick', event => {
    event.notification.close();
    const url = event.notification.data || '/';
    event.waitUntil(
        clients.matchAll({ type: 'window', includeUncontrolled: true }).then(windowClients => {
            // Если вкладка уже открыта, фокусируемся на ней
            for (let client of windowClients) {
                if (client.url.includes(url) && 'focus' in client) {
                    return client.focus();
                }
            }
            // Иначе открываем новую
            return clients.openWindow(url);
        })
    );
});