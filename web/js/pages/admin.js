// Admin console: numbers, a year of printing, the approval queue, printers and the full log.

import { fill, h, rings, shape } from '../dom.js';
import { api, ApiError, fetchAll, joinLink, PAGE_SIZE } from '../api.js';
import { dayKey, flagLabel, longDate, MONTHS_SHORT, printerState, quietHoursText, receiptDate, seqNo, shortDate, STATUS, timeAgo } from '../format.js';
import { closePane, confirmButton, copyField, openPane } from '../pane.js';
import { joinLinksList, quietHoursForm } from '../printer-tools.js';
import { whenVisible } from '../visibility.js';

const REFRESH_MS = 20000;
const WEEKS = 52;

const FILTERS = [
  ['all', 'All', null],
  ['review', 'In review', 'awaiting_review'],
  ['waiting', 'Awaiting', 'awaiting_approval'],
  ['queue', 'In queue', 'pending,claimed,processed'],
  ['printed', 'Printed', 'printed'],
  ['problems', 'Failed & rejected', 'failed,rejected'],
];

function heatLevel(n) {
  if (n === 0) return 0;
  if (n <= 2) return 1;
  if (n <= 4) return 2;
  if (n <= 7) return 3;
  return 4;
}

/** 52 week columns, Sunday first, ending with the current week. */
function activityGrid(today) {
  const end = new Date(today.getFullYear(), today.getMonth(), today.getDate() + (6 - today.getDay()));
  const start = new Date(end.getFullYear(), end.getMonth(), end.getDate() - (WEEKS * 7 - 1));
  const weeks = [];
  for (let w = 0; w < WEEKS; w++) {
    const days = [];
    for (let d = 0; d < 7; d++) days.push(new Date(start.getFullYear(), start.getMonth(), start.getDate() + w * 7 + d));
    const first = days.find((day) => day.getDate() === 1);
    weeks.push({ days, month: first ? MONTHS_SHORT[first.getMonth()] : '' });
  }
  return { start, weeks };
}

export function adminPage(ctx) {
  const st = {
    printers: [],
    users: [],
    waiting: [],
    reviewCount: 0, // web messages the automated moderation hasn't decided on yet
    moderation: null, // true / false once loaded
    moderationBusy: false,
    moderationError: null,
    queueCount: null,
    activity: null, // { counts: Map(dayKey → n), total, year }
    activityError: null,
    filter: 'all',
    page: 0,
    rows: [],
    rowsMore: false,
    rowsLoading: true,
    error: null,
    busy: new Set(), // message ids with an action in flight
    notice: null,
    hover: null,
  };
  const timers = new Set();
  let destroyed = false;

  const printerNames = () => new Map(st.printers.map((p) => [p.id, p.recipient_name]));
  const toName = (id) => printerNames().get(id) ?? 'Unknown';

  // -- Sections, each repainted on its own --

  const statsEl = h('div', { class: 'stats' });
  const activityEl = h('section', { id: 'activity', class: 'admin-section', 'aria-labelledby': 'activity-h' });
  // The moderation switch has its own element, so the 20 s refresh doesn't reset a pending "Turn off?" confirmation.
  const queueHeadEl = h('div');
  const moderationEl = h('div', { class: 'moderation' });
  const queueBodyEl = h('div', { class: 'queue__body' });
  const queueEl = h('section', { id: 'queue', class: 'admin-section', 'aria-labelledby': 'queue-h' }, queueHeadEl, moderationEl, queueBodyEl);
  const printersEl = h('section', { id: 'printers', class: 'admin-section', 'aria-labelledby': 'printers-h' });
  const usersEl = h('section', { id: 'users', class: 'admin-section', 'aria-labelledby': 'users-h' });
  const logEl = h('section', { id: 'log', class: 'admin-section', 'aria-labelledby': 'log-h' });
  const errorEl = h('p', { class: 'form-error', role: 'alert' });

  const el = h('main', { class: 'page admin' },
    h('div', { class: 'admin__top' },
      h('div', { class: 'page-head' },
        h('div', { class: 'page-head__title' },
          h('h1', { class: 'display' }, 'Admin'),
          h('span', { class: 'emblem', 'aria-hidden': 'true' },
            rings([48, 41, 34, 27, 20, 13]),
            h('span', { class: 'emblem__dot' }),
            h('span', { class: 'emblem__dot emblem__dot--small' }))),
        h('nav', { class: 'subnav', 'aria-label': 'Admin sections' },
          h('a', { href: '#activity' }, 'Activity'),
          h('a', { href: '#queue' }, 'Approvals'),
          h('a', { href: '#printers' }, 'Printers'),
          h('a', { href: '#users' }, 'Users'),
          h('a', { href: '#log' }, 'All messages'))),
      errorEl,
      statsEl),
    activityEl,
    queueEl,
    printersEl,
    usersEl,
    logEl);

  function stat(label, value, { accent = false, suffix = null } = {}) {
    return h('div', { class: 'stats__item' },
      h('span', { class: 'stats__label' }, label),
      h('span', { class: `stats__value${accent ? ' is-accent' : ''}` }, value, suffix && h('span', { class: 'stats__suffix' }, suffix)));
  }

  function paintStats() {
    const online = st.printers.filter((p) => p.online).length;
    fill(statsEl, 
      stat('Waiting for approval', String(st.waiting.length), { accent: st.waiting.length > 0 }),
      stat('In print queue', st.queueCount == null ? '…' : String(st.queueCount)),
      stat('Printers online', String(online), { suffix: `/${st.printers.length}` }),
      stat('Printed this year', st.activity ? st.activity.year.toLocaleString('en') : '…'));
    errorEl.textContent = st.error ?? '';
    ctx.setAdminWaiting(st.waiting.length);
  }

  function paintActivity() {
    const today = new Date();
    const { weeks } = activityGrid(today);
    const counts = st.activity?.counts ?? new Map();
    const total = st.activity?.total ?? 0;
    const readout = st.activityError ? st.activityError
      : !st.activity ? 'Loading…'
        : st.hover ?? `${total.toLocaleString('en')} ${total === 1 ? 'message' : 'messages'} printed in the last year`;

    const labelRow = (text) => h('span', { class: 'heatmap__label' }, text);
    fill(activityEl, 
      h('div', { class: 'section-head' },
        h('h2', { id: 'activity-h', class: 'section-title' }, 'Activity'),
        h('span', { class: 'heatmap__readout', 'aria-live': 'polite' }, readout)),
      h('div', { class: 'panel' },
        h('div', { class: 'heatmap__scroll' },
          h('div', { class: 'heatmap', role: 'img', 'aria-label': `One dot per day for the last ${WEEKS} weeks; darker red means more messages printed. ${total} in total.` },
            h('div', { class: 'heatmap__col heatmap__days', 'aria-hidden': 'true' },
              ['', '', 'Mon', '', 'Wed', '', 'Fri', ''].map(labelRow)),
            weeks.map((week) => h('div', { class: 'heatmap__col', 'aria-hidden': 'true' },
              labelRow(week.month),
              week.days.map((day) => {
                if (day > today) return h('span', { class: 'heat heat--future' });
                const n = counts.get(dayKey(day)) ?? 0;
                const text = `${n === 0 ? 'No messages' : n === 1 ? '1 message' : `${n} messages`} printed on ${longDate(day)}`;
                return h('span', {
                  class: `heat heat--${heatLevel(n)}`,
                  title: text,
                  onMouseenter: () => {
                    st.hover = text;
                    activityEl.querySelector('.heatmap__readout').textContent = text;
                  },
                });
              }))))),
        h('div', { class: 'heatmap__foot' },
          h('span', null, `Messages printed per day, last ${WEEKS} weeks`),
          h('span', { class: 'heatmap__scale' }, 'Less', [0, 1, 2, 3, 4].map((lv) => h('span', { class: `heat heat--${lv}` })), 'More'))));
  }

  async function act(message, action) {
    st.busy.add(message.id);
    st.notice = null;
    paintQueue();
    paintLog();
    try {
      await api[action](message.id);
    } catch (err) {
      st.notice = err instanceof ApiError && err.status === 409
        ? `#${seqNo(message.sequence_number)} was already handled.`
        : `Couldn’t ${action} #${seqNo(message.sequence_number)}. ${err.message}`;
    }
    st.busy.delete(message.id);
    if (!destroyed) await loadCore();
  }

  /** The reasons the automated moderation recorded, one line each. */
  function flagList(flags) {
    if (!flags?.length) return null;
    return h('ul', { class: 'flags', 'aria-label': 'Moderation flags' }, flags.map((f) => h('li', { class: 'flag' },
      h('span', { class: 'flag__code' }, flagLabel(f.code)),
      f.detail && h('span', { class: 'flag__detail' }, f.detail))));
  }

  async function setModeration(enabled) {
    st.moderationBusy = true;
    st.moderationError = null;
    paintModeration();
    try {
      const res = await api.adminSetModeration(enabled);
      st.moderation = res.enabled;
    } catch (err) {
      st.moderationError = `Couldn’t turn moderation ${enabled ? 'on' : 'off'}. ${err.message}`;
    }
    st.moderationBusy = false;
    paintModeration();
    // Turning it off releases the messages still in review, so counts change.
    if (!destroyed) loadCore();
  }

  function paintModeration() {
    const on = st.moderation;
    moderationEl.classList.toggle('moderation--off', on === false);
    fill(moderationEl,
      h('div', { class: 'moderation__text' },
        h('span', { class: 'strong' }, on == null ? 'Automated moderation' : `Automated moderation is ${on ? 'on' : 'off'}`),
        h('span', { class: 'muted small' }, on == null
          ? (st.moderationError ? '' : 'Loading…')
          : on
            ? 'Web messages are checked first: anything flagged waits here, with the reasons.'
            : 'Web messages go straight to the printer, unchecked. It turns back on when the server restarts.')),
      on != null && h('div', { class: 'moderation__actions' },
        on
          ? confirmButton('Turn off', {
            question: 'Print web messages without checking them?',
            yes: 'Turn off',
            run: () => setModeration(false),
            onError: (err) => {
              st.moderationError = err.message;
              paintModeration();
            },
          })
          : h('button', { type: 'button', class: 'btn btn--solid btn--sm', disabled: st.moderationBusy, onClick: () => setModeration(true) }, 'Turn on')),
      st.moderationError && h('p', { class: 'form-error', role: 'alert' }, st.moderationError));
  }

  function paintQueue() {
    const subtitle = ['Oldest first · approved messages go straight to the printer'];
    if (st.reviewCount > 0) subtitle.push(`${st.reviewCount} in automated review`);
    fill(queueHeadEl,
      h('div', { class: 'section-head' },
        h('h2', { id: 'queue-h', class: 'section-title' }, 'Approvals'),
        h('span', { class: 'muted small' }, subtitle.join(' · '))));
    fill(queueBodyEl,
      st.notice && h('p', { class: 'notice', role: 'status' }, st.notice),
      st.waiting.length > 0
        ? h('div', { class: 'approvals' }, st.waiting.map((m) => {
          const busy = st.busy.has(m.id);
          return h('article', { class: 'approval' },
            h('div', { class: 'approval__main' },
              h('div', { class: 'approval__who' },
                h('span', { class: 'strong' }, m.sender_name, h('span', { class: 'muted normal' }, ' to '), toName(m.printer_id)),
                h('span', { class: 'mono small muted' }, [`#${seqNo(m.sequence_number)}`, timeAgo(m.created_at), m.sender_ip].filter(Boolean).join(' · '))),
              h('p', { class: 'approval__body' }, m.content),
              flagList(m.moderation_flags)),
            h('div', { class: 'approval__actions' },
              h('button', { type: 'button', class: 'btn btn--outline', disabled: busy, onClick: () => act(m, 'reject') }, 'Reject'),
              h('button', { type: 'button', class: 'btn btn--solid', disabled: busy, onClick: () => act(m, 'approve') }, 'Approve')));
        }))
        : h('div', { class: 'empty' },
          h('span', { class: 'empty__title' }, 'All clear.'),
          h('span', { class: 'muted' }, st.moderation === false
            ? 'Moderation is off: web messages go straight to the printer.'
            : 'Web messages the automated moderation flags will show up here.')));
  }

  /** Everything about one message, including why the moderation held or passed it. */
  function openMessagePane(m) {
    const moderatedText = m.source === 'admin'
      ? 'Sent by an admin: skipped moderation.'
      : m.moderated_by === 'auto'
        ? `Passed automatically${m.moderated_at ? `, ${receiptDate(m.moderated_at)}` : ''}.`
        : m.moderated_by === 'admin'
          ? `Decided by an admin${m.moderated_at ? `, ${receiptDate(m.moderated_at)}` : ''}.`
          : m.status === 'awaiting_review'
            ? 'The automated moderation hasn’t looked at it yet.'
            : m.status === 'awaiting_approval'
              ? 'Waiting for an admin.'
              : m.status === 'rejected'
                ? 'Rejected with no moderator recorded, usually because the sender was removed from the printer.'
                : m.moderated_at
                  ? `Moderated ${receiptDate(m.moderated_at)}, before moderation was recorded.`
                  : 'Not moderated.';

    openPane({
      title: `#${seqNo(m.sequence_number)}`,
      render(body) {
        const actions = m.status === 'awaiting_approval' && h('div', { class: 'tool__actions' },
          h('button', { type: 'button', class: 'btn btn--outline btn--sm', onClick: () => { closePane(); act(m, 'reject'); } }, 'Reject'),
          h('button', { type: 'button', class: 'btn btn--solid btn--sm', onClick: () => { closePane(); act(m, 'approve'); } }, 'Approve'));
        fill(body,
          paneSection('Message',
            h('p', { class: 'strong' }, m.sender_name, h('span', { class: 'muted normal' }, ' to '), toName(m.printer_id)),
            h('p', { class: 'approval__body' }, m.content),
            h('p', { class: 'mono small muted' }, [receiptDate(m.created_at), m.sender_ip].filter(Boolean).join(' · ')),
            h('span', { class: 'status-cell' }, shape((STATUS[m.status] ?? STATUS.awaiting_approval).shape), STATUS[m.status]?.label ?? m.status)),
          paneSection('Moderation',
            h('p', { class: 'tool__lede' }, moderatedText),
            flagList(m.moderation_flags) ?? h('p', { class: 'muted small' }, 'No flags.'),
            actions),
          m.last_error && paneSection('Printing',
            h('p', { class: 'small' }, `Last error: ${m.last_error}`),
            h('p', { class: 'muted small' }, `${m.attempts} ${m.attempts === 1 ? 'attempt' : 'attempts'}`)));
      },
    });
  }

  function paintPrinters() {
    fill(printersEl, 
      h('h2', { id: 'printers-h', class: 'section-title' }, 'Printers'),
      st.printers.length === 0
        ? h('div', { class: 'empty' }, h('span', { class: 'empty__title' }, 'No printers yet.'), h('span', { class: 'muted' }, 'Add one with POST /api/v1/admin/printers.'))
        : h('div', { class: 'printer-grid' }, st.printers.map((p) => {
          const state = printerState(p);
          return h('article', { class: `printer-card printer-card--${state.kind}` },
            h('div', { class: 'printer-card__head' },
              h('h3', { class: 'printer-card__name' }, p.recipient_name),
              shape(state.kind, state.label)),
            h('div', { class: 'printer-card__info' },
              h('span', { class: 'strong' }, `${state.label} · ${p.last_heartbeat_at ? `seen ${timeAgo(p.last_heartbeat_at)}` : 'never seen'}`),
              h('span', null, p.model),
              h('span', null, quietHoursText(p)),
              h('span', null, ownerOf(p.id) ? `Owner: ${ownerOf(p.id).name}` : 'No owner'),
              p.error && h('span', { class: 'printer-card__error' }, `ERROR: ${p.error.toUpperCase()}`),
              h('button', { type: 'button', class: 'btn btn--outline btn--sm printer-card__manage', 'aria-haspopup': 'dialog', onClick: () => openPrinterPane(p) }, 'Manage')));
        })));
  }

  function paintLog() {
    const statusCell = (m) => {
      const meta = STATUS[m.status] ?? { shape: 'awaiting', label: m.status };
      return h('span', { class: 'status-cell' }, shape(meta.shape), meta.label);
    };
    const when = (m) => {
      let text = timeAgo(m.last_updated_at);
      if (m.status === 'failed') text += ` · ${m.attempts} ${m.attempts === 1 ? 'attempt' : 'attempts'}`;
      return text;
    };

    fill(logEl, 
      h('div', { class: 'section-head' },
        h('h2', { id: 'log-h', class: 'section-title' }, 'All messages'),
        h('div', { class: 'chips', role: 'group', 'aria-label': 'Filter by status' },
          FILTERS.map(([id, label]) => h('button', {
            type: 'button',
            class: 'chip',
            'aria-pressed': st.filter === id ? 'true' : 'false',
            onClick: () => {
              st.filter = id;
              st.page = 0;
              loadRows();
            },
          }, label)))),
      h('div', { class: 'table-wrap' },
        h('table', { class: 'log' },
          h('thead', null, h('tr', null,
            h('th', { scope: 'col' }, 'No.'),
            h('th', { scope: 'col' }, 'From → To'),
            h('th', { scope: 'col' }, 'Message'),
            h('th', { scope: 'col' }, 'Status'),
            h('th', { scope: 'col' }, 'Updated'),
            h('th', { scope: 'col' }, h('span', { class: 'visually-hidden' }, 'Actions')))),
          h('tbody', null,
            st.rows.length === 0 && h('tr', null, h('td', { colspan: 6, class: 'log__empty' }, st.rowsLoading ? 'Loading…' : 'No messages here.')),
            st.rows.map((m) => h('tr', null,
              h('td', { class: 'mono muted' }, `#${seqNo(m.sequence_number)}`),
              h('td', { class: 'nowrap strong' }, m.sender_name, h('span', { class: 'muted normal' }, ' → '), toName(m.printer_id),
                m.source === 'admin' && h('span', { class: 'tag', title: 'Sent as admin: skipped moderation' }, 'Admin')),
              h('td', { class: 'log__content', title: m.content }, m.content),
              h('td', null, statusCell(m)),
              h('td', { class: 'nowrap muted', title: m.last_error ? `Last error: ${m.last_error}` : null }, when(m)),
              h('td', { class: 'log__actions' },
                m.status === 'failed' && h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: st.busy.has(m.id), onClick: () => act(m, 'requeue') }, 'Requeue'),
                h('button', {
                  type: 'button',
                  class: `btn btn--outline btn--sm${m.moderation_flags?.length ? ' btn--flagged' : ''}`,
                  'aria-haspopup': 'dialog',
                  title: m.moderation_flags?.length ? m.moderation_flags.map((f) => flagLabel(f.code)).join(', ') : null,
                  onClick: () => openMessagePane(m),
                }, m.moderation_flags?.length ? `${m.moderation_flags.length} ${m.moderation_flags.length === 1 ? 'flag' : 'flags'}` : 'Details'))))))),
      (st.page > 0 || st.rowsMore) && h('div', { class: 'pager' },
        h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: st.page === 0 || st.rowsLoading, onClick: () => { st.page -= 1; loadRows(); } }, '← Newer'),
        h('span', { class: 'muted small' }, `Page ${st.page + 1}`),
        h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: !st.rowsMore || st.rowsLoading, onClick: () => { st.page += 1; loadRows(); } }, 'Older →')));
  }

  // -- Users and ownership (management opens in panes) --

  const ownerOf = (pid) => st.users.find((u) => u.printer_accesses.some((a) => a.printer_id === pid && a.role === 'owner'));
  const peopleOn = (pid) => st.users.filter((u) => u.printer_accesses.some((a) => a.printer_id === pid));
  const ownedBy = (u) => u.printer_accesses.find((a) => a.role === 'owner');
  const isMe = (u) => u.id === ctx.me?.id;

  function accessesText(u) {
    if (u.printer_accesses.length === 0) return [h('span', { class: 'muted' }, 'No printer')];
    return u.printer_accesses.map((a, i) => [
      i > 0 && ', ',
      toName(a.printer_id),
      a.role === 'owner' && h('span', { class: 'tag tag--accent' }, 'Owner'),
    ]);
  }

  function paintUsers() {
    fill(usersEl,
      h('div', { class: 'section-head' },
        h('div', { class: 'page-head__title' },
          h('h2', { id: 'users-h', class: 'section-title' }, 'Users'),
          h('span', { class: 'muted small' }, `${st.users.length} · most join with a link from a printer’s owner`)),
        h('button', { type: 'button', class: 'btn btn--outline btn--sm', 'aria-haspopup': 'dialog', onClick: openNewUserPane }, 'New user')),
      st.users.length === 0
        ? h('div', { class: 'empty' }, h('span', { class: 'empty__title' }, 'No users yet.'), h('span', { class: 'muted' }, 'Hand out a join link from a printer’s Manage pane.'))
        : h('div', { class: 'table-wrap' },
          h('table', { class: 'log log--users' },
            h('thead', null, h('tr', null,
              h('th', { scope: 'col' }, 'Name'),
              h('th', { scope: 'col' }, 'Printers'),
              h('th', { scope: 'col' }, 'Added'),
              h('th', { scope: 'col' }, h('span', { class: 'visually-hidden' }, 'Actions')))),
            h('tbody', null, st.users.map((u) => h('tr', null,
              h('td', { class: 'strong nowrap' }, u.name,
                u.is_admin && h('span', { class: 'tag' }, 'Admin'),
                isMe(u) && h('span', { class: 'muted normal' }, ' (you)')),
              h('td', null, accessesText(u)),
              h('td', { class: 'nowrap muted' }, shortDate(u.created_at)),
              h('td', { class: 'log__actions' },
                h('button', { type: 'button', class: 'btn btn--outline btn--sm', 'aria-haspopup': 'dialog', onClick: () => openUserPane(u) }, 'Manage'))))))));
  }

  function paneSection(title, ...children) {
    return h('section', { class: 'pane-section' }, h('h3', { class: 'pane-section__title' }, title), ...children);
  }

  /** One printer: who owns it, who's on it, quiet hours and join links. */
  function openPrinterPane(printer) {
    const ownerEl = h('div');
    let ownerError = '';

    function paintOwner() {
      const owner = ownerOf(printer.id);
      const people = peopleOn(printer.id);
      const select = h('select', { id: 'owner-select', class: 'input' },
        h('option', { value: '' }, 'Choose someone…'),
        st.users
          .filter((u) => u.id !== owner?.id)
          .map((u) => {
            const owns = ownedBy(u);
            return h('option', { value: u.id, disabled: Boolean(owns) }, owns ? `${u.name} (owns ${toName(owns.printer_id)})` : u.name);
          }));
      const keep = h('input', { type: 'checkbox', id: 'keep-member', checked: true });

      async function transfer(event) {
        event.preventDefault();
        if (!select.value) return;
        ownerError = '';
        try {
          await api.adminSetOwner(printer.id, select.value);
          await loadCore();
        } catch (err) {
          ownerError = err.message;
        }
        paintOwner();
      }

      fill(ownerEl, paneSection('Owner',
        h('p', { class: 'tool__lede' }, owner
          ? `${owner.name} owns this printer: they set its quiet hours, hand out join links and remove people.`
          : 'Nobody owns this printer. An owner sets its quiet hours, hands out join links and removes people.'),
        h('form', { class: 'tool__form', onSubmit: transfer },
          h('div', { class: 'field' },
            h('label', { class: 'field__label', for: 'owner-select' }, owner ? 'Hand it to' : 'Make owner'),
            select,
            h('span', { class: 'field__hint' }, owner ? `${owner.name} stays on as a member. ` : '', 'Someone not on this printer is added to it.')),
          h('div', { class: 'tool__actions' },
            h('button', { type: 'submit', class: 'btn btn--solid btn--sm' }, owner ? 'Transfer' : 'Make owner'))),
        owner && h('div', { class: 'tool__actions' },
          h('label', { class: 'check' }, keep, `Keep ${owner.name} as a member`),
          confirmButton('Remove owner', {
            question: 'Remove the owner?',
            run: async () => {
              await api.adminRemoveOwner(printer.id, keep.checked);
              await loadCore();
              paintOwner();
            },
            onError: (err) => {
              ownerError = err.message;
              paintOwner();
            },
          })),
        h('p', { class: 'form-error', role: 'alert' }, ownerError),
        h('p', { class: 'muted small' }, people.length
          ? `On this printer: ${people.map((u) => u.name).join(', ')}.`
          : 'Nobody is on this printer yet.')));
    }

    openPane({
      title: printer.recipient_name,
      render(body) {
        fill(body,
          ownerEl,
          paneSection('Quiet hours', quietHoursForm(printer, {
            save: (b) => api.adminSetQuietHours(printer.id, b),
            clear: () => api.adminClearQuietHours(printer.id),
            onSaved: () => loadCore(),
          })),
          paneSection('Join links', joinLinksList({
            list: () => api.adminJoinTokens(printer.id),
            create: () => api.adminCreateJoinToken(printer.id),
            revoke: (tokenId) => api.adminRevokeJoinToken(printer.id, tokenId),
          })));
        paintOwner();
      },
    });
  }

  /** Checkboxes, one per printer, each with a role. Returns { el, value() }. */
  function accessPicker(accesses, { roles = true } = {}) {
    const chosen = new Map(accesses.map((a) => [a.printer_id, a.role]));
    const el = h('ul', { class: 'row-list' }, st.printers.map((p) => {
      const box = h('input', { type: 'checkbox', id: `acc-${p.id}`, checked: chosen.has(p.id) });
      const role = roles && h('select', { class: 'input input--sm', 'aria-label': `Role on ${p.recipient_name}`, disabled: !chosen.has(p.id) },
        h('option', { value: 'member' }, 'Member'),
        h('option', { value: 'owner' }, 'Owner'));
      if (role) role.value = chosen.get(p.id) ?? 'member';
      box.addEventListener('change', () => {
        if (box.checked) chosen.set(p.id, role ? role.value : 'member');
        else chosen.delete(p.id);
        if (role) role.disabled = !box.checked;
      }); // ...then the change bubbles up to listeners on the list
      if (role) role.addEventListener('change', () => chosen.set(p.id, role.value));
      return h('li', { class: 'row-list__item' },
        h('label', { class: 'check', for: box.id }, box, p.recipient_name),
        role);
    }));
    return { el, value: () => [...chosen].map(([printer_id, role]) => ({ printer_id, role })) };
  }

  /** One user: their printers and roles, their devices, and a recovery invite. */
  function openUserPane(user) {
    let u = user;
    const accessEl = h('div');
    const devicesEl = h('div');
    const recoveryEl = h('div');
    const dev = { sessions: null, error: '' };
    const rec = { invite: null, error: '' };

    function paintAccess(error = '', saved = '') {
      const picker = accessPicker(u.printer_accesses);
      const warn = h('p', { class: 'notice', hidden: true }, isMe(u)
        ? 'With no printer, you’re signed out everywhere, this device included, and lose admin access until someone gives you a printer and a recovery invite.'
        : `With no printer, ${u.name} is signed out everywhere and their invite stops working. Their history is kept.`);
      picker.el.addEventListener('change', () => {
        warn.hidden = picker.value().length > 0;
      });
      const submit = h('button', { type: 'submit', class: 'btn btn--solid btn--sm' }, 'Save');

      async function onSubmit(event) {
        event.preventDefault();
        submit.disabled = true;
        try {
          u = await api.adminSetUserPrinters(u.id, picker.value());
          // Own printers changed: Write and Settings show them, so reload /me.
          if (isMe(u)) await ctx.refreshMe();
          await loadCore();
          paintAccess('', 'Saved.');
          loadSessions();
        } catch (err) {
          paintAccess(err.message);
        }
      }

      fill(accessEl, paneSection('Printers',
        h('form', { class: 'tool__form', onSubmit },
          h('p', { class: 'tool__lede' }, 'Removing a printer drops their queued messages to it. A printer has one owner, and someone owns at most one printer.'),
          picker.el,
          warn,
          h('div', { class: 'tool__actions' }, submit, saved && h('span', { class: 'muted small', role: 'status' }, saved)),
          h('p', { class: 'form-error', role: 'alert' }, error))));
    }

    async function loadSessions() {
      try {
        dev.sessions = await api.adminUserSessions(u.id);
        dev.error = '';
      } catch (err) {
        dev.error = err.message;
      }
      paintDevices();
    }

    function paintDevices() {
      const active = (dev.sessions ?? []).filter((s) => !s.revoked_at);
      const onError = (err) => {
        dev.error = err.message;
        paintDevices();
      };
      fill(devicesEl, paneSection('Devices',
        dev.sessions == null
          ? !dev.error && h('p', { class: 'muted small' }, 'Loading…')
          : active.length === 0
            ? h('p', { class: 'muted small' }, 'Not signed in anywhere.')
            : h('ul', { class: 'row-list' }, active.map((s) => h('li', { class: 'row-list__item' },
              h('div', { class: 'row-list__main' },
                h('span', { class: 'strong' }, s.device_name),
                h('span', { class: 'row-list__meta' }, `Added ${shortDate(s.created_at)} · used ${timeAgo(s.last_used_at)}`)),
              confirmButton('Revoke', {
                question: 'Sign it out?',
                run: async () => {
                  await api.adminRevokeSession(u.id, s.id);
                  await loadSessions();
                },
                onError,
              })))),
        active.length > 1 && h('div', { class: 'tool__actions' },
          confirmButton('Sign out everywhere', {
            question: isMe(u) ? 'Sign yourself out of every device, this one included?' : `Sign ${u.name} out of every device?`,
            yes: 'Sign out all',
            run: async () => {
              await api.adminRevokeAllSessions(u.id);
              await loadSessions();
            },
            onError,
          })),
        h('p', { class: 'form-error', role: 'alert' }, dev.error)));
    }

    function paintRecovery() {
      const revoke = h('input', { type: 'checkbox', id: 'rec-revoke', checked: true });
      const submit = h('button', { type: 'button', class: 'btn btn--outline btn--sm' }, rec.invite ? 'New invite' : 'Create invite');
      submit.addEventListener('click', async () => {
        submit.disabled = true;
        rec.error = '';
        try {
          rec.invite = await api.adminRecoveryInvite(u.id, revoke.checked);
          if (revoke.checked) loadSessions();
        } catch (err) {
          rec.error = err instanceof ApiError && err.status === 409
            ? `${u.name} isn’t on any printer. Give them one first.`
            : err.message;
        }
        paintRecovery();
      });

      fill(recoveryEl, paneSection('Recovery',
        h('p', { class: 'tool__lede' }, `For when ${u.name} has lost every device. The invite signs one device in as them and lasts 2 days. A new one replaces the last.`),
        rec.invite && copyField(joinLink(rec.invite.invite), { label: 'Recovery invite', expiresAt: rec.invite.expires_at, note: `Send it to ${u.name} only. It’s shown only now.` }),
        h('div', { class: 'tool__actions' },
          h('label', { class: 'check' }, revoke, 'Also sign out their devices'),
          submit),
        h('p', { class: 'form-error', role: 'alert' }, rec.error)));
    }

    openPane({
      title: u.name,
      render(body) {
        fill(body, accessEl, devicesEl, recoveryEl);
        paintAccess();
        paintDevices();
        paintRecovery();
        loadSessions();
      },
    });
  }

  /** The admin's own way in: name an account, pick its printers, hand over the invite. */
  function openNewUserPane() {
    openPane({
      title: 'New user',
      render(body) {
        const name = h('input', { id: 'new-user-name', class: 'input', type: 'text', maxlength: 20, required: true, autocomplete: 'off' });
        const picker = accessPicker([], { roles: false });
        const error = h('p', { class: 'form-error', role: 'alert' });
        const submit = h('button', { type: 'submit', class: 'btn btn--solid btn--sm' }, 'Create');

        async function onSubmit(event) {
          event.preventDefault();
          const username = name.value.trim();
          if (!username) return;
          submit.disabled = true;
          error.textContent = '';
          try {
            const created = await api.adminCreateUser(username, picker.value().map((a) => a.printer_id));
            await loadCore();
            fill(body,
              paneSection(`${created.name} is ready`,
                copyField(joinLink(created.invite), { label: 'Invite', note: `Send it to ${created.name}. It works once, for 2 days, and is shown only now.` })));
          } catch (err) {
            error.textContent = err.message;
            submit.disabled = false;
          }
        }

        fill(body, h('form', { class: 'tool__form', onSubmit },
          h('p', { class: 'tool__lede' }, 'Usually a printer’s owner sends a join link instead, and people pick their own name. Use this when someone can’t.'),
          h('div', { class: 'field' },
            h('label', { class: 'field__label', for: 'new-user-name' }, 'Name'),
            name,
            h('span', { class: 'field__hint' }, 'Printed on their messages. Must be unique on each of their printers.')),
          h('div', { class: 'field' },
            h('span', { class: 'field__label' }, 'Printers'),
            picker.el),
          h('div', { class: 'tool__actions' }, submit),
          error));
        name.focus();
      },
    });
  }

  // -- Loading --

  async function loadRows() {
    st.rowsLoading = true;
    paintLog();
    const status = FILTERS.find(([id]) => id === st.filter)[2];
    try {
      const rows = await api.adminMessages({ status, limit: PAGE_SIZE, offset: st.page * PAGE_SIZE });
      if (destroyed) return;
      st.rows = rows;
      st.rowsMore = rows.length === PAGE_SIZE;
    } catch (err) {
      st.error = `Couldn’t load messages. ${err.message}`;
    }
    st.rowsLoading = false;
    if (!destroyed) {
      paintLog();
      paintStats();
    }
  }

  async function loadCore() {
    try {
      const [printers, users, waiting, review, queue] = await Promise.all([
        fetchAll(api.adminPrinters),
        fetchAll(api.adminUsers),
        fetchAll(api.adminMessages, { status: 'awaiting_approval', order: 'asc' }),
        fetchAll(api.adminMessages, { status: 'awaiting_review' }),
        fetchAll(api.adminMessages, { status: 'pending,claimed,processed' }),
      ]);
      if (destroyed) return;
      st.printers = printers;
      st.users = users;
      st.waiting = waiting;
      st.reviewCount = review.length;
      st.queueCount = queue.length;
      st.error = null;
    } catch (err) {
      if (destroyed) return;
      st.error = `Couldn’t refresh. ${err.message}`;
    }
    await loadModeration();
    if (destroyed) return;
    paintStats();
    paintQueue();
    paintPrinters();
    paintUsers();
    await loadRows();
  }

  /** Repaints the switch only when its state changes, so an open confirmation survives refreshes. */
  async function loadModeration() {
    if (st.moderationBusy) return;
    try {
      const { enabled } = await api.adminModeration();
      if (destroyed) return;
      const changed = enabled !== st.moderation || st.moderationError;
      st.moderation = enabled;
      st.moderationError = null;
      if (changed) paintModeration();
    } catch (err) {
      if (destroyed) return;
      st.moderationError = `Couldn’t load the moderation setting. ${err.message}`;
      paintModeration();
    }
  }

  /**
   * There is no stats endpoint per day yet, so walk the printed messages,
   * newest first, back to the start of the grid (or January 1st, if earlier).
   */
  async function loadActivity() {
    const today = new Date();
    const { start } = activityGrid(today);
    const yearStart = new Date(today.getFullYear(), 0, 1);
    const cutoff = start < yearStart ? start : yearStart;
    try {
      const printed = await fetchAll(api.adminMessages, { status: 'printed', order: 'desc' }, {
        until: (m) => new Date(m.created_at) < cutoff,
      });
      if (destroyed) return;
      const counts = new Map();
      let total = 0;
      let year = 0;
      for (const m of printed) {
        const at = new Date(m.printed_at ?? m.created_at);
        if (at.getFullYear() === today.getFullYear()) year += 1;
        if (at < start) continue;
        const key = dayKey(at);
        counts.set(key, (counts.get(key) ?? 0) + 1);
        total += 1;
      }
      st.activity = { counts, total, year };
      st.activityError = null;
    } catch (err) {
      if (destroyed) return;
      st.activityError = `Couldn’t load activity. ${err.message}`;
    }
    paintActivity();
    paintStats();
  }

  function schedule() {
    const t = setTimeout(() => {
      timers.delete(t);
      if (destroyed) return;
      whenVisible(async () => {
        if (destroyed) return;
        if (st.busy.size === 0) await loadCore();
        if (!destroyed) schedule();
      });
    }, REFRESH_MS);
    timers.add(t);
  }

  paintStats();
  paintActivity();
  paintModeration();
  paintQueue();
  paintPrinters();
  paintUsers();
  paintLog();
  loadCore().then(() => {
    if (!destroyed) schedule();
  });
  loadActivity();

  return {
    el,
    title: 'Admin',
    destroy() {
      destroyed = true;
      timers.forEach(clearTimeout);
    },
  };
}
