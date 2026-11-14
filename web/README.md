# Burn Before Reading: web frontend

The website people use to write messages, follow them until they're printed, and moderate them. It's plain HTML, CSS and JavaScript modules: no build step, no dependencies. The only external request is Google Fonts.

## Pages

| Path | Who | What |
|---|---|---|
| `/` | user or admin | **Write.** The receipt is the composer: pick a recipient, write, print. After sending, the circle and the four-step bar follow the message (awaiting → approved → queued → printed), polling every 3 s. |
| `/history` | user | The user's own messages as small receipts, filterable by recipient. |
| `/admin` | admin | Counts, a 52-week activity grid, the approval queue, printer status and the full message log (with requeue for failed messages). |
| `/join#bbr_invite_…` | anyone | Claims an invite for this device. The same form shows on `/` when nobody is signed in. |

## Sign-in

- **Users** open the invite link an admin sent them (`/join#bbr_invite_…`), name the device and get a session token. The invite is in the URL fragment, so it's never sent to the server in a request line or logged, and the page removes it from the address bar.
- **Admins** sign in on `/admin` with the server's `ADMIN_TOKEN`. A "Sign out" link there forgets it.
- Both tokens are kept in `localStorage` and sent as `Authorization: Bearer`. A `401` forgets the token and shows the sign-in again. When the server sets the session cookie described in `SCRATCHPAD.md`, the user token can stop living in `localStorage`.
- With an admin token, **Write** sends through `POST /admin/messages`: it skips moderation and the receipt is signed `ADMIN` (admin messages have no sender).

## Running it

The API doesn't send CORS headers, so the frontend must be served from the same origin. The Go server serves it when `WEB_DIR` is set:

```shell
WEB_DIR=../web ADMIN_TOKEN=mytoken go run .
```

Then open http://localhost:8080. In the devcontainer, `web/` is mounted at `/web` and `WEB_DIR` is already set (rebuild the container once after pulling this change).

With the seed data, paste `bbr_invite_dev_grace` on the join form, or put a dev session straight into the browser console:

```js
localStorage.setItem('bbr.userToken', 'bbr_user_dev_dave'); location.reload();
```

In production, any reverse proxy works as long as `/api/v1/` reaches the API and everything else serves `web/` (falling back to `index.html` for `/history`, `/admin` and `/join`).

## Layout

```
web/
  index.html        shell, fonts
  styles.css        all styles; tokens at the top
  js/app.js         router, session, nav
  js/api.js         fetch wrapper, endpoints, pagination helper
  js/auth.js        token storage
  js/dom.js         h() element helper (never innerHTML)
  js/format.js      dates, printer state, status mapping
  js/pages/*.js     one module per page
```

## Known limits

- List routes return 10 items per page, so the admin counts and the activity grid walk pages: the grid fetches every printed message from the last year. A stats endpoint (counts per status, printed per day) would replace that.
- History can't filter by recipient on the server, so the filter applies to the pages loaded so far.
- There's no user or printer management UI yet (creating users, invites, sessions, quiet hours): use the API directly.
