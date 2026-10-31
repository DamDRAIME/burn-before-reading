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

> These diagrams describe where the project is heading, not what is implemented today. Endpoint paths for the admin and printer APIs are provisional.

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
API->>DB: UPDATE status=pending, approved_at=now()<br/>WHERE status=awaiting_approval
else reject
A->>API: PATCH /api/v1/admin/messages/{id}/reject
API->>DB: UPDATE status=rejected<br/>WHERE status=awaiting_approval
end
API-->>A: 200 (409 if status already changed)
Note over U,P: 3. Printing
loop every few seconds
P->>API: POST /api/v1/printers/{id}/heartbeat
API->>DB: UPDATE printers SET last_seen_at=now()
end
P->>API: POST /api/v1/printers/{id}/claim
API->>DB: SELECT oldest pending FOR UPDATE SKIP LOCKED<br/>UPDATE status=claimed, claimed_at, printer_id, attempts+1
alt queue empty
API-->>P: 204 No Content
else job claimed
API-->>P: 200 {message}
P->>API: PATCH /api/v1/jobs/{id}/printing
API->>DB: UPDATE status=printing
alt print ok
P->>API: PATCH /api/v1/jobs/{id}/printed
API->>DB: UPDATE status=printed, printed_at<br/>printers.last_job_at=now()
else print error
P->>API: PATCH /api/v1/jobs/{id}/failed {error}
API->>DB: UPDATE last_error,<br/>status = attempts < max ? pending : failed
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
awaiting_approval --> pending : admin approves<br/>(approved_at)
awaiting_approval --> rejected : admin rejects
pending --> claimed : printer claims oldest job<br/>(claimed_at, printer_id, attempts+1)
claimed --> printing : printer starts
claimed --> pending : claim timed out<br/>(printer went silent)
printing --> printed : success (printed_at)
printing --> pending : error, attempts < max<br/>(last_error)
printing --> failed : error, attempts >= max
failed --> pending : admin requeues
printed --> []
rejected --> []
failed --> []
```

### Printer states

```mermaid
stateDiagram-v2
direction LR
[] --> active : registered
active --> sleeping : quiet hours / paused
sleeping --> active : resumed
active --> inactive : no heartbeat (last_seen_at stale)
inactive --> active : heartbeat received
```

Only `active` printers may claim jobs. A background reaper returns jobs stuck in `claimed` / `printing` on an `inactive` printer to `pending`.

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
### Run the API
```shell
go run .
```

Then try submitting a message:
```shell
curl -s -X POST localhost:8080/api/v1/messages -H 'Content-Type: application/json' -d '{"sender":"Bob","body":"Hello!"}' | jq
```