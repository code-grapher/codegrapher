---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Secure remote browser API

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/secure-remote-browser-api?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/secure-remote-browser-api?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/secure-remote-browser-api?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/secure-remote-browser-api?op=request-change) |
**Status:** Implementing
**Source Ideas:** —

## Summary

Serve the browser API remotely only through an authority-matching, browser-trusted HTTPS endpoint.

## Problem

The authenticated browser API is safe on loopback, but a browser URL may also
name a LAN address or public DNS host. Binding the same bearer-token API to a
non-loopback clear-text listener would expose credentials and source content in
transit. Merely enabling TLS is insufficient when the certificate does not
cover the authority in the browser URL or is not trusted by the browser.

## Behavior

### Loopback remains the only clear-text boundary

#### REQ: cleartext-is-loopback-only

HTTP serving MUST remain available on the browser client's supported loopback
authorities: `localhost`, `127.0.0.1`, and `[::1]`. Other addresses, including
other IPs in `127.0.0.0/8`, MUST use TLS so a provider never emits a link that
the browser interprets with a different scheme. A listener that can accept
non-loopback traffic MUST NOT start in HTTP mode. Wildcard addresses are
non-loopback even when a caller intends to use them locally. Invalid, missing,
or partially supplied TLS configuration MUST fail before the public listener is
created; there is no insecure fallback and no browser link is emitted.

### Direct TLS validates the advertised authority

#### REQ: direct-tls-matches-browser-authority

Direct HTTPS serving MUST require a certificate, matching private key, and an
explicit public authority. The authority MUST be a DNS name, IPv4 address, or
bracketed IPv6 address with an optional valid port and no scheme, credentials,
path, query, or fragment. Startup MUST reject expired/not-yet-valid
certificates, mismatched keys, and certificates whose DNS/IP subject
alternative names do not cover that authority. The canonical
`/browse/<authority>/...` link MUST use the validated authority and place the
browser credential only in its URL fragment.

### External TLS termination stays loopback-confined

#### REQ: explicit-external-termination

An HTTPS reverse proxy or tunnel MAY terminate browser-trusted TLS while the
CodeGrapher listener remains HTTP on loopback. This mode MUST be explicit,
require an HTTPS public authority distinct from the loopback bind address, and
emit the same canonical browser route. It MUST NOT make CodeGrapher bind an
unprotected non-loopback socket. Documentation MUST include teardown and the
existing exact-origin CORS boundary.

### Trust is proved by a real browser

#### REQ: browser-trusted-remote-journey

Remote acceptance MUST use a certificate chain trusted by the browser for the
authority in the URL. Chromium, Firefox, and WebKit MUST connect without an
ignore-certificate-errors setting, bypass prompt, or fixture daemon. They MUST
use the existing generated TypeSpec client and authenticated
`/codegrapher/v1/` contract.

## Acceptance Criteria

### AC: insecure-remote-listeners-are-refused

**Given** loopback, wildcard, DNS, IPv4, and bracketed IPv6 bind addresses with
absent, partial, malformed, or complete TLS inputs

**When** `codegrapher serve --api` validates its public listener

**Then** HTTP remains supported for `localhost`, `127.0.0.1`, and `[::1]`, every
other clear-text or invalid TLS configuration fails before binding, and failure
emits neither an insecure listener nor a token-bearing browser link.

### AC: direct-tls-authority-is-verified

**Given** a certificate/key pair and explicit browser authority

**When** direct HTTPS serving starts

**Then** the key matches, the certificate is currently valid, its SAN covers
the authority host or IP, and the emitted canonical browser link uses exactly
that authority with the credential only in the fragment

**And** mismatched authority, key, validity, or malformed authority is rejected
without falling back to HTTP.

### AC: trusted-https-serves-the-real-api

**Given** either valid direct TLS or explicit loopback-only serving behind an
HTTPS tunnel/reverse proxy with a browser-trusted certificate

**When** Chromium, Firefox, and WebKit open the generated browser route without
certificate bypasses

**Then** each engine authenticates to the real registered repository through
the generated `/codegrapher/v1/` client and can read one real indexed symbol

**And** joined teardown leaves no CodeGrapher listener, proxy, tunnel, or test
repository owner running.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*
