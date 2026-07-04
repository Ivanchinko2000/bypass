import { useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { Power, FileText, Settings as SettingsIcon } from 'lucide-react';
import { tunnelStore } from '../lib/stores/tunnelStore';

const sidebarStyle: React.CSSProperties = {
  width: 64,
  minWidth: 64,
  background: 'var(--bg-2)',
  borderRight: '1px solid var(--border)',
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  paddingTop: 16,
  gap: 4,
};

const logoStyle: React.CSSProperties = {
  width: 36,
  height: 36,
  borderRadius: 10,
  background: 'var(--accent)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  marginBottom: 24,
  color: '#fff',
  fontWeight: 700,
  fontSize: 14,
  letterSpacing: -0.5,
};

const navButtonStyle = (active: boolean): React.CSSProperties => ({
  width: 44,
  height: 44,
  borderRadius: 'var(--radius-sm)',
  border: 'none',
  background: active ? 'var(--bg-3)' : 'transparent',
  color: active ? 'var(--text)' : 'var(--text-3)',
  cursor: 'pointer',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  transition: 'all 0.15s ease',
});

const statusDotStyle: React.CSSProperties = {
  width: 8,
  height: 8,
  borderRadius: '50%',
  background: 'var(--text-4)',
  marginTop: 'auto',
  marginBottom: 12,
  transition: 'background 0.3s ease',
};

export default function Layout() {
  const location = useLocation();
  const navigate = useNavigate();
  const [tunnelState, setTunnelState] = useState(() => tunnelStore.get());

  tunnelStore.subscribe(setTunnelState);

  const isConnected = tunnelState === 'connected';
  const isConnecting = tunnelState === 'connecting' || tunnelState === 'disconnecting';

  return (
    <div
      style={{
        display: 'flex',
        height: '100vh',
        background: 'var(--bg)',
        boxSizing: 'border-box',
      }}
    >
      {/* Боковая панель */}
      <nav style={sidebarStyle}>
        {/* Логотип */}
        <div style={logoStyle}>BV</div>

        {/* Навигация */}
        <button
          style={navButtonStyle(location.pathname === '/')}
          onClick={() => navigate('/')}
          title="Подключение"
        >
          <Power size={20} strokeWidth={1.8} />
        </button>

        <button
          style={navButtonStyle(location.pathname === '/logs')}
          onClick={() => navigate('/logs')}
          title="Логи"
        >
          <FileText size={20} strokeWidth={1.8} />
        </button>

        <button
          style={navButtonStyle(location.pathname === '/settings')}
          onClick={() => navigate('/settings')}
          title="Настройки"
        >
          <SettingsIcon size={20} strokeWidth={1.8} />
        </button>

        {/* Индикатор состояния */}
        <div
          style={{
            ...statusDotStyle,
            background: isConnected
              ? 'var(--green)'
              : isConnecting
              ? 'var(--orange)'
              : 'var(--text-4)',
          }}
        />
      </nav>

      {/* Основное содержимое */}
      <div
        style={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
        }}
      >
        <Outlet />
      </div>
    </div>
  );
}