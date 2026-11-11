# Burn Before Reading
**Burn Before Reading** is a project to physically print messages left by people on a website. The short messages are submitted by vetted users via a simple website and handled by this Golang API. A Raspberry Pi does the interface with the printer. What makes this project stand out is that messages will be printed almost instantly by a *receipt thermal printer* at home.

> Thermal printers do not require any ink to print as they directly burn tiny dots on the paper

## High-level overview
Here are the main steps involved in submitting, processing, and, finally, printing a message:

1. The user authenticates on the website;
2. ... and submits a message.
3. The message is delivered to the server, via this API, which stores the message in a database and marks with the status `Pending` or `AwaitingApproval` depending on its source.
4. If the message has to be approved, a moderator performs some sanity checks on it.
5. The Raspberry Pi polls the server at regular intervals to check for `Pending` messages.
6. The Raspberry Pi claims the message and renders it according to the printer's specificities
7. The printer prints the message

## Message lifecycle

> These diagrams describe where the project is heading, not what is implemented today. Admin endpoint paths are provisional; the printer endpoints match the current implementation (see [Printer authentication](#printer-authentication)).

### Request flow

```mermaid
sequenceDiagram
autonumber
actor U as Web user
actor A as Admin
participant API as bbr API<br/>(handler → service → store)
participant DB as Postgres
participant P as Printer agent
Note over U,P: 1. Submission
U->>API: POST /api/v1/messages {sender, body}
API->>API: validate sender (1..20) / content (1..500)<br/>source=web, capture sender_ip
API->>DB: INSERT status=awaiting_approval
API-->>U: 201 {id, status}
A->>API: POST /api/v1/admin/messages {sender, body}
API->>DB: INSERT source=admin, status=pending
API-->>A: 201
Note over U,P: 2. Moderation
A->>API: GET /api/v1/admin/messages?status=awaiting_approval
API-->>A: 200 [messages]
alt approve
A->>API: PATCH /api/v1/admin/messages/{id}/approve
API->>DB: UPDATE status=pending, moderated_at=now()<br/>WHERE status=awaiting_approval
else reject
A->>API: PATCH /api/v1/admin/messages/{id}/reject
API->>DB: UPDATE status=rejected<br/>WHERE status=awaiting_approval
end
API-->>A: 200 (409 if status already changed)
Note over U,P: 3. Printing
Note over API,P: Every printer request carries "Authorization: Bearer <printer token>".<br/>The API looks up sha256(token) in printers.token_hash: unknown → 401,<br/>otherwise the printer ID comes from the token (never from the URL).
loop every few seconds
P->>API: POST /api/v1/printer/heartbeat {error?}
API->>DB: UPDATE printers SET last_heartbeat_at=now(), error
end
P->>API: POST /api/v1/printer/messages/oldest/claim
API->>DB: SELECT oldest pending of this printer FOR UPDATE SKIP LOCKED<br/>UPDATE status=claimed, claimed_at, attempts+1<br/>(also counts as a heartbeat)
alt queue empty or quiet hours
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
API->>DB: UPDATE last_error,<br/>status = attempts < max ? pending : failed<br/>WHERE printer_id=caller
end
end
Note over U,P: 4. Status check
U->>API: GET /api/v1/messages/{id}
API-->>U: 200 {status}
```

### Message states
```mermaid
stateDiagram-v2
direction LR
[] --> awaiting_approval : submitted via web
[] --> pending : submitted by admin / direct
awaiting_approval --> pending : admin approves<br/>(moderated_at)
awaiting_approval --> rejected : admin rejects<br/>(moderated_at)
pending --> claimed : printer claims oldest job<br/>(claimed_at, attempts+1)
claimed --> processed : printer starts<br/>(processed_at)
claimed --> pending : claim timed out<br/>(printer went silent)
processed --> printed : success (printed_at)
processed --> pending : error, attempts < max<br/>(last_error)
processed --> failed : error, attempts >= max
failed --> pending : admin requeues
printed --> []
rejected --> []
failed --> []
```

## Printer authentication

Each printer has its own API token. The Raspberry Pi sends it on every request to the `/api/v1/printer` routes:

```
Authorization: Bearer bbr_printer_…
```

### Issuing and rotating tokens
- **Issued once:** `POST /api/v1/admin/printers` returns the new printer with a `token` field. This is the only time the plain token is shown. Copy it into the Pi's configuration.
- **Stored hashed:** only `sha256(token)` is kept, in `printers.token_hash`. Someone who gets a copy of the database can't use it to call the API as a printer.
- **Rotation:** `POST /api/v1/admin/printers/{id}/rotate_token` returns a new token. The old one stops working immediately. Use it when a token is lost (for example after reflashing the Pi's SD card) or may have leaked. The printer keeps its ID and message history.
- Both responses are sent with `Cache-Control: no-store`.

### What a token allows
- The printer's identity comes **from the token**, so printer routes don't take a printer ID.
- A printer can only claim and update **its own** messages. Another printer's message gets `404`, the same as an unknown ID, so its existence isn't revealed.
- A missing or invalid token gets `401`. A `500` means the server couldn't check the token (for example, the database is down): retry later rather than treating the token as revoked.

| Method | Route | Purpose |
|---|---|---|
| `POST` | `/api/v1/printer/heartbeat` | Report liveness, optionally with `{"error": "…"}` |
| `POST` | `/api/v1/printer/messages/oldest/claim` | Claim the oldest pending message (`204` if none, or during quiet hours) |
| `PATCH` | `/api/v1/printer/messages/{id}/processed` | Message has been processed and is ready for printing |
| `PATCH` | `/api/v1/printer/messages/{id}/printed` | Printing succeeded |
| `PATCH` | `/api/v1/printer/messages/{id}/failed` | Printing failed, with `{"error": "…"}` |


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
Wipes all data and inserts three printers and sample messages:
```shell
psql "$DATABASE_URL" -f migrations/seed.sql
```

The seeded printers have fixed, readable dev tokens. They must never be used outside development:

| Printer | ID | Token |
|---|---|---|
| Alice | `11111111-1111-1111-1111-111111111111` | `bbr_printer_dev_alice` |
| Bob | `22222222-2222-2222-2222-222222222222` | `bbr_printer_dev_bob` |
| Carol | `33333333-3333-3333-3333-333333333333` | `bbr_printer_dev_carol` |

### Run the API
`ADMIN_TOKEN` is required; it protects the `/api/v1/admin` routes:
```shell
ADMIN_TOKEN=mytoken go run .
```

Then try submitting a message:
```shell
curl -s -X POST localhost:8080/api/v1/messages -H 'Content-Type: application/json' -d '{"sender":"Bob","body":"Hello!", "printer_id":"11111111-1111-1111-1111-111111111111"}' | jq
```

Admin routes need the admin token:
```shell
curl -s 'localhost:8080/api/v1/admin/messages?status=awaiting_approval' -H 'Authorization: Bearer mytoken' | jq
```

Create a printer (the response contains its token, shown only once):
```shell
curl -s -X POST localhost:8080/api/v1/admin/printers -H 'Authorization: Bearer mytoken' \
  -H 'Content-Type: application/json' -d '{"model":"Epson TM-T20III","recipient_name":"Zoe"}' | jq
```

Act as a printer, here Alice:
```shell
curl -i -X POST localhost:8080/api/v1/printer/heartbeat -H 'Authorization: Bearer bbr_printer_dev_alice'
curl -s -X POST localhost:8080/api/v1/printer/messages/oldest/claim -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
curl -s -X PATCH localhost:8080/api/v1/printer/messages/<id>/processed -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
curl -s -X PATCH localhost:8080/api/v1/printer/messages/<id>/printed -H 'Authorization: Bearer bbr_printer_dev_alice' | jq
```
