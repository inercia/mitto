const openPickers = new Set();

/**
 * Register an open picker. Opening a picker closes every other registered
 * picker, so at most one popover is open at a time. Each picker keeps its own
 * open state; this registry only coordinates transitions.
 *
 * @param {() => void} close
 * @returns {() => void} unregister
 */
export function registerExclusivePicker(close) {
  for (const otherClose of [...openPickers]) {
    if (otherClose !== close) otherClose();
  }
  openPickers.add(close);
  return () => {
    openPickers.delete(close);
  };
}
