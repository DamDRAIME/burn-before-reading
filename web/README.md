# Burn Before Reading: web frontend

The website people use to write messages, follow them until they're printed, and moderate them. It's plain HTML, CSS and JavaScript modules: no build step, no dependencies. The only external request is Google Fonts.

## Pages

| Path | Who | What |
|---|---|---|
| `/` | user or admin | **Write.** The receipt is the composer: pick a recipient, write, print. After sending, the circle and the four-step bar follow the message (awaiting → approved → queued → printed), polling every 3 s. The draft is kept on this device until it's sent, so a reload or a trip to History doesn't lose it. |
| `/history` | user | The user's own messages as small receipts, filterable by recipient. |
| `/admin` | admin | (A signed-in user who isn't an admin sees "Admins only".) Counts, a 52-week activity grid, the approval queue, printers, users and the full message log (with requeue for failed messages). Each printer and user has a **Manage** pane: ownership, quiet hours, join links, printer access, devices and recovery invites. |
| `/join#bbr_join_…` | anyone | Signs up with a printer owner's join link (pick a name, name the device). Someone already signed in gets a "Join as *name*" confirmation instead, which adds the printer to their account. |
| `/join#bbr_invite_…` | anyone | Claims an invite (from the admin, or a device invite from your own account) for this device. The same form shows on `/` when nobody is signed in, and accepts either kind of code. |

### Settings

Settings never sit on a page: clicking your name in the header opens a pane on the right (a modal `<dialog>`; Esc or a click outside closes it).

- **Printers:** the ones you can write to, with **Leave** (leaving your only printer signs you out everywhere, so it says so first). On the printer you own, **Manage this printer** unfolds quiet hours, members (with **Remove**) and join links (create, copy or share, revoke).
- **Your devices:** every signed-in device, **Remove** for the others, **Sign out** for this one, and **Add a device**, which shows a 15-minute device invite link.
- **Join another printer:** paste a join link to add a printer under your current name.

Links and invites are shown once, with Copy and (where the browser supports it) Share.

### Refreshing

- Pages refresh in the background (Write every 3 s while following a message, History and Admin every 20 s), but **not while the tab is hidden**: a refresh that comes due then waits, and runs as soon as the tab is shown again.
- When two refreshes in a row fail, the page says "Can't reach the server. Retrying…" and keeps trying; the line goes away on the next answer.
- On `/admin`, the number of messages waiting for approval shows in the nav ("Admin · 3") and in the tab title ("(3) Admin"), so a moderator sees it from another tab.

## Sign-in

- **New users** open the join link a printer's owner sent them (`/join#bbr_join_…`), pick their name and name the device, and get a session token. The admin can also create an account and send an invite (`/join#bbr_invite_…`), which only asks for a device name.
- **More devices:** a signed-in user creates a device invite in Settings and opens it on the other device.
- Codes travel in the URL fragment, so they're never sent to the server in a request line or logged, and the page removes them from the address bar.
- **Admins** are regular users with `is_admin` set on the server (see the server README). They sign in the same way; `GET /user/me` returns `is_admin`, which shows the **Admin** link in the nav. Signing out is the usual **Sign out** in Settings.
- The session token is kept in `localStorage` and sent as `Authorization: Bearer`, on user and admin routes alike. A `401` forgets it and shows the sign-in again. A `403` on an admin route (the account was demoted) reloads `/user/me`, so the Admin link goes away. When the server sets the session cookie described in `SCRATCHPAD.md`, the token can stop living in `localStorage`.
- For an admin, the receipt on **Write** has an extra line, **AS** `MEMBER` / `ADMIN` (hover either for what it means; the choice is remembered on the device):
  - `MEMBER`: like any user, through `POST /user/messages`. A moderator approves it, and only the admin's own printers are listed.
  - `ADMIN`: through `POST /admin/messages`. It skips moderation and membership, so every printer is listed and it prints right away.
  - Either way the message is signed with the admin's name and shows in their History; admin sends carry an `ADMIN` tag there and in the admin log.

## Running it

The API doesn't send CORS headers, so the frontend must be served from the same origin. The Go server serves it when `WEB_DIR` is set:

```shell
WEB_DIR=../web go run .
```

Then open http://localhost:8080. In the devcontainer, `web/` is mounted at `/web` and `WEB_DIR` is already set (rebuild the container once after pulling this change).

With the seed data, open `/join#bbr_join_dev_marc` to sign up with a join link, paste `bbr_invite_dev_grace` on the join form, or put a dev session straight into the browser console. Dave owns the printer "Alice and Dave" and is the seeded admin; use `bbr_session_dev_eve` to see a regular user:

```js
localStorage.setItem('bbr.userToken', 'bbr_session_dev_dave'); location.reload();
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
  js/pane.js        side pane (<dialog>), copy field, confirm button
  js/printer-tools.js  quiet hours and join links, shared by owners and the admin
  js/visibility.js  pauses background refreshes while the tab is hidden
  js/dom.js         h() element helper (never innerHTML)
  js/format.js      dates, printer state, status mapping
  js/pages/*.js     one module per page
```

## Known limits

- List routes return 10 items per page, so the admin counts and the activity grid walk pages: the grid fetches every printed message from the last year. A stats endpoint (counts per status, printed per day) would replace that.
- History can't filter by recipient on the server, so the filter applies to the pages loaded so far.
- Accounts can't be renamed or deleted (the API doesn't support it yet), and printers are still created and renamed through the API.
- Device invites and join links are shown as links, not QR codes.
