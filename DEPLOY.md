# Deploying Burn Before Reading

This guide puts the website and the API online on one machine with Docker Compose, behind a Cloudflare Tunnel. The stack is defined in [docker-compose.prod.yml](docker-compose.prod.yml):

| Service | What it does |
|---|---|
| `db` | Postgres 16, data kept in the `pgdata` volume |
| `migrate` | Applies the pending migrations, then exits |
| `llm` | llama.cpp running the moderation classifier |
| `api` | The Go API, also serving the website from `web/` |
| `cloudflared` | The Cloudflare Tunnel that connects the site to your domain |

No port is published. The site can be reached through the tunnel only. That's what makes `TRUST_CLOUDFLARE=true` safe: nobody can connect directly and fake the `CF-Connecting-IP` header.

## Requirements

- A machine with Docker and the Compose plugin, and about 4 GB of RAM. A small VPS works, and so does a PC at home, since the tunnel needs no port forwarding.
- A domain on Cloudflare.
- The moderation model, which isn't in git: a Q8_0 GGUF of Qwen3Guard-Gen 0.6B (about 800 MB).

## 1. Get the code and the model

```sh
git clone <repository url> burn-before-reading
cd burn-before-reading
```

Copy the model to `server/models/Qwen3Guard-Gen-0.6B.Q8_0.gguf`. To copy it from your computer to a server:

```sh
scp server/models/Qwen3Guard-Gen-0.6B.Q8_0.gguf user@server:burn-before-reading/server/models/
```

## 2. Create the Cloudflare Tunnel

1. In the Cloudflare dashboard, go to **Zero Trust → Networks → Tunnels → Create a tunnel** and pick **Cloudflared**.
2. Copy the token from the install command that the dashboard shows. You don't need to run that command, because the `cloudflared` service runs the tunnel.
3. Add a public hostname: your domain → type **HTTP** → URL **`api:8080`**.

## 3. Fill in `.env`

```sh
cp .env.example .env
```

| Variable | Value |
|---|---|
| `POSTGRES_USER`, `POSTGRES_DB` | Keep the defaults, or choose your own |
| `POSTGRES_PASSWORD` | Letters and digits only, because it goes into a connection URL: `openssl rand -hex 32` |
| `TUNNEL_TOKEN` | The token from step 2 |
| `LLM_THREADS` | CPU threads for the classifier. Leave at least one core for Postgres and the API |

`.env` is ignored by git. Never commit it.

## 4. Start the stack

Stop any `cloudflared` container you started by hand for this tunnel, for example with the dashboard's install command. Cloudflare spreads requests across every connector of a tunnel. A connector outside the compose network can't reach `api`, so the requests sent to it fail with a 502.

```sh
docker ps                       # look for a cloudflare/cloudflared container
docker rm -f <container name>
```

Then, from the repository root:

```sh
docker compose -f docker-compose.prod.yml up -d --build
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs -f api cloudflared
```

- The API waits until the classifier is healthy, which takes about 30 seconds while the model loads.
- `migrate` shows `Exited (0)` once the migrations are applied. That's expected.
- In the `cloudflared` logs, look for `Registered tunnel connection`.

**Never load `server/migrations/seed.sql` here.** Its tokens are published in the server README, including an admin session.

## 5. Check that it works

- `https://<your domain>/health` returns `OK`.
- `https://<your domain>/` shows the website. You can't sign in until the first admin exists (step 7).

To reach the API from the machine itself while debugging, uncomment the `ports` section of `api` in the compose file. It binds to `127.0.0.1`, so it stays off the internet. Then:

```sh
curl localhost:8080/health
```

## 6. Configure Cloudflare

- **SSL/TLS → Edge Certificates:** turn on **Always Use HTTPS**.
- **Security → WAF → Rate limiting rules:** add a rule for the requests whose path is `/api/v1/users/join` or `/api/v1/users/claim`. The API doesn't rate-limit these unauthenticated routes itself (see the server README). The free plan includes one rule, which can cover both paths.

## 7. Create the first admin

There's no API route to create the first admin yet, and no bootstrap script. The first account has to be created in the database: a user with `is_admin = true`, linked to at least one printer, with an invite to claim from the website. See [Admins](server/README.md#admins) in the server README.

To open a Postgres shell:

```sh
docker compose -f docker-compose.prod.yml exec db sh -c 'psql -U "$POSTGRES_USER" "$POSTGRES_DB"'
```

## 8. Connect the Raspberry Pi

1. As the admin, create the printer with `POST /api/v1/admin/printers` (see the server README). The response contains the printer's token, **shown only once**.
2. Put the token in the Pi's configuration, and point the Pi at `https://<your domain>/api/v1`.

## Backups

Dump the database, for example once a day from cron, and keep the copies on another machine:

```sh
docker compose -f docker-compose.prod.yml exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' > bbr-$(date +%F).sql
```

The dump contains the schema too, so restore it into a new, empty database, before `migrate` runs. Start only `db`, restore, then start the rest:

```sh
docker compose -f docker-compose.prod.yml up -d db
docker compose -f docker-compose.prod.yml exec -T db sh -c 'psql -U "$POSTGRES_USER" "$POSTGRES_DB"' < bbr-YYYY-MM-DD.sql
docker compose -f docker-compose.prod.yml up -d --build
```

`migrate` then finds the migrations already recorded in the dump and has nothing to do.

## Updating

```sh
git pull
docker compose -f docker-compose.prod.yml up -d --build
```

- **Website:** `web/` is mounted into the API container, so changes to it show up without a rebuild.
- **API:** changes to `server/` need `--build`.
- **Migrations:** the `migrate` service applies new migrations on every `up`.

## Moving to another machine

You can test the whole stack on your own computer first. Before you start it on the server, stop the local copy. Otherwise both machines serve the same tunnel, and requests get split between them.

```sh
docker compose -f docker-compose.prod.yml down      # keeps the database volume
docker compose -f docker-compose.prod.yml down -v   # also deletes the database
```

To move the data, take a [backup](#backups) on the old machine and restore it on the new one before the first full `up`, as described there.

## Before going further

- `cloudflared` runs the `latest` image. Once the site works, pin a [release](https://github.com/cloudflare/cloudflared/releases) in the compose file.
- [server/TODO](server/TODO) lists what's still open, such as rate limiting per user and a length limit on printer error strings.
