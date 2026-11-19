// Settings: everything a user (and a printer's owner) can change, in one pane opened
// from their name in the header. Each section loads when the pane opens.

import { fill, h } from '../dom.js';
import { api, ApiError, joinLink, parseCode } from '../api.js';
import { printerState, shortDate, timeAgo } from '../format.js';
import { confirmButton, copyField, openPane } from '../pane.js';
import { joinLinksList, quietHoursForm } from '../printer-tools.js';

function section(title, ...children) {
  return h('section', { class: 'pane-section' }, h('h3', { class: 'pane-section__title' }, title), ...children);
}

export function openSettings(ctx) {
  let me = ctx.me;
  let printers = null; // from /user/printers, for names and state
  let printersError = '';

  const devicesEl = h('div');
  const printersEl = h('div');
  const joinEl = h('div');

  /** Re-reads /me after a change and tells the page. Null once signed out. */
  async function refresh() {
    try {
      me = await ctx.refreshMe();
      printers = await api.userPrinters();
      printersError = '';
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) return null;
      printersError = err.message;
    }
    paintPrinters();
    return me;
  }

  /** The owner routes answer 403 once ownership has moved on. */
  function ownerError(err) {
    if (err instanceof ApiError && err.status === 403) {
      refresh();
      return 'You no longer own this printer.';
    }
    return err.message;
  }

  // -- Devices --

  const dev = { sessions: null, error: '', invite: null, inviting: false };

  async function loadSessions() {
    try {
      dev.sessions = await api.mySessions();
      dev.error = '';
    } catch (err) {
      dev.error = err.message;
    }
    paintDevices();
  }

  async function addDevice() {
    dev.inviting = true;
    dev.error = '';
    paintDevices();
    try {
      dev.invite = await api.myInvite();
    } catch (err) {
      dev.error = err.message;
    }
    dev.inviting = false;
    paintDevices();
  }

  function deviceRow(s) {
    const meta = [`Added ${shortDate(s.created_at)}`, s.current ? null : `used ${timeAgo(s.last_used_at)}`].filter(Boolean).join(' · ');
    return h('li', { class: 'row-list__item' },
      h('div', { class: 'row-list__main' },
        h('span', { class: 'strong' }, s.device_name, s.current && h('span', { class: 'tag' }, 'This device')),
        h('span', { class: 'row-list__meta' }, meta)),
      s.current
        ? confirmButton('Sign out', {
          question: 'Sign out here?',
          run: async () => {
            await api.revokeMySession(s.id);
            ctx.signedOut();
          },
          onError: (err) => {
            dev.error = err.message;
            paintDevices();
          },
        })
        : confirmButton('Remove', {
          question: 'Sign it out?',
          run: async () => {
            await api.revokeMySession(s.id);
            await loadSessions();
          },
          onError: (err) => {
            dev.error = err.message;
            paintDevices();
          },
        }));
  }

  function paintDevices() {
    const active = (dev.sessions ?? []).filter((s) => !s.revoked_at);
    fill(devicesEl, section('Your devices',
      dev.sessions == null
        ? !dev.error && h('p', { class: 'muted small' }, 'Loading…')
        : h('ul', { class: 'row-list' }, active.map(deviceRow)),
      dev.invite
        ? copyField(joinLink(dev.invite.invite), {
          label: 'Device invite',
          expiresAt: dev.invite.expires_at,
          note: 'Open it on your other device. It signs that device in as you, so keep it to yourself.',
        })
        : h('div', { class: 'tool__actions' },
          h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: dev.inviting, onClick: addDevice }, dev.inviting ? 'Creating…' : 'Add a device')),
      h('p', { class: 'form-error', role: 'alert' }, dev.error)));
  }

  // -- Printers --

  const printerErrors = new Map(); // printer id → message

  function ownerTools(p) {
    const tools = h('div', { class: 'owner-tools' });
    const details = h('details', { class: 'owner', onToggle: () => {
      // Built on first open, so members and links load only when asked for.
      if (details.open && !tools.firstChild) {
        fill(tools,
          h('h4', { class: 'owner__title' }, 'Quiet hours'),
          quietHoursForm(p, {
            save: (body) => api.ownerSetQuietHours(p.id, body),
            clear: () => api.ownerClearQuietHours(p.id),
            onSaved: (updated) => {
              printers = printers?.map((x) => (x.id === updated.id ? updated : x)) ?? null;
              paintPrinters();
              ctx.refreshMe().catch(() => {});
            },
            describe: ownerError,
          }),
          h('h4', { class: 'owner__title' }, 'Members'),
          membersList(p),
          h('h4', { class: 'owner__title' }, 'Join links'),
          joinLinksList({
            list: () => api.ownerJoinTokens(p.id),
            create: () => api.ownerCreateJoinToken(p.id),
            revoke: (tokenId) => api.ownerRevokeJoinToken(p.id, tokenId),
            describe: ownerError,
          }));
      }
    } },
    h('summary', { class: 'owner__summary' }, 'Manage this printer'),
    tools);
    return details;
  }

  function membersList(p) {
    const st = { members: null, error: '' };
    const el = h('div', { class: 'tool' });

    async function load() {
      try {
        st.members = await api.ownerMembers(p.id);
        st.error = '';
      } catch (err) {
        st.error = ownerError(err);
      }
      paint();
    }

    function paint() {
      fill(el,
        st.members == null
          ? !st.error && h('p', { class: 'muted small' }, 'Loading…')
          : st.members.length === 0
            ? h('p', { class: 'muted small' }, 'Nobody else yet. Send someone a join link.')
            : h('ul', { class: 'row-list' }, st.members.map((m) => h('li', { class: 'row-list__item' },
              h('div', { class: 'row-list__main' }, h('span', { class: 'strong' }, m.name)),
              confirmButton('Remove', {
                question: `Remove ${m.name}? Their queued messages won’t print.`,
                run: async () => {
                  await api.ownerRemoveMember(p.id, m.id);
                  await load();
                },
                onError: (err) => {
                  st.error = ownerError(err);
                  paint();
                },
              })))),
        h('p', { class: 'form-error', role: 'alert' }, st.error));
    }

    paint();
    load();
    return el;
  }

  function printerRow(access) {
    const p = printers?.find((x) => x.id === access.printer_id);
    const name = p?.recipient_name ?? 'Printer';
    const isOwner = access.role === 'owner';
    const onlyOne = me.printer_accesses.length === 1;
    const state = p ? printerState(p) : null;

    return h('li', { class: 'row-list__item row-list__item--stack' },
      h('div', { class: 'row-list__line' },
        h('div', { class: 'row-list__main' },
          h('span', { class: 'strong' }, name, isOwner && h('span', { class: 'tag tag--accent' }, 'Owner')),
          state && h('span', { class: 'row-list__meta' }, `${state.label}${p.quiet_start ? ` · quiet ${p.quiet_start}–${p.quiet_end}` : ''}`)),
        !isOwner && confirmButton('Leave', {
          question: onlyOne
            ? `It’s your only printer: leaving signs you out on every device.`
            : `Leave ${name}? Your queued messages won’t print.`,
          yes: 'Leave',
          run: async () => {
            await api.leavePrinter(access.printer_id);
            if (onlyOne) {
              ctx.signedOut();
              return;
            }
            await refresh();
          },
          onError: (err) => {
            printerErrors.set(access.printer_id, err.message);
            paintPrinters();
          },
        })),
      isOwner && p && ownerTools(p),
      h('p', { class: 'form-error', role: 'alert' }, printerErrors.get(access.printer_id) ?? ''));
  }

  function paintPrinters() {
    // Keep owner panels that are open (and their state) across repaints.
    const openOwner = printersEl.querySelector('details.owner[open]');
    fill(printersEl, section('Printers',
      printers == null && !printersError
        ? h('p', { class: 'muted small' }, 'Loading…')
        : me.printer_accesses.length === 0
          ? h('p', { class: 'muted small' }, 'You aren’t on any printer.')
          : h('ul', { class: 'row-list' }, me.printer_accesses.map(printerRow)),
      h('p', { class: 'form-error', role: 'alert' }, printersError)));
    if (openOwner) {
      const fresh = printersEl.querySelector('details.owner');
      fresh?.replaceWith(openOwner);
    }
  }

  // -- Join another printer --

  function paintJoin() {
    const input = h('input', { id: 'join-code', class: 'input input--mono', type: 'text', autocomplete: 'off', spellcheck: 'false', placeholder: 'bbr_join_… or the link' });
    const error = h('p', { class: 'form-error', role: 'alert' });
    const done = h('p', { class: 'muted small', role: 'status' });
    const submit = h('button', { type: 'submit', class: 'btn btn--solid btn--sm' }, 'Join');

    async function onSubmit(event) {
      event.preventDefault();
      error.textContent = '';
      done.textContent = '';
      const parsed = parseCode(input.value);
      if (!parsed || parsed.kind !== 'join') {
        error.textContent = 'That isn’t a join link. It starts with bbr_join_.';
        return;
      }
      submit.disabled = true;
      try {
        const before = new Set(me.printer_accesses.map((a) => a.printer_id));
        await api.redeemJoin(parsed.code);
        await refresh();
        const added = me.printer_accesses.find((a) => !before.has(a.printer_id));
        const name = printers?.find((p) => p.id === added?.printer_id)?.recipient_name;
        input.value = '';
        done.textContent = name ? `Done. You can now write to ${name}.` : 'Done.';
      } catch (err) {
        error.textContent = err instanceof ApiError && err.status === 400
          ? 'This link doesn’t work any more: it may have expired, been used or been revoked. Ask for a new one.'
          : err.message;
      }
      submit.disabled = false;
    }

    fill(joinEl, section('Join another printer',
      h('form', { class: 'tool__form', onSubmit },
        h('div', { class: 'field' },
          h('label', { for: 'join-code', class: 'field__label' }, 'Join link'),
          input,
          h('span', { class: 'field__hint' }, `You’ll join as ${me.name}.`)),
        h('div', { class: 'tool__actions' }, submit),
        error,
        done)));
  }

  openPane({
    title: 'Settings',
    render(body) {
      fill(body,
        h('p', { class: 'muted small pane__lede' }, `Signed in as ${me.name}.`),
        printersEl,
        devicesEl,
        joinEl);
      paintPrinters();
      paintDevices();
      paintJoin();
      loadSessions();
      api.userPrinters().then((list) => {
        printers = list;
        paintPrinters();
      }, (err) => {
        printersError = `Couldn’t load printers. ${err.message}`;
        paintPrinters();
      });
    },
  });
}
