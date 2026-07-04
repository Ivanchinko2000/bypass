type LogLevel = string;

interface LogEntry {
  level: LogLevel;
  message: string;
  time: string;
}

const MAX_ENTRIES = 2000;
let entries: LogEntry[] = [];
const listeners = new Set<() => void>();

export const logStore = {
  push: (level: LogLevel, message: string) => {
    const now = new Date();
    const time = now.toLocaleTimeString('ru-RU', {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
    entries.push({ level, message, time });
    if (entries.length > MAX_ENTRIES) {
      entries = entries.slice(-MAX_ENTRIES);
    }
    listeners.forEach((fn) => fn());
  },
  getAll: () => entries,
  clear: () => {
    entries = [];
    listeners.forEach((fn) => fn());
  },
  subscribe: (fn: () => void) => {
    listeners.add(fn);
    return () => {
      listeners.delete(fn);
    };
  },
};