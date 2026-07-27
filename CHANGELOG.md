# Changelog

## [Unreleased]
### Added
- WebAuthn passkey support
- i18n localization (357 Russian + 353 English keys)
- PWA with service worker
- CORS support
- 2FA (TOTP) with backup codes
- SSE real-time game notifications
- Global rate limiting with Valkey support
- Breadcrumb navigation
- Image preview on upload
- HTTPS and HSTS support
- Sentry error tracking
- Prometheus metrics

### Fixed
- 260+ bugs and issues across 17 deep review passes
- SQL migration model consistency (000018_add_missing_columns)
- CSP nonce compliance (replaced 21 inline onclick handlers)
- 2FA enable/disable flow
- Tournament transaction integrity
- WebSocket orphaned connections
- Auto-save password leak in localStorage
