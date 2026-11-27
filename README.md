# Burn Before Reading

Burn Before Reading prints short messages left on a website, almost instantly, on a *receipt thermal printer* at home. Vetted users write the messages, an automated moderation checks them (with an admin approving anything it flags), and a Raspberry Pi next to the printer prints them.

> Thermal printers don't need ink: they burn tiny dots directly into the paper.

## Repository

| Folder | What | Docs |
|---|---|---|
| [server/](server/) | The Go API: accounts and printers, the message queue, moderation (rules and an LLM classifier), with Postgres | [server/README.md](server/README.md): roles, API reference, database, development |
| [web/](web/) | The website: write, history, admin. Plain HTML, CSS and JavaScript, served by the API | [web/README.md](web/README.md): pages, moderation, sign-in |

## Deployment

The whole stack runs with Docker Compose behind a Cloudflare Tunnel. See [DEPLOY.md](DEPLOY.md).
