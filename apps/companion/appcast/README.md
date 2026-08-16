# Companion appcast

Where Sparkle looks for Companion updates, and how a release gets there.

## The feed

```
https://raw.githubusercontent.com/jandro-es/axon/main/apps/companion/appcast/companion-appcast.xml
```

**`companion-appcast.xml` in this directory IS the published feed.** Committing
it is what publishes an update.

It is deliberately *not* served from
`releases/latest/download/`. GitHub resolves `latest` across **all** releases in
a repo, and this repo publishes two independent streams — daemon `v*` and
`companion-v*`. Whichever released most recently wins, so the next daemon
release would take `latest` over and 404 this feed permanently, for every copy
already installed with that URL baked into its Info.plist. A repo path does not
care about release order.

Release *assets* still live on the `companion-v<version>` release — the
`<enclosure url>` in each entry points at its own tag, which is stable.

This is the **only** non-loopback host Companion contacts. It is disclosed in
Settings → About, per CFR-81.

## Publishing a release

```bash
# 1. Bump both in apps/companion/version.env. BUILD_NUMBER must INCREASE:
#    Sparkle compares CFBundleVersion, so a release that reuses it is
#    invisible to every existing install. make_appcast.sh refuses to publish
#    one — the previous entries are in the committed appcast, so a clean
#    checkout already has what it needs to check.

# 2. Build, sign, notarize, staple:
make companion-release

# 3. Sign the appcast entry:
make companion-appcast

# 4. Publish. The tag must be companion-v<version>, and --latest=false keeps
#    the daemon's own release as the repo's headline:
gh release create companion-v0.1.0 \
  apps/companion/dist/Axon-0.1.0.zip \
  --title "Axon Companion 0.1.0" --notes-file <notes> --latest=false

# 5. Commit the appcast — THIS is what makes the update visible:
git add apps/companion/appcast/companion-appcast.xml && git commit && git push
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
curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/apps/companion/appcast/companion-appcast.xml
```

Each `<item>` must carry a `sparkle:edSignature`, a `sparkle:version` greater
than the previous entry's, and an `<enclosure url>` that actually resolves.
