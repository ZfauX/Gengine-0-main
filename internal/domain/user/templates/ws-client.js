// internal/domain/user/templates/ws-client.js
// Универсальный клиент WebSocket с поддержкой reconnection и heartbeat

(function(global) {
    'use strict';

    function createWebSocketClient(options) {
        const opts = Object.assign({
            url: null,
            roomId: null,
            onMessage: function() {},
            onOpen: function() {},
            onClose: function() {},
            onError: function() {},
            pongWait: 45000,
            pingPeriod: (45000 * 9) / 10,
            reconnectAttempts: 0,
            maxReconnectAttempts: 10,
            baseReconnectDelay: 1000,
            maxReconnectDelay: 30000,
            manualPing: false
        }, options);

        let socket = null;
        let pingTimer = null;
        let reconnectTimer = null;
        let shouldReconnect = true;
        let currentReconnectAttempt = 0;

        function getWebSocketUrl() {
            if (opts.url) return opts.url;
            const wsProtocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
            if (opts.roomId) {
                return `${wsProtocol}//${location.host}/ws/${opts.roomId}`;
            }
            return `${wsProtocol}//${location.host}/ws`;
        }

        function clearTimers() {
            if (pingTimer) {
                clearInterval(pingTimer);
                pingTimer = null;
            }
            if (reconnectTimer) {
                clearTimeout(reconnectTimer);
                reconnectTimer = null;
            }
        }

        function startPing() {
            if (opts.manualPing) return;
            clearTimers();
            pingTimer = setInterval(function() {
                if (socket && socket.readyState === WebSocket.OPEN) {
                    socket.send(JSON.stringify({ type: 'ping' }));
                }
            }, opts.pingPeriod);
        }

        function scheduleReconnect() {
            if (!shouldReconnect) return;
            if (currentReconnectAttempt >= opts.maxReconnectAttempts) {
                console.warn('WebSocket: max reconnect attempts reached');
                opts.onError(new Error('Max reconnect attempts reached'));
                return;
            }

            const delay = Math.min(
                opts.maxReconnectDelay,
                opts.baseReconnectDelay * Math.pow(2, currentReconnectAttempt)
            );

            console.info(`WebSocket: reconnecting in ${delay}ms (attempt ${currentReconnectAttempt + 1})`);
            currentReconnectAttempt++;

            reconnectTimer = setTimeout(function() {
                connect();
            }, delay);
        }

        function connect() {
            clearTimers();

            try {
                socket = new WebSocket(getWebSocketUrl());
            } catch (e) {
                console.error('WebSocket: connection failed', e);
                scheduleReconnect();
                return;
            }

            socket.onopen = function(event) {
                console.info('WebSocket: connected');
                currentReconnectAttempt = 0;
                opts.onOpen(event);
                startPing();
            };

            socket.onmessage = function(event) {
                try {
                    const data = JSON.parse(event.data);
                    if (data.type === 'pong') return;
                    opts.onMessage(data, event);
                } catch (e) {
                    console.error('WebSocket: message parse error', e);
                }
            };

            socket.onclose = function(event) {
                console.info('WebSocket: closed', event.code, event.reason);
                clearTimers();
                opts.onClose(event);
                if (event.code !== 1000 && shouldReconnect) {
                    scheduleReconnect();
                }
            };

            socket.onerror = function(event) {
                console.error('WebSocket: error', event);
                opts.onError(event);
            };
        }

        function send(data) {
            if (socket && socket.readyState === WebSocket.OPEN) {
                socket.send(typeof data === 'string' ? data : JSON.stringify(data));
                return true;
            }
            console.warn('WebSocket: cannot send, not connected');
            return false;
        }

        function close() {
            shouldReconnect = false;
            clearTimers();
            if (socket) {
                socket.close(1000, 'Client closing');
            }
        }

        function forceReconnect() {
            shouldReconnect = true;
            currentReconnectAttempt = 0;
            if (socket) {
                socket.close(1000, 'Force reconnect');
            }
            connect();
        }

        function isConnected() {
            return socket && socket.readyState === WebSocket.OPEN;
        }

        function getReadyState() {
            if (!socket) return WebSocket.CLOSED;
            return socket.readyState;
        }

        return {
            connect: connect,
            close: close,
            send: send,
            forceReconnect: forceReconnect,
            isConnected: isConnected,
            getReadyState: getReadyState,
            clearTimers: clearTimers
        };
    }

    global.createWebSocketClient = createWebSocketClient;
})(typeof window !== 'undefined' ? window : global);