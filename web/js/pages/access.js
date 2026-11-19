// Pages for getting in: a join link or an invite, signing in as admin, and the fallbacks.

import { fill, h, rings } from '../dom.js';
import { api, ApiError, parseCode } from '../api.js';
import { auth } from '../auth.js';

const NAME_MAX = 20;
const DEVICE_MAX = 50;

const SPENT_JOIN = 'This join link doesn’t work any more. It may have expired (links last 2 days), been used or been revoked. Ask for a new one.';
const SPENT_INVITE = 'This invite doesn’t work any more. It may have expired or been used already. Ask for a new one.';

function guessDeviceName() {
  const ua = navigator.userAgent;
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser';
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
  return os ? `${browser} on ${os}` : browser;
}

/** One word per line, each its own element so each gets its own dither (like Write); the last line a bit more. */
function displayTitle(title) {
  const words = title.split(' ');
  return h('h1', { class: 'display', 'aria-label': title },
    words.map((word, i) => h('span', { class: i < words.length - 1 ? 'dither-line dither-line--light' : 'dither-line', 'aria-hidden': 'true' }, word)));
}

function hero(title, lede) {
  return h('section', { class: 'access__hero' },
    displayTitle(title),
    typeof lede === 'string' ? h('p', { class: 'lede' }, lede) : lede);
}

function field(id, label, input, hint) {
  return h('div', { class: 'field' },
    h('label', { for: id, class: 'field__label' }, label),
    input,
    hint && h('span', { class: 'field__hint', id: `${id}-hint` }, hint));
}

/** Reads the code from "/join#bbr_…" and removes it from the address bar and history. */
function takeCodeFromHash() {
  let raw = window.location.hash.slice(1);
  try {
    raw = decodeURIComponent(raw);
  } catch {
    // Keep it as typed.
  }
  if (window.location.hash) window.history.replaceState(null, '', window.location.pathname);
  return parseCode(raw);
}

/**
 * "/join#bbr_join_…", "/join#bbr_invite_…", or the signed-out landing page.
 * Someone already signed in who opens a join link adds its printer to their account.
 */
export function joinPage(ctx) {
  const fromHash = takeCodeFromHash();
  if (fromHash?.kind === 'join' && ctx.isUser) return joinAsMemberPage(ctx, fromHash.code);
  return connectPage(ctx, fromHash);
}

function connectPage(ctx, fromHash) {
  // A join link asks for a name (it makes a new account); an invite doesn't (the account exists).
  let kind = fromHash?.kind ?? null;

  const codeInput = h('input', { id: 'code', class: 'input input--mono', type: 'text', autocomplete: 'off', spellcheck: 'false', required: true, value: fromHash?.code ?? '', placeholder: 'bbr_join_… or bbr_invite_…', 'aria-describedby': 'code-hint' });
  const nameInput = h('input', { id: 'name', class: 'input', type: 'text', maxlength: NAME_MAX, autocomplete: 'nickname', 'aria-describedby': 'name-hint' });
  const deviceInput = h('input', { id: 'device', class: 'input', type: 'text', maxlength: DEVICE_MAX, required: true, value: guessDeviceName(), 'aria-describedby': 'device-hint' });
  const nameField = field('name', 'Your name', nameInput, 'Printed on everything you send. Nobody else on this printer can use it.');
  const error = h('p', { class: 'form-error', role: 'alert' });
  const submit = h('button', { type: 'submit', class: 'btn btn--solid' });
  const intro = h('p', { class: 'lede' });

  function update() {
    kind = parseCode(codeInput.value)?.kind ?? null;
    nameField.hidden = kind !== 'join';
    nameInput.required = kind === 'join';
    submit.textContent = kind === 'join' ? 'Join →' : 'Connect this device →';
    intro.textContent = kind === 'join'
      ? 'You’ve been sent a join link. Pick the name you’ll write under and you’re in. You won’t need to do this again here.'
      : kind === 'invite'
        ? 'You’ve been sent an invite. Name this device and you’re in. You won’t need to do this again here.'
        : 'Notes you write here are printed on a receipt printer in someone’s home. To get in, open the join link the printer’s owner sent you, or paste it below.';
  }
  codeInput.addEventListener('input', update);

  async function onSubmit(event) {
    event.preventDefault();
    const parsed = parseCode(codeInput.value);
    const deviceName = deviceInput.value.trim();
    const name = nameInput.value.trim();
    error.textContent = '';
    if (!parsed) {
      error.textContent = 'That doesn’t look like a join link or an invite. Paste the whole link you were sent.';
      return;
    }
    if (!deviceName || (parsed.kind === 'join' && !name)) return;
    submit.disabled = true;
    try {
      const { token } = parsed.kind === 'join'
        ? await api.join(parsed.code, name, deviceName)
        : await api.claim(parsed.code, deviceName);
      auth.setUserToken(token);
      ctx.navigate('/', { replace: true });
      ctx.signedIn();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        error.textContent = parsed.kind === 'join' ? SPENT_JOIN : SPENT_INVITE;
      } else if (err instanceof ApiError && err.status === 409) {
        error.textContent = `Someone on this printer is already called ${name}. Pick another name; the link still works.`;
        nameInput.focus();
        nameInput.select();
      } else {
        error.textContent = err.message;
      }
      submit.disabled = false;
    }
  }

  update();
  const el = h('main', { class: 'page access' },
    hero('Burn Before Reading', intro),
    h('section', { class: 'access__panel' },
      rings([48, 41, 34, 27, 20, 13], { className: 'access__rings' }),
      h('form', { class: 'card-form', onSubmit },
        h('h2', { class: 'section-title' }, 'Get in'),
        field('code', 'Join link or invite', codeInput, 'Paste the code or the whole link. Each one works once.'),
        nameField,
        field('device', 'Device name', deviceInput, 'Helps you tell your devices apart in Settings.'),
        submit,
        error),
      h('p', { class: 'muted small' }, 'Running this? ', h('a', { href: '/admin', 'data-link': true }, 'Sign in as admin'), '.')));

  if (fromHash) queueMicrotask(() => (kind === 'join' ? nameInput : deviceInput).focus());
  return { el, title: 'Join' };
}

/** Signed in and opened a join link: confirm, then add the printer to this account. */
function joinAsMemberPage(ctx, code) {
  const error = h('p', { class: 'form-error', role: 'alert' });
  const submit = h('button', { type: 'submit', class: 'btn btn--solid' }, `Join as ${ctx.me.name} →`);
  const actions = h('div', { class: 'card-form__actions' },
    submit,
    h('a', { href: '/', 'data-link': true, class: 'btn btn--outline' }, 'Not now'));

  async function onSubmit(event) {
    event.preventDefault();
    submit.disabled = true;
    error.textContent = '';
    try {
      await api.redeemJoin(code);
      await ctx.refreshMe();
      ctx.navigate('/', { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 400) {
        error.textContent = SPENT_JOIN;
        fill(actions, h('a', { href: '/', 'data-link': true, class: 'btn btn--solid' }, 'Go to Write'));
        return;
      }
      error.textContent = err.message; // 409: already on this printer, or the name is taken there
      submit.disabled = false;
    }
  }

  const el = h('main', { class: 'page access' },
    hero('Join a printer', 'Someone sent you a join link. It adds their printer to the account on this device, so you can write to it too.'),
    h('section', { class: 'access__panel' },
      rings([48, 41, 34, 27, 20, 13], { className: 'access__rings' }),
      h('form', { class: 'card-form', onSubmit },
        h('h2', { class: 'section-title' }, 'Join'),
        h('p', { class: 'muted' }, `You’ll write as ${ctx.me.name}, the name on this device. The printer’s owner will see who joined.`),
        actions,
        error)));

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
