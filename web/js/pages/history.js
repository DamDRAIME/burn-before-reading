// History: the signed-in user's own messages, as small receipts.

import { fill, h, rings, shape } from '../dom.js';
import { api, PAGE_SIZE } from '../api.js';
import { clock, printerState, seqNo, senderStage, shortDate } from '../format.js';

const REFRESH_MS = 20000;

const CARD = {
  printed: { shape: 'printed', label: 'Printed' },
  queued: { shape: 'queued', label: 'In queue' },
  printing: { shape: 'queued', label: 'In queue' },
  awaiting: { shape: 'awaiting', label: 'Awaiting approval' },
  rejected: { shape: 'rejected', label: 'Not printed' },
  failed: { shape: 'rejected', label: 'Not printed' },
};

export function historyPage() {
  const st = {
    printers: new Map(),
    printerList: [],
    items: [],
    more: false,
    loading: true,
    error: null,
    filter: 'all',
  };
  const timers = new Set();
  let destroyed = false;

  const el = h('main', { class: 'page history' });

  function detail(m, stage) {
    const p = st.printers.get(m.printer_id);
    switch (stage) {
      case 'printed':
        return m.printed_at ? clock(m.printed_at) : '';
      case 'queued':
        return p && printerState(p).kind === 'asleep' ? `PRINTS ${p.quiet_end}` : 'IN LINE';
      case 'printing':
        return 'PRINTING';
      case 'rejected':
        return 'DECLINED';
      case 'failed':
        return 'FAILED';
      default:
        return clock(m.created_at);
    }
  }

  function card(m) {
    const stage = senderStage(m.status);
    const meta = CARD[stage] ?? CARD.awaiting;
    const to = st.printers.get(m.printer_id)?.recipient_name ?? 'someone';
    return h('li', { class: 'note-card' },
      h('article', { class: 'paper note-card__paper' },
        h('div', { class: 'note-card__head' },
          h('div', { class: 'note-card__title' },
            h('span', { class: 'note-card__meta' }, `No.${seqNo(m.sequence_number)} · ${shortDate(m.created_at)}`),
            h('h2', { class: 'note-card__to' }, `To ${to}`)),
          shape(meta.shape, meta.label)),
        h('hr', { class: 'rule' }),
        h('p', { class: `note-card__body${stage === 'rejected' || stage === 'failed' ? ' is-faded' : ''}` }, m.content),
        h('hr', { class: 'rule' }),
        h('div', { class: 'note-card__foot' },
          h('span', { class: 'strong' }, meta.label.toUpperCase()),
          h('span', { class: 'muted' }, detail(m, stage)))));
  }

  function paint() {
    const tabs = [['all', 'Everyone'], ...st.printerList.map((p) => [p.id, p.recipient_name])];
    const visible = st.items.filter((m) => st.filter === 'all' || m.printer_id === st.filter);
    const total = `${String(st.items.length).padStart(2, '0')}${st.more ? '+' : ''}`;

    fill(el, 
      h('div', { class: 'page-head' },
        h('div', { class: 'page-head__title' },
          h('h1', { class: 'display' }, 'History'),
          h('span', { class: 'page-head__count' }, `/${total}`),
          h('span', { class: 'emblem', 'aria-hidden': 'true' },
            rings([48, 41, 34, 27, 20, 13]),
            h('span', { class: 'emblem__dot emblem__dot--accent' }))),
        h('ul', { class: 'legend', 'aria-label': 'Legend' },
          h('li', null, shape('printed'), 'Printed'),
          h('li', null, shape('queued'), 'In queue'),
          h('li', null, shape('awaiting'), 'Awaiting approval'),
          h('li', null, shape('rejected'), 'Not printed'))),
      tabs.length > 2 && h('div', { class: 'chips chips--rule', role: 'group', 'aria-label': 'Filter by recipient' },
        tabs.map(([id, label]) => h('button', {
          type: 'button',
          class: 'chip',
          'aria-pressed': st.filter === id ? 'true' : 'false',
          onClick: () => {
            st.filter = id;
            paint();
          },
        }, label))),
      st.error && h('p', { class: 'form-error', role: 'alert' }, st.error),
      visible.length > 0
        ? h('ol', { class: 'note-grid' }, visible.map(card))
        : !st.loading && h('div', { class: 'empty' },
          h('span', { class: 'empty__title' }, st.items.length ? 'Nothing for this person yet.' : 'Nothing sent yet.'),
          h('span', { class: 'muted' }, h('a', { href: '/', 'data-link': true }, 'Write a note'), ' and it’ll show up here.')),
      (st.more || st.loading) && h('div', { class: 'load-more' },
        h('button', { type: 'button', class: 'btn btn--ghost', disabled: st.loading, onClick: loadMore }, st.loading ? 'Loading…' : 'Show older')));
  }

  function merge(items, { prepend }) {
    const byId = new Map(st.items.map((m) => [m.id, m]));
    const fresh = [];
    for (const m of items) {
      if (byId.has(m.id)) byId.set(m.id, m);
      else fresh.push(m);
    }
    const updated = st.items.map((m) => byId.get(m.id));
    st.items = prepend ? [...fresh, ...updated] : [...updated, ...fresh];
    st.items.sort((a, b) => b.sequence_number - a.sequence_number);
  }

  async function loadMore() {
    st.loading = true;
    paint();
    try {
      const page = await api.userMessages({ limit: PAGE_SIZE, offset: st.items.length });
      merge(page, { prepend: false });
      st.more = page.length === PAGE_SIZE;
      st.error = null;
    } catch (err) {
      st.error = `Couldn’t load your messages. ${err.message}`;
    }
    st.loading = false;
    if (!destroyed) paint();
  }

  async function refresh() {
    try {
      const page = await api.userMessages({ limit: PAGE_SIZE, offset: 0 });
      if (destroyed) return;
      merge(page, { prepend: true });
      paint();
    } catch {
      // Try again on the next tick.
    }
    schedule();
  }

  function schedule() {
    const t = setTimeout(() => {
      timers.delete(t);
      if (!destroyed) refresh();
    }, REFRESH_MS);
    timers.add(t);
  }

  async function start() {
    try {
      st.printerList = await api.userPrinters();
      st.printers = new Map(st.printerList.map((p) => [p.id, p]));
    } catch {
      // Recipients show as "someone"; the list still loads.
    }
    if (destroyed) return;
    await loadMore();
    schedule();
  }

  paint();
  start();

  return {
    el,
    title: 'History',
    destroy() {
      destroyed = true;
      timers.forEach(clearTimeout);
    },
  };
}
