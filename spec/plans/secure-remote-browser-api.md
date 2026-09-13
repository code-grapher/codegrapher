---
format: https://specscore.md/plan-specification
status: Approved
---

# Plan: Secure remote browser API

**Status:** Approved
**Source Feature:** secure-remote-browser-api
**Date:** 2026-09-13
**Owner:** alex
**Supersedes:** —

## Summary

Let an operator expose the authenticated browser API on a remote IP address or
domain without sending its bearer credential over clear-text HTTP.

## Approach

Add direct TLS certificate/key inputs to the existing foreground API server,
refuse non-loopback clear-text binds, validate that the certificate covers the
advertised authority, and separate bind address from the public authority in
the generated browser link. Keep an explicit loopback-only reverse-proxy/tunnel
mode as the alternative to direct TLS.

## End-to-End User Journey

1. An operator starts `codegrapher serve --watch --api` on loopback. The
   observable good result is the existing HTTP API and `codegrapher.dev` link.
2. The operator chooses a remote bind without usable TLS. The observable good
   result is refusal before binding, with no insecure listener or browser link.
3. The operator supplies a certificate, key, and public DNS/IP authority. The
   observable good result is an HTTPS API and a copyable canonical browser link
   whose host is covered by that certificate, contains no filesystem path, and
   carries the secret only in the fragment.
4. The operator instead uses a trusted HTTPS tunnel over CodeGrapher's loopback
   listener. The observable good result is the same route and API, followed by
   explicit teardown of both processes.

## Tasks

### Task 1: Specify the secure remote serving boundary

**Verifies:** secure-remote-browser-api#ac:insecure-remote-listeners-are-refused, secure-remote-browser-api#ac:direct-tls-authority-is-verified
**Status:** planning

Define loopback compatibility, non-loopback refusal, certificate/key pairing,
public authority/SAN validation, browser trust, and proxy/tunnel alternatives.

### Task 2: Implement TLS and public authority selection

**Verifies:** secure-remote-browser-api#ac:insecure-remote-listeners-are-refused, secure-remote-browser-api#ac:direct-tls-authority-is-verified
**Depends-On:** 1
**Status:** planning

Wrap the existing API listener in TLS when configured, validate key, validity,
SAN, bind safety, and public authority before listening, and generate the
canonical route without weakening token or CORS handling. Keep external
termination loopback-only and explicit.

### Task 3: Prove local and remote API journeys

**Verifies:** secure-remote-browser-api#ac:trusted-https-serves-the-real-api
**Depends-On:** 2
**Status:** planning

Add focused unit/server coverage while retaining the existing loopback HTTP
journey. Run one bounded browser-trusted HTTPS journey in Chromium, Firefox,
and WebKit against a real repository, without certificate bypasses, and prove
joined teardown. Document direct TLS and reverse-proxy/tunnel operation.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/plan-specification*
