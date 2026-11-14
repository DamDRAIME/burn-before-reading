// Admin console: numbers, a year of printing, the approval queue, printers and the full log.

import { fill, h, rings, shape } from '../dom.js';
import { api, ApiError, fetchAll, PAGE_SIZE } from '../api.js';
import { auth } from '../auth.js';
import { dayKey, longDate, MONTHS_SHORT, printerState, quietHoursText, seqNo, STATUS, timeAgo } from '../format.js';

const REFRESH_MS = 20000;
const WEEKS = 52;

const FILTERS = [
  ['all', 'All', null],
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
    waiting: [],
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
  const queueEl = h('section', { id: 'queue', class: 'admin-section', 'aria-labelledby': 'queue-h' });
  const printersEl = h('section', { id: 'printers', class: 'admin-section', 'aria-labelledby': 'printers-h' });
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
          h('a', { href: '#log' }, 'All messages'),
          h('button', {
            type: 'button',
            class: 'link-button',
            onClick: () => {
              auth.setAdminToken(null);
              ctx.navigate('/');
              ctx.signedIn();
            },
          }, 'Sign out'))),
      errorEl,
      statsEl),
    activityEl,
    queueEl,
    printersEl,
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

  function paintQueue() {
    fill(queueEl, 
      h('div', { class: 'section-head' },
        h('h2', { id: 'queue-h', class: 'section-title' }, 'Approvals'),
        h('span', { class: 'muted small' }, 'Oldest first · approved messages go straight to the printer')),
      st.notice && h('p', { class: 'notice', role: 'status' }, st.notice),
      st.waiting.length > 0
        ? h('div', { class: 'approvals' }, st.waiting.map((m) => {
          const busy = st.busy.has(m.id);
          return h('article', { class: 'approval' },
            h('div', { class: 'approval__main' },
              h('div', { class: 'approval__who' },
                h('span', { class: 'strong' }, m.sender_name ?? 'Admin', h('span', { class: 'muted normal' }, ' to '), toName(m.printer_id)),
                h('span', { class: 'mono small muted' }, [`#${seqNo(m.sequence_number)}`, timeAgo(m.created_at), m.sender_ip].filter(Boolean).join(' · '))),
              h('p', { class: 'approval__body' }, m.content)),
            h('div', { class: 'approval__actions' },
              h('button', { type: 'button', class: 'btn btn--outline', disabled: busy, onClick: () => act(m, 'reject') }, 'Reject'),
              h('button', { type: 'button', class: 'btn btn--solid', disabled: busy, onClick: () => act(m, 'approve') }, 'Approve')));
        }))
        : h('div', { class: 'empty' },
          h('span', { class: 'empty__title' }, 'All clear.'),
          h('span', { class: 'muted' }, 'New web messages will show up here.')));
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
              p.error && h('span', { class: 'printer-card__error' }, `ERROR: ${p.error.toUpperCase()}`)));
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
              h('td', { class: 'nowrap strong' }, m.sender_name ?? 'Admin', h('span', { class: 'muted normal' }, ' → '), toName(m.printer_id)),
              h('td', { class: 'log__content', title: m.content }, m.content),
              h('td', null, statusCell(m)),
              h('td', { class: 'nowrap muted', title: m.last_error ? `Last error: ${m.last_error}` : null }, when(m)),
              h('td', { class: 'log__actions' },
                m.status === 'failed' && h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: st.busy.has(m.id), onClick: () => act(m, 'requeue') }, 'Requeue'))))))),
      (st.page > 0 || st.rowsMore) && h('div', { class: 'pager' },
        h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: st.page === 0 || st.rowsLoading, onClick: () => { st.page -= 1; loadRows(); } }, '← Newer'),
        h('span', { class: 'muted small' }, `Page ${st.page + 1}`),
        h('button', { type: 'button', class: 'btn btn--outline btn--sm', disabled: !st.rowsMore || st.rowsLoading, onClick: () => { st.page += 1; loadRows(); } }, 'Older →')));
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
      const [printers, waiting, queue] = await Promise.all([
        fetchAll(api.adminPrinters),
        fetchAll(api.adminMessages, { status: 'awaiting_approval', order: 'asc' }),
        fetchAll(api.adminMessages, { status: 'pending,claimed,processed' }),
      ]);
      if (destroyed) return;
      st.printers = printers;
      st.waiting = waiting;
      st.queueCount = queue.length;
      st.error = null;
    } catch (err) {
      if (destroyed) return;
      st.error = `Couldn’t refresh. ${err.message}`;
    }
    paintStats();
    paintQueue();
    paintPrinters();
    await loadRows();
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
    const t = setTimeout(async () => {
      timers.delete(t);
      if (destroyed) return;
      if (st.busy.size === 0) await loadCore();
      if (!destroyed) schedule();
    }, REFRESH_MS);
    timers.add(t);
  }

  paintStats();
  paintActivity();
  paintQueue();
  paintPrinters();
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
