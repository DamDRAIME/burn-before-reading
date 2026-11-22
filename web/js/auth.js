// The session token is kept in localStorage so a device stays signed in after the browser
// restarts. Admins use the same session: admin rights come from `is_admin` on /user/me.
// See SCRATCHPAD.md: once the server sets an HttpOnly session cookie on claim, the token
// can stop living here.

const USER_KEY = 'bbr.userToken';

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

// The shared ADMIN_TOKEN is gone from the server: forget one left over from before.
write('bbr.adminToken', null);

const memory = { [USER_KEY]: null };

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
};

export function readPref(key) {
  return read(`bbr.pref.${key}`);
}

export function writePref(key, value) {
  write(`bbr.pref.${key}`, value);
}
