import { useState, useEffect, useRef, useCallback } from 'react';
import {
  Power,
  ChevronDown,
  ChevronUp,
  Plus,
  X,
  ArrowDownToLine,
  ArrowUpFromLine,
  Wifi,
  WifiOff,
  Loader2,
  Shield,
  Globe,
  Zap,
  Server,
} from 'lucide-react';
import { tunnelStore } from '../lib/stores/tunnelStore';
import { logStore } from '../lib/stores/logStore';
import { toastStore } from '../lib/stores/toastStore';
import type { TunnelState, ServerProfile } from '../lib/types';
import { Connect, Disconnect, GetProfiles, SaveProfile, DeleteProfile } from '../../wailsjs/go/backend/App';

// Форматирование байтов в читаемый вид
function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 Б';
  const units = ['Б', 'КБ', 'МБ', 'ГБ'];
  const k = 1024;
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const value = bytes / Math.pow(k, i);
  return `${value.toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
}

// Текст состояния туннеля
const STATE_LABEL: Record<TunnelState, string> = {
  idle: 'Подключить',
  connecting: 'Подключение...',
  connected: 'Отключить',
  disconnecting: 'Отключение...',
  reconnecting: 'Переподключение...',
  error: 'Ошибка',
};

// Иконка режима
function ModeIcon({ mode }: { mode: string }) {
  switch (mode) {
    case 'wdtt':
      return <Zap size={16} />;
    case 'vless':
      return <Shield size={16} />;
    default:
      return <Globe size={16} />;
  }
}

// Режимы подключения
const MODES = [
  { value: 'auto', label: 'Авто', desc: 'Автоматический выбор' },
  { value: 'wdtt', label: 'WDTT', desc: 'WireGuard через TURN' },
  { value: 'vless', label: 'VLESS', desc: 'VLESS + Reality' },
];

export default function Connect() {
  const [profiles, setProfiles] = useState<Record<string, ServerProfile>>({});
  const [selectedProfile, setSelectedProfile] = useState<string>('');
  const [mode, setMode] = useState('auto');
  const [listOpen, setListOpen] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [tunnelState, setTunnelState] = useState<TunnelState>(() => tunnelStore.get());
  const [rx, setRx] = useState(0);
  const [tx, setTx] = useState(0);
  const [reconnectAt, setReconnectAt] = useState(0);

  // Новые данные профиля (для модалки добавления)
  const [newName, setNewName] = useState('');
  const [newServerURL, setNewServerURL] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newMode, setNewMode] = useState('auto');
  const [newWDTTPeer, setNewWDTTPeer] = useState('');
  const [newVLESSAddr, setNewVLESSAddr] = useState('');
  const [newVLESSUUID, setNewVLESSUUID] = useState('');
  const [newVLESSPubKey, setNewVLESSPubKey] = useState('');
  const [newVLESSShortID, setNewVLESSShortID] = useState('');
  const [newVLESSSNI, setNewVLESSSNI] = useState('');

  // Подписка на события
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);
  useEffect(() => tunnelStore.subscribeStats(setRx, setTx), []);

  // Загрузка профилей при монтировании
  useEffect(() => {
    GetProfiles().then((p) => {
      if (p) {
        setProfiles(p);
        const names = Object.keys(p);
        if (names.length > 0 && !selectedProfile) {
          setSelectedProfile(names[0]);
        }
      }
    }).catch(() => {});
  }, []);

  const isConnected = tunnelState === 'connected';
  const isSpinning = tunnelState === 'connecting' || tunnelState === 'disconnecting' || tunnelState === 'reconnecting';
  const isBusy = tunnelState === 'disconnecting';

  const handleConnect = useCallback(async () => {
    if (!selectedProfile) {
      toastStore.show('Выберите профиль');
      return;
    }
    if (tunnelState === 'idle') {
      if (Date.now() < reconnectAt) {
        const secs = Math.ceil((reconnectAt - Date.now()) / 1000);
        toastStore.show(`Подождите ${secs} сек.`, 2000);
        return;
      }
      tunnelStore.set('connecting');
      logStore.clear();
      logStore.push('INFO', `Подключение: профиль=${selectedProfile}, режим=${mode}`);
      try {
        await Connect({ profileName: selectedProfile, mode });
      } catch {
        tunnelStore.set('idle');
      }
    } else {
      tunnelStore.set('disconnecting');
      await Disconnect();
      tunnelStore.set('idle');
      setReconnectAt(Date.now() + 4000);
    }
  }, [selectedProfile, mode, tunnelState, reconnectAt]);

  const handleDelete = useCallback(async (name: string) => {
    await DeleteProfile(name);
    setProfiles((prev) => {
      const next = { ...prev };
      delete next[name];
      return next;
    });
    if (selectedProfile === name) {
      const remaining = Object.keys(profiles).filter((n) => n !== name);
      setSelectedProfile(remaining[0] ?? '');
    }
    toastStore.show(`Профиль "${name}" удалён`);
  }, [selectedProfile, profiles]);

  const handleAdd = useCallback(async () => {
    if (!newName.trim() || !newServerURL.trim() || !newPassword.trim()) {
      toastStore.show('Заполните обязательные поля');
      return;
    }
    const profile: ServerProfile = {
      name: newName.trim(),
      server_url: newServerURL.trim(),
      password: newPassword,
      mode: newMode,
      wdtt: newWDTTPeer ? { peer: newWDTTPeer } : undefined,
      vless: newVLESSAddr
        ? {
            remote_addr: newVLESSAddr,
            uuid: newVLESSUUID,
            public_key: newVLESSPubKey,
            short_id: newVLESSShortID,
            server_name: newVLESSSNI,
          }
        : undefined,
    };
    await SaveProfile(newName.trim(), profile);
    const updated = await GetProfiles();
    if (updated) setProfiles(updated);
    setSelectedProfile(newName.trim());
    setShowAdd(false);
    setNewName('');
    setNewServerURL('');
    setNewPassword('');
    setNewWDTTPeer('');
    setNewVLESSAddr('');
    setNewVLESSUUID('');
    setNewVLESSPubKey('');
    setNewVLESSShortID('');
    setNewVLESSSNI('');
    toastStore.show(`Профиль "${newName}" создан`);
  }, [newName, newServerURL, newPassword, newMode, newWDTTPeer, newVLESSAddr, newVLESSUUID, newVLESSPubKey, newVLESSShortID, newVLESSSNI]);

  const powerColor = isConnected ? 'var(--green)' : isSpinning ? 'var(--orange)' : 'var(--text-3)';
  const powerGlow = isConnected ? '0 0 60px rgba(0,214,143,0.3)' : isSpinning ? '0 0 60px rgba(255,165,2,0.2)' : 'none';
  const currentProfile = profiles[selectedProfile];

  return (
    <div style={styles.main}>
      <button style={styles.addBtn} onClick={() => setShowAdd(true)} title="Добавить профиль">
        <Plus size={20} />
      </button>

      {/* Кнопка питания */}
      <div style={styles.powerContainer}>
        <button
          style={{ ...styles.powerBtn, boxShadow: powerGlow }}
          onClick={handleConnect}
          disabled={!selectedProfile || isBusy}
          title={selectedProfile ? STATE_LABEL[tunnelState] : 'Добавьте профиль'}
        >
          <div
            style={{
              ...styles.powerOrb,
              animation: isSpinning ? 'spin 2s linear infinite' : isConnected ? 'pulse 2s ease-in-out infinite' : 'none',
              borderColor: powerColor,
            }}
          >
            {isConnected ? <Wifi size={32} color="var(--green)" /> : isSpinning ? <Loader2 size={32} color="var(--orange)" /> : <WifiOff size={32} color={powerColor} />}
          </div>
        </button>
        <span style={{ ...styles.powerLabel, color: powerColor }}>
          {selectedProfile ? STATE_LABEL[tunnelState] : 'Нет профиля'}
        </span>
      </div>

      {/* Статистика */}
      {isConnected && (
        <div style={styles.statsRow}>
          <div style={styles.statItem}>
            <ArrowDownToLine size={14} color="var(--green)" />
            <span style={styles.statValue}>{formatBytes(rx)}</span>
            <span style={styles.statLabel}>↓</span>
          </div>
          <div style={styles.statItem}>
            <ArrowUpFromLine size={14} color="var(--accent)" />
            <span style={styles.statValue}>{formatBytes(tx)}</span>
            <span style={styles.statLabel}>↑</span>
          </div>
          <div style={styles.modeBadge}>
            <ModeIcon mode={mode} />
            <span>{MODES.find((m) => m.value === mode)?.label ?? mode}</span>
          </div>
        </div>
      )}

      {/* Выбор профиля и режима */}
      <div style={styles.bottomBar}>
        <div style={styles.modeSelector}>
          {MODES.map((m) => (
            <button
              key={m.value}
              style={{
                ...styles.modeBtn,
                background: mode === m.value ? 'var(--accent)' : 'var(--bg-2)',
                color: mode === m.value ? '#fff' : 'var(--text-3)',
              }}
              onClick={() => setMode(m.value)}
              title={m.desc}
              disabled={isConnected || isSpinning}
            >
              <ModeIcon mode={m.value} />
              <span style={{ fontSize: 12 }}>{m.label}</span>
            </button>
          ))}
        </div>

        {listOpen && Object.keys(profiles).length > 0 && (
          <div style={styles.profileList}>
            {Object.entries(profiles).map(([name, prof]) => (
              <div
                key={name}
                style={{ ...styles.profileItem, background: name === selectedProfile ? 'var(--bg-3)' : 'transparent' }}
                onClick={() => { setSelectedProfile(name); setListOpen(false); }}
              >
                <Server size={16} style={{ color: 'var(--text-3)', flexShrink: 0 }} />
                <span style={styles.profileName}>{name}</span>
                <span style={styles.profileUrl}>{prof.server_url}</span>
                <button style={styles.deleteBtn} onClick={(e) => { e.stopPropagation(); handleDelete(name); }} title="Удалить">
                  <X size={14} />
                </button>
              </div>
            ))}
          </div>
        )}

        <button style={styles.profileSelector} onClick={() => setListOpen((o) => !o)}>
          <Server size={18} style={{ color: 'var(--text-3)' }} />
          <span style={{ flex: 1, textAlign: 'left' }}>
            {currentProfile ? currentProfile.name : 'Нет профиля'}
          </span>
          {currentProfile && (
            <span style={styles.profileMode}>
              <ModeIcon mode={currentProfile.mode || 'auto'} />
            </span>
          )}
          {listOpen ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
        </button>
      </div>

      {/* Модалка добавления профиля */}
      {showAdd && (
        <div style={styles.modalOverlay} onClick={() => setShowAdd(false)}>
          <div style={styles.modal} onClick={(e) => e.stopPropagation()}>
            <div style={styles.modalHeader}>
              <h3 style={styles.modalTitle}>Новый профиль</h3>
              <button style={styles.modalClose} onClick={() => setShowAdd(false)}><X size={18} /></button>
            </div>
            <div style={styles.modalBody}>
              <label style={styles.label}>
                <span>Название *</span>
                <input style={styles.input} value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="Мой сервер" />
              </label>
              <label style={styles.label}>
                <span>URL API сервера *</span>
                <input style={styles.input} value={newServerURL} onChange={(e) => setNewServerURL(e.target.value)} placeholder="https://1.2.3.4:8080" />
              </label>
              <label style={styles.label}>
                <span>Пароль *</span>
                <input style={styles.input} type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} placeholder="••••••••" />
              </label>
              <label style={styles.label}>
                <span>Режим</span>
                <select style={styles.select} value={newMode} onChange={(e) => setNewMode(e.target.value)}>
                  {MODES.map((m) => (<option key={m.value} value={m.value}>{m.label} — {m.desc}</option>))}
                </select>
              </label>
              <div style={styles.divider} />
              <h4 style={styles.sectionTitle}>WDTT параметры</h4>
              <label style={styles.label}>
                <span>Peer адрес (host:port)</span>
                <input style={styles.input} value={newWDTTPeer} onChange={(e) => setNewWDTTPeer(e.target.value)} placeholder="1.2.3.4:56000" />
              </label>
              <div style={styles.divider} />
              <h4 style={styles.sectionTitle}>VLESS параметры</h4>
              <label style={styles.label}>
                <span>Адрес сервера (host:port)</span>
                <input style={styles.input} value={newVLESSAddr} onChange={(e) => setNewVLESSAddr(e.target.value)} placeholder="5.6.7.8:443" />
              </label>
              <label style={styles.label}>
                <span>UUID</span>
                <input style={styles.input} value={newVLESSUUID} onChange={(e) => setNewVLESSUUID(e.target.value)} placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" />
              </label>
              <label style={styles.label}>
                <span>Reality Public Key</span>
                <input style={styles.input} value={newVLESSPubKey} onChange={(e) => setNewVLESSPubKey(e.target.value)} placeholder="base64..." />
              </label>
              <label style={styles.label}>
                <span>Short ID</span>
                <input style={styles.input} value={newVLESSShortID} onChange={(e) => setNewVLESSShortID(e.target.value)} placeholder="12345678" />
              </label>
              <label style={styles.label}>
                <span>SNI</span>
                <input style={styles.input} value={newVLESSSNI} onChange={(e) => setNewVLESSSNI(e.target.value)} placeholder="www.microsoft.com" />
              </label>
            </div>
            <div style={styles.modalFooter}>
              <button style={styles.cancelBtn} onClick={() => setShowAdd(false)}>Отмена</button>
              <button style={styles.saveBtn} onClick={handleAdd}>Создать</button>
            </div>
          </div>
        </div>
      )}

      <Toast />
      <style>{`
        @keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
        @keyframes pulse { 0%,100% { transform: scale(1); opacity: 1; } 50% { transform: scale(1.08); opacity: 0.9; } }
        @keyframes slideDown { from { opacity: 0; transform: translateY(-8px); } to { opacity: 1; transform: translateY(0); } }
      `}</style>
    </div>
  );
}

function Toast() {
  const [msg, setMsg] = useState(() => toastStore.get());
  useEffect(() => toastStore.subscribe(setMsg), []);
  if (!msg) return null;
  return <div style={styles.toast}><span>{msg}</span></div>;
}

const styles: Record<string, React.CSSProperties> = {
  main: { flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', position: 'relative', background: 'var(--bg)', animation: 'slideDown 0.2s ease-out' },
  addBtn: { position: 'absolute', top: 16, right: 20, background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-3)', padding: 4, borderRadius: 6, transition: 'color 0.15s' },
  powerContainer: { display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12 },
  powerBtn: { width: 150, height: 150, borderRadius: '50%', border: 'none', background: 'var(--bg-2)', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', transition: 'opacity 0.2s, box-shadow 0.5s' },
  powerOrb: { width: 100, height: 100, borderRadius: '50%', border: '2px solid var(--text-4)', display: 'flex', alignItems: 'center', justifyContent: 'center', transition: 'border-color 0.3s' },
  powerLabel: { fontSize: 13, fontWeight: 500, letterSpacing: 0.3 },
  statsRow: { display: 'flex', alignItems: 'center', gap: 20, marginTop: 4 },
  statItem: { display: 'flex', alignItems: 'center', gap: 6 },
  statValue: { fontSize: 14, fontWeight: 600, color: 'var(--text)', fontVariantNumeric: 'tabular-nums' as const },
  statLabel: { fontSize: 11, color: 'var(--text-3)' },
  modeBadge: { display: 'flex', alignItems: 'center', gap: 5, fontSize: 12, color: 'var(--accent)', background: 'rgba(108,92,231,0.12)', padding: '3px 10px', borderRadius: 20 },
  bottomBar: { position: 'absolute', bottom: 24, left: '50%', transform: 'translateX(-50%)', display: 'flex', flexDirection: 'column', alignItems: 'stretch', width: 400, gap: 8 },
  modeSelector: { display: 'flex', gap: 4, background: 'var(--bg-2)', borderRadius: 'var(--radius)', padding: 3 },
  modeBtn: { flex: 1, border: 'none', borderRadius: 8, padding: '8px 0', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6, fontSize: 13, fontWeight: 500, transition: 'all 0.15s', fontFamily: 'inherit' },
  profileList: { border: '1px solid var(--border)', borderRadius: 'var(--radius)', overflow: 'hidden', background: 'var(--surface)', animation: 'slideDown 0.2s ease-out' },
  profileItem: { display: 'flex', alignItems: 'center', gap: 10, padding: '10px 16px', cursor: 'pointer', borderBottom: '1px solid var(--border-2)', transition: 'background 0.1s' },
  profileName: { flex: 1, fontSize: 14, fontWeight: 500 },
  profileUrl: { fontSize: 12, color: 'var(--text-3)', maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const },
  profileMode: { display: 'flex', color: 'var(--text-3)' },
  deleteBtn: { background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-4)', padding: 2, borderRadius: 4, opacity: 0, transition: 'opacity 0.15s, color 0.15s' },
  profileSelector: { display: 'flex', alignItems: 'center', gap: 10, background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '10px 16px', color: 'var(--text)', fontSize: 14, fontWeight: 500, cursor: 'pointer', fontFamily: 'inherit', transition: 'background 0.15s' },
  modalOverlay: { position: 'fixed' as const, inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100 },
  modal: { background: 'var(--bg-2)', border: '1px solid var(--border)', borderRadius: 16, width: 460, maxHeight: '80vh', display: 'flex', flexDirection: 'column', boxShadow: '0 24px 80px rgba(0,0,0,0.5)' },
  modalHeader: { display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '16px 20px', borderBottom: '1px solid var(--border)' },
  modalTitle: { fontSize: 16, fontWeight: 600, color: 'var(--text)' },
  modalClose: { background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-3)', padding: 4, borderRadius: 6 },
  modalBody: { padding: '16px 20px', overflowY: 'auto' as const, display: 'flex', flexDirection: 'column', gap: 12 },
  modalFooter: { display: 'flex', justifyContent: 'flex-end', gap: 8, padding: '12px 20px', borderTop: '1px solid var(--border)' },
  label: { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 13, color: 'var(--text-2)', fontWeight: 500 },
  input: { background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '8px 12px', color: 'var(--text)', fontSize: 14, outline: 'none', transition: 'border-color 0.15s', fontFamily: 'inherit' },
  select: { background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '8px 12px', color: 'var(--text)', fontSize: 14, outline: 'none', fontFamily: 'inherit', cursor: 'pointer' },
  divider: { height: 1, background: 'var(--border)', margin: '4px 0' },
  sectionTitle: { fontSize: 13, fontWeight: 600, color: 'var(--text-2)', margin: 0 },
  cancelBtn: { background: 'var(--bg-3)', border: 'none', borderRadius: 'var(--radius-sm)', padding: '8px 20px', color: 'var(--text-2)', fontSize: 13, fontWeight: 500, cursor: 'pointer', fontFamily: 'inherit' },
  saveBtn: { background: 'var(--accent)', border: 'none', borderRadius: 'var(--radius-sm)', padding: '8px 20px', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', fontFamily: 'inherit', transition: 'background 0.15s' },
  toast: { position: 'fixed' as const, bottom: 100, left: '50%', transform: 'translateX(-50%)', background: 'var(--bg-3)', border: '1px solid var(--border)', color: 'var(--text)', padding: '8px 20px', borderRadius: 'var(--radius)', fontSize: 13, fontWeight: 500, boxShadow: 'var(--shadow)', zIndex: 200, animation: 'slideDown 0.2s ease-out' },
};