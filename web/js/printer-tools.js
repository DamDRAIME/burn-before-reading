// Printer tools shared by the owner's Settings pane and the admin's printer pane.
// The API calls are passed in, so the same UI serves /user/printers/… and /admin/printers/….

import { fill, h } from './dom.js';
import { joinLink } from './api.js';
import { DEFAULT_TIMEZONE, joinTokenStatusText, shortDate } from './format.js';
import { confirmButton, copyField } from './pane.js';

let uid = 0;

function timezones() {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return [DEFAULT_TIMEZONE];
  }
}

/**
 * Quiet hours: the printer holds messages between two times (may cross midnight).
 * save(body) and clear() resolve to the updated printer; onSaved(printer) follows.
 */
export function quietHoursForm(printer, { save, clear, onSaved, describe = (err) => err.message }) {
  const n = ++uid;
  let p = printer;
  let busy = false;
  let error = '';
  let saved = '';

  const el = h('div', { class: 'tool' });

  const startInput = h('input', { id: `qh-start-${n}`, class: 'input', type: 'time', required: true });
  const endInput = h('input', { id: `qh-end-${n}`, class: 'input', type: 'time', required: true });
  const zoneInput = h('input', { id: `qh-zone-${n}`, class: 'input', type: 'text', list: `qh-zones-${n}`, autocomplete: 'off', spellcheck: 'false' });
  const zoneList = h('datalist', { id: `qh-zones-${n}` }, timezones().map((z) => h('option', { value: z })));

  function reset() {
    startInput.value = p.quiet_start ?? '22:00';
    endInput.value = p.quiet_end ?? '07:00';
    zoneInput.value = p.timezone ?? DEFAULT_TIMEZONE;
  }

  async function run(action) {
    busy = true;
    error = '';
    saved = '';
    paint();
    try {
      p = await action();
      saved = p.quiet_start ? 'Saved.' : 'Quiet hours turned off.';
      reset();
      onSaved?.(p);
    } catch (err) {
      error = describe(err);
    }
    busy = false;
    paint();
  }

  function onSubmit(event) {
    event.preventDefault();
    const body = { quiet_start: startInput.value, quiet_end: endInput.value, timezone: zoneInput.value.trim() || DEFAULT_TIMEZONE };
    if (!body.quiet_start || !body.quiet_end) {
      error = 'Pick both times.';
      paint();
      return;
    }
    if (body.quiet_start === body.quiet_end) {
      error = 'The start and end must differ.';
      paint();
      return;
    }
    run(() => save(body));
  }

  function paint() {
    const on = p.quiet_start && p.quiet_end;
    fill(el,
      h('p', { class: 'tool__lede' }, on
        ? `Asleep from ${p.quiet_start} to ${p.quiet_end} (${p.timezone ?? DEFAULT_TIMEZONE}). Messages wait and print when it wakes.`
        : 'Always awake. Set quiet hours to hold messages overnight.'),
      h('form', { class: 'tool__form', onSubmit },
        h('div', { class: 'tool__row' },
          h('div', { class: 'field' }, h('label', { class: 'field__label', for: startInput.id }, 'From'), startInput),
          h('div', { class: 'field' }, h('label', { class: 'field__label', for: endInput.id }, 'To'), endInput)),
        h('div', { class: 'field' }, h('label', { class: 'field__label', for: zoneInput.id }, 'Time zone'), zoneInput, zoneList),
        h('div', { class: 'tool__actions' },
          h('button', { type: 'submit', class: 'btn btn--solid btn--sm', disabled: busy }, on ? 'Save' : 'Turn on'),
          on && h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: busy, onClick: () => run(clear) }, 'Turn off'),
          saved && h('span', { class: 'muted small', role: 'status' }, saved))),
      h('p', { class: 'form-error', role: 'alert' }, error));
  }

  reset();
  paint();
  return el;
}

/**
 * Join links for one printer: the newest ones with their state, revoke for unused
 * ones, and "New join link", whose code is shown once.
 */
export function joinLinksList({ list, create, revoke, describe = (err) => err.message }) {
  const st = { tokens: null, error: '', fresh: null, creating: false };
  const el = h('div', { class: 'tool' });

  async function load() {
    try {
      st.tokens = (await list()) ?? [];
    } catch (err) {
      st.error = describe(err);
    }
    paint();
  }

  async function onCreate() {
    st.creating = true;
    st.error = '';
    paint();
    try {
      st.fresh = await create();
      await load();
    } catch (err) {
      st.error = describe(err);
    }
    st.creating = false;
    paint();
  }

  function row(t) {
    return h('li', { class: 'row-list__item' },
      h('div', { class: 'row-list__main' },
        h('span', { class: t.status === 'pending' ? 'strong' : null }, joinTokenStatusText(t)),
        h('span', { class: 'row-list__meta' }, `Made ${shortDate(t.created_at)}${t.created_by ? '' : ' by the admin'}`)),
      t.status === 'pending' && confirmButton('Revoke', {
        question: 'Revoke this link?',
        run: async () => {
          await revoke(t.id);
          if (st.fresh?.id === t.id) st.fresh = null;
          await load();
        },
        onError: (err) => {
          st.error = describe(err);
          paint();
        },
      }));
  }

  function paint() {
    fill(el,
      h('p', { class: 'tool__lede' }, 'A join link adds one person to this printer, under the name they choose. Each link works once, for 2 days.'),
      st.fresh && copyField(joinLink(st.fresh.join_token), {
        label: 'New join link',
        note: 'Send it to one person. It’s shown only now.',
      }),
      h('div', { class: 'tool__actions' },
        h('button', { type: 'button', class: 'btn btn--solid btn--sm', disabled: st.creating, onClick: onCreate }, st.creating ? 'Creating…' : 'New join link')),
      h('p', { class: 'form-error', role: 'alert' }, st.error),
      st.tokens == null
        ? !st.error && h('p', { class: 'muted small' }, 'Loading…')
        : st.tokens.length > 0 && h('ul', { class: 'row-list' }, st.tokens.map(row)));
  }

  paint();
  load();
  return el;
}
