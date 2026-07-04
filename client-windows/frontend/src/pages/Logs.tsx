import { useState, useEffect, useRef } from 'react';
import { Trash2, Download, AlertCircle, Info, AlertTriangle, Bug } from 'lucide-react';
import { logStore } from '../lib/stores/logStore';
import type { LogEntry } from '../lib/types';

// Цвет уровня логирования
const LEVEL_COLOR: Record<string, string> = {
  INFO: 'var(--text-2)',
  WARN: 'var(--orange)',
  ERROR: 'var(--red)',
  DEBUG: 'var(--text-4)',
};

// Иконка уровня
function LevelIcon({ level }: { level: string }) {
  switch (level) {
    case 'ERROR':
      return <AlertCircle size={13} style={{ color: 'var(--red)', flexShrink: 0 }} />;
    case 'WARN':
      return <AlertTriangle size={13} style={{ color: 'var(--orange)', flexShrink: 0 }} />;
    case 'DEBUG':
      return <Bug size={13} style={{ color: 'var(--text-4)', flexShrink: 0 }} />;
    default:
      return <Info size={13} style={{ color: 'var(--text-3)', flexShrink: 0 }} />;
  }
}

// Фильтры
type Filter = 'all' | 'INFO' | 'WARN' | 'ERROR' | 'DEBUG';
const FILTERS: { value: Filter; label: string }[] = [
  { value: 'all', label: 'Все' },
  { value: 'INFO', label: 'INFO' },
  { value: 'WARN', label: 'WARN' },
  { value: 'ERROR', label: 'ERROR' },
  { value: 'DEBUG', label: 'DEBUG' },
];

export default function Logs() {
  const [logs, setLogs] = useState<LogEntry[]>(() => logStore.getAll());
  const [filter, setFilter] = useState<Filter>('all');
  const [autoScroll, setAutoScroll] = useState(true);
  const bottomRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => logStore.subscribe(() => setLogs(logStore.getAll())), []);

  // Автопрокрутка вниз
  useEffect(() => {
    if (autoScroll && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [logs, autoScroll]);

  // Определяем, нужно ли скроллить
  const handleScroll = () => {
    if (!containerRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = containerRef.current;
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 60);
  };

  // Фильтрация
  const filteredLogs = filter === 'all' ? logs : logs.filter((l) => l.level === filter);

  // Считаем количество по уровням
  const counts = {
    all: logs.length,
    INFO: logs.filter((l) => l.level === 'INFO').length,
    WARN: logs.filter((l) => l.level === 'WARN').length,
    ERROR: logs.filter((l) => l.level === 'ERROR').length,
    DEBUG: logs.filter((l) => l.level === 'DEBUG').length,
  };

  return (
    <div style={styles.container}>
      {/* Заголовок */}
      <div style={styles.header}>
        <h2 style={styles.title}>Логи</h2>
        <div style={styles.headerActions}>
          <span style={styles.count}>{filteredLogs.length} записей</span>
          <button style={styles.clearBtn} onClick={() => logStore.clear()} title="Очистить">
            <Trash2 size={16} />
          </button>
        </div>
      </div>

      {/* Фильтры */}
      <div style={styles.filters}>
        {FILTERS.map((f) => (
          <button
            key={f.value}
            style={{ ...styles.filterBtn, background: filter === f.value ? 'var(--accent)' : 'var(--bg-2)', color: filter === f.value ? '#fff' : 'var(--text-3)' }}
            onClick={() => setFilter(f.value)}
          >
            {f.label}
            <span style={styles.filterCount}>{counts[f.value]}</span>
          </button>
        ))}
      </div>

      {/* Логи */}
      <div ref={containerRef} style={styles.logContainer} onScroll={handleScroll}>
        {filteredLogs.length === 0 && (
          <div style={styles.empty}>
            <Info size={24} style={{ color: 'var(--text-4)', marginBottom: 8 }} />
            <span>Нет записей</span>
          </div>
        )}
        {filteredLogs.map((entry, i) => (
          <div key={i} style={{ ...styles.logEntry, borderLeftColor: LEVEL_COLOR[entry.level] ?? 'var(--text-4)' }}>
            <span style={styles.logTime}>{entry.time}</span>
            <span style={{ ...styles.logLevel, color: LEVEL_COLOR[entry.level] ?? 'var(--text-3)' }}>{entry.level}</span>
            <LevelIcon level={entry.level} />
            <span style={styles.logMessage}>{entry.message}</span>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>

      {/* Кнопка прокрутки вниз */}
      {!autoScroll && logs.length > 0 && (
        <button style={styles.scrollDownBtn} onClick={() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }); setAutoScroll(true); }}>
          <Download size={16} />
          <span>Вниз</span>
        </button>
      )}
    </div>
  );
}

const styles: Record<string, React.CSSProperties> = {
  container: { flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', padding: '20px 24px', background: 'var(--bg)' },
  header: { display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 },
  title: { fontSize: 18, fontWeight: 600, color: 'var(--text)', margin: 0 },
  headerActions: { display: 'flex', alignItems: 'center', gap: 12 },
  count: { fontSize: 13, color: 'var(--text-3)' },
  clearBtn: { background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-3)', padding: 4, borderRadius: 6, transition: 'color 0.15s' },
  filters: { display: 'flex', gap: 4, marginBottom: 12, background: 'var(--bg-2)', borderRadius: 'var(--radius-sm)', padding: 3 },
  filterBtn: { display: 'flex', alignItems: 'center', gap: 5, border: 'none', borderRadius: 6, padding: '5px 12px', cursor: 'pointer', fontSize: 12, fontWeight: 500, fontFamily: 'inherit', transition: 'all 0.15s' },
  filterCount: { fontSize: 11, opacity: 0.7 },
  logContainer: { flex: 1, overflowY: 'auto' as const, background: 'var(--bg-2)', borderRadius: 'var(--radius)', border: '1px solid var(--border)', padding: '8px 0' },
  logEntry: { display: 'flex', alignItems: 'flex-start', gap: 8, padding: '4px 16px', borderLeft: '2px solid', fontSize: 13, lineHeight: '20px', fontFamily: "'Cascadia Code', 'Fira Code', 'Consolas', monospace", transition: 'background 0.1s' },
  logTime: { color: 'var(--text-4)', flexShrink: 0, fontSize: 12, minWidth: 58 },
  logLevel: { flexShrink: 0, fontSize: 11, fontWeight: 700, minWidth: 42, textAlign: 'right' as const },
  logMessage: { color: 'var(--text-2)', wordBreak: 'break-word' as const },
  empty: { display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 60, color: 'var(--text-4)', fontSize: 14 },
  scrollDownBtn: { position: 'absolute' as const, bottom: 30, right: 40, display: 'flex', alignItems: 'center', gap: 6, background: 'var(--accent)', color: '#fff', border: 'none', borderRadius: 20, padding: '6px 14px', cursor: 'pointer', fontSize: 12, fontWeight: 600, fontFamily: 'inherit', boxShadow: '0 4px 16px rgba(108,92,231,0.4)', transition: 'transform 0.15s' },
};