// Side pane: settings open on request, in a modal <dialog> sliding in from the right.
// The dialog traps focus, closes on Esc and makes the page behind it inert.

import { h } from './dom.js';
import { countdown } from './format.js';

let current = null;

/**
 * Opens a pane (closing any other one) and calls render(body, pane) to fill it.
 * pane: { body, close() }. The pane's node is removed once it closes.
 */
export function openPane({ title, render, onClose }) {
  closePane();

  const body = h('div', { class: 'pane__body' });
  const closeButton = h('button', { type: 'button', class: 'pane__close', 'aria-label': 'Close' }, '×');
  const titleId = `pane-title-${Date.now()}`;
  const dialog = h('dialog', { class: 'pane', 'aria-labelledby': titleId },
    h('div', { class: 'pane__inner' },
      h('div', { class: 'pane__head' },
        h('h2', { id: titleId, class: 'pane__title' }, title),
        closeButton),
      body));

  const pane = {
    body,
    close: () => {
      if (dialog.open) dialog.close();
    },
  };

  closeButton.addEventListener('click', pane.close);
  // A click on the backdrop lands on the dialog itself; .pane__inner covers the rest.
  // Both ends of the click must be outside, so selecting text and releasing outside doesn't close it.
  let downOnBackdrop = false;
  dialog.addEventListener('mousedown', (event) => {
    downOnBackdrop = event.target === dialog;
  });
  dialog.addEventListener('click', (event) => {
    if (downOnBackdrop && event.target === dialog) pane.close();
    downOnBackdrop = false;
  });
  dialog.addEventListener('close', () => {
    if (current === pane) current = null;
    dialog.remove();
    onClose?.();
  });

  document.body.append(dialog);
  dialog.showModal();
  current = pane;
  render(body, pane);
  if (!body.contains(document.activeElement)) closeButton.focus();
  return pane;
}

export function closePane() {
  current?.close();
}

/**
 * A value to hand to someone else: read-only field, Copy, Share (where the browser
 * has it) and, with expiresAt, a live countdown. The value is shown only here, once.
 */
export function copyField(value, { label = 'Link', expiresAt = null, note = null } = {}) {
  const input = h('input', { class: 'input input--mono copy-field__input', type: 'text', readonly: true, value, 'aria-label': label, onFocus: (e) => e.target.select() });
  const status = h('span', { class: 'copy-field__status', role: 'status' });
  const timer = expiresAt ? h('span', { class: 'copy-field__timer' }) : null;

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      status.textContent = 'Copied.';
    } catch {
      input.focus();
      input.select();
      status.textContent = 'Press Ctrl+C (⌘C) to copy.';
    }
  }

  const share = typeof navigator.share === 'function' && h('button', {
    type: 'button',
    class: 'btn btn--outline btn--sm',
    onClick: () => navigator.share({ title: 'Burn Before Reading', url: value }).catch(() => {}),
  }, 'Share');

  const el = h('div', { class: 'copy-field' },
    input,
    h('div', { class: 'copy-field__actions' },
      h('button', { type: 'button', class: 'btn btn--solid btn--sm', onClick: copy }, 'Copy'),
      share,
      timer,
      status),
    note && h('p', { class: 'muted small copy-field__note' }, note));

  if (timer) {
    const tick = () => {
      const left = countdown(expiresAt);
      timer.textContent = left ? `Expires in ${left}` : 'Expired';
      el.classList.toggle('is-expired', !left);
      // Stop once the field is gone (pane closed or repainted) or expired.
      if (left && (el.isConnected || !started)) setTimeout(tick, 1000);
    };
    let started = false;
    tick();
    started = true;
  }

  return el;
}

/**
 * A button that asks before acting: the first click swaps it for the question,
 * "Yes" and "Cancel". run() may return a promise; errors land in onError.
 */
export function confirmButton(label, { question, yes = label, run, onError, className = 'btn btn--outline btn--sm' }) {
  const wrap = h('span', { class: 'confirm' });

  function idle() {
    wrap.replaceChildren(h('button', { type: 'button', class: className, onClick: ask }, label));
  }

  function ask() {
    const yesButton = h('button', { type: 'button', class: 'btn btn--solid btn--sm', onClick: go }, yes);
    const noButton = h('button', { type: 'button', class: 'btn btn--outline btn--sm', onClick: idle }, 'Cancel');
    wrap.replaceChildren(h('span', { class: 'confirm__question' }, question), yesButton, noButton);
    noButton.focus();
  }

  async function go(event) {
    for (const b of wrap.querySelectorAll('button')) b.disabled = true;
    event.target.textContent = 'Working…';
    try {
      await run();
    } catch (err) {
      onError?.(err);
    }
    // Usually the caller has repainted by now; if not, go back to the button.
    if (wrap.isConnected) idle();
  }

  idle();
  return wrap;
}
