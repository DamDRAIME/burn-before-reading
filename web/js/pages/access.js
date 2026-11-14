// Pages for getting in: claiming an invite, signing in as admin, and the fallbacks.

import { h, rings } from '../dom.js';
import { api, ApiError } from '../api.js';
import { auth } from '../auth.js';

const INVITE_PATTERN = /bbr_invite_[A-Za-z0-9_-]+/;

/** Accepts the bare invite or a whole link such as https://…/join#bbr_invite_… */
function extractInvite(text) {
  const match = text.match(INVITE_PATTERN);
  return match ? match[0] : text.trim();
}

function guessDeviceName() {
  const ua = navigator.userAgent;
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser';
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
  return os ? `${browser} on ${os}` : browser;
}

function hero(title, lede) {
  return h('section', { class: 'access__hero' },
    h('h1', { class: 'display' }, ...title.split(' ').flatMap((word, i) => (i ? [h('br'), word] : [word]))),
    h('p', { class: 'lede' }, lede));
}

function field(id, label, input, hint) {
  return h('div', { class: 'field' },
    h('label', { for: id, class: 'field__label' }, label),
    input,
    hint && h('span', { class: 'field__hint', id: `${id}-hint` }, hint));
}

/** "/join#bbr_invite_…" or the signed-out landing page. */
export function joinPage(ctx) {
  let rawHash = window.location.hash.slice(1);
  try {
    rawHash = decodeURIComponent(rawHash);
  } catch {
    // Keep it as typed.
  }
  const fromHash = extractInvite(rawHash);
  if (window.location.hash) {
    // Don't leave the invite in the address bar or the history.
    window.history.replaceState(null, '', window.location.pathname);
  }
  const hasInvite = INVITE_PATTERN.test(fromHash);

  const inviteInput = h('input', { id: 'invite', class: 'input input--mono', type: 'text', autocomplete: 'off', spellcheck: 'false', required: true, value: hasInvite ? fromHash : '', placeholder: 'bbr_invite_…', 'aria-describedby': 'invite-hint' });
  const deviceInput = h('input', { id: 'device', class: 'input', type: 'text', maxlength: 50, required: true, value: guessDeviceName(), 'aria-describedby': 'device-hint' });
  const error = h('p', { class: 'form-error', role: 'alert' });
  const submit = h('button', { type: 'submit', class: 'btn btn--solid' }, 'Connect this device →');

  async function onSubmit(event) {
    event.preventDefault();
    const invite = extractInvite(inviteInput.value);
    const deviceName = deviceInput.value.trim();
    if (!invite || !deviceName) return;
    submit.disabled = true;
    error.textContent = '';
    try {
      const { token } = await api.claim(invite, deviceName);
      auth.setUserToken(token);
      ctx.navigate('/', { replace: true });
      ctx.signedIn();
    } catch (err) {
      error.textContent = err instanceof ApiError && err.status === 401
        ? 'This invite doesn’t work. It may have expired (invites last 2 days) or been used already. Ask for a new one.'
        : err.message;
      submit.disabled = false;
    }
  }

  const el = h('main', { class: 'page access' },
    hero('Burn Before Reading', hasInvite
      ? 'You’ve been invited. Name this device and you’re in. You won’t need to do this again here.'
      : 'Notes you write here are printed on a receipt printer in someone’s home. It’s invite-only: open the invite link you were sent, or paste it below.'),
    h('section', { class: 'access__panel' },
      rings([48, 41, 34, 27, 20, 13], { className: 'access__rings' }),
      h('form', { class: 'card-form', onSubmit },
        h('h2', { class: 'section-title' }, 'Connect this device'),
        field('invite', 'Invite', inviteInput, 'Paste the invite or the whole link. Each one works once.'),
        field('device', 'Device name', deviceInput, 'Helps the admin tell your devices apart.'),
        submit,
        error),
      h('p', { class: 'muted small' }, 'Running this? ', h('a', { href: '/admin', 'data-link': true }, 'Sign in as admin'), '.')));

  return { el, title: 'Join' };
}

export function adminSignInPage(ctx) {
  const tokenInput = h('input', { id: 'admin-token', class: 'input input--mono', type: 'password', autocomplete: 'current-password', required: true });
  const error = h('p', { class: 'form-error', role: 'alert' });
  const submit = h('button', { type: 'submit', class: 'btn btn--solid' }, 'Sign in');

  async function onSubmit(event) {
    event.preventDefault();
    const token = tokenInput.value.trim();
    if (!token) return;
    submit.disabled = true;
    error.textContent = '';
    try {
      await api.checkAdminToken(token);
      auth.setAdminToken(token);
      ctx.signedIn();
    } catch (err) {
      error.textContent = err instanceof ApiError && err.status === 401 ? 'That token isn’t right.' : err.message;
      submit.disabled = false;
    }
  }

  const el = h('main', { class: 'page access' },
    hero('Admin', 'Approve messages, watch the printers, and send without moderation.'),
    h('section', { class: 'access__panel' },
      h('form', { class: 'card-form', onSubmit },
        h('h2', { class: 'section-title' }, 'Sign in'),
        field('admin-token', 'Admin token', tokenInput, 'The server’s ADMIN_TOKEN. It’s remembered in this browser until you sign out.'),
        submit,
        error)));

  return { el, title: 'Admin sign-in' };
}

export function sessionErrorPage(ctx, err, retry) {
  const el = h('main', { class: 'page access' },
    hero('Not now', err.message),
    h('section', { class: 'access__panel' },
      h('button', { type: 'button', class: 'btn btn--solid', onClick: retry }, 'Try again')));
  return { el, title: 'Error' };
}

export function notFoundPage() {
  const el = h('main', { class: 'page access' },
    hero('Nothing here', 'This page doesn’t exist.'),
    h('section', { class: 'access__panel' }, h('a', { href: '/', 'data-link': true, class: 'btn btn--solid' }, 'Go to Write')));
  return { el, title: 'Not found' };
}
