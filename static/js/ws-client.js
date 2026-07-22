(function() {
    'use strict';

    if (window.ReconnectingWebSocket) {
        console.log('ReconnectingWebSocket already loaded');
        return;
    }

    function ReconnectingWebSocket(url, protocols, options) {
        this.url = url;
        this.protocols = protocols;
        this.options = options || {};
        this.maxRetries = this.options.maxRetries || 10;
        this.initialDelay = this.options.initialDelay || 1000;
        this.maxDelay = this.options.maxDelay || 30000;
        this.retryCount = 0;
        this.ws = null;
        this.isManualClose = false;
        this.eventListeners = {};

        this.connect();
    }

    ReconnectingWebSocket.prototype.connect = function() {
        var self = this;
        var wsUrl = this.url;

        if (this.protocols) {
            this.ws = new WebSocket(wsUrl, this.protocols);
        } else {
            this.ws = new WebSocket(wsUrl);
        }

        this.ws.onopen = function(event) {
            console.log('WebSocket connected');
            self.retryCount = 0;
            self.isManualClose = false;
            
            if (self.eventListeners['open']) {
                self.eventListeners['open'].forEach(function(callback) {
                    callback(event);
                });
            }
        };

        this.ws.onclose = function(event) {
            console.log('WebSocket closed', event.reason || 'no reason');
            self.isManualClose = event.code === 1000;
            
            if (!self.isManualClose && self.retryCount < self.maxRetries) {
                var delay = self.calculateDelay();
                console.log('Reconnecting in', delay, 'ms (attempt', self.retryCount + 1, 'of', self.maxRetries, ')');
                
                setTimeout(function() {
                    self.retryCount++;
                    self.connect();
                }, delay);
            }

            if (self.eventListeners['close']) {
                self.eventListeners['close'].forEach(function(callback) {
                    callback(event);
                });
            }
        };

        this.ws.onmessage = function(event) {
            if (self.eventListeners['message']) {
                self.eventListeners['message'].forEach(function(callback) {
                    callback(event);
                });
            }
        };

        this.ws.onerror = function(event) {
            console.error('WebSocket error', event);
            if (self.eventListeners['error']) {
                self.eventListeners['error'].forEach(function(callback) {
                    callback(event);
                });
            }
        };
    };

    ReconnectingWebSocket.prototype.calculateDelay = function() {
        var delay = this.initialDelay * Math.pow(2, this.retryCount);
        return Math.min(delay, this.maxDelay);
    };

    ReconnectingWebSocket.prototype.send = function(data) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(data);
        } else {
            console.warn('WebSocket not open, cannot send:', data);
        }
    };

    ReconnectingWebSocket.prototype.close = function() {
        this.isManualClose = true;
        if (this.ws) {
            this.ws.close(1000, 'Manual close');
        }
    };

    ReconnectingWebSocket.prototype.addEventListener = function(event, callback) {
        if (!this.eventListeners[event]) {
            this.eventListeners[event] = [];
        }
        this.eventListeners[event].push(callback);
    };

    ReconnectingWebSocket.prototype.removeEventListener = function(event, callback) {
        if (this.eventListeners[event]) {
            this.eventListeners[event] = this.eventListeners[event].filter(function(cb) {
                return cb !== callback;
            });
        }
    };

    ReconnectingWebSocket.prototype.ping = function() {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify({ type: 'ping', timestamp: Date.now() }));
        }
    };

    window.ReconnectingWebSocket = ReconnectingWebSocket;
})();