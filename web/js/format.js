const MONTHS = ['JAN', 'FEB', 'MAR', 'APR', 'MAY', 'JUN', 'JUL', 'AUG', 'SEP', 'OCT', 'NOV', 'DEC'];
export const MONTHS_SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

const two = (n) => String(n).padStart(2, '0');

export const DEFAULT_TIMEZONE = 'Europe/Brussels';

/** 8 → "0008" */
export function seqNo(n) {
  return n == null ? '—' : String(n).padStart(4, '0');
}

/** "14 NOV 2026 14:02" */
export function receiptDate(value) {
  const d = new Date(value);
  return `${two(d.getDate())} ${MONTHS[d.getMonth()]} ${d.getFullYear()} ${clock(d)}`;
}

/** "14 NOV" */
export function shortDate(value) {
  const d = new Date(value);
  return `${two(d.getDate())} ${MONTHS[d.getMonth()]}`;
}

/** "14:02" */
export function clock(value) {
  const d = new Date(value);
  return `${two(d.getHours())}:${two(d.getMinutes())}`;
}

/** "14 Nov 2026" */
export function longDate(d) {
  return `${d.getDate()} ${MONTHS_SHORT[d.getMonth()]} ${d.getFullYear()}`;
}

/** "just now", "5 min ago", "3 h ago", "2 d ago" */
export function timeAgo(value, now = Date.now()) {
  if (!value) return 'never';
  const seconds = Math.max(0, Math.round((now - new Date(value).getTime()) / 1000));
  if (seconds < 45) return seconds < 10 ? 'just now' : `${seconds} s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}

/** Local calendar day key, "2026-11-14". */
export function dayKey(d) {
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}`;
}

// -- Printers ----

function minutesOf(hhmm) {
  const [h, m] = hhmm.split(':').map(Number);
  return h * 60 + m;
}

/** Minutes since midnight, now, in the printer's time zone. */
function minutesInZone(date, timeZone) {
  try {
    const parts = new Intl.DateTimeFormat('en-GB', { timeZone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(date);
    const get = (type) => Number(parts.find((p) => p.type === type)?.value ?? 0);
    return get('hour') * 60 + get('minute');
  } catch {
    return date.getHours() * 60 + date.getMinutes();
  }
}

/** Mirrors the server: quiet hours may cross midnight ("22:00" to "07:00"). */
export function inQuietHours(printer, now = new Date()) {
  if (!printer.quiet_start || !printer.quiet_end) return false;
  const start = minutesOf(printer.quiet_start);
  const end = minutesOf(printer.quiet_end);
  const current = minutesInZone(now, printer.timezone || DEFAULT_TIMEZONE);
  return start < end ? current >= start && current < end : current >= start || current < end;
}

/**
 * kind: 'online' | 'asleep' | 'offline'.
 * Quiet hours win: a sleeping printer won't print even if it's connected.
 */
export function printerState(printer, now = new Date()) {
  if (inQuietHours(printer, now)) {
    return { kind: 'asleep', label: 'Asleep', text: `asleep until ${printer.quiet_end}` };
  }
  if (printer.online) return { kind: 'online', label: 'Online', text: 'online' };
  return { kind: 'offline', label: 'Offline', text: 'offline' };
}

export function quietHoursText(printer) {
  return printer.quiet_start && printer.quiet_end ? `Quiet ${printer.quiet_start}–${printer.quiet_end}` : 'No quiet hours';
}

// -- Access ----

/** "in 2 d", "in 14 min", "now" */
export function timeUntil(value, now = Date.now()) {
  const seconds = Math.round((new Date(value).getTime() - now) / 1000);
  if (seconds <= 0) return 'now';
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `in ${minutes} min`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return `in ${hours} h`;
  return `in ${Math.round(hours / 24)} d`;
}

/** Time left before expiry, "14:32", or null once expired. */
export function countdown(value, now = Date.now()) {
  const seconds = Math.floor((new Date(value).getTime() - now) / 1000);
  if (seconds <= 0) return null;
  return `${Math.floor(seconds / 60)}:${two(seconds % 60)}`;
}

/** "Pending · expires in 2 d", "Used by Heidi", "Revoked", "Expired" */
export function joinTokenStatusText(token) {
  switch (token.status) {
    case 'pending':
      return `Unused · expires ${timeUntil(token.expires_at)}`;
    case 'used':
      return `Used by ${token.used_by?.name ?? 'a deleted account'}`;
    case 'revoked':
      return 'Revoked';
    case 'expired':
      return 'Expired';
    default:
      return token.status;
  }
}

// -- Messages ----

export const TERMINAL_STATUSES = new Set(['printed', 'rejected', 'failed']);

/** What the sender sees: awaiting | queued | printing | printed | rejected | failed. */
export function senderStage(status) {
  switch (status) {
    case 'awaiting_approval':
      return 'awaiting';
    case 'pending':
      return 'queued';
    case 'claimed':
    case 'processed':
      return 'printing';
    default:
      return status;
  }
}

/** Status shapes and labels, as drawn in the design. */
export const STATUS = {
  awaiting_approval: { shape: 'awaiting', label: 'Awaiting approval' },
  pending: { shape: 'queued', label: 'In queue' },
  claimed: { shape: 'printing', label: 'Printing' },
  processed: { shape: 'printing', label: 'Printing' },
  printed: { shape: 'printed', label: 'Printed' },
  failed: { shape: 'failed', label: 'Failed' },
  rejected: { shape: 'rejected', label: 'Rejected' },
};
