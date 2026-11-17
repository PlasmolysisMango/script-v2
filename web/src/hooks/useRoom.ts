import { useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError, errorText, socketUrl } from '../api';
import type { Connection, GameEvent, ServerMessage, Session, View } from '../types';

type Pending = { resolve: () => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> };
export function mergeEvents(previous: GameEvent[], incoming: GameEvent[]): GameEvent[] {
  const bySeq = new Map(previous.map(event => [event.seq, event]));
  for (const event of incoming) bySeq.set(event.seq, event);
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq);
}
export function useRoom(session: Session | null, onEvent: (event: GameEvent) => void) {
  const [view, setView] = useState<View | null>(null);
  const [events, setEvents] = useState<GameEvent[]>([]);
  const [connection, setConnection] = useState<Connection>('offline');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [hostAllowed, setHostAllowed] = useState(false);
  const [retryKey, setRetryKey] = useState(0);
  const currentView = useRef<View | null>(null);
  const ws = useRef<WebSocket | null>(null);
  const lastSeq = useRef(0);
  const pending = useRef(new Map<string, Pending>());
  const eventCallback = useRef(onEvent);
  const refreshRef = useRef<() => Promise<void>>(async () => {});
  const actionLock = useRef(false);
  const generation = useRef(0);
  eventCallback.current = onEvent;
  const roomID = session?.room_id, token = session?.token, hostToken = session?.host_token;

  useEffect(() => {
    const active = roomID && (token || hostToken) ? { room_id: roomID, token, host_token: hostToken } : null;
    const gen = ++generation.current;
    currentView.current = null;
    lastSeq.current = 0;
    setView(null); setEvents([]); setError(''); setHostAllowed(false);
    actionLock.current = false; setBusy(false);
    if (!active) { setConnection('offline'); return; }
    let disposed = false;
    let reconnect: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    let initialized = false;
    const controller = new AbortController();
    const applyView = (next: View) => {
      if (disposed || next.room_id !== active.room_id) return;
      if (!currentView.current || next.state_id >= currentView.current.state_id) {
        currentView.current = next; setView(next);
      }
      // last_seq on a view is NOT an event replay cursor: a view may precede missed events.
    };
    const ingest = (incoming: GameEvent[], speak: boolean) => {
      if (disposed) return;
      const sorted = [...incoming].sort((a, b) => a.seq - b.seq);
      for (const event of sorted) {
        if (event.seq > lastSeq.current) {
          lastSeq.current = event.seq;
          if (speak) eventCallback.current(event);
        }
      }
      setEvents(previous => mergeEvents(previous, sorted));
    };
    const sync = async () => {
      const [next, missed] = await Promise.all([
        api.view(active, controller.signal), api.log(active, lastSeq.current, controller.signal),
      ]);
      if (disposed) return;
      ingest(missed || [], initialized);
      initialized = true;
      applyView(next);
    };
    refreshRef.current = sync;
    const failPending = (message: string) => {
      for (const request of pending.current.values()) {
        clearTimeout(request.timer); request.reject(new Error(message));
      }
      pending.current.clear();
    };
    const schedule = () => {
      if (disposed) return;
      setConnection('reconnecting');
      reconnect = setTimeout(() => { void connect(); }, Math.min(1000 * 2 ** attempt++, 15000));
    };
    const connect = async () => {
      if (disposed) return;
      setConnection(attempt ? 'reconnecting' : 'connecting');
      try {
        await sync();
      } catch (cause) {
        if (disposed) return;
        setError(errorText(cause));
        if (cause instanceof ApiError && [401, 403, 404].includes(cause.status)) {
          setConnection('offline'); return;
        }
        schedule(); return;
      }
      if (disposed) return;
      let socket: WebSocket;
      try { socket = new WebSocket(socketUrl(active, lastSeq.current)); }
      catch (cause) { setError(errorText(cause)); schedule(); return; }
      ws.current = socket;
      socket.onopen = () => {
        if (disposed) { socket.close(); return; }
        attempt = 0; setConnection('live'); setError('');
      };
      socket.onmessage = message => {
        if (disposed) return;
        try {
          const msg = JSON.parse(String(message.data)) as ServerMessage;
          if (msg.t === 'view') applyView(msg.view);
          else if (msg.t === 'event') ingest([msg.event], true);
          else if (msg.t === 'ack') {
            const request = pending.current.get(msg.id);
            if (!request) return;
            clearTimeout(request.timer); pending.current.delete(msg.id);
            if (msg.ok) request.resolve();
            else request.reject(new Error(msg.error || '操作被拒绝，请确认当前阶段与行动权限。'));
          }
        } catch { setError('收到无法解析的房间消息，请重新连接。'); }
      };
      socket.onerror = () => {
        if (!disposed) setError('实时连接暂不可用，正在重新连接；已收到的内容仍可阅读。');
      };
      socket.onclose = () => {
        if (ws.current === socket) ws.current = null;
        if (disposed) return;
        failPending('连接中断，操作结果尚未确认。请先核对记录，勿重复提交。');
        schedule();
      };
    };
    void connect();
    return () => {
      disposed = true;
      controller.abort(); clearTimeout(reconnect);
      const socket = ws.current;
      if (socket) { socket.onclose = null; socket.close(); ws.current = null; }
      failPending('房间连接已关闭。');
      if (generation.current === gen) refreshRef.current = async () => {};
    };
  }, [roomID, token, hostToken, retryKey]);

  useEffect(() => {
    if (!roomID || !hostToken || !view) return;
    const controller = new AbortController();
    setHostAllowed(false);
    if (!token) { setHostAllowed(view.allowed_actions?.some(action => action.type === 'host.advance') || false); return; }
    api.view({ room_id: roomID, token, host_token: hostToken }, controller.signal, true)
      .then(hostView => setHostAllowed(hostView.allowed_actions?.some(action => action.type === 'host.advance') || false))
      .catch(() => { if (!controller.signal.aborted) setHostAllowed(false); });
    return () => controller.abort();
  }, [roomID, token, hostToken, view?.state_id]);

  const send = useCallback(async (type: string, payload?: Record<string, unknown>, host = false) => {
    if (!session || !currentView.current) throw new Error('房间尚未连接，请稍后重试。');
    if (host ? !session.host_token : !session.token) throw new Error(host ? '未持有主持人凭证。' : '请先选择角色加入房间。');
    if (actionLock.current) throw new Error('上一项操作正在确认，请稍后。');
    const gen = generation.current;
    actionLock.current = true; setBusy(true); setError('');
    const action = { type, payload, state_id: currentView.current.state_id };
    try {
      const socket = ws.current;
      // Host credentials are used only on this HTTP request; never replace the player's socket token.
      if (!host && socket?.readyState === WebSocket.OPEN) {
        await new Promise<void>((resolve, reject) => {
          const id = crypto.randomUUID();
          const timer = setTimeout(() => {
            pending.current.delete(id);
            reject(new Error('操作确认超时。请先刷新并核对记录，勿重复提交。'));
          }, 12000);
          pending.current.set(id, { resolve, reject, timer });
          try { socket.send(JSON.stringify({ t: 'action', id, action })); }
          catch (cause) { clearTimeout(timer); pending.current.delete(id); reject(cause); }
        });
      } else {
        await api.action(session, action, host);
      }
      try { await refreshRef.current(); } catch {
        if (generation.current === gen) setError('操作已确认，但刷新失败。请重新连接查看最新状态。');
      }
    } catch (cause) {
      if (generation.current === gen) {
        setError(errorText(cause));
        try { await refreshRef.current(); } catch { /* Retain the actionable original error. */ }
      }
      throw cause;
    } finally {
      if (generation.current === gen) { actionLock.current = false; setBusy(false); }
    }
  }, [session]);
  return { view, events, connection, error, busy, hostAllowed, send,
    retry: () => setRetryKey(key => key + 1), clearError: () => setError('') };
}
