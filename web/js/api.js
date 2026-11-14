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

  // User session
  me: () => request('user', 'GET', '/user/me'),
  userPrinters: () => request('user', 'GET', '/user/printers'),
  userMessages: (query) => request('user', 'GET', '/user/messages', { query }),
  userMessage: (messageId) => request('user', 'GET', `/user/messages/${id(messageId)}`),
  sendAsUser: (printerId, content) => request('user', 'POST', '/user/messages', { body: { printer_id: printerId, content } }),

  // Admin
  checkAdminToken: (token) => request('admin', 'GET', '/admin/printers', { token, quiet: true, query: { limit: 1 } }),
  adminPrinters: (query) => request('admin', 'GET', '/admin/printers', { query }),
  adminMessages: (query) => request('admin', 'GET', '/admin/messages', { query }),
  adminMessage: (messageId) => request('admin', 'GET', `/admin/messages/${id(messageId)}`),
  sendAsAdmin: (printerId, content) => request('admin', 'POST', '/admin/messages', { body: { printer_id: printerId, content } }),
  approve: (messageId) => request('admin', 'PATCH', `/admin/messages/${id(messageId)}/approve`),
  reject: (messageId) => request('admin', 'PATCH', `/admin/messages/${id(messageId)}/reject`),
  requeue: (messageId) => request('admin', 'PATCH', `/admin/messages/${id(messageId)}/requeue`),
};

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
