export type TunnelState = 'idle' | 'connecting' | 'connected' | 'disconnecting' | 'reconnecting' | 'error';

export interface ServerProfile {
  name: string;
  server_url: string;
  password: string;
  device_id?: string;
  mode?: string;
  wdtt?: {
    peer?: string;
    hashes?: string[];
    listen?: string;
    turn?: string;
    port?: string;
    workers?: number;
    mtu?: number;
  };
  vless?: {
    remote_addr?: string;
    uuid?: string;
    public_key?: string;
    short_id?: string;
    fingerprint?: string;
    server_name?: string;
  };
}

export interface AppState {
  connected: boolean;
  state: string;
  mode: string;
  profileName: string;
  rxBytes: number;
  txBytes: number;
  killSwitch: boolean;
  dnsLeak: boolean;
  dpiDomains: number;
  geoDomains: number;
}

export interface Settings {
  killSwitch: boolean;
  dnsLeak: boolean;
  autoConnect: boolean;
  autoStart: boolean;
  tray: boolean;
  mode: string;
  mtu: number;
  dohServers?: string[];
}

export interface LogEntry {
  level: string;
  message: string;
  time: string;
}