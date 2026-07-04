import type { TunnelState } from '../types';

type Listener = (state: TunnelState) => void;

let state: TunnelState = 'idle';
const listeners = new Set<Listener>();

// Статистика трафика
let rxBytes = 0;
let txBytes = 0;
const statsListeners = new Set<(rx: number, tx: number) => void>();

export const tunnelStore = {
  get: () => state,
  set: (s: TunnelState) => {
    state = s;
    listeners.forEach((fn) => fn(state));
  },
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    fn(state);
    return () => {
      listeners.delete(fn);
    };
  },

  // Статистика
  getStats: () => ({ rx: rxBytes, tx: txBytes }),
  setStats: (rx: number, tx: number) => {
    rxBytes = rx;
    txBytes = tx;
    statsListeners.forEach((fn) => fn(rx, tx));
  },
  subscribeStats: (fn: (rx: number, tx: number) => void) => {
    statsListeners.add(fn);
    fn(rxBytes, txBytes);
    return () => {
      statsListeners.delete(fn);
    };
  },
};