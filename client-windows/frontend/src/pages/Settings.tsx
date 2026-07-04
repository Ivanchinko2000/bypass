import { useState, useEffect } from 'react';
import { Shield, Globe, Play, MonitorCheck, Monitor, RotateCcw } from 'lucide-react';
import { GetSettings, SaveSettings } from '../../wailsjs/go/backend/App';
import type { Settings as SettingsType } from '../lib/types';
import { toastStore } from '../lib/stores/toastStore';

export default function Settings() {
  const [settings, setSettings] = useState<SettingsType>({
    killSwitch: true,
    dnsLeak: true,
    autoConnect: false,
    autoStart: false,
    tray: true,
    mode: 'auto',
    mtu: 1300,
    dohServers: [],
  });
  const [newDohServer, setNewDohServer] = useState('');
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    GetSettings().then((s) => { if (s) setSettings(s); }).catch(() => {});
  }, []);

  const handleSave = async () => {
    try {
      await SaveSettings(settings);
      setSaved(true);
      toastStore.show('Настройки сохранены');
      setTimeout(() => setSaved(false), 2000);
    } catch (e) {
      toastStore.show('Ошибка сохранения: ' + String(e));
    }
  };

  const addDohServer = () => {
    const url = newDohServer.trim();
    if (!url) return;
    if (!url.startsWith('https://')) { toastStore.show('DoH URL должен начинаться с https://'); return; }
    if (settings.dohServers?.includes(url)) { toastStore.show('Этот сервер уже добавлен'); return; }
    setSettings((prev) => ({ ...prev, dohServers: [...(prev.dohServers ?? []), url] }));
    setNewDohServer('');
  };

  const removeDohServer = (url: string) => {
    setSettings((prev) => ({ ...prev, dohServers: (prev.dohServers ?? []).filter((s) => s !== url) }));
  };

  const resetToDefaults = () => {
    setSettings({ killSwitch: true, dnsLeak: true, autoConnect: false, autoStart: false, tray: true, mode: 'auto', mtu: 1300, dohServers: [] });
    setNewDohServer('');
  };

  const Toggle = ({ checked, onChange, label, description, icon }: { checked: boolean; onChange: (v: boolean) => void; label: string; description: string; icon: React.ReactNode }) => (
    <div style={styles.toggleRow}>
      <div style={styles.toggleIcon}>{icon}</div>
      <div style={styles.toggleText}>
        <div style={styles.toggleLabel}>{label}</div>
        <div style={styles.toggleDesc}>{description}</div>
      </div>
      <button style={{ ...styles.toggleTrack, background: checked ? 'var(--accent)' : 'var(--bg-3)' }} onClick={() => onChange(!checked)}>
        <div style={{ ...styles.toggleThumb, transform: checked ? 'translateX(18px)' : 'translateX(2px)' }} />
      </button>
    </div>
  );

  return (
    <div style={styles.container}>
      <div style={styles.header}>
        <h2 style={styles.title}>Настройки</h2>
        <div style={styles.headerActions}>
          <button style={styles.resetBtn} onClick={resetToDefaults} title="Сбросить">
            <RotateCcw size={15} /><span>По умолчанию</span>
          </button>
          <button style={{ ...styles.saveBtn, background: saved ? 'var(--green)' : 'var(--accent)' }} onClick={handleSave}>
            {saved ? '✓ Сохранено' : 'Сохранить'}
          </button>
        </div>
      </div>

      {/* Безопасность */}
      <div style={styles.section}>
        <h3 style={styles.sectionTitle}><Shield size={16} /> Безопасность</h3>
        <div style={styles.sectionBody}>
          <Toggle checked={settings.killSwitch} onChange={(v) => setSettings((p) => ({ ...p, killSwitch: v }))} label="Kill Switch" description="Блокирует весь трафик при разрыве туннеля для предотвращения утечек" icon={<Shield size={18} style={{ color: 'var(--red)' }} />} />
          <Toggle checked={settings.dnsLeak} onChange={(v) => setSettings((p) => ({ ...p, dnsLeak: v }))} label="Защита от DNS-утечек" description="Блокирует DNS-запросы вне туннеля (DoH только)" icon={<Globe size={18} style={{ color: 'var(--blue)' }} />} />
        </div>
      </div>

      {/* Подключение */}
      <div style={styles.section}>
        <h3 style={styles.sectionTitle}><Play size={16} /> Подключение</h3>
        <div style={styles.sectionBody}>
          <Toggle checked={settings.autoConnect} onChange={(v) => setSettings((p) => ({ ...p, autoConnect: v }))} label="Автоподключение" description="Подключаться к последнему серверу при запуске приложения" icon={<Play size={18} style={{ color: 'var(--green)' }} />} />
          <Toggle checked={settings.autoStart} onChange={(v) => setSettings((p) => ({ ...p, autoStart: v }))} label="Автозапуск" description="Запускать BypassVPN при старте Windows" icon={<MonitorCheck size={18} style={{ color: 'var(--orange)' }} />} />
          <Toggle checked={settings.tray} onChange={(v) => setSettings((p) => ({ ...p, tray: v }))} label="Системный трей" description="Минимизировать в трей вместо закрытия" icon={<Monitor size={18} style={{ color: 'var(--text-3)' }} />} />
          <div style={styles.row}>
            <div style={styles.rowIcon}><Globe size={18} style={{ color: 'var(--accent)' }} /></div>
            <div style={styles.rowText}><div style={styles.rowLabel}>Режим по умолчанию</div></div>
            <select style={styles.select} value={settings.mode} onChange={(e) => setSettings((p) => ({ ...p, mode: e.target.value }))}>
              <option value="auto">Авто</option>
              <option value="wdtt">WDTT</option>
              <option value="vless">VLESS</option>
            </select>
          </div>
          <div style={styles.row}>
            <div style={styles.rowIcon}><Shield size={18} style={{ color: 'var(--text-3)' }} /></div>
            <div style={styles.rowText}>
              <div style={styles.rowLabel}>MTU</div>
              <div style={styles.rowDesc}>Максимальный размер пакета (рекомендуется 1300)</div>
            </div>
            <input style={styles.input} type="number" min={576} max={1500} value={settings.mtu} onChange={(e) => setSettings((p) => ({ ...p, mtu: parseInt(e.target.value) || 1300 }))} />
          </div>
        </div>
      </div>

      {/* DNS */}
      <div style={styles.section}>
        <h3 style={styles.sectionTitle}><Globe size={16} /> DNS (DoH)</h3>
        <div style={styles.sectionBody}>
          <div style={styles.dohList}>
            {(settings.dohServers ?? []).length === 0 && (
              <div style={styles.emptyDoh}>Используются серверы по умолчанию (Google, Cloudflare, Quad9)</div>
            )}
            {(settings.dohServers ?? []).map((url) => (
              <div key={url} style={styles.dohItem}>
                <Globe size={14} style={{ color: 'var(--text-3)', flexShrink: 0 }} />
                <span style={styles.dohUrl}>{url}</span>
                <button style={styles.dohRemove} onClick={() => removeDohServer(url)} title="Удалить">✕</button>
              </div>
            ))}
          </div>
          <div style={styles.dohAdd}>
            <input style={styles.dohInput} placeholder="https://dns.example.com/dns-query" value={newDohServer} onChange={(e) => setNewDohServer(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && addDohServer()} />
            <button style={styles.dohAddBtn} onClick={addDohServer}>+ Добавить</button>
          </div>
        </div>
      </div>
    </div>
  );
}

const styles: Record<string, React.CSSProperties> = {
  container: { flex: 1, overflowY: 'auto' as const, padding: '20px 24px', background: 'var(--bg)' },
  header: { display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 },
  title: { fontSize: 18, fontWeight: 600, color: 'var(--text)', margin: 0 },
  headerActions: { display: 'flex', gap: 8 },
  resetBtn: { display: 'flex', alignItems: 'center', gap: 6, background: 'var(--bg-2)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '6px 14px', color: 'var(--text-2)', fontSize: 13, cursor: 'pointer', fontFamily: 'inherit' },
  saveBtn: { background: 'var(--accent)', border: 'none', borderRadius: 'var(--radius-sm)', padding: '6px 18px', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit', transition: 'background 0.2s' },
  section: { marginBottom: 20 },
  sectionTitle: { display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, fontWeight: 600, color: 'var(--text-2)', margin: '0 0 8px 0' },
  sectionBody: { background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '8px 0' },
  toggleRow: { display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px' },
  toggleIcon: { width: 36, height: 36, borderRadius: 'var(--radius-sm)', background: 'var(--bg-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 },
  toggleText: { flex: 1 },
  toggleLabel: { fontSize: 14, fontWeight: 500, color: 'var(--text)', marginBottom: 2 },
  toggleDesc: { fontSize: 12, color: 'var(--text-3)', lineHeight: 1.4 },
  toggleTrack: { width: 40, height: 22, borderRadius: 11, border: 'none', cursor: 'pointer', padding: 0, transition: 'background 0.2s', position: 'relative' as const },
  toggleThumb: { width: 18, height: 18, borderRadius: '50%', background: '#fff', position: 'absolute' as const, top: 2, transition: 'transform 0.2s' },
  row: { display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px' },
  rowIcon: { width: 36, height: 36, borderRadius: 'var(--radius-sm)', background: 'var(--bg-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 },
  rowText: { flex: 1 },
  rowLabel: { fontSize: 14, fontWeight: 500, color: 'var(--text)', marginBottom: 2 },
  rowDesc: { fontSize: 12, color: 'var(--text-3)', lineHeight: 1.4 },
  select: { background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '6px 12px', color: 'var(--text)', fontSize: 13, fontFamily: 'inherit', cursor: 'pointer', outline: 'none' },
  input: { width: 80, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '6px 10px', color: 'var(--text)', fontSize: 13, fontFamily: 'inherit', outline: 'none', textAlign: 'right' as const },
  dohList: { padding: '8px 16px' },
  emptyDoh: { fontSize: 13, color: 'var(--text-3)', padding: '8px 0' },
  dohItem: { display: 'flex', alignItems: 'center', gap: 8, padding: '6px 0' },
  dohUrl: { flex: 1, fontSize: 13, color: 'var(--text-2)', fontFamily: "'Cascadia Code', monospace" },
  dohRemove: { background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-4)', fontSize: 14, padding: 2, borderRadius: 4, transition: 'color 0.15s' },
  dohAdd: { display: 'flex', gap: 8, padding: '8px 16px 12px' },
  dohInput: { flex: 1, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '6px 12px', color: 'var(--text)', fontSize: 13, fontFamily: "'Cascadia Code', monospace", outline: 'none' },
  dohAddBtn: { background: 'var(--bg-2)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '6px 14px', color: 'var(--text-2)', fontSize: 13, cursor: 'pointer', fontFamily: 'inherit', whiteSpace: 'nowrap' as const },
};