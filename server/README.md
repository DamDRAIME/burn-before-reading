# Burn Before Reading
**Burn Before Reading** is a project to physically print messages left by people on a website. The short messages are submitted by vetted users via a simple website and handled by this Golang API. A Raspberry Pi does the interface with the printer. What makes this project stand out is that messages will be printed almost instantly by a *receipt thermal printer* at home.

> Thermal printers do not require any ink to print as they directly burn tiny dots on the paper

## High-level overview
Here are the main steps involved in submitting, processing, and, finally, printing a message:

1. The owner of a printer (or the admin) hands out a one-time **join token** for that printer. A new user signs up with it from their device and receives a **session token**; an existing user redeems it from their session to add the printer to their account. _(The admin can also create an account directly and hand out a one-time **invite**, which the user claims for a session token.)_
2. The user can add more devices to their own account with a short-lived device invite.
3. The user submits a message to one of their allowed recipients. The API stores it with the status `awaiting_approval`. _(Messages sent by an admin skip moderation and start as `pending`)_
4. A moderator approves or rejects the message.
5. The Raspberry Pi polls the API at regular intervals and claims the oldest `pending` message of its printer.
6. The Raspberry Pi renders the message according to the printer's specificities and reports back.
7. The printer prints the message.

## Roles and authentication

Every protected route expects an `Authorization: Bearer <token>` header. The token type decides which route group can be called:

| Role | Route prefix | Token | Where it comes from |
|---|---|---|---|
| Admin | `/api/v1/admin` | `bbr_session_…` of a user with `is_admin = true` | a regular user session; the flag is set in the database (see [Admins](#admins)) |
| Printer | `/api/v1/printer` | `bbr_printer_…` | `POST /admin/printers` or `POST /admin/printers/{id}/rotate_token` |
| User | `/api/v1/user` | `bbr_session_…` | `POST /users/join` with a `bbr_join_…` join token, or `POST /users/claim` with a `bbr_invite_…` invite |
| Printer owner | `/api/v1/user/printers/{id}/…` | `bbr_session_…` of the user who owns printer `{id}` | an admin makes the user the owner of that printer |
| Public | `/api/v1/users/join`, `/api/v1/users/claim`, `/health` | none | |

- Printer, session, invite and join tokens are random (about 130 bits) and only ever stored as `sha256(token)`. Someone who gets a copy of the database can't use it to call the API.
- Every response that contains a plain token, invite or join token is sent with `Cache-Control: no-store`, and it is the only time that value is shown.
- A missing or invalid token gets `401`. A valid session of a non-admin user on `/api/v1/admin` gets `403`. A `500` from the auth middleware means the server couldn't check the token (for example, the database is down): retry later rather than treating the token as revoked.

### Tokens

| Token | Prefix | Issued by | Grants | Lifetime | Used at |
|---|---|---|---|---|---|
| Printer | `bbr_printer_` | admin (create printer, `rotate_token`) | act as that printer | until rotated | `Authorization` on `/api/v1/printer/*` |
| Session | `bbr_session_` | the server, when an invite is claimed or a join token is used to sign up | act as that user; also the whole admin API if the user is an admin | until revoked | `Authorization` on `/api/v1/user/*` (and `/api/v1/admin/*` for admins) |
| Invite | `bbr_invite_` | admin (new account, or recovery) or the user themselves (device invite) | **one** new session on **that** account | admin: 2 days, device invite: 15 minutes; single use | `POST /users/claim` |
| Join | `bbr_join_` | the printer's owner, or the admin | **membership of one printer** for the account that uses it (an existing one, or a new one created at that moment). Never a login to an existing account. | 2 days, single use, revocable | `POST /users/join` (no session) or `POST /user/memberships` (with a session) |

An account has at most one pending invite: issuing a new one replaces it.

### Admins
An admin is a regular user with `users.is_admin = true`. There is no shared admin secret: each admin signs in with their own session, so admin access is revoked per device or per person like any other session (`DELETE /user/sessions/{id}`, or `DELETE /admin/users/{id}/sessions`). The flag is read on every request, so promoting or demoting takes effect on the next request.

There is no API route to grant the flag yet. Set it directly in the database:
```sql
UPDATE users SET is_admin = true WHERE id = '<user id>';
```

An admin still follows the [user lifecycle](#user-lifecycle). In particular, **an admin left with no printer loses all their sessions**, and a recovery invite can't be issued to a user with no printer. Keep every admin linked to at least one printer.

### Printer tokens
- **Issued once:** `POST /api/v1/admin/printers` returns the new printer with a `token` field. Copy it into the Pi's configuration.
- **Rotation:** `POST /api/v1/admin/printers/{id}/rotate_token` returns a new token. The old one stops working immediately. Use it when a token is lost (for example after reflashing the Pi's SD card) or may have leaked. The printer keeps its ID and message history.
- The printer's identity comes **from the token**, so printer routes don't take a printer ID.
- A printer can only claim and update **its own** messages. Another printer's message gets `404`, the same as an unknown ID, so its existence isn't revealed.

### User lifecycle

**Principle: every person creates their own credential.** Owners never create accounts or hold a login for someone else. They hand out join tokens, which grant membership of their printer to whoever uses them, under that person's own name. Only two things can create a session on an existing account: the user themselves (device invite) and the admin (recovery invite).

Each link between a user and a printer has a **role**, `member` or `owner`. Both may send to the printer. **Names are unique per printer** (ignoring case), not globally, so nothing printed is ambiguous, and nobody can probe which names exist elsewhere. A clash gets `409`.

| Action | Admin | Owner (own printer) | User (self) |
|---|---|---|---|
| Bring in a new person | `POST /admin/users` (invite), or a join token | a join token | — |
| Add an existing user to a printer | `PUT /admin/users/{id}/printers`, or a join token | a join token | redeem it: `POST /user/memberships` |
| Manage join tokens | `/admin/printers/{id}/join_tokens` | `/user/printers/{id}/join_tokens` | — |
| New device | recovery: `POST /admin/users/{id}/invite` | — | `POST /user/invite` |
| Sessions | `/admin/users/{id}/sessions` | — | `/user/sessions` |
| Remove from a printer | `PUT /admin/users/{id}/printers` | `DELETE /user/printers/{id}/members/{userID}` | leave: `DELETE /user/memberships/{printerID}` |
| Ownership | `PUT` / `DELETE /admin/printers/{id}/owner` | — | — |

1. **Getting an account.**
   - *With a join token (normal path):* the person's device sends `POST /users/join {join_token, username, device_name}`. In one transaction, the API checks the name is free on that printer, creates the account as a member of the printer, uses up the token and returns a session token (`201`). An unusable token gets `401`. A taken name gets `409`, and the token remains usable.
   - *Created by the admin:* `POST /admin/users {username, printer_ids}` returns a `bbr_invite_…` invite, valid 2 days, which the person claims with `POST /users/claim {invite, device_name}`. An unknown, expired or already used invite gets `401`.
2. **More devices.** `POST /user/invite` returns an invite for the caller's own account, valid **15 minutes** (meant to be shown as a QR code). Another device claims it with `POST /users/claim`. Existing sessions are kept. The account comes only from the session, never from the request.
3. **Sessions.** A user has one session per device. `GET /user/sessions` lists them, with `current: true` on the one making the request. `DELETE /user/sessions/{id}` revokes one (`404` for someone else's session); revoking the current one logs out. The admin can list and revoke them too. Each session's `last_used_at` is updated when it authenticates a request, at most once every 5 minutes.
4. **Joining another printer.** A signed-in user sends `POST /user/memberships {join_token}`. The account becomes a member of the token's printer, and the response is the updated `/me`. A token that can't be used gets `400` (not `401`: the session is fine). Being already on that printer, or a name clash there, gets `409`. In both cases the token remains usable. Whoever uses a token joins **as themselves**; the owner sees who it was (`used_by`) in the token list and can remove them.
5. **Sending.** A user may only send to the printers they are linked to. Any other printer gets `404 printer not found`, so its existence isn't revealed. `GET /user/printers` lists the allowed ones.
6. **Leaving or being removed.** The owner removes a member, the admin drops the printer with `PUT /admin/users/{id}/printers`, or the user leaves with `DELETE /user/memberships/{printerID}`. An owner can't leave their own printer (`409`); the admin removes or transfers ownership. On every path:
   - the user's `pending` and `awaiting_approval` messages to that printer are rejected, so nothing they sent is printed afterwards;
   - if the user has no printer left, all their sessions are revoked and their pending invite stops working. The account and its message history are kept. If they join again with a token and no session, a new account is created.
7. **Ownership (admin only).** There is one owner per printer and one owned printer per user, enforced by two partial unique indexes; an owner can still be a member of other printers.
   - `PUT /admin/printers/{id}/owner {user_id}` does it in one transaction: it demotes the current owner to member, then promotes or adds the new owner. The new owner already owning another printer gets `409`.
   - `DELETE /admin/printers/{id}/owner` demotes the owner to member, or with `?keep_as_member=false` removes their link (with the effects of step 6).
   - A join token created by an owner only works while that user still owns the printer. Losing ownership revokes their pending tokens.
   - Owner routes are checked per printer: on a printer you don't own you get `403`. Losing ownership takes effect on the next request.
8. **Recovery.** If a user has lost every device, `POST /admin/users/{id}/invite` sets a new 2-day invite and **revokes all their sessions** (a lost phone may be in someone else's hands). Send `{"revoke_sessions": false}` to keep them. A user with no printer gets `409`.

Not covered yet: deleting or renaming accounts.

## Database

```mermaid
erDiagram
    users ||--o{ user_sessions : "signs in with"
    users ||--o{ user_printers : "may send to"
    printers ||--o{ user_printers : "accepts from"
    printers ||--o{ messages : "prints"
    users |o--o{ messages : "sends (NULL for admin)"
    printers ||--o{ printer_join_tokens : "can be joined with"
    users |o--o{ printer_join_tokens : "created / used by"

    users {
        UUID id PK
        VARCHAR name "1..20 chars, shown as sender_name"
        BOOLEAN is_admin "default false, grants /api/v1/admin"
        BYTEA invite_hash UK "sha256 of the pending invite, NULL once claimed"
        TIMESTAMPTZ invite_expires_at "set together with invite_hash"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ last_updated_at "trigger"
    }
    user_sessions {
        UUID id PK
        UUID user_id FK "ON DELETE CASCADE"
        VARCHAR device_name "1..50 chars"
        BYTEA token_hash UK "sha256 of the bbr_session_ token"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ last_used_at "set on auth, at most every 5 min"
        TIMESTAMPTZ revoked_at "NULL while active"
    }
    user_printers {
        UUID user_id PK, FK "ON DELETE CASCADE"
        UUID printer_id PK, FK "ON DELETE CASCADE"
        VARCHAR role "member (default) or owner"
    }
    printer_join_tokens {
        UUID id PK
        UUID printer_id FK "ON DELETE CASCADE"
        UUID created_by FK "NULL = admin; ON DELETE CASCADE"
        UUID used_by FK "ON DELETE SET NULL"
        BYTEA token_hash UK "sha256 of the bbr_join_ token"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ expires_at
        TIMESTAMPTZ used_at "NULL until used"
        TIMESTAMPTZ revoked_at "NULL unless revoked"
    }
    printers {
        UUID id PK
        VARCHAR model "1..50 chars"
        VARCHAR recipient_name "1..20 chars"
        TIME quiet_start "NULL together with quiet_end"
        TIME quiet_end
        TEXT timezone "IANA name; Europe/Brussels by default with quiet hours, NULL without"
        TEXT error "last error reported by heartbeat"
        BYTEA token_hash UK "sha256 of the bbr_printer_ token"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ last_updated_at "trigger, ignores heartbeats"
        TIMESTAMPTZ last_heartbeat_at
    }
    messages {
        UUID id PK
        BIGINT sequence_number UK "identity, used for ordering lists"
        UUID sender_id FK "the user, or the admin who sent it"
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

Indexes worth knowing about:

- `idx_messages_pending (printer_id, created_at) WHERE status = 'pending'` serves the claim query.
- `idx_messages_sender (sender_id, sequence_number)` serves a user's message list.
- `uq_user_printers_one_owner_per_printer (printer_id) WHERE role = 'owner'` and `uq_user_printers_one_printer_per_owner (user_id) WHERE role = 'owner'` enforce the [ownership rules](#user-lifecycle).
- `idx_printer_join_tokens_printer (printer_id, created_at DESC)` serves the join token lists.

Membership changes lock the affected `printers` rows (`SELECT … FOR UPDATE`) so the per-printer name check can't race. Transactions always lock printers first, then the user, then links and tokens.

### What changed with join tokens
- **Owners no longer create accounts.** `POST /user/printers/{id}/members` is gone. Owners create join tokens instead, and people sign up with them (`POST /users/join`) or, if they already have an account, redeem them (`POST /user/memberships`). This removes copies of the same person on different printers, and owners no longer receive anyone's login.
- **New table:** `printer_join_tokens`.
- **Ownership moved to the printer:** `PUT /admin/users/{id}/printers/{printerID}/owner` was replaced by `PUT /admin/printers/{id}/owner {user_id}` (transfers in one call) and `DELETE /admin/printers/{id}/owner`.
- **Admin invite reissue is recovery:** it now revokes the user's sessions unless `{"revoke_sessions": false}`, and returns `expires_at`.
- **New user routes:** device invites, own session list and revocation, redeeming a join token, leaving a printer.
- **Names are unique per printer.**
- **`PUT /admin/users/{id}/printers` now rejects queued messages** to the printers it removes, like the other removal paths.

### What changed with printer ownership
- **New column:** `user_printers.role`, `member` by default. Existing links became members.
- **`PUT /admin/users/{id}/printers` takes roles.** The body changed from `{"printer_ids": [...]}` to `{"printer_accesses": [{"printer_id", "role"}]}`.
- **User responses changed:** `printer_ids` was replaced by `printer_accesses: [{printer_id, role}]` in admin user responses and in `GET /user/me`.
- **New routes:** `PUT /admin/users/{id}/printers/{printerID}/owner` (since replaced, see above), and the [owner routes](#owner-routes) under `/user/printers/{id}`.

### What changed with the users table
User accounts replaced the anonymous, free-text sender:

- **`messages.sender_name` was removed** and replaced by `sender_id`, a foreign key to `users`. The `sender_name` field still exists in API responses, but it is now read from `users.name`. Admin messages record the admin who sent them, so `sender_name` is never `null`.
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
alt join token (from the printer's owner or the admin)
U->>API: POST /api/v1/users/join {join_token, username, device_name}
API->>DB: find usable token, lock its printer<br/>name free on that printer? (409)
API->>DB: INSERT users, mark token used,<br/>INSERT user_printers (member), INSERT user_sessions
API-->>U: 201 {token}
else account created by the admin
A->>API: POST /api/v1/admin/users {username, printer_ids}
API->>DB: INSERT users (invite_hash, invite_expires_at)<br/>INSERT user_printers
API-->>A: 201 {user, invite}
U->>API: POST /api/v1/users/claim {invite, device_name}
API->>DB: clear invite_hash WHERE not expired<br/>INSERT user_sessions
API-->>U: 200 {token}
end
opt already has an account: join another printer
U->>API: POST /api/v1/user/memberships {join_token}
API->>DB: lock printer, check name and membership,<br/>mark token used, INSERT user_printers (member)
API-->>U: 200 {me}
end
Note over U,P: 1. Submission
U->>API: POST /api/v1/user/messages {printer_id, content}
API->>DB: check user_printers (unknown pair → 404)
API->>API: sanitize and validate content (1..500)<br/>source=web, capture sender_ip
API->>DB: INSERT sender_id, status=awaiting_approval
API-->>U: 201 {message}
A->>API: POST /api/v1/admin/messages {printer_id, content}
API->>DB: INSERT sender_id=admin, source=admin, status=pending
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
awaiting_approval --> rejected : admin rejects, or sender removed<br/>from the printer (moderated_at)
pending --> rejected : sender removed<br/>from the printer (moderated_at)
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
| `400` | Invalid body, path ID, query parameter or field value, including an unknown printer ID inside a request body, or a join token that can't be used when redeemed with a session |
| `401` | Missing or invalid token, an invalid invite, or a join token that can't be used to sign up |
| `403` | Valid user token, but the caller doesn't own the printer in the URL ([owner routes](#owner-routes)) |
| `404` | The resource in the URL doesn't exist or isn't visible to the caller (another printer's message, another user's message or session, a printer the user may not send to, a user who isn't a member of the owner's printer, a used or unknown join token) |
| `409` | The message's current status doesn't allow this transition, an ownership rule would be broken (printer already owned, user already owns a printer, owner leaving their printer), the name is already used on that printer, the user is already on that printer, or an invite is requested for a user with no printer |
| `500` | Unexpected server error |

### List query parameters

These apply to `GET /admin/messages`, `GET /admin/printers`, `GET /admin/users`, `GET /user/messages` and the join token lists. An invalid value gets `400`; it is never silently ignored or replaced.

| Parameter | Values | Default |
|---|---|---|
| `limit` | integer, 1 to 10 | `10` |
| `offset` | integer, ≥ 0 | `0` |
| `status` *(messages only)* | one or more statuses, comma-separated or repeated: `?status=pending,failed` or `?status=pending&status=failed` | all statuses |
| `order` *(messages only)* | `asc` or `desc` (case-insensitive), by `sequence_number` | `desc` |

Printers, users and join tokens are listed newest first.

### Message content
- Leading and trailing whitespace is trimmed; the result must be 1 to 500 characters.
- Control and invisible formatting characters are stripped, to prevent ESC/POS command injection. Newlines are kept: `\r\n` and `\r` become `\n`, and lines after the 10th newline are joined with spaces.
- Error strings sent by printers (`heartbeat`, `failed`) are stripped of control characters other than newline and tab.

### Public routes

| Method | Route | Body | Response |
|---|---|---|---|
| `GET` | `/health` | | `200 OK` if the database answers, otherwise `500` |
| `POST` | `/api/v1/users/claim` | `{"invite", "device_name"}` | `200 {"token"}` |
| `POST` | `/api/v1/users/join` | `{"join_token", "username", "device_name"}` | `201 {"token"}`: a new account, member of the token's printer |

### Admin routes
All prefixed with `/api/v1/admin`, with the session token of an [admin](#admins). Other users get `403`.

**Messages**

| Method | Route | Body | Response |
|---|---|---|---|
| `GET` | `/messages` | | `200 [message]` with [list parameters](#list-query-parameters) |
| `POST` | `/messages` | `{"printer_id", "content"}` | `201 message`, status `pending` |
| `GET` | `/messages/{id}` | | `200 message` |
| `PATCH` | `/messages/{id}/approve` | | `200 message` (`awaiting_approval` → `pending`) |
| `PATCH` | `/messages/{id}/reject` | | `200 message` (`awaiting_approval` → `rejected`) |
| `PATCH` | `/messages/{id}/requeue` | | `200 message` (`failed` → `pending`) |

Admin message responses include `sender_ip`, `attempts` and `last_error`, which user and printer responses leave out. Every response includes `source` (`web`, or `admin` for a message an admin sent through `POST /admin/messages`), so an admin sees in their own history which messages skipped moderation.

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
| `PUT` | `/printers/{id}/owner` | `{"user_id"}` | `200 user`: the new owner; the previous owner becomes a member |
| `DELETE` | `/printers/{id}/owner` | query `keep_as_member` (default `true`) | `204`; demotes the owner to member, or removes their link; `404` if there is no owner |
| `POST` | `/printers/{id}/join_tokens` | | `201 join token` with `join_token`, `created_by: null` |
| `GET` | `/printers/{id}/join_tokens` | | `200 [join token]` with [list parameters](#list-query-parameters) |
| `DELETE` | `/printers/{id}/join_tokens/{tokenID}` | | `204`; `404` if unknown or already used |

Quiet hours are `"HH:MM"` strings and may cross midnight (`"22:00"` to `"07:00"`). They need both ends, and the ends must differ. `timezone` is an IANA name. It defaults to Europe/Brussels when quiet hours are set and is null otherwise. A printer is `online` when its last heartbeat is less than 20 seconds old.

**Users**

| Method | Route | Body | Response |
|---|---|---|---|
| `POST` | `/users` | `{"username", "printer_ids"?}` | `201 user` with `invite` |
| `GET` | `/users` | | `200 [user]` |
| `GET` | `/users/{id}` | | `200 user` |
| `PUT` | `/users/{id}/printers` | `{"printer_accesses": [{"printer_id", "role"}]}` (required; replaces all links; `[]` removes all access) | `200 user` |
| `POST` | `/users/{id}/invite` | optional `{"revoke_sessions"}` (default `true`) | `200 {"invite", "expires_at"}`; account recovery: revokes all sessions unless `{"revoke_sessions": false}` |
| `GET` | `/users/{id}/sessions` | | `200 [session]`, newest first |
| `DELETE` | `/users/{id}/sessions` | | `204`, revokes every active session |
| `DELETE` | `/users/{id}/sessions/{sessionID}` | | `204` (repeating it is harmless) |

An unknown user or printer in the URL gets `404`. An unknown printer in `printer_ids` or `printer_accesses` gets `400`, and nothing is changed. In `printer_accesses`, `role` must be `member` or `owner`, and listing the same printer twice with different roles gets `400`. A name already used on one of the printers gets `409`. Users created with `POST /users` are always members. Use `PUT /printers/{id}/owner`, or `printer_accesses`, to make someone an owner. A user who is left with no printer loses their sessions and invite (see [User lifecycle](#user-lifecycle)).

A join token looks like `{id, printer_id, created_by, status, used_by: {id, name} | null, created_at, expires_at, used_at, revoked_at}`. `status` is `pending`, `used`, `revoked` or `expired`. The plain `join_token` is only in the create response.

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
| `GET` | `/me` | | `200 {id, name, is_admin, printer_accesses: [{printer_id, role}]}` |
| `GET` | `/printers` | | `200 [printer]`, the printers this user may send to |
| `GET` | `/messages` | | `200 [message]`, only the caller's, with [list parameters](#list-query-parameters) |
| `POST` | `/messages` | `{"printer_id", "content"}` | `201 message`, status `awaiting_approval` |
| `GET` | `/messages/{id}` | | `200 message`, `404` if it isn't the caller's |
| `POST` | `/invite` | | `200 {"invite", "expires_at"}`: a 15-minute invite for the caller's own account, to claim on another device |
| `GET` | `/sessions` | | `200 [session]` with `current`, newest first |
| `DELETE` | `/sessions/{sessionID}` | | `204`; `404` if it isn't the caller's; revoking the current session logs out |
| `POST` | `/memberships` | `{"join_token"}` | `200 me`: the caller is now a member of the token's printer |
| `DELETE` | `/memberships/{printerID}` | | `204`: leave a printer; `409` for the owner; `404` if not a member |

### Owner routes
All prefixed with `/api/v1/user/printers/{id}`, with the session token of the user who owns printer `{id}`. Anyone else gets `403`.

| Method | Route | Body | Response |
|---|---|---|---|
| `PUT` | `/quiet_hours` | `{"quiet_start", "quiet_end", "timezone"?}` | `200 printer` (same rules as the admin route) |
| `DELETE` | `/quiet_hours` | | `200 printer` (also clears `timezone`) |
| `GET` | `/members` | | `200 [{id, name, role}]`, newest first, without the owner |
| `DELETE` | `/members/{userID}` | | `204`; `404` if that user isn't a member of this printer |
| `POST` | `/join_tokens` | | `201 join token` with `join_token`; works only while the caller owns the printer |
| `GET` | `/join_tokens` | | `200 [join token]` with [list parameters](#list-query-parameters), including who used each one |
| `DELETE` | `/join_tokens/{tokenID}` | | `204`; `404` if unknown, from another printer, or already used |

Owners get the user-facing printer response, without `error` and `last_heartbeat_at`. Owners can't create accounts: people join with a join token, under the name they choose.

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
Wipes all data and inserts four printers, six users, three join tokens and sample messages in every status:
```shell
psql "$DATABASE_URL" -f migrations/seed.sql
```

The seeded printers and users have fixed, readable dev tokens. They must never be used outside development.

| Printer | ID | Token | Notes |
|---|---|---|---|
| Alice | `11111111-1111-1111-1111-111111111111` | `bbr_printer_dev_alice` | no quiet hours: use it to test the printer flow |
| Bob | `22222222-2222-2222-2222-222222222222` | `bbr_printer_dev_bob` | quiet hours 22:00–07:00 |
| Carol | `33333333-3333-3333-3333-333333333333` | `bbr_printer_dev_carol` | quiet hours 12:00–13:00, offline, reports `paper jam` |
| Marc | `44444444-4444-4444-4444-444444444444` | `bbr_printer_dev_marc` | quiet hours 12:00–13:00, offline |

| User | ID | Token | Owns | Member of |
|---|---|---|---|---|
| Dave | `aaaaaaaa-0000-0000-0000-000000000001` | session `bbr_session_dev_dave` (**admin**) | Alice | Marc |
| Eve | `aaaaaaaa-0000-0000-0000-000000000002` | session `bbr_session_dev_eve` | Bob | Alice, Marc |
| Frank | `aaaaaaaa-0000-0000-0000-000000000003` | session `bbr_session_dev_frank` (revoked) | Carol | Marc |
| Grace | `aaaaaaaa-0000-0000-0000-000000000004` | invite `bbr_invite_dev_grace` (unclaimed) | | Alice |
| Marc | `aaaaaaaa-0000-0000-0000-000000000005` | session `bbr_session_dev_marc` | Marc | |
| Heidi | `aaaaaaaa-0000-0000-0000-000000000006` | session `bbr_session_dev_heidi` | | Alice (only printer: removing her revokes her session); signed up with Dave's join token |

| Join token | Printer | Created by | State |
|---|---|---|---|
| `bbr_join_dev_marc` | Marc | Marc | pending: sign up with it, or redeem it from a session |
| `bbr_join_dev_heidi` | Alice | Dave | used by Heidi |
| `bbr_join_dev_eve` | Bob | admin | revoked |

### Run the API
```shell
go run .
```

Set `WEB_DIR` to serve the website from the same origin (see [web/README.md](../web/README.md)):
```shell
WEB_DIR=../web go run .
```
Environment variables:

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | Postgres connection string |
| `TRUST_CLOUDFLARE` | no | `true` or `false` (anything `strconv.ParseBool` accepts), defaults to `false`. `true` takes the client IP from the `CF-Connecting-IP` header; `false` uses the TCP peer address. Use `false` in development |
| `ADDR` | no | Listen address, defaults to `:8080` |
| `WEB_DIR` | no | Directory of the website to serve from the same origin |

The client IP is used in the request logs and stored as `sender_ip` on messages. Only set `TRUST_CLOUDFLARE=true` when the server can be reached through Cloudflare alone (Cloudflare Tunnel, or a firewall that only allows [Cloudflare's IP ranges](https://www.cloudflare.com/ips/)). The header is set by the client otherwise, so anyone connecting directly could choose their logged IP.

Per-IP rate limiting of the unauthenticated `POST /users/join` and `POST /users/claim` is done by a Cloudflare rate limiting rule, not by the API.

With the seed data, Dave is the admin: the admin examples below use his session.

Submit a message as Dave:
```shell
curl -s -X POST localhost:8080/api/v1/user/messages -H 'Authorization: Bearer bbr_session_dev_dave' \
  -H 'Content-Type: application/json' -d '{"printer_id":"11111111-1111-1111-1111-111111111111","content":"Hello!"}' | jq
```

Moderate it as an admin:
```shell
curl -s 'localhost:8080/api/v1/admin/messages?status=awaiting_approval' -H 'Authorization: Bearer bbr_session_dev_dave' | jq
curl -s -X PATCH localhost:8080/api/v1/admin/messages/<id>/approve -H 'Authorization: Bearer bbr_session_dev_dave' | jq
```

Create a printer (the response contains its token, shown only once):
```shell
curl -s -X POST localhost:8080/api/v1/admin/printers -H 'Authorization: Bearer bbr_session_dev_dave' \
  -H 'Content-Type: application/json' -d '{"model":"Epson TM-T20III","recipient_name":"Zoe"}' | jq
```

Create a user who may send to Alice, then claim the invite from their device:
```shell
curl -s -X POST localhost:8080/api/v1/admin/users -H 'Authorization: Bearer bbr_session_dev_dave' \
  -H 'Content-Type: application/json' -d '{"name":"Ivan","printer_ids":["11111111-1111-1111-1111-111111111111"]}' | jq
curl -s -X POST localhost:8080/api/v1/users/claim \
  -H 'Content-Type: application/json' -d '{"invite":"<invite>","device_name":"Ivan phone"}' | jq
```

Sign up with Marc's join token, then redeem another one from an existing account (Heidi):
```shell
curl -s -X POST localhost:8080/api/v1/users/join \
  -H 'Content-Type: application/json' -d '{"join_token":"bbr_join_dev_marc","username":"Judy","device_name":"Judy phone"}' | jq
curl -s -X POST localhost:8080/api/v1/user/printers/44444444-4444-4444-4444-444444444444/join_tokens \
  -H 'Authorization: Bearer bbr_session_dev_marc' | jq
curl -s -X POST localhost:8080/api/v1/user/memberships -H 'Authorization: Bearer bbr_session_dev_heidi' \
  -H 'Content-Type: application/json' -d '{"join_token":"<join_token>"}' | jq
```

Add a device to your own account (as Dave), then list your sessions:
```shell
curl -s -X POST localhost:8080/api/v1/user/invite -H 'Authorization: Bearer bbr_session_dev_dave' | jq
curl -s -X POST localhost:8080/api/v1/users/claim \
  -H 'Content-Type: application/json' -d '{"invite":"<invite>","device_name":"Dave laptop"}' | jq
curl -s localhost:8080/api/v1/user/sessions -H 'Authorization: Bearer bbr_session_dev_dave' | jq
```

Manage printer Alice as its owner, Dave:
```shell
curl -s -X PUT localhost:8080/api/v1/user/printers/11111111-1111-1111-1111-111111111111/quiet_hours \
  -H 'Authorization: Bearer bbr_session_dev_dave' -H 'Content-Type: application/json' \
  -d '{"quiet_start":"22:00","quiet_end":"07:00"}' | jq
curl -s localhost:8080/api/v1/user/printers/11111111-1111-1111-1111-111111111111/members \
  -H 'Authorization: Bearer bbr_session_dev_dave' | jq
curl -s localhost:8080/api/v1/user/printers/11111111-1111-1111-1111-111111111111/join_tokens \
  -H 'Authorization: Bearer bbr_session_dev_dave' | jq
curl -i -X DELETE localhost:8080/api/v1/user/printers/11111111-1111-1111-1111-111111111111/members/aaaaaaaa-0000-0000-0000-000000000006 \
  -H 'Authorization: Bearer bbr_session_dev_dave'
```

Transfer a printer, as an admin (here Grace becomes the owner of Marc's printer, and Marc a member):
```shell
curl -s -X PUT localhost:8080/api/v1/admin/printers/44444444-4444-4444-4444-444444444444/owner \
  -H 'Authorization: Bearer bbr_session_dev_dave' -H 'Content-Type: application/json' \
  -d '{"user_id":"aaaaaaaa-0000-0000-0000-000000000004"}' | jq
```

Act as a printer, here Alice:
```shell
curl -i -X POST localhost:8080/api/v1/printer/heartbeat -H 'Authorization: Bearer bbr_printer_dev_alice'
curl -s -X POST localhost:8080/api/v1/printer/messages/oldest/claim -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
curl -s -X PATCH localhost:8080/api/v1/printer/messages/<id>/processed -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
curl -s -X PATCH localhost:8080/api/v1/printer/messages/<id>/printed -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
```
