# The iOS app

The iOS app is the web interface bundled into a native shell with
[Capacitor](https://capacitorjs.com). It is not a second interface: the same
Svelte build runs in both, and everything that differs between a browser and the
app lives in `web/src/lib/platform.js`.

It talks to **any** Verdande server. The first screen asks for the address, checks
it with `/api/v1/ping`, and then signs in there.

## How the app differs from a browser

| | Browser | App |
|---|---|---|
| Where the interface comes from | the server | inside the app, `capacitor://localhost` |
| Which server | the one it was loaded from | the one its owner typed in |
| Session | httpOnly cookie | a session token, sent as `Authorization: Bearer` |
| Live sync | WebSocket with the cookie | WebSocket opened with a one-use ticket (`POST /ws-ticket`) |
| Pictures and downloads | plain `<img>` and links | fetched with the token and shown from a blob (`nativemedia.js`) |
| Service worker, Web Push | yes | no — WKWebView has neither |
| Passkeys | yes | not yet — a passkey is bound to the server's domain |
| Creating the first account | yes | no — done in a browser, then sign in from the app |

The server side of this is `client: "app"` on `/auth/login`, the
`capacitor://localhost` CORS rule, and the ticket. All three are in
`docs/openapi.yaml`.

An app session slides: every use moves its end forward, up to
`VERDANDE_APP_SESSION_TTL` (180 days) unused. It is listed under *Enheder* in
settings with the name the app gave itself, and can be signed out from there.

## Building

Everything up to Xcode runs anywhere, and runs in CI (`iOS app shell`):

```sh
cd app
npm ci
npm run sync      # builds web with VERDANDE_TARGET=app, copies it into ios/
```

On a Mac with Xcode:

```sh
npm run open      # opens ios/App/App.xcodeproj
```

Choose a simulator and press Run. In the app, type the address of a Verdande —
`http://localhost:8080` reaches a dev server on the same Mac from the simulator.

Plain `http` is allowed to **local** addresses only (`NSAllowsLocalNetworking` in
`Info.plist`), so a home server without a certificate works on the same network;
anything on the internet has to be `https`.

`app/ios/` is committed. The web build inside it (`App/App/public`) and the
generated config are not — `npm run sync` puts them there.

The icon and launch image come from `web/static/icon.svg`:

```sh
cd web && node scripts/generate-ios-assets.js
```

## Putting it on a phone

These steps are the account owner's — they involve Apple's terms, a payment and
signing keys.

1. **Apple Developer Program** membership (99 USD a year) on the Apple ID that
   should own the app.
2. In Xcode → *Signing & Capabilities*: choose that team. The bundle id is
   `dk.nolimit.verdande`; it can change until the first upload and never after.
3. *Product → Archive*, then *Distribute App → TestFlight*.
4. In App Store Connect, add testers to the TestFlight build.

## Not yet

- **Notifications.** Web Push does not exist in the app. Notifications on an
  iPhone go through Apple's APNs, and the key that sends them is bound to the
  app's bundle id — so it cannot live on every self-hosted server. That means a
  small relay run by the app's publisher, which every instance with an app user
  would talk to. That is a new connection from other people's servers, and it
  waits on a decision before it is built.
- **The session token in the Keychain.** It is in the WebView's own storage for
  now, behind one object in `platform.js` so the Keychain plugin can replace it
  without anything else changing.
- **Passkeys** in the app.
- **Share sheet** (share text or a link *into* Verdande from another app).
