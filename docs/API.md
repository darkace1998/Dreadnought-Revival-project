# Dreadnought Private Server — API Reference

## Auth Server (`:8081`)

Replaces `profile-api.prod.greybox.sixfoot.live`.

---

### POST `/auth/register`
Register a new player account.

**Request:**
```json
{
  "username": "string (required)",
  "email": "string (required)",
  "password": "string (min 6 chars, required)"
}
```
**Response 201:**
```json
{"id": "uuid", "username": "string"}
```
**Errors:** 400 (validation), 409 (username/email taken)

---

### POST `/auth/`
Login and receive a JWT. This matches the Dreadnought launcher's auth endpoint.

**Request:**
```json
{"username": "string", "password": "string"}
```
**Response 200:**
```json
{
  "token": "eyJ...",
  "token_type": "Bearer",
  "user_id": "uuid",
  "username": "string",
  "realm": "dreadnought.pc-us",
  "expires_at": "2026-01-01T00:00:00Z"
}
```
**Errors:** 401 (invalid credentials)

---

### GET `/auth/me`
Returns the authenticated user's info. Requires `Authorization: Bearer <token>`.

**Response 200:**
```json
{"user_id": "uuid", "username": "string", "realm": "dreadnought.pc-us"}
```

---

### POST `/auth/logout`
Invalidates the current session token.

**Response 200:** `{"status": "logged out"}`

---

### GET `/health`
**Response 200:** `{"status": "ok", "service": "auth-server"}`

---

## Legacy API (`:8082`)

Replaces `legacyapi.prod.greybox.sixfoot.live`.

---

### GET `/v2/dreadnought/launcher/dn/tiles/`
Returns launcher news tiles (no auth required).

**Response 200:**
```json
{"tiles": [{"id": "welcome", "title": "...", "body": "...", "type": "announcement"}]}
```

---

### GET `/v2/dreadnought/ageconsent/`
Always returns approved (no auth required).

**Response 200:** `{"consent_required": false, "consented": true}`

---

### GET `/v2/dreadnought/player/{id}/profile`
Requires auth. Auto-creates profile on first access.

**Response 200:**
```json
{
  "user_id": "uuid",
  "display_name": "string",
  "created_at": "datetime",
  "stats": {
    "kills": 0, "deaths": 0, "matches_played": 0,
    "wins": 0, "xp_total": 0, "credits": 10000
  }
}
```

---

### GET `/v2/dreadnought/player/{id}/inventory`
Requires auth. Seeds and normalizes starter ships, loadouts, weapons, and abilities.

**Response 200:**
```json
{
  "user_id": "uuid",
  "items": [
    {"id": "uuid", "item_type": "ship", "item_id": "184483982", "ship_id": 184483982, "name": "Assault Medium T1", "acquired_at": "datetime"},
    {"id": "uuid", "item_type": "weapon", "item_id": "100597772", "loadout_id": 33489262, "slot_name": "weaponPrimary", "name": "Repeater Turrets", "acquired_at": "datetime"}
  ]
}
```

---

### POST `/v2/dreadnought/match/result`
Record match outcome. Requires auth.

**Request:**
```json
{
  "match_id": "uuid (optional, auto-generated)",
  "mode": "TeamDeathmatch",
  "map": "Charon",
  "players": [
    {"user_id": "uuid", "team": 0, "score": 1000, "kills": 5, "deaths": 2, "damage": 50000, "won": true}
  ]
}
```
**Response 200:** `{"match_id": "uuid", "status": "recorded"}`

---

## YMmogbrain Emulator (`:8083`)

Handles matchmaking, match assignment, and chat.

---

### POST `/mmog/queue`
Join the matchmaking queue. Requires auth.

**Request:**
```json
{"game_mode": "TeamDeathmatch", "tier_min": 1, "tier_max": 5}
```
**Response 201:** `{"entry_id": "uuid", "status": "waiting", "game_mode": "TeamDeathmatch"}`

---

### GET `/mmog/queue/status`
Poll for match assignment. Requires auth.

**Response 200 (waiting):** `{"status": "waiting", "entry_id": "uuid"}`

**Response 200 (matched):**
```json
{
  "status": "matched",
  "match_id": "uuid",
  "server_ip": "1.2.3.4",
  "server_port": 7777,
  "game_mode": "TeamDeathmatch",
  "map": "Charon"
}
```

---

### DELETE `/mmog/queue`
Leave the matchmaking queue. Requires auth.

**Response 200:** `{"status": "left queue"}`

---

### GET `/mmog/match/{id}`
Get match details. Requires auth.

**Response 200:**
```json
{
  "match_id": "uuid",
  "game_mode": "TeamDeathmatch",
  "map": "Charon",
  "server_ip": "1.2.3.4",
  "server_port": 7777,
  "status": "active",
  "players": [{"user_id": "uuid", "team": 0}]
}
```

---

### POST `/mmog/chat`
Send a chat message. Requires auth.

**Request:** `{"channel": "global", "content": "Hello!"}`
**Response 201:** `{"message_id": "uuid"}`

---

### GET `/mmog/chat?channel=global`
Get recent chat messages (no auth required).

**Response 200:**
```json
{"channel": "global", "messages": [{"id": "uuid", "sender_id": "uuid", "content": "Hello!", "sent_at": "datetime"}]}
```

---

## Master Server (`:8084`)

Server registry and browser.

---

### POST `/servers/register`
Register a game server instance (called by game-manager).

**Request:**
```json
{"name": "Match-abc123", "ip": "1.2.3.4", "port": 7777, "game_mode": "TeamDeathmatch", "map": "Charon", "max_players": 10}
```
**Response 201:** `{"id": "uuid", "status": "registered"}`

---

### POST `/servers/{id}/heartbeat`
Keep-alive from game server (every 30s).

**Request:** `{"current_players": 8}`
**Response 200:** `{"status": "ok"}`

---

### DELETE `/servers/{id}`
Deregister a game server.

**Response 200:** `{"status": "deregistered"}`

---

### GET `/servers`
Server browser list (online servers only).

**Response 200:**
```json
{
  "count": 2,
  "servers": [
    {"id": "uuid", "name": "Match-abc123", "ip": "1.2.3.4", "port": 7777,
     "game_mode": "TeamDeathmatch", "map": "Charon", "current_players": 8, "max_players": 10, "status": "online"}
  ]
}
```

---

### GET `/health`
**Response 200:** `{"status": "ok", "service": "master-server", "servers_online": 2}`

---

## Game Instance Manager (`:8085`)

Spawns and manages `DreadGame-Win64-Shipping.exe` processes via Wine.

---

### POST `/instances`
Launch a new match instance.

**Request:**
```json
{"game_mode": "TeamDeathmatch", "map": "Charon", "players": ["user-uuid-1", "user-uuid-2"]}
```
**Response 201:**
```json
{"instance_id": "uuid", "match_id": "uuid", "ip": "1.2.3.4", "port": 7777, "game_mode": "TeamDeathmatch", "map": "Charon"}
```

---

### GET `/instances`
List all running instances.

**Response 200:**
```json
{"count": 1, "ports_used": 1, "instances": [{"id": "uuid", "match_id": "uuid", "port": 7777, "game_mode": "...", "map": "...", "players": [], "started_at": "datetime"}]}
```

---

### DELETE `/instances/{id}`
Stop a running instance.

**Response 200:** `{"status": "stopped"}`

---

### GET `/health`
**Response 200:** `{"status": "ok", "service": "game-manager", "instances": 1, "ports_used": 1}`
