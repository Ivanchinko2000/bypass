import { useEffect } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Connect from './pages/Connect';
import Logs from './pages/Logs';
import Settings from './pages/Settings';
import { tunnelStore } from './lib/stores/tunnelStore';
import { logStore } from './lib/stores/logStore';
import { toastStore } from './lib/stores/toastStore';
import { EventsOn } from '../../wailsjs/runtime/runtime';

// Тип уровня логирования
type LogLevel = 'INFO' | 'WARN' | 'ERROR' | 'DEBUG';

// Подписка на Wails-события (аналогично PWDTT App.tsx)
function useWailsEvents() {
  useEffect(() => {
    const offs = [
      // Лог-сообщения от бэкенда
      EventsOn('log', (level: unknown, msg: unknown) => {
        logStore.push((level as LogLevel) ?? 'INFO', String(msg ?? ''));
      }),
      // Ошибки (показываем как toast)
      EventsOn('error', (msg: unknown) => {
        const s = String(msg ?? '');
        logStore.push('ERROR', s);
        toastStore.show(s, 5000);
      }),
      // Изменение состояния туннеля
      EventsOn('state_changed', (stateData: unknown) => {
        const data = stateData as {
          state?: string;
          connected?: boolean;
          mode?: string;
        };
        const s = data?.state ?? '';
        if (s === 'connected' || s === 'running' || data?.connected) {
          tunnelStore.set('connected');
          logStore.push('INFO', '✓ Туннель активен');
        } else if (s === 'connecting') {
          tunnelStore.set('connecting');
          logStore.clear();
          logStore.push('INFO', '⟳ Подключение...');
        } else if (s === 'reconnecting') {
          tunnelStore.set('connecting');
          logStore.push('WARN', '⟳ Переподключение...');
        } else if (
          s === 'stopped' ||
          s === 'error' ||
          s === 'disconnected' ||
          s === 'idle'
        ) {
          tunnelStore.set('idle');
          logStore.push('INFO', '— Отключено');
        }
      }),
      // Статистика трафика
      EventsOn('stats', (statsData: unknown) => {
        const data = statsData as { rx?: number; tx?: number };
        if (data) {
          tunnelStore.setStats(data.rx ?? 0, data.tx ?? 0);
        }
      }),
    ];
    return () => {
      offs.forEach((off) => off());
    };
  }, []);
}

export default function App() {
  useWailsEvents();

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Connect />} />
          <Route path="/logs" element={<Logs />} />
          <Route path="/settings" element={<Settings />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}