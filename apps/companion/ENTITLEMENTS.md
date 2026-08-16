# Axon Companion — entitlements rationale

Companion runs with the **hardened runtime** and is **not App-Sandboxed** in
v1. This file records why, so the decision is reviewable rather than assumed.

## Why not sandboxed

Two structural reasons, not laziness:

1. **Companion executes the `axon` CLI.** The binary lives outside any
   container (`/usr/local/bin`, `/opt/homebrew/bin`, or a path the user sets),
   and a sandboxed app cannot `exec` an arbitrary binary at a user-chosen path.
   Every mutation Companion performs goes through that CLI by design (CFR-41,
   CFR-11) — removing the exec removes the app's whole reason to exist.

2. **It reveals the vault and log folders in Finder.** Those paths come from
   `axon profiles --json`; the user never picks them in Companion. Under the
   sandbox each would need a security-scoped bookmark granted through an open
   panel, for a folder the user did not choose in this app — a confusing
   permission prompt for a one-click "show me my notes".

Revisit after 2.0 alongside the daemon's own least-privilege work
([[Axon 2.0 — PRD]] P6). The likely shape is an XPC helper owning the exec
path, plus bookmarks for the folder reveals.

## What is locked down

- **Hardened runtime** is enabled (`codesign --options runtime`).
- **No JIT**, no unsigned executable memory, no `DYLD_*` injection. Each of
  those is a separate entitlement and every one is omitted, which means denied.
- **No network server entitlement.** Companion is a client only, and only to
  `127.0.0.1` plus Sparkle's appcast host.
- **No** camera, microphone, contacts, calendar, photos, location, or Address
  Book access.
- **No Keychain access.** Companion never reads or holds a secret; it does not
  open `~/.axon/.env` at all, and it decodes `oauth_token_ref` out of
  `axon profiles --json` deliberately nowhere (see `ProfileInfo`).

## The one entitlement that is present

`com.apple.security.cs.disable-library-validation`

Required by **Sparkle 2**: its updater launches helper tools signed by the
Sparkle project rather than by our Team ID, and under the hardened runtime
library validation rejects them, so updates fail at install time.

It is the narrowest option of the three that would work here — it still
requires loaded code to be signed by Apple or a Developer ID, so it cannot be
used to load unsigned code. The alternatives (`allow-dyld-environment-
variables`, `allow-unsigned-executable-memory`) are strictly broader and are
not used.

If Companion ever drops Sparkle, this entitlement goes with it.
