# WebServicesPlugin: the complete endpoint table (and why "session type" is a dead end)

Investigated 2026-08-05 in answer to AGENT-CHAT C32.2, which read the client's
"wrong session type" error string as evidence that the original backend had a
**server-flavoured session**, and that we might have to build one so battle
servers can obtain data.

That reading is not supported. What follows is what was measured.

## The call C32.2 pointed at

`FUN_39BC10` (781 bytes, `.pdata` entry, `LoginGateManager.cpp`) is the login-gate
step. It calls **`FUN_2AADFD0`** at `0x39BCAA` — that is the function that actually
builds the request, and the one that owns all three error strings:

```
FUN_2AADFD0 (706 bytes)
  2AAE0AB  'Mmog connection info request'
  2AAE0BF  'Requesting mmog connection info...'
  2AAE0DD  'Requesting mmog connection info failed.'
  2AAE124  'Mmog Connection Info - 400 - If the Authorization header refers to a valid
            session, but the session is the wrong session type for this gateway. ...'
  2AAE18D  'Mmog Connection Info - 401 - Not authorized (missing or invalid Authorization header).'
  2AAE1F6  'Mmog Connection Info - 403 - You have been blacklisted.'
```

The 400/401/403 strings are **pre-registered human-readable descriptions** of
status codes, sitting alongside the request's own name. They are logged only when
the request fails. They document the *original Greybox backend's* behaviour; they
are not something the client can produce or select.

## The request, as actually observed

From a live client log (`/root/projects/DreadGame.log`, 2026-08-04):

```
LogWebServicesPlugin: Requesting mmog connection info...
LogWebServicesPlugin: URL: https://10.0.0.73:65443/api/v1/play/lkg
LogWebServicesPlugin: X-Request-ID: 11290C56-47B1-CD91-4207-88A263141EC2
LogWebServicesPlugin: User-Agent: X-UnrealEngine-Agent
LogWebServicesPlugin: Authorization: Session 924b0769-2ea0-4e48-b3ca-316b146863f0
```

`GET /api/v1/play/lkg`, no query string. We already serve it
(`mmogbrain/gateway_server.go:77`, `handleGWPlayLkg`) and already accept both
Authorization schemes (`Session ` at :137/:189/:322, `Bearer ` at :161).

**The failure strings have never fired.** `grep -c` for
`Mmog Connection Info - 40` and `Requesting mmog connection info failed` returns
**0** in all three client logs on this box. There is no bug here to fix.

## The full constant table

The endpoint strings have no `lea` xrefs and no absolute pointers; they are
returned by one-line accessor functions collected in a contiguous pointer table
at `.rdata:0x02DD7320-0x02DD7568`. Dumping it gives the plugin's entire surface:

| RVA | accessor | literal |
|---|---|---|
| 02DD7320 | FUN_20E6D0 | `POST` |
| 02DD7328 | FUN_20D8C0 | `GET` |
| 02DD7330 | FUN_20DAC0 | `X-Request-ID` |
| 02DD7338 | FUN_20DA80 | `Authorization` |
| 02DD7340 | FUN_20D980 | `Session ` |
| 02DD7348 | FUN_20D940 | `Bearer ` |
| 02DD7350 | FUN_20D9C0 | `Content-Type` |
| 02DD7358 | FUN_20DA00 | `application/json` |
| 02DD7360 | FUN_20DB00 | `User-Agent` |
| 02DD7368 | FUN_20DB40 | `X-UnrealEngine-Agent` |
| 02DD7370 | FUN_20DA40 | `Accept-Encoding` |
| 02DD7378 | FUN_20D900 | `application/gzip` |
| 02DD7380 | FUN_20D700 | `MmogAddress=` |
| 02DD7388 | FUN_20D740 | `MmogPort=` |
| 02DD7390 | FUN_20D780 | `promoted=` |
| 02DD7398 | FUN_20D6C0 | `UseMarketItemCatalogTestData=` |
| 02DD73A0 | FUN_20D680 | `UseMarketCurrencyCatalogTestData=` |
| 02DD73A8 | FUN_20D640 | `UseMarketBundlesTestData=` |
| 02DD73B0 | FUN_20E610 | `api/v1/play` |
| 02DD73B8 | FUN_20E690 | `lkg` |
| 02DD73C0 | FUN_20E650 | `latest` |
| 02DD73C8 | FUN_20DB80 | `api/v1/session/touch` |
| 02DD73D0 | FUN_20D560 | `api/v1/authentication/login` |
| 02DD73D8 | FUN_20D5A0 | `api/v1/authentication/logout` |
| 02DD73E0 | FUN_20E310 | `api/v1/account/legal` |
| 02DD73E8 | FUN_20E350 | `document` |
| 02DD73F0 | FUN_20E390 | `text` |
| 02DD73F8 | FUN_20DBD0 | `api/v1/account/legal/document/accept` |
| 02DD7400 | FUN_20DED0 | `api/v1/account/legal/attest` |
| 02DD7408 | FUN_20E5D0 | `api/v1/ping` |
| 02DD7410 | FUN_20E510 | `api/v1/catalog/digital_items_rmt` |
| 02DD7418 | FUN_20E550 | `api/v1/catalog/digital_items_vc` |
| 02DD7420 | FUN_20E490 | `api/v1/catalog/currency_pack_rmt` |
| 02DD7428 | FUN_20E4D0 | `api/v1/catalog/currency_pack_vc` |
| 02DD7430 | FUN_20E450 | `api/v1/bundles` |
| 02DD7438.. | | response field names: `SessionID`, `Username`, `Code`, `Wait`, `Position`, `Priority`, `NextRequestDelay`, **`serverHost`**, **`serverPort`**, `Documents`, `Attestations`, … |

That is the whole thing. **There is no server, dedicated, gameserver, or
service-account endpoint.** The table is contiguous and bounded on both sides by
unrelated data, so this is the complete set, not a sample.

## Why no server session is needed

The battle server is the same executable run headless. Across **all eight**
battle-server logs in `run/battle-logs/`:

```
LogHttp: Start request   →  0 occurrences   (total, all logs)
LoginGateManager         →  0 occurrences   (total, all logs)
```

It loads `WebServicesPlugin` and `YMmogbrain` as plugins and then never issues a
single HTTP request. The login gate is a UI screen flow
(`UI_Screen_LoginGate`); a `-server -nullrhi` process never enters it.

So there is nothing for a server-flavoured session to authenticate: the battle
server does not ask the backend for data, and adding a session type it never
requests would change nothing. This is the same wall `battle-server-mod/`
already exists to work around — the host's loadout manager can only be filled
from a login the process never performs. C32.2's finding is, if anything,
independent confirmation that the DLL route was necessary.

## What "session type" most likely meant (NOT verified)

Five strings mention it — the mmog connection info 400 and four
`api/v1/account/legal*` 400s. All are gateway-side descriptions. The client
creates exactly one kind of session ("Create session request" →
`api/v1/authentication/login`) and presents it as `Authorization: Session <uuid>`.
A plausible reading is that Greybox ran several products behind one auth service
and tagged sessions per product/gateway, so a Dreadnought session presented to
another product's gateway got a 400. **This is theory** — nothing in the binary
names a second session type, and no measurement here distinguishes it from other
explanations. It has no bearing on our backend either way.

## Loose ends worth knowing about (unused, not blocking)

`MmogAddress=`, `MmogPort=` and `promoted=` are query parameters in the same
table as `api/v1/play`. The observed request carries **no** query string, so none
of them is exercised in our flow. `MmogAddress=`/`MmogPort=` reading as a client
-side override of the Firmament endpoint is **theory** — untested.
The three `Use*TestData=` parameters sit beside them and are equally unused.
