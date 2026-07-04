let message = '';
let timeoutId: ReturnType<typeof setTimeout> | null = null;
const listeners = new Set<() => void>();

export const toastStore = {
  show: (msg: string, duration = 3000) => {
    message = msg;
    listeners.forEach((fn) => fn());
    if (timeoutId) clearTimeout(timeoutId);
    timeoutId = setTimeout(() => {
      message = '';
      listeners.forEach((fn) => fn());
    }, duration);
  },
  get: () => message,
  subscribe: (fn: () => void) => {
    listeners.add(fn);
    fn();
    return () => {
      listeners.delete(fn);
    };
  },
};