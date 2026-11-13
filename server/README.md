# Burn Before Reading
**Burn Before Reading** is a project to physically print messages left by people on a website. The short messages are submitted by vetted users via a simple website and handled by this Golang API. A Raspberry Pi does the interface with the printer. What makes this project stand out is that messages will be printed almost instantly by a *receipt thermal printer* at home.

> Thermal printers do not require any ink to print as they directly burn tiny dots on the paper

## High-level overview
Here are the main steps involved in submitting, processing, and, finally, printing a message:

1. An admin creates a user account and links it to the printers that user may send to. The API returns a one-time **invite**.
2. The user claims the invite from their device and receives a **session token**.
3. The user submits a message to one of their allowed recipients. The API stores it with the status `awaiting_approval`. _(Messages sent by an admin skip moderation and start as `pending`)_
4. A moderator approves or rejects the message.
5. The Raspberry Pi polls the API at regular intervals and claims the oldest `pending` message of its printer.
6. The Raspberry Pi renders the message according to the printer's specificities and reports back.
7. The printer prints the message.

## Roles and authentication

Every protected route expects an `Authorization: Bearer <token>` header. The token type decides which route group can be called:

| Role | Route prefix | Token | Where it comes from |
|---|---|---|---|
| Admin | `/api/v1/admin` | the `ADMIN_TOKEN` environment variable | server configuration (placeholder until real admin accounts exist) |
| Printer | `/api/v1/printer` | `bbr_printer_…` | `POST /admin/printers` or `POST /admin/printers/{id}/rotate_token` |
| User | `/api/v1/user` | `bbr_user_…` | `POST /users/claim`, in exchange for a `bbr_invite_…` invite |
| Public | `/api/v1/users/claim`, `/health` | none | |

- Printer, user and invite tokens are only ever stored as `sha256(token)`. Someone who gets a copy of the database can't use it to call the API.
- Every response that contains a plain token or invite is sent with `Cache-Control: no-store`, and it is the only time that value is shown.
- A missing or invalid token gets `401`. A `500` from the auth middleware means the server couldn't check the token (for example, the database is down): retry later rather than treating the token as revoked.

### Printer tokens
- **Issued once:** `POST /api/v1/admin/printers` returns the new printer with a `token` field. Copy it into the Pi's configuration.
- **Rotation:** `POST /api/v1/admin/printers/{id}/rotate_token` returns a new token. The old one stops working immediately. Use it when a token is lost (for example after reflashing the Pi's SD card) or may have leaked. The printer keeps its ID and message history.
- The printer's identity comes **from the token**, so printer routes don't take a printer ID.
- A printer can only claim and update **its own** messages. Another printer's message gets `404`, the same as an unknown ID, so its existence isn't revealed.

### User invites and sessions
- **Invite:** `POST /api/v1/admin/users` creates the user and returns a `bbr_invite_…` invite, valid for **2 days** and usable **once**. `POST /api/v1/admin/users/{id}/invite` issues a new one; the previous invite stops working, but existing sessions are kept.
- **Claim:** the user's device sends the invite to `POST /api/v1/users/claim` with a `device_name`. The response holds a `bbr_user_…` session token. An unknown, expired or already used invite gets `401`.
- **Sessions:** a user can have several sessions, one per device. An admin can list them and revoke one or all of them. A revoked session token gets `401`.
- **Printer access:** a user may only send to the printers they are linked to (`user_printers`). Sending to any other printer gets `404 printer not found`, so other printers' existence isn't revealed. `GET /api/v1/user/printers` lists the allowed ones.

## Database

```mermaid
erDiagram
    users ||--o{ user_sessions : "signs in with"
    users ||--o{ user_printers : "may send to"
    printers ||--o{ user_printers : "accepts from"
    printers ||--o{ messages : "prints"
    users |o--o{ messages : "sends (NULL for admin)"

    users {
        UUID id PK
        VARCHAR name "1..20 chars, shown as sender_name"
        BYTEA invite_hash UK "sha256 of the pending invite, NULL once claimed"
        TIMESTAMPTZ invite_expires_at "set together with invite_hash"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ last_updated_at "trigger"
    }
    user_sessions {
        UUID id PK
        UUID user_id FK "ON DELETE CASCADE"
        VARCHAR device_name "1..50 chars"
        BYTEA token_hash UK "sha256 of the bbr_user_ token"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ last_used_at "not written yet"
        TIMESTAMPTZ revoked_at "NULL while active"
    }
    user_printers {
        UUID user_id PK, FK "ON DELETE CASCADE"
        UUID printer_id PK, FK "ON DELETE CASCADE"
    }
    printers {
        UUID id PK
        VARCHAR model "1..50 chars"
        VARCHAR recipient_name "1..20 chars"
        TIME quiet_start "NULL together with quiet_end"
        TIME quiet_end
        TEXT timezone "IANA name, default Europe/Brussels"
        TEXT error "last error reported by heartbeat"
        BYTEA token_hash UK "sha256 of the bbr_printer_ token"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ last_updated_at "trigger, ignores heartbeats"
        TIMESTAMPTZ last_heartbeat_at
    }
    messages {
        UUID id PK
        BIGINT sequence_number UK "identity, used for ordering lists"
        UUID sender_id FK "NULL for admin messages"
        INET sender_ip
        VARCHAR source "web or admin"
        UUID printer_id FK
        TEXT content "1..500 chars"
        VARCHAR status "see Message states"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ moderated_at
        TIMESTAMPTZ claimed_at
        TIMESTAMPTZ processed_at
        TIMESTAMPTZ printed_at
        TIMESTAMPTZ last_updated_at "trigger"
        SMALLINT attempts "incremented on each claim"
        TEXT last_error
    }
```

Indexes worth knowing about: `idx_messages_pending (printer_id, created_at) WHERE status = 'pending'` serves the claim query, and `idx_messages_sender (sender_id, sequence_number)` serves a user's message list.

### What changed with the users table
User accounts replaced the anonymous, free-text sender:

- **`messages.sender_name` was removed** and replaced by `sender_id`, a foreign key to `users`. The `sender_name` field still exists in API responses, but it is now read from `users.name` (it is `null` for admin messages).
- **Submitting requires a session.** The public `POST /api/v1/messages` and `GET /api/v1/messages/{id}` routes are gone. Users now call `POST /api/v1/user/messages` and `GET /api/v1/user/messages/{id}` with their session token, and can only read their own messages.
- **The request body changed** from `{sender, body}` to `{printer_id, content}`. The sender comes from the session token, so it can't be impersonated.
- **Printers are no longer open to anyone who knows their UUID.** `user_printers` decides which printers each user may send to.
- **New tables:** `users`, `user_sessions` and `user_printers`, and new admin routes to manage users, invites and sessions (see [Admin routes](#admin-routes)).

## Message lifecycle

### Request flow

```mermaid
sequenceDiagram
autonumber
actor U as Web user
actor A as Admin
participant API as bbr API<br/>(handler → service → store)
participant DB as Postgres
participant P as Printer agent
Note over U,P: 0. Onboarding
A->>API: POST /api/v1/admin/users {name, printer_ids}
API->>DB: INSERT users (invite_hash, invite_expires_at)<br/>INSERT user_printers
API-->>A: 201 {user, invite}
U->>API: POST /api/v1/users/claim {invite, device_name}
API->>DB: clear invite_hash WHERE not expired<br/>INSERT user_sessions
API-->>U: 200 {token}
Note over U,P: 1. Submission
U->>API: POST /api/v1/user/messages {printer_id, content}
API->>DB: check user_printers (unknown pair → 404)
API->>API: sanitize and validate content (1..500)<br/>source=web, capture sender_ip
API->>DB: INSERT sender_id, status=awaiting_approval
API-->>U: 201 {message}
A->>API: POST /api/v1/admin/messages {printer_id, content}
API->>DB: INSERT sender_id=NULL, source=admin, status=pending
API-->>A: 201
Note over U,P: 2. Moderation
A->>API: GET /api/v1/admin/messages?status=awaiting_approval
API-->>A: 200 [messages]
alt approve
A->>API: PATCH /api/v1/admin/messages/{id}/approve
API->>DB: UPDATE status=pending, moderated_at=now()<br/>WHERE status=awaiting_approval
else reject
A->>API: PATCH /api/v1/admin/messages/{id}/reject
API->>DB: UPDATE status=rejected, moderated_at=now()<br/>WHERE status=awaiting_approval
end
API-->>A: 200 (409 if status already changed)
Note over U,P: 3. Printing
Note over API,P: The printer ID comes from the token (never from the URL).
loop every few seconds
P->>API: POST /api/v1/printer/heartbeat {error?}
API->>DB: UPDATE printers SET last_heartbeat_at=now(), error
end
P->>API: POST /api/v1/printer/messages/oldest/claim
alt quiet hours
API-->>P: 204 No Content (not recorded as a heartbeat)
else
API->>DB: SELECT oldest pending of this printer FOR UPDATE SKIP LOCKED<br/>UPDATE status=claimed, claimed_at, attempts+1<br/>(also counts as a heartbeat)
alt queue empty
API-->>P: 204 No Content
else job claimed
API-->>P: 200 {message}
P->>API: PATCH /api/v1/printer/messages/{id}/processed
API->>DB: UPDATE status=processed, processed_at<br/>WHERE status=claimed AND printer_id=caller
alt print ok
P->>API: PATCH /api/v1/printer/messages/{id}/printed
API->>DB: UPDATE status=printed, printed_at<br/>WHERE status=processed AND printer_id=caller
else print error
P->>API: PATCH /api/v1/printer/messages/{id}/failed {error}
API->>DB: UPDATE last_error,<br/>status = attempts < 3 ? pending : failed<br/>WHERE status IN (claimed, processed) AND printer_id=caller
end
end
end
Note over U,P: 4. Status check
U->>API: GET /api/v1/user/messages/{id}
API-->>U: 200 {message} (404 if not the caller's)
```

### Message states
```mermaid
stateDiagram-v2
direction LR
[*] --> awaiting_approval : submitted by a user
[*] --> pending : submitted by an admin
awaiting_approval --> pending : admin approves<br/>(moderated_at)
awaiting_approval --> rejected : admin rejects<br/>(moderated_at)
pending --> claimed : printer claims oldest job<br/>(claimed_at, attempts+1)
claimed --> processed : printer has rendered it<br/>(processed_at)
processed --> printed : success (printed_at)
claimed --> pending : error or claim timed out,<br/>attempts < 3 (last_error)
processed --> pending : error or claim timed out,<br/>attempts < 3 (last_error)
claimed --> failed : attempts >= 3
processed --> failed : attempts >= 3
failed --> pending : admin requeues
printed --> [*]
rejected --> [*]
```

- **Retries:** a message is attempted at most 3 times. `attempts` is incremented on each claim.
- **Stale claims:** a background job runs at startup and then every 5 minutes. It releases messages that have been `claimed` or `processed` for more than 2 minutes, with `last_error = 'claim timed out'`, following the same retry rule.
- **Requeue:** `attempts` is not reset, so a requeued message that fails again goes straight back to `failed`.

## API reference

All request and response bodies are JSON. Errors always have the shape `{"error": "…"}`. Unknown fields in a request body are rejected with `400`.

### Status codes

| Code | Meaning |
|---|---|
| `200` / `201` | Success / resource created |
| `204` | Success with no body (heartbeat, session revocation, empty claim queue or quiet hours) |
| `400` | Invalid body, path ID, query parameter or field value, including an unknown printer ID inside a request body |
| `401` | Missing or invalid token, or an invalid invite |
| `404` | The resource in the URL doesn't exist or isn't visible to the caller (another printer's message, another user's message, a printer the user may not send to) |
| `409` | The message's current status doesn't allow this transition |
| `500` | Unexpected server error |

### List query parameters

These apply to `GET /admin/messages`, `GET /admin/printers`, `GET /admin/users` and `GET /user/messages`. An invalid value gets `400`; it is never silently ignored or replaced.

| Parameter | Values | Default |
|---|---|---|
| `limit` | integer, 1 to 10 | `10` |
| `offset` | integer, ≥ 0 | `0` |
| `status` *(messages only)* | one or more statuses, comma-separated or repeated: `?status=pending,failed` or `?status=pending&status=failed` | all statuses |
| `order` *(messages only)* | `asc` or `desc` (case-insensitive), by `sequence_number` | `desc` |

Printers and users are listed newest first.

### Message content
- Leading and trailing whitespace is trimmed; the result must be 1 to 500 characters.
- Control and invisible formatting characters are stripped, to prevent ESC/POS command injection. Newlines are kept: `\r\n` and `\r` become `\n`, and lines after the 10th newline are joined with spaces.
- Error strings sent by printers (`heartbeat`, `failed`) are stripped of control characters other than newline and tab.

### Public routes

| Method | Route | Body | Response |
|---|---|---|---|
| `GET` | `/health` | | `200 OK` if the database answers, otherwise `500` |
| `POST` | `/api/v1/users/claim` | `{"invite", "device_name"}` | `200 {"token"}` |

### Admin routes
All prefixed with `/api/v1/admin`.

**Messages**

| Method | Route | Body | Response |
|---|---|---|---|
| `GET` | `/messages` | | `200 [message]` with [list parameters](#list-query-parameters) |
| `POST` | `/messages` | `{"printer_id", "content"}` | `201 message`, status `pending` |
| `GET` | `/messages/{id}` | | `200 message` |
| `PATCH` | `/messages/{id}/approve` | | `200 message` (`awaiting_approval` → `pending`) |
| `PATCH` | `/messages/{id}/reject` | | `200 message` (`awaiting_approval` → `rejected`) |
| `PATCH` | `/messages/{id}/requeue` | | `200 message` (`failed` → `pending`) |

Admin message responses include `sender_ip`, `source`, `attempts` and `last_error`, which user and printer responses leave out.

**Printers**

| Method | Route | Body | Response |
|---|---|---|---|
| `GET` | `/printers` | | `200 [printer]` |
| `POST` | `/printers` | `{"model", "recipient_name", "quiet_start"?, "quiet_end"?, "timezone"?}` | `201 printer` with `token` |
| `GET` | `/printers/{id}` | | `200 printer` |
| `PATCH` | `/printers/{id}` | `{"model"?, "recipient_name"?}` | `200 printer` |
| `GET` | `/printers/{id}/stats` | | `200 {messages_claimed, messages_printed, messages_failed, print_attempts, last_claimed_at, last_printed_at}` |
| `PUT` | `/printers/{id}/quiet_hours` | `{"quiet_start", "quiet_end", "timezone"?}` | `200 printer` |
| `DELETE` | `/printers/{id}/quiet_hours` | | `200 printer` (also clears `timezone`) |
| `POST` | `/printers/{id}/rotate_token` | | `200 {"token"}` |

Quiet hours are `"HH:MM"` strings and may cross midnight (`"22:00"` to `"07:00"`). They need both ends, and the ends must differ. `timezone` is an IANA name and defaults to `Europe/Brussels`. A printer is `online` when its last heartbeat is less than 20 seconds old.

**Users**

| Method | Route | Body | Response |
|---|---|---|---|
| `POST` | `/users` | `{"name", "printer_ids"?}` | `201 user` with `invite` |
| `GET` | `/users` | | `200 [user]` |
| `GET` | `/users/{id}` | | `200 user` |
| `PUT` | `/users/{id}/printers` | `{"printer_ids"}` (required; `[]` removes all access) | `200 user` |
| `POST` | `/users/{id}/invite` | | `200 {"invite"}` |
| `GET` | `/users/{id}/sessions` | | `200 [session]`, newest first |
| `DELETE` | `/users/{id}/sessions` | | `204`, revokes every active session |
| `DELETE` | `/users/{id}/sessions/{sessionID}` | | `204` (repeating it is harmless) |

An unknown user in the URL gets `404`. An unknown printer in `printer_ids` gets `400`, and nothing is changed.

### Printer routes
All prefixed with `/api/v1/printer`, with the printer's token.

| Method | Route | Body | Response |
|---|---|---|---|
| `POST` | `/heartbeat` | optional `{"error"}`; omitting it clears the printer's error | `204` |
| `POST` | `/messages/oldest/claim` | | `200 message`, or `204` if the queue is empty or during quiet hours |
| `PATCH` | `/messages/{id}/processed` | | `200 message` (`claimed` → `processed`) |
| `PATCH` | `/messages/{id}/printed` | | `200 message` (`processed` → `printed`) |
| `PATCH` | `/messages/{id}/failed` | `{"error"}` | `200 message` (back to `pending`, or `failed` after 3 attempts) |

### User routes
All prefixed with `/api/v1/user`, with the user's session token.

| Method | Route | Body | Response |
|---|---|---|---|
| `GET` | `/me` | | `200 {id, name, printer_ids}` |
| `GET` | `/printers` | | `200 [printer]`, the printers this user may send to |
| `GET` | `/messages` | | `200 [message]`, only the caller's, with [list parameters](#list-query-parameters) |
| `POST` | `/messages` | `{"printer_id", "content"}` | `201 message`, status `awaiting_approval` |
| `GET` | `/messages/{id}` | | `200 message`, `404` if it isn't the caller's |

## Development
### Dev database
The devcontainer's docker-compose.yml starts Postgres as the `db` service and runs migrations on boot. Don't run `docker compose` from inside the devcontainer.

`DATABASE_URL` is already set in the container:
> postgres://myuser:mypassword@db:5432/mydatabase?sslmode=disable

#### Apply / roll back migrations
```shell
migrate -path migrations -database "$DATABASE_URL" up
migrate -path migrations -database "$DATABASE_URL" down 1
```
#### Inspect the database
```shell
psql "$DATABASE_URL"
```
#### Load the seed data
Wipes all data and inserts three printers, four users and sample messages in every status:
```shell
psql "$DATABASE_URL" -f migrations/seed.sql
```

The seeded printers and users have fixed, readable dev tokens. They must never be used outside development.

| Printer | ID | Token | Notes |
|---|---|---|---|
| Alice | `11111111-1111-1111-1111-111111111111` | `bbr_printer_dev_alice` | no quiet hours: use it to test the printer flow |
| Bob | `22222222-2222-2222-2222-222222222222` | `bbr_printer_dev_bob` | quiet hours 22:00–07:00 |
| Carol | `33333333-3333-3333-3333-333333333333` | `bbr_printer_dev_carol` | quiet hours 12:00–13:00, offline, reports `paper jam` |

| User | ID | Token | May send to |
|---|---|---|---|
| Dave | `aaaaaaaa-0000-0000-0000-000000000001` | session `bbr_user_dev_dave` | Alice |
| Eve | `aaaaaaaa-0000-0000-0000-000000000002` | session `bbr_user_dev_eve` | Alice, Bob |
| Frank | `aaaaaaaa-0000-0000-0000-000000000003` | session `bbr_user_dev_frank` (revoked) | Carol |
| Grace | `aaaaaaaa-0000-0000-0000-000000000004` | invite `bbr_invite_dev_grace` (unclaimed) | Alice |

### Run the API
`ADMIN_TOKEN` is required; it protects the `/api/v1/admin` routes:
```shell
ADMIN_TOKEN=mytoken go run .
```

Submit a message as Dave:
```shell
curl -s -X POST localhost:8080/api/v1/user/messages -H 'Authorization: Bearer bbr_user_dev_dave' \
  -H 'Content-Type: application/json' -d '{"printer_id":"11111111-1111-1111-1111-111111111111","content":"Hello!"}' | jq
```

Moderate it as an admin:
```shell
curl -s 'localhost:8080/api/v1/admin/messages?status=awaiting_approval' -H 'Authorization: Bearer mytoken' | jq
curl -s -X PATCH localhost:8080/api/v1/admin/messages/<id>/approve -H 'Authorization: Bearer mytoken' | jq
```

Create a printer (the response contains its token, shown only once):
```shell
curl -s -X POST localhost:8080/api/v1/admin/printers -H 'Authorization: Bearer mytoken' \
  -H 'Content-Type: application/json' -d '{"model":"Epson TM-T20III","recipient_name":"Zoe"}' | jq
```

Create a user who may send to Alice, then claim the invite from their device:
```shell
curl -s -X POST localhost:8080/api/v1/admin/users -H 'Authorization: Bearer mytoken' \
  -H 'Content-Type: application/json' -d '{"name":"Heidi","printer_ids":["11111111-1111-1111-1111-111111111111"]}' | jq
curl -s -X POST localhost:8080/api/v1/users/claim \
  -H 'Content-Type: application/json' -d '{"invite":"<invite>","device_name":"Heidi phone"}' | jq
```

Act as a printer, here Alice:
```shell
curl -i -X POST localhost:8080/api/v1/printer/heartbeat -H 'Authorization: Bearer bbr_printer_dev_alice'
curl -s -X POST localhost:8080/api/v1/printer/messages/oldest/claim -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
curl -s -X PATCH localhost:8080/api/v1/printer/messages/<id>/processed -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
curl -s -X PATCH localhost:8080/api/v1/printer/messages/<id>/printed -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
```
