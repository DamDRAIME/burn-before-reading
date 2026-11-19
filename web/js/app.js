import { h } from './dom.js';
import { installDither } from './dither.js';
import { api, ApiError, onUnauthorized } from './api.js';
import { auth } from './auth.js';
import { writePage } from './pages/write.js';
import { historyPage } from './pages/history.js';
import { adminPage } from './pages/admin.js';
import { adminSignInPage, joinPage, notFoundPage, sessionErrorPage } from './pages/access.js';
import { openSettings } from './pages/settings.js';
import { closePane } from './pane.js';

const root = document.getElementById('app');
const state = { me: null };
let current = null;
let renderId = 0;
let renderQueued = false;

export function navigate(path, { replace = false } = {}) {
  if (replace) window.history.replaceState(null, '', path);
  else window.history.pushState(null, '', path);
  scheduleRender();
}

function scheduleRender() {
  if (renderQueued) return;
  renderQueued = true;
  queueMicrotask(() => {
    renderQueued = false;
    render();
  });
}

onUnauthorized((role) => {
  closePane();
  if (role === 'user') {
    auth.setUserToken(null);
    state.me = null;
  } else {
    auth.setAdminToken(null);
  }
  scheduleRender();
});

// Internal links: <a href="/history" data-link>
document.addEventListener('click', (event) => {
  const link = event.target.closest('a[data-link]');
  if (!link || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  if (link.getAttribute('href') !== window.location.pathname) navigate(link.getAttribute('href'));
});

// Back/forward. In-page anchors (#queue) also fire popstate: those keep the page as it is.
let renderedPath = null;
window.addEventListener('popstate', () => {
  if (window.location.pathname !== renderedPath) scheduleRender();
});

/** Loads /user/me when a user token is stored. Returns an error to show, if any. */
async function loadSession() {
  if (!auth.userToken() || state.me) return null;
  try {
    state.me = await api.me();
    return null;
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return null; // token cleared by the 401 handler
    return err;
  }
}

function nav(ctx, path) {
  const link = (href, label) => h('a', { href, 'data-link': true, class: 'nav__link', 'aria-current': path === href ? 'page' : null }, label);
  const who = ctx.me ? ctx.me.name : ctx.isAdmin ? 'Admin' : null;
  return h('header', { class: 'site-header' },
    h('a', { href: '/', 'data-link': true, class: 'site-header__brand' }, 'Burn Before Reading'),
    (ctx.me || ctx.isAdmin) && h('nav', { class: 'nav', 'aria-label': 'Main' },
      link('/', 'Write'),
      ctx.me && link('/history', 'History'),
      ctx.isAdmin && link('/admin', 'Admin'),
      who && h(ctx.me ? 'button' : 'span', ctx.me
        ? { type: 'button', class: 'nav__who nav__who--button', 'aria-haspopup': 'dialog', title: 'Settings', onClick: () => openSettings(ctx) }
        : { class: 'nav__who' },
      h('span', { class: 'avatar', 'aria-hidden': 'true' }, who.slice(0, 1).toUpperCase()),
      who,
      ctx.me && h('span', { class: 'visually-hidden' }, ': settings'))));
}

async function render() {
  const id = ++renderId;
  const sessionError = await loadSession();
  if (id !== renderId) return;

  // A pane belongs to the page it was opened on.
  if (window.location.pathname !== renderedPath) closePane();
  current?.destroy?.();
  current = null;

  const path = window.location.pathname.replace(/\/+$/, '') || '/';
  const ctx = {
    me: state.me,
    isUser: Boolean(state.me),
    isAdmin: Boolean(auth.adminToken()),
    navigate,
    signedIn() {
      state.me = null;
      scheduleRender();
    },
    signedOut() {
      closePane();
      auth.setUserToken(null);
      state.me = null;
      scheduleRender();
    },
    /**
     * After a change to the account (joined, left, quiet hours): reloads /me and lets
     * the page refresh what it shows, without re-rendering it (a draft stays put).
     */
    async refreshMe() {
      const me = await api.me();
      state.me = me;
      ctx.me = me;
      current?.meChanged?.(me);
      return me;
    },
  };

  let page;
  if (sessionError) page = sessionErrorPage(ctx, sessionError, scheduleRender);
  else if (path === '/join') page = joinPage(ctx);
  else if (path === '/admin') page = ctx.isAdmin ? adminPage(ctx) : adminSignInPage(ctx);
  else if (path === '/history') page = ctx.isUser ? historyPage(ctx) : joinPage(ctx);
  else if (path === '/') page = ctx.isUser || ctx.isAdmin ? writePage(ctx) : joinPage(ctx);
  else page = notFoundPage(ctx);

  document.title = page.title ? `${page.title} · Burn Before Reading` : 'Burn Before Reading';
  root.replaceChildren(h('div', { class: 'shell' }, nav(ctx, path), page.el));
  current = page;
  renderedPath = window.location.pathname;
  window.scrollTo(0, 0);
}

installDither();
render();
