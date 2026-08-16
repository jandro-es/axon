# Companion appcast

Where Sparkle looks for Companion updates, and how a release gets there.

## The feed

```
https://github.com/jandro-es/axon/releases/latest/download/companion-appcast.xml
```

GitHub Releases, alongside the daemon's own release assets — one hosting
story, no extra infrastructure. The `latest/download/` form always resolves to
the newest release's asset, so the URL baked into shipped apps never changes.

This is the **only** non-loopback host Companion contacts. It is disclosed in
Settings → About, per CFR-81.

## Publishing a release

```bash
# 1. Bump both in apps/companion/version.env. BUILD_NUMBER must INCREASE:
#    Sparkle compares CFBundleVersion, so a release that reuses it is
#    invisible to every existing install. make_appcast.sh refuses to publish
#    one, but only if the previous appcast is present in dist/ — pull the
#    published one down first when releasing from a clean checkout:
curl -fsSL -o apps/companion/dist/companion-appcast.xml \
  https://github.com/jandro-es/axon/releases/latest/download/companion-appcast.xml

# 2. Build, sign, notarize, staple:
make companion-release

# 3. Sign the appcast entry:
make companion-appcast

# 4. Upload BOTH to a release tagged companion-v<version>:
gh release create companion-v0.1.0 \
  apps/companion/dist/Axon-0.1.0.zip \
  apps/companion/dist/companion-appcast.xml \
  --title "Axon Companion 0.1.0" --notes-file <notes>
```

The tag must be `companion-v<version>` — `SPARKLE_DOWNLOAD_URL_PREFIX` in
`version.env` builds each enclosure URL from it, and a mismatched tag produces
an appcast whose download links 404.

## Keys

Two separate credentials, neither in this repo:

| | Purpose | Where it lives | If lost |
|---|---|---|---|
| **Developer ID Application** certificate | Signs the app so Gatekeeper accepts it | Login keychain | Re-issue from the Apple Developer portal |
| **Sparkle EdDSA private key** | Signs each appcast entry | Login keychain (`generate_keys`) | **Unrecoverable.** Every existing install rejects every future update; the only fix is a manual reinstall. |

The Sparkle public key is committed as `SPARKLE_PUBLIC_KEY` in `version.env`
and baked into `Info.plist` as `SUPublicEDKey`. The private half must be backed
up somewhere durable — a password manager entry is enough.

Export it with:

```bash
.build/artifacts/sparkle/Sparkle/bin/generate_keys -x sparkle-private-key.txt
# ...then store that file somewhere safe and delete the local copy.
```

## Verifying a published feed

```bash
curl -fsSL https://github.com/jandro-es/axon/releases/latest/download/companion-appcast.xml
```

Each `<item>` must carry a `sparkle:edSignature`, a `sparkle:version` greater
than the previous entry's, and an `<enclosure url>` that actually resolves.
