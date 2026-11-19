// Write: the receipt is the composer. After sending, the circle behind it and the
// four-step bar below it follow the message until it's printed.

import { fill, h, rings } from '../dom.js';
import { api, fetchAll } from '../api.js';
import { readPref, writePref } from '../auth.js';
import { printerState, receiptDate, seqNo, senderStage, TERMINAL_STATUSES } from '../format.js';
import { OFFLINE_AFTER, whenVisible } from '../visibility.js';

const MAX_LENGTH = 500;
const POLL_MS = 3000;
const PRINTERS_REFRESH_MS = 30000;

const STAMPS = {
  draft: 'DRAFT',
  awaiting: 'AWAITING APPROVAL',
  queued: 'IN QUEUE',
  printing: 'PRINTING',
  printed: 'PRINTED',
  rejected: 'NOT PRINTED',
  failed: 'NOT PRINTED',
};

// A quiet nudge, written vertically through the rings. Short enough to fit one line.
const IDEAS = [
  'Say you love them',
  'Tell them a joke',
  'Tell them how your day went',
  'Share a memory together',
  'Wish them sweet dreams',
  'Ask what they’re reading',
  'Thank them for something small',
  'Leave a riddle for tomorrow',
  'Draw something in letters',
  'Recommend a song',
  'Tell them what you ate today',
  'Write a two-line poem',
  'Say good morning, early',
  'Plan your next meal together',
  'Tell them what they did well',
  'Send a tiny to-do list',
  'Describe the view from here',
  'Ask a would-you-rather',
];

/** A random idea, never the same as the last one shown (on this device). */
function pickIdea() {
  const last = Number(readPref('idea'));
  let i = Math.floor(Math.random() * IDEAS.length);
  if (i === last) i = (i + 1) % IDEAS.length;
  writePref('idea', String(i));
  return IDEAS[i];
}

export function writePage(ctx) {
  const asAdmin = ctx.isAdmin;
  const senderName = asAdmin ? 'Admin' : ctx.me.name;

  const st = {
    printers: null,
    printersError: null,
    to: readPref('recipient'),
    sending: false,
    sendError: null,
    message: null, // the sent message, while tracking it
    sentTo: null,
    pollFailures: 0, // status checks failed in a row
    idea: pickIdea(),
  };
  const timers = new Set();
  let destroyed = false;

  function later(fn, ms) {
    const t = setTimeout(() => {
      timers.delete(t);
      if (!destroyed) whenVisible(() => !destroyed && fn());
    }, ms);
    timers.add(t);
  }

  // -- Elements kept across repaints, so typing never loses focus --

  const textarea = h('textarea', {
    class: 'receipt__write',
    rows: 9,
    maxlength: MAX_LENGTH,
    'aria-label': 'Message',
    placeholder: 'Start writing…',
    // The draft survives a reload or a trip to another page (on this device).
    value: readPref('draft') ?? '',
    onInput: () => {
      writePref('draft', textarea.value);
      updateCount();
      updateSendButton();
    },
    onKeydown: (event) => {
      if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
        event.preventDefault();
        send();
      }
    },
  });
  const countEl = h('span', { class: 'receipt__count' });
  const dateEl = h('span');
  let sendButton = null;

  const left = h('section', { class: 'write__intro' });
  const right = h('section', { class: 'write__stage', 'aria-label': 'Receipt' });
  const el = h('main', { class: 'page write' }, left, right);

  // -- Derived values --

  function printers() {
    return st.printers ?? [];
  }

  function selected() {
    const list = printers();
    return list.find((p) => p.id === st.to) ?? list[0] ?? null;
  }

  function stage() {
    return st.message ? senderStage(st.message.status) : 'draft';
  }

  function updateCount() {
    const n = (st.message ? st.message.content : textarea.value).length;
    countEl.textContent = `${n}/${MAX_LENGTH}`;
    countEl.classList.toggle('is-near', n > MAX_LENGTH - 50);
  }

  function updateSendButton() {
    if (sendButton) sendButton.disabled = st.sending || !selected() || !textarea.value.trim();
  }

  function updateDate() {
    dateEl.textContent = receiptDate(st.message ? st.message.created_at : new Date());
  }

  // -- Painting --

  function paintLeft() {
    const s = stage();
    const list = printers();
    fill(left, 
      // One element per line so each gets its own dither; the last line a bit more.
      h('h1', { class: 'display', 'aria-label': 'Burn Before Reading' },
        h('span', { class: 'dither-line dither-line--light', 'aria-hidden': 'true' }, 'Burn'),
        h('span', { class: 'dither-line dither-line--light', 'aria-hidden': 'true' }, 'Before'),
        h('span', { class: 'dither-line', 'aria-hidden': 'true' }, 'Reading')),
      h('p', { class: 'lede' }, s === 'draft'
        ? 'Write on the receipt. It prints exactly like this, in someone’s home, within seconds.'
        : TERMINAL_STATUSES.has(st.message.status)
          ? 'That one’s done. Write another whenever you like.'
          : 'Your note is on its way. Keep this page open to watch it print.'),
      list.length > 0 && h('div', { class: 'printer-list' },
        h('span', { class: 'printer-list__title' }, 'Printers'),
        list.map((p) => h('span', null, `${p.recipient_name} · ${printerState(p).text}`))),
      st.message && h('span', { class: 'write__seq', 'aria-label': `Message number ${st.message.sequence_number}` }, `/${seqNo(st.message.sequence_number)}`));
  }

  function recipientField() {
    if (st.message) return h('span', { class: 'receipt__strong' }, (st.sentTo?.recipient_name ?? '—').toUpperCase());
    if (st.printersError) return h('span', { class: 'receipt__muted' }, 'COULDN’T LOAD');
    if (!st.printers) return h('span', { class: 'receipt__muted' }, 'LOADING…');
    if (st.printers.length === 0) return h('span', { class: 'receipt__muted' }, 'NOBODY YET');
    const current = selected();
    return h('div', { role: 'group', 'aria-label': 'Recipient', class: 'receipt__recipients' },
      st.printers.map((p) => h('button', {
        type: 'button',
        class: 'who',
        'aria-pressed': p.id === current.id ? 'true' : 'false',
        onClick: () => {
          st.to = p.id;
          writePref('recipient', p.id);
          paint();
        },
      }, p.recipient_name.toUpperCase())));
  }

  function receipt() {
    const s = stage();
    const msg = st.message;
    return h('div', { class: 'receipt-wrap' },
      h('form', { class: 'receipt paper', 'aria-label': 'Your message, as a receipt', onSubmit: (e) => e.preventDefault() },
        h('div', { class: 'receipt__head' },
          h('span', null, 'BURN BEFORE READING'),
          h('span', { class: 'receipt__muted' }, `No.${msg ? seqNo(msg.sequence_number) : '—'}`)),
        h('hr', { class: 'rule' }),
        h('div', { class: 'receipt__fields' },
          h('span', { class: 'receipt__muted' }, 'TO'),
          recipientField(),
          h('span', { class: 'receipt__muted' }, 'FROM'),
          h('span', null, senderName.toUpperCase()),
          h('span', { class: 'receipt__muted' }, 'DATE'),
          dateEl),
        h('hr', { class: 'rule' }),
        msg ? h('p', { class: 'receipt__body' }, msg.content) : textarea,
        h('hr', { class: 'rule' }),
        h('div', { class: 'receipt__foot' },
          h('span', { class: `stamp stamp--${s}` }, STAMPS[s] ?? ''),
          countEl)));
  }

  function draftActions() {
    const p = selected();
    const state = p ? printerState(p) : null;
    let hint = asAdmin ? 'Admin: skips approval, prints right away.' : 'A moderator glances at it first.';
    if (state?.kind === 'asleep') hint = `${p.recipient_name}’s printer is asleep until ${p.quiet_end}. It’ll print then.`;
    else if (state?.kind === 'offline') hint = `${p.recipient_name}’s printer is offline. It’ll print when it’s back.`;
    if (st.printers && st.printers.length === 0) {
      hint = asAdmin ? 'There are no printers yet.' : 'You can’t send to anyone yet. Ask a printer’s owner for a join link.';
    }

    sendButton = h('button', { type: 'button', class: 'btn btn--solid btn--lg', onClick: send },
      st.sending ? 'Sending…' : p ? `Print for ${p.recipient_name} →` : 'Print →');
    return h('div', { class: 'write__actions' },
      sendButton,
      h('span', { class: 'muted small' }, hint),
      st.sendError && h('p', { class: 'form-error', role: 'alert' }, st.sendError),
      st.printersError && h('p', { class: 'form-error', role: 'alert' }, st.printersError));
  }

  function progress() {
    const s = stage();
    // How many steps are done; the next one is "current" unless the message stopped.
    const reached = { awaiting: 1, queued: 3, printing: 3, printed: 4, rejected: 1, failed: 3 }[s];
    const names = ['Sent', s === 'rejected' ? 'Declined' : 'Approved', 'Queued', s === 'failed' ? 'Failed' : 'Printed'];
    const stopped = s === 'rejected' || s === 'failed';

    const p = st.sentTo;
    const pState = p ? printerState(p) : null;
    const prefix = asAdmin ? 'Queued.' : 'Approved.';
    const lines = {
      awaiting: 'Waiting for a moderator.',
      queued: pState?.kind === 'asleep' ? `${prefix} Prints at ${p.quiet_end}.`
        : pState?.kind === 'offline' ? `${prefix} Waiting for the printer.`
          : `${prefix} Next in line.`,
      printing: 'Printing now.',
      printed: 'Printed. It’s on paper now.',
      rejected: 'Declined by a moderator.',
      failed: 'The printer couldn’t print it.',
    };

    return h('div', { class: 'write__progress', role: 'status', 'aria-live': 'polite' },
      h('ol', { class: 'steps', 'aria-label': 'Progress' },
        names.map((text, i) => {
          const cls = stopped && i === reached ? 'is-stopped' : i < reached ? 'is-done' : i === reached && !stopped ? 'is-current' : '';
          return h('li', { class: `steps__item ${cls}`, 'aria-current': cls === 'is-current' ? 'step' : null },
            h('span', { class: 'steps__bar' }),
            h('span', { class: 'steps__label' }, text));
        })),
      h('div', { class: 'write__status' },
        h('span', { class: 'write__status-line' }, lines[s] ?? ''),
        h('button', { type: 'button', class: 'btn btn--ghost', onClick: reset }, 'Write another')),
      st.pollFailures >= OFFLINE_AFTER && h('p', { class: 'offline-line' }, 'Can’t reach the server. Retrying…'));
  }

  function paint() {
    const s = stage();
    // Moving the textarea to a new parent blurs it: put the caret back afterwards.
    const typing = document.activeElement === textarea;
    const { selectionStart, selectionEnd } = textarea;

    paintLeft();
    updateDate();
    updateCount();
    fill(right, 
      h('div', { class: `sun sun--${s}`, 'aria-hidden': 'true' }),
      // The idea shares the rings' box so it can start at their centre.
      h('div', { class: 'write__orbit' },
        rings([144, 131, 118, 105, 92, 79, 66, 53], { viewBox: 300, className: 'write__rings' }),
        s === 'draft' && h('p', { class: 'write__idea' }, h('span', { class: 'visually-hidden' }, 'Idea: '), st.idea)),
      receipt(),
      s === 'draft' ? draftActions() : progress());
    if (s === 'draft') updateSendButton();

    if (typing && textarea.isConnected) {
      textarea.focus();
      textarea.setSelectionRange(selectionStart, selectionEnd);
    }
  }

  // -- Actions --

  async function fetchPrinters() {
    try {
      st.printers = asAdmin ? await fetchAll(api.adminPrinters) : await api.userPrinters();
      st.printersError = null;
    } catch (err) {
      if (!st.printers) st.printersError = `Couldn’t load printers. ${err.message}`;
    }
    if (destroyed) return;
    if (st.sentTo) st.sentTo = st.printers?.find((p) => p.id === st.sentTo.id) ?? st.sentTo;
    paint();
  }

  async function loadPrinters() {
    await fetchPrinters();
    if (!destroyed) later(loadPrinters, PRINTERS_REFRESH_MS);
  }

  async function send() {
    const p = selected();
    const content = textarea.value.trim();
    if (!p || !content || st.sending) return;
    st.sending = true;
    st.sendError = null;
    paint();
    try {
      const message = asAdmin ? await api.sendAsAdmin(p.id, content) : await api.sendAsUser(p.id, content);
      if (destroyed) return;
      st.message = message;
      st.sentTo = p;
      textarea.value = '';
      writePref('draft', null);
      st.pollFailures = 0;
      poll(message.id);
    } catch (err) {
      st.sendError = err.message;
    } finally {
      st.sending = false;
      if (!destroyed) paint();
    }
  }

  function poll(id) {
    later(async () => {
      if (st.message?.id !== id) return;
      try {
        const message = asAdmin ? await api.adminMessage(id) : await api.userMessage(id);
        if (destroyed || st.message?.id !== id) return;
        const wasOffline = st.pollFailures >= OFFLINE_AFTER;
        st.pollFailures = 0;
        if (message.status !== st.message.status || wasOffline) {
          st.message = message;
          paint();
        }
      } catch {
        // Keep the last known state and try again; say so once it keeps failing.
        st.pollFailures += 1;
        if (st.pollFailures === OFFLINE_AFTER && !destroyed) paint();
      }
      if (st.message?.id === id && !TERMINAL_STATUSES.has(st.message.status)) poll(id);
    }, POLL_MS);
  }

  function reset() {
    st.message = null;
    st.sentTo = null;
    st.sendError = null;
    st.idea = pickIdea();
    paint();
    textarea.focus();
  }

  function tick() {
    if (!st.message) updateDate();
    later(tick, 15000);
  }

  paint();
  loadPrinters();
  tick();

  return {
    el,
    title: 'Write',
    // Joined or left a printer, or changed quiet hours, from Settings.
    meChanged: fetchPrinters,
    destroy() {
      destroyed = true;
      timers.forEach(clearTimeout);
    },
  };
}
