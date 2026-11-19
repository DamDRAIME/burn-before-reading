// Background tabs don't poll: a refresh that comes due while the tab is hidden
// waits for it to be shown again, then runs straight away.

/** Calls fn now if the page is visible, otherwise as soon as it becomes visible. */
export function whenVisible(fn) {
  if (!document.hidden) {
    fn();
    return;
  }
  const onChange = () => {
    if (document.hidden) return;
    document.removeEventListener('visibilitychange', onChange);
    fn();
  };
  document.addEventListener('visibilitychange', onChange);
}

/** Failed refreshes in a row before saying the server can't be reached. */
export const OFFLINE_AFTER = 2;
