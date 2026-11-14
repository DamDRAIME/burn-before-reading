// Tokens are kept in localStorage so a device stays signed in after the browser restarts.
// See SCRATCHPAD.md: once the server sets an HttpOnly session cookie on claim, the user
// token can stop living here.

const USER_KEY = 'bbr.userToken';
const ADMIN_KEY = 'bbr.adminToken';

function read(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key, value) {
  try {
    if (value) localStorage.setItem(key, value);
    else localStorage.removeItem(key);
  } catch {
    // Storage blocked (private mode, site data disabled): the session lasts until reload.
  }
}

const memory = { [USER_KEY]: null, [ADMIN_KEY]: null };

function get(key) {
  return read(key) ?? memory[key];
}

function set(key, value) {
  memory[key] = value || null;
  write(key, value);
}

export const auth = {
  userToken: () => get(USER_KEY),
  setUserToken: (token) => set(USER_KEY, token),
  adminToken: () => get(ADMIN_KEY),
  setAdminToken: (token) => set(ADMIN_KEY, token),
};

export function readPref(key) {
  return read(`bbr.pref.${key}`);
}

export function writePref(key, value) {
  write(`bbr.pref.${key}`, value);
}
