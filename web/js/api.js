import { auth } from './auth.js';

const BASE = '/api/v1';

/** The API caps list pages at 10 items. */
export const PAGE_SIZE = 10;

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

let unauthorizedHandler = () => {};

/** Called with 'user' or 'admin' when a request with that role's token gets a 401. */
export function onUnauthorized(fn) {
  unauthorizedHandler = fn;
}

function capitalize(text) {
  return text ? text[0].toUpperCase() + text.slice(1) : text;
}

async function request(role, method, path, { body, query, token, quiet = false } = {}) {
  const url = new URL(BASE + path, window.location.origin);
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value != null && value !== '') url.searchParams.set(key, String(value));
  }

  const headers = { Accept: 'application/json' };
  const bearer = token ?? (role === 'user' ? auth.userToken() : role === 'admin' ? auth.adminToken() : null);
  if (role && !bearer) throw new ApiError(401, 'Not signed in.');
  if (bearer) headers.Authorization = `Bearer ${bearer}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let res;
  try {
    res = await fetch(url, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: 'no-store',
      credentials: 'same-origin',
    });
  } catch {
    throw new ApiError(0, 'Can’t reach the server. Check your connection and try again.');
  }

  if (res.status === 204) return null;

  let data = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }

  if (!res.ok) {
    if (res.status === 401 && role && !quiet) unauthorizedHandler(role);
    const message = data && typeof data.error === 'string' ? capitalize(data.error) : `Request failed (${res.status}).`;
    throw new ApiError(res.status, message);
  }
  return data;
}

const id = (value) => encodeURIComponent(value);

export const api = {
  // Public
  claim: (invite, deviceName) => request(null, 'POST', '/users/claim', { body: { invite, device_name: deviceName } }),
  join: (joinToken, username, deviceName) => request(null, 'POST', '/users/join', { body: { join_token: joinToken, username, device_name: deviceName } }),

  // User session
  me: () => request('user', 'GET', '/user/me'),
  userPrinters: () => request('user', 'GET', '/user/printers'),
  userMessages: (query) => request('user', 'GET', '/user/messages', { query }),
  userMessage: (messageId) => request('user', 'GET', `/user/messages/${id(messageId)}`),
  sendAsUser: (printerId, content) => request('user', 'POST', '/user/messages', { body: { printer_id: printerId, content } }),
  myInvite: () => request('user', 'POST', '/user/invite'),
  mySessions: () => request('user', 'GET', '/user/sessions'),
  revokeMySession: (sessionId) => request('user', 'DELETE', `/user/sessions/${id(sessionId)}`),
  redeemJoin: (joinToken) => request('user', 'POST', '/user/memberships', { body: { join_token: joinToken } }),
  leavePrinter: (printerId) => request('user', 'DELETE', `/user/memberships/${id(printerId)}`),

  // Printer owner (user session, on the printer they own)
  ownerSetQuietHours: (printerId, body) => request('user', 'PUT', `/user/printers/${id(printerId)}/quiet_hours`, { body }),
  ownerClearQuietHours: (printerId) => request('user', 'DELETE', `/user/printers/${id(printerId)}/quiet_hours`),
  ownerMembers: (printerId) => request('user', 'GET', `/user/printers/${id(printerId)}/members`),
  ownerRemoveMember: (printerId, userId) => request('user', 'DELETE', `/user/printers/${id(printerId)}/members/${id(userId)}`),
  ownerJoinTokens: (printerId, query) => request('user', 'GET', `/user/printers/${id(printerId)}/join_tokens`, { query }),
  ownerCreateJoinToken: (printerId) => request('user', 'POST', `/user/printers/${id(printerId)}/join_tokens`),
  ownerRevokeJoinToken: (printerId, tokenId) => request('user', 'DELETE', `/user/printers/${id(printerId)}/join_tokens/${id(tokenId)}`),

  // Admin
  checkAdminToken: (token) => request('admin', 'GET', '/admin/printers', { token, quiet: true, query: { limit: 1 } }),
  adminPrinters: (query) => request('admin', 'GET', '/admin/printers', { query }),
  adminMessages: (query) => request('admin', 'GET', '/admin/messages', { query }),
  adminMessage: (messageId) => request('admin', 'GET', `/admin/messages/${id(messageId)}`),
  sendAsAdmin: (printerId, content) => request('admin', 'POST', '/admin/messages', { body: { printer_id: printerId, content } }),
  approve: (messageId) => request('admin', 'PATCH', `/admin/messages/${id(messageId)}/approve`),
  reject: (messageId) => request('admin', 'PATCH', `/admin/messages/${id(messageId)}/reject`),
  requeue: (messageId) => request('admin', 'PATCH', `/admin/messages/${id(messageId)}/requeue`),
  adminSetQuietHours: (printerId, body) => request('admin', 'PUT', `/admin/printers/${id(printerId)}/quiet_hours`, { body }),
  adminClearQuietHours: (printerId) => request('admin', 'DELETE', `/admin/printers/${id(printerId)}/quiet_hours`),
  adminSetOwner: (printerId, userId) => request('admin', 'PUT', `/admin/printers/${id(printerId)}/owner`, { body: { user_id: userId } }),
  adminRemoveOwner: (printerId, keepAsMember) => request('admin', 'DELETE', `/admin/printers/${id(printerId)}/owner`, { query: { keep_as_member: keepAsMember } }),
  adminJoinTokens: (printerId, query) => request('admin', 'GET', `/admin/printers/${id(printerId)}/join_tokens`, { query }),
  adminCreateJoinToken: (printerId) => request('admin', 'POST', `/admin/printers/${id(printerId)}/join_tokens`),
  adminRevokeJoinToken: (printerId, tokenId) => request('admin', 'DELETE', `/admin/printers/${id(printerId)}/join_tokens/${id(tokenId)}`),
  adminUsers: (query) => request('admin', 'GET', '/admin/users', { query }),
  adminUser: (userId) => request('admin', 'GET', `/admin/users/${id(userId)}`),
  adminCreateUser: (username, printerIds) => request('admin', 'POST', '/admin/users', { body: { username, printer_ids: printerIds } }),
  adminSetUserPrinters: (userId, accesses) => request('admin', 'PUT', `/admin/users/${id(userId)}/printers`, { body: { printer_accesses: accesses } }),
  adminRecoveryInvite: (userId, revokeSessions) => request('admin', 'POST', `/admin/users/${id(userId)}/invite`, { body: { revoke_sessions: revokeSessions } }),
  adminUserSessions: (userId) => request('admin', 'GET', `/admin/users/${id(userId)}/sessions`),
  adminRevokeSession: (userId, sessionId) => request('admin', 'DELETE', `/admin/users/${id(userId)}/sessions/${id(sessionId)}`),
  adminRevokeAllSessions: (userId) => request('admin', 'DELETE', `/admin/users/${id(userId)}/sessions`),
};

const CODE_PATTERN = /bbr_(invite|join)_[A-Za-z0-9_-]+/;

/**
 * Finds an invite or join token in pasted text: the bare code or a whole link.
 * Returns { kind: 'invite' | 'join', code } or null.
 */
export function parseCode(text) {
  const match = String(text ?? '').match(CODE_PATTERN);
  return match ? { kind: match[1], code: match[0] } : null;
}

/** "https://host/join#bbr_…": the code stays in the fragment, so it's never sent in a request line. */
export function joinLink(code) {
  return `${window.location.origin}/join#${code}`;
}

/**
 * Walks a paginated list route page by page.
 * `until(item)` stops after the first page that contains a matching item.
 * Items are de-duplicated by id, since offsets shift when new rows arrive mid-walk.
 */
export async function fetchAll(fetchPage, query = {}, { until, maxPages = 500 } = {}) {
  const seen = new Set();
  const out = [];
  for (let page = 0; page < maxPages; page++) {
    const items = (await fetchPage({ ...query, limit: PAGE_SIZE, offset: page * PAGE_SIZE })) ?? [];
    for (const item of items) {
      if (seen.has(item.id)) continue;
      seen.add(item.id);
      out.push(item);
    }
    if (items.length < PAGE_SIZE) break;
    if (until && items.some(until)) break;
  }
  return out;
}
