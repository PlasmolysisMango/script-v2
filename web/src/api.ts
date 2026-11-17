import type { Action, GameEvent, RoomInfo, ScriptEntry, Session, View } from './types';

export class ApiError extends Error {
  constructor(message: string, public status: number) { super(message); this.name = 'ApiError'; }
}
export async function request<T>(path: string, init: RequestInit = {}, token?: string): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body) headers.set('Content-Type', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(path, { ...init, headers, cache: 'no-store' });
  const text = await response.text();
  let data: unknown;
  try { data = text ? JSON.parse(text) : undefined; } catch {
    throw new ApiError(response.ok ? '服务未返回 JSON。请检查 /api 的代理或部署配置。' : `服务请求失败（${response.status}）。请检查后端连接。`, response.status);
  }
  if (!response.ok) {
    const detail = data && typeof data === 'object' ? (data as Record<string, unknown>).error : data;
    throw new ApiError(typeof detail === 'string' ? detail : `请求失败（${response.status}），请重试。`, response.status);
  }
  return data as T;
}
const roomPath = (id: string) => `/api/rooms/${encodeURIComponent(id)}`;
const post = (body: unknown): RequestInit => ({ method: 'POST', body: JSON.stringify(body) });
export const api = {
  scripts: (signal?: AbortSignal) => request<ScriptEntry[]>('/api/scripts', { signal }),
  rooms: (signal?: AbortSignal) => request<RoomInfo[]>('/api/rooms', { signal }),
  create: (script_id: string, ai_seats: string[], llm: 'mock' | 'qoder') =>
    request<{ room_id: string; host_token: string }>('/api/rooms', post({ script_id, ai_seats, llm })),
  join: (id: string, char_id: string, name: string) =>
    request<{ room_id: string; player_id: string; token: string }>(`${roomPath(id)}/join`, post({ char_id, name })),
  view: (session: Session, signal?: AbortSignal, host = false) =>
    request<View>(`${roomPath(session.room_id)}/view`, { signal }, host ? session.host_token : session.token || session.host_token),
  log: (session: Session, since = 0, signal?: AbortSignal) =>
    request<GameEvent[]>(`${roomPath(session.room_id)}/log?since=${since}`, { signal }, session.token || session.host_token),
  action: (session: Session, action: Action, host = false) =>
    request<unknown>(`${roomPath(session.room_id)}/action`, post(action), host ? session.host_token : session.token),
};
export function socketUrl(session: Session, since: number): string {
  const url = new URL(`/ws/rooms/${encodeURIComponent(session.room_id)}`, window.location.href);
  url.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('token', session.token || session.host_token || '');
  url.searchParams.set('since', String(since));
  return url.toString();
}
const STORAGE_KEY = 'fog-harbor.session.v1';
export function loadSession(): Session | null {
  try {
    const value = JSON.parse(localStorage.getItem(STORAGE_KEY) || 'null') as Session | null;
    if (!value || typeof value.room_id !== 'string' || !value.room_id ||
      (typeof value.token !== 'string' && typeof value.host_token !== 'string')) return null;
    return { room_id: value.room_id, token: typeof value.token === 'string' ? value.token : undefined,
      host_token: typeof value.host_token === 'string' ? value.host_token : undefined,
      player_id: typeof value.player_id === 'string' ? value.player_id : undefined };
  } catch { return null; }
}
export function storeSession(session: Session | null): boolean {
  try {
    if (session) localStorage.setItem(STORAGE_KEY, JSON.stringify(session));
    else localStorage.removeItem(STORAGE_KEY);
    return true;
  } catch { return false; }
}
export function joinedSession(previous: Session | null, joined: { room_id: string; player_id: string; token: string }): Session {
  return { ...joined, host_token: previous?.room_id === joined.room_id ? previous.host_token : undefined };
}
export const errorText = (error: unknown) => error instanceof Error ? error.message : '操作未完成，请重试。';
