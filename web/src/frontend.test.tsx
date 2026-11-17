import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, joinedSession, loadSession, request, socketUrl, storeSession } from './api';
import type { GameEvent, Session, View } from './types';
import { chooseChineseVoice, eventSpeech, useSpeech, type SpeechControls } from './hooks/useSpeech';
import { mergeEvents, useRoom } from './hooks/useRoom';
import { CluesPanel, GameContext, PanelHost, ReadyPanel, TurnPanel, VotesPanel, panelCategory } from './components/PanelHost';
import Conversation from './components/Conversation';
import App from './App';

const session: Session = { room_id: 'ocean', token: 'player-secret', host_token: 'host-secret', player_id: 'captain' };
const makeView = (state = 1): View => ({
  room_id: 'ocean', state_id: state, last_seq: 100,
  meta: { id: 'east', title: '东方之星', players: 2, intro: '午夜发生了一桩命案。', lang: 'zh-CN' },
  phase: { id: 'investigate', type: 'search', title: '甲板调查', index: 2, act: 1 },
  pid: 'captain', ended: false, seats: [
    { id: 'captain', name: '测试乘客', char_name: '船长', public_intro: '掌舵的人。', kind: 'human', joined: true, ready: true },
    { id: 'doctor', name: '医生', char_name: '船医', public_intro: '沉默的船医。', kind: 'ai', joined: true, ready: true },
  ],
  me: { id: 'captain', name: '船长', public_intro: '掌舵的人。', tts: { voice_hint: '中文' }, can_lie: false,
    sections: [{ act: 1, title: '午夜之前', body: '你看见了一封信。' }] },
  allowed_actions: [{ type: 'search', desc: '调查', params: { location: '地点' } }, { type: 'publish', desc: '公开线索' }],
  panels: [], data: { clues: { ap: 2, own: [{ id: 'custom-letter', title: '航海日志', body: '停航记录', location: 'custom-bridge', act: 1, visibility: 'private' }], public: [], locations: [{ id: 'custom-bridge', name: '驾驶台', remaining: 1 }] } },
});
const event = (seq: number, type = 'say'): GameEvent => ({ seq, type, at: '2026-11-17T00:00:00Z', public: true, actor: 'doctor', payload: { text: `证词 ${seq}` } });
const json = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

class FakeSocket {
  static OPEN = 1;
  static instances: FakeSocket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  send = vi.fn();
  constructor(public url: string) { FakeSocket.instances.push(this); }
  open() { this.readyState = 1; this.onopen?.(); }
  emit(data: unknown) { this.onmessage?.({ data: JSON.stringify(data) }); }
  close() { this.readyState = 3; this.onclose?.(); }
}
class FakeUtterance {
  voice: SpeechSynthesisVoice | null = null;
  lang = ''; rate = 1; pitch = 1;
  onend: (() => void) | null = null;
  onerror: ((event: { error: string }) => void) | null = null;
  constructor(public text: string) {}
}
const zhVoice = { name: '中文女声', lang: 'zh-CN', default: true } as SpeechSynthesisVoice;
const speechMock = () => ({
  getVoices: vi.fn(() => [zhVoice]), speak: vi.fn(), cancel: vi.fn(), resume: vi.fn(),
  addEventListener: vi.fn(), removeEventListener: vi.fn(),
});
beforeEach(() => { localStorage.clear(); FakeSocket.instances = []; });
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

describe('HTTP and separate credentials', () => {
  it('uses exact create and join payloads without private character data', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(json({ room_id: 'ocean', host_token: 'host-secret' }))
      .mockResolvedValueOnce(json({ room_id: 'ocean', token: 'player-secret', player_id: 'captain' }));
    vi.stubGlobal('fetch', fetchMock);
    const created = await api.create('east', ['doctor'], 'qoder');
    const joined = await api.join(created.room_id, 'captain', '小林');
    const next = joinedSession({ ...created }, joined);
    expect(next.token).toBe('player-secret'); expect(next.host_token).toBe('host-secret');
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ script_id: 'east', ai_seats: ['doctor'], llm: 'qoder' });
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ char_id: 'captain', name: '小林' });
  });
  it('never sends the host token for normal actions', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(json({ ok: true })));
    vi.stubGlobal('fetch', fetchMock);
    await api.action(session, { type: 'say', payload: { text: '你好' }, state_id: 9 });
    await api.action(session, { type: 'host.advance', state_id: 9 }, true);
    expect(fetchMock.mock.calls[0][1].headers.get('Authorization')).toBe('Bearer player-secret');
    expect(fetchMock.mock.calls[1][1].headers.get('Authorization')).toBe('Bearer host-secret');
    expect(session.token).toBe('player-secret');
  });
  it('stores and recovers only session credentials and handles corrupt storage', () => {
    storeSession(session); expect(loadSession()).toEqual(session);
    localStorage.setItem('fog-harbor.session.v1', '{'); expect(loadSession()).toBeNull();
    storeSession(null); expect(loadSession()).toBeNull();
    expect(joinedSession(session, { room_id: 'another', player_id: 'doctor', token: 'new' }).host_token).toBeUndefined();
  });
  it('makes invalid JSON and authorization errors actionable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response('<html>not api</html>'))
      .mockResolvedValueOnce(json({ error: '凭证失效' }, 401)));
    await expect(request('/api/scripts')).rejects.toThrow('服务未返回 JSON');
    await expect(request('/api/rooms/ocean/view')).rejects.toThrow('凭证失效');
  });
  it('encodes WebSocket token and event cursor separately from host credentials', () => {
    const url = new URL(socketUrl(session, 6));
    expect(url.pathname).toBe('/ws/rooms/ocean');
    expect(url.searchParams.get('token')).toBe('player-secret'); expect(url.searchParams.get('since')).toBe('6');
    expect(url.toString()).not.toContain('host-secret');
  });
});

describe('live room protocol', () => {
  function prepare() {
    const fetchMock = vi.fn().mockImplementation((path: string, init: RequestInit) => {
      if (path.includes('/log')) return Promise.resolve(json([event(1)]));
      if (path.includes('/action')) return Promise.resolve(json({ ok: true }));
      const host = new Headers(init.headers).get('Authorization') === 'Bearer host-secret';
      return Promise.resolve(json(host ? { ...makeView(), allowed_actions: [{ type: 'host.advance', desc: '推进' }] } : makeView()));
    });
    vi.stubGlobal('fetch', fetchMock); vi.stubGlobal('WebSocket', FakeSocket);
    return fetchMock;
  }
  it('does not use view.last_seq as cursor or speak historical events', async () => {
    prepare(); const speak = vi.fn();
    const { result } = renderHook(() => useRoom(session, speak));
    await waitFor(() => expect(result.current.view?.room_id).toBe('ocean'));
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const socket = FakeSocket.instances[0];
    expect(new URL(socket.url).searchParams.get('since')).toBe('1'); expect(speak).not.toHaveBeenCalled();
    act(() => { socket.open(); socket.emit({ t: 'view', view: makeView(50) }); socket.emit({ t: 'event', event: event(2, 'tts') }); socket.emit({ t: 'event', event: event(2, 'tts') }); });
    expect(result.current.events.map(item => item.seq)).toEqual([1, 2]);
    expect(speak).toHaveBeenCalledTimes(1); expect(result.current.connection).toBe('live');
    act(() => socket.emit({ t: 'view', view: makeView(2) }));
    expect(result.current.view?.state_id).toBe(50);
  });
  it('sends state-aware actions and resolves correlated acknowledgments', async () => {
    prepare(); const { result } = renderHook(() => useRoom(session, vi.fn()));
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const socket = FakeSocket.instances[0]; act(() => socket.open());
    let promise!: Promise<void>;
    act(() => { promise = result.current.send('say', { text: '我在甲板上。' }); });
    const message = JSON.parse(socket.send.mock.calls[0][0]);
    expect(message.t).toBe('action'); expect(message.action).toEqual({ type: 'say', payload: { text: '我在甲板上。' }, state_id: 1 });
    expect(message.action.actor).toBeUndefined();
    await act(async () => { socket.emit({ t: 'ack', id: message.id, ok: true }); await promise; });
    expect(result.current.busy).toBe(false);
  });
  it('rejects denied ack and retains error without automatic resubmit', async () => {
    prepare(); const { result } = renderHook(() => useRoom(session, vi.fn()));
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const socket = FakeSocket.instances[0]; act(() => socket.open());
    let promise!: Promise<void>;
    act(() => { promise = result.current.send('search', { location: 'custom-bridge' }); });
    const message = JSON.parse(socket.send.mock.calls[0][0]);
    await act(async () => {
      const rejected = expect(promise).rejects.toThrow('stale state_id');
      socket.emit({ t: 'ack', id: message.id, ok: false, error: 'stale state_id' }); await rejected;
    });
    expect(result.current.error).toBe('stale state_id'); expect(socket.send).toHaveBeenCalledTimes(1);
  });
  it('uses host HTTP authorization while keeping the player socket intact', async () => {
    const fetchMock = prepare(); const { result } = renderHook(() => useRoom(session, vi.fn()));
    await waitFor(() => expect(result.current.hostAllowed).toBe(true));
    const socket = FakeSocket.instances[0]; act(() => socket.open());
    await act(async () => { await result.current.send('host.advance', undefined, true); });
    const call = fetchMock.mock.calls.find(([path]) => path.endsWith('/action'))!;
    expect(call[1].headers.get('Authorization')).toBe('Bearer host-secret');
    expect(socket.send).not.toHaveBeenCalled(); expect(new URL(socket.url).searchParams.get('token')).toBe('player-secret');
  });
  it('reconnects with the last actually received sequence', async () => {
    prepare(); const { result } = renderHook(() => useRoom(session, vi.fn()));
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const socket = FakeSocket.instances[0];
    act(() => { socket.open(); socket.emit({ t: 'event', event: event(7) }); socket.emit({ t: 'view', view: makeView(90) }); });
    vi.useFakeTimers();
    act(() => socket.close());
    expect(result.current.connection).toBe('reconnecting');
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(FakeSocket.instances.length).toBe(2);
    expect(new URL(FakeSocket.instances[1].url).searchParams.get('since')).toBe('7');
  });
  it('merges replayed events in order without duplicate messages', () => {
    expect(mergeEvents([event(2)], [event(3), event(1), event(2)]).map(item => item.seq)).toEqual([1, 2, 3]);
  });
});

describe('browser-only Chinese speech', () => {
  it('selects Chinese voices and never substitutes an English-only voice', () => {
    const en = { name: 'English', lang: 'en-US' } as SpeechSynthesisVoice;
    const other = { name: '中文男声', lang: 'zh-CN' } as SpeechSynthesisVoice;
    expect(chooseChineseVoice([en, zhVoice, other], '男声')).toBe(other);
    expect(chooseChineseVoice([en])).toBeUndefined();
    expect(eventSpeech({ ...event(1, 'tts'), payload: { text: '你好', tts: { rate: 1.2, pitch: .8, voice_hint: '女声' } } })?.voice.rate).toBe(1.2);
    expect(eventSpeech(event(1, 'say'))).toBeNull();
  });
  it('does not speak before a click, then queues events serially with rate and pitch', () => {
    const synth = speechMock(); vi.stubGlobal('speechSynthesis', synth); vi.stubGlobal('SpeechSynthesisUtterance', FakeUtterance);
    const { result } = renderHook(useSpeech);
    act(() => result.current.onEvent(event(1, 'tts'))); expect(synth.speak).not.toHaveBeenCalled();
    act(() => result.current.toggle());
    act(() => { result.current.onEvent({ ...event(2, 'tts'), payload: { text: '第一句', voice_hint: '女声', rate: 1.3, pitch: .7 } }); result.current.onEvent(event(3, 'tts')); });
    expect(synth.speak).toHaveBeenCalledTimes(1);
    const first = synth.speak.mock.calls[0][0] as FakeUtterance;
    expect(first.text).toBe('第一句'); expect(first.rate).toBe(1.3); expect(first.pitch).toBe(.7); expect(first.voice).toBe(zhVoice);
    act(() => first.onend?.()); expect(synth.speak).toHaveBeenCalledTimes(2);
    act(() => result.current.toggle()); expect(synth.cancel).toHaveBeenCalled(); expect(result.current.enabled).toBe(false);
  });
  it('applies the user-selected reading speed to new speech', () => {
    const synth = speechMock(); vi.stubGlobal('speechSynthesis', synth); vi.stubGlobal('SpeechSynthesisUtterance', FakeUtterance);
    const { result } = renderHook(useSpeech);
    act(() => result.current.changeRate(1.5));
    act(() => result.current.read('加速朗读', { rate: 1.2 }));
    expect(synth.speak.mock.calls[0][0].rate).toBeCloseTo(1.8);
  });
  it('manual script reading activates speech and stop clears the queue', () => {
    const synth = speechMock(); vi.stubGlobal('speechSynthesis', synth); vi.stubGlobal('SpeechSynthesisUtterance', FakeUtterance);
    const { result } = renderHook(useSpeech);
    act(() => result.current.read('我的剧本', { rate: 99, pitch: -5 }));
    const spoken = synth.speak.mock.calls[0][0] as FakeUtterance;
    expect(result.current.enabled).toBe(true); expect(spoken.rate).toBe(10); expect(spoken.pitch).toBe(0);
    act(() => result.current.stop()); expect(result.current.speaking).toBe(false);
  });
  it('shows an explicit unsupported state and does not invent fallback audio', () => {
    vi.stubGlobal('speechSynthesis', undefined); vi.stubGlobal('SpeechSynthesisUtterance', undefined);
    // Remove properties entirely; browser support is an API existence check.
    delete (window as unknown as Record<string, unknown>).speechSynthesis;
    delete (window as unknown as Record<string, unknown>).SpeechSynthesisUtterance;
    const { result } = renderHook(useSpeech);
    expect(result.current.supported).toBe(false);
    act(() => result.current.read('剧本'));
    expect(result.current.error).toContain('不支持 Web Speech');
  });
  it('recovers from a speech error and ignores stale utterance callbacks', () => {
    const synth = speechMock(); vi.stubGlobal('speechSynthesis', synth); vi.stubGlobal('SpeechSynthesisUtterance', FakeUtterance);
    const { result } = renderHook(useSpeech);
    act(() => result.current.read('第一句'));
    const first = synth.speak.mock.calls[0][0] as FakeUtterance;
    act(() => { result.current.onEvent(event(2, 'tts')); first.onerror?.({ error: 'network' }); });
    expect(result.current.speaking).toBe(false); expect(result.current.error).toContain('network');
    act(() => result.current.read('重试朗读'));
    expect(synth.speak).toHaveBeenCalledTimes(2); expect(result.current.error).toBe('');
    act(() => first.onend?.()); expect(result.current.speaking).toBe(true);
    act(() => (synth.speak.mock.calls[1][0] as FakeUtterance).onend?.());
    expect(result.current.speaking).toBe(false); expect(synth.speak).toHaveBeenCalledTimes(2);
  });
  it('allows reading after Chinese voices become available', () => {
    const synth = speechMock(); synth.getVoices.mockReturnValue([]);
    vi.stubGlobal('speechSynthesis', synth); vi.stubGlobal('SpeechSynthesisUtterance', FakeUtterance);
    const { result } = renderHook(useSpeech);
    act(() => result.current.read('暂时没有音色'));
    expect(synth.speak).not.toHaveBeenCalled(); expect(result.current.error).toContain('未提供中文语音');
    synth.getVoices.mockReturnValue([zhVoice]);
    act(() => result.current.read('音色已可用'));
    expect(result.current.error).toBe(''); expect(synth.speak).toHaveBeenCalledTimes(1);
    expect((synth.speak.mock.calls[0][0] as FakeUtterance).voice).toBe(zhVoice);
  });
  it('explains missing Chinese voices instead of selecting non-Chinese speech', () => {
    const synth = speechMock(); synth.getVoices.mockReturnValue([]);
    vi.stubGlobal('speechSynthesis', synth); vi.stubGlobal('SpeechSynthesisUtterance', FakeUtterance);
    const { result } = renderHook(useSpeech);
    act(() => result.current.read('中文剧本'));
    expect(synth.speak).not.toHaveBeenCalled(); expect(result.current.error).toContain('未提供中文语音');
  });
});

describe('semantic panels and canonical action parameters', () => {
  function inGame(children: React.ReactNode, view = makeView(), events: GameEvent[] = []) {
    const send = vi.fn().mockResolvedValue(undefined);
    const speech = { supported: false, enabled: false, speaking: false, voiceCount: 0, rate: 1, changeRate: vi.fn(), error: '', toggle: vi.fn(), stop: vi.fn(), read: vi.fn(), onEvent: vi.fn() } as SpeechControls;
    render(<GameContext.Provider value={{ view, events, send, busy: false, speech, player: true }}>{children}</GameContext.Provider>);
    return send;
  }
  it('routes custom panel IDs by kind/plugin and keeps unknown JSON', () => {
    expect(panelCategory({ id: 'anything', kind: 'investigation', plugin: 'custom', title: '调查', order: 2 })).toBe('clues');
    expect(panelCategory({ id: 'not-vote', kind: 'ballot', plugin: 'custom', title: '投票', order: 3 })).toBe('votes');
    inGame(<PanelHost panel={{ id: 'arbitrary', plugin: 'other', kind: 'device', title: '无线电', data_key: 'radio', order: 9 }}/>, { ...makeView(), data: { radio: { channel: '海事频道' } } });
    expect(screen.getByText(/海事频道/).textContent).toContain('channel');
  });
  it('sends search.location and publish.clue even for custom IDs', async () => {
    const send = inGame(<CluesPanel/>);
    fireEvent.click(screen.getByRole('button', { name: /驾驶台.*待发现/ }));
    fireEvent.click(screen.getByRole('button', { name: '调查此处' }));
    await waitFor(() => expect(send).toHaveBeenCalledWith('search', { location: 'custom-bridge' }));
    fireEvent.click(screen.getByRole('button', { name: '公开这条线索' }));
    expect(send).toHaveBeenCalledWith('publish', { clue: 'custom-letter' });
  });
  it('sends vote.question and vote.option using server IDs', () => {
    const view = makeView(); view.allowed_actions = [{ type: 'vote.cast', desc: '指认' }];
    view.data.votes = { questions: [{ id: 'custom-culprit', question: '谁是凶手？', options: [{ value: 'custom-doctor', label: '船医' }] }], cast: {} };
    const send = inGame(<VotesPanel/>, view);
    fireEvent.click(screen.getByRole('radio', { name: '船医' }));
    fireEvent.click(screen.getByRole('button', { name: '确认指认' }));
    expect(send).toHaveBeenCalledWith('vote.cast', { question: 'custom-culprit', option: 'custom-doctor' });
  });
  it('disables unavailable investigation and explains why', () => {
    const view = makeView(); view.allowed_actions = []; view.hint = '等待下一位玩家。';
    inGame(<CluesPanel/>, view);
    expect((screen.getByRole('button', { name: '调查此处' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getAllByText('等待下一位玩家。').length).toBeGreaterThan(0);
  });
  it('sends ready and pass without inventing actor or parameters', () => {
    const view = makeView();
    view.allowed_actions = [{ type: 'ready', desc: '准备' }, { type: 'pass', desc: '结束发言' }];
    view.data.turn = { pid: 'captain', round: 1, total_rounds: 2 };
    const send = inGame(<><ReadyPanel/><TurnPanel/></>, view);
    expect(screen.getByRole('heading', { name: '轮到你发言' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '我已准备好' }));
    fireEvent.click(screen.getByRole('button', { name: '结束我的发言' }));
    expect(send).toHaveBeenCalledWith('ready', undefined);
    expect(send).toHaveBeenCalledWith('pass', undefined);
  });
  it('keeps TTS-only text readable without speech support and hides private text from public chat', () => {
    const tts = { ...event(1, 'tts'), payload: { text: '唯独这一份证词。' } };
    const privateEvent = { ...event(2, 'pm.send'), public: false, payload: { text: '这是秘密。' } };
    inGame(<Conversation/>, makeView(), [tts, privateEvent]);
    expect(screen.getByText('唯独这一份证词。')).toBeTruthy();
    expect(screen.queryByText('这是秘密。')).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: '私信' }));
    expect(screen.getByText('这是秘密。')).toBeTruthy();
    expect(screen.queryByText('唯独这一份证词。')).toBeNull();
  });
  it('sends public text and private target/text while preserving failed drafts', async () => {
    const view = makeView();
    view.allowed_actions = [{ type: 'say', desc: '发言' }, { type: 'pm.send', desc: '私信', options: [{ value: 'custom-recipient', label: '船医' }] }];
    const send = inGame(<Conversation/>, view);
    fireEvent.change(screen.getByRole('textbox', { name: '公开发言内容' }), { target: { value: '我在驾驶台。' } });
    fireEvent.click(screen.getByRole('button', { name: '发表证词' }));
    await waitFor(() => expect(send).toHaveBeenCalledWith('say', { text: '我在驾驶台。' }));
    await waitFor(() => expect((screen.getByRole('textbox', { name: '公开发言内容' }) as HTMLTextAreaElement).value).toBe(''));
    fireEvent.click(screen.getByRole('tab', { name: '私信' }));
    fireEvent.change(screen.getByRole('textbox', { name: '私信内容' }), { target: { value: '请保守秘密。' } });
    send.mockRejectedValueOnce(new Error('未发送'));
    fireEvent.click(screen.getByRole('button', { name: '发送私信' }));
    await waitFor(() => expect(send).toHaveBeenCalledWith('pm.send', { to: 'custom-recipient', text: '请保守秘密。' }));
    expect((screen.getByRole('textbox', { name: '私信内容' }) as HTMLTextAreaElement).value).toBe('请保守秘密。');
  });
});

describe('library and failure recovery', () => {
  it('renders the maritime library and a recoverable offline error, never fabricated scripts', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Failed to fetch')));
    render(<App/>);
    expect(screen.getByRole('heading', { name: '东方之星' })).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('确认后端已启动'));
    expect(screen.getByRole('button', { name: '重试' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: '尚无可用剧本' })).toBeTruthy();
  });
  it('opens publicly sourced character registration with an accessible dialog', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((path: string) => Promise.resolve(json(path === '/api/scripts' ? [{ meta: makeView().meta, characters: [{ id: 'captain', name: '船长', public_intro: '掌舵的人。', tts: {} }] }] : []))));
    render(<App/>);
    await waitFor(() => expect(screen.getByRole('button', { name: '查看角色 / 创建房间' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: '查看角色 / 创建房间' }));
    expect(screen.getByRole('dialog', { name: '登船登记 · 东方之星' })).toBeTruthy();
    expect(screen.queryByText('你看见了一封信。')).toBeNull();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });
  it('keeps a newly created host receipt even when joining a role fails', async () => {
    const hostView = { ...makeView(), pid: 'host', me: undefined, data: {},
      allowed_actions: [{ type: 'host.advance', desc: '推进' }],
      seats: makeView().seats.map(seat => ({ ...seat, joined: seat.id !== 'captain', ready: false })) };
    vi.stubGlobal('WebSocket', FakeSocket);
    vi.stubGlobal('fetch', vi.fn().mockImplementation((path: string, init?: RequestInit) => {
      if (path === '/api/scripts') return Promise.resolve(json([{ meta: makeView().meta, characters: [{ id: 'captain', name: '船长', public_intro: '掌舵的人。', tts: {} }] }]));
      if (path === '/api/rooms' && init?.method === 'POST') return Promise.resolve(json({ room_id: 'ocean', host_token: 'kept-host' }));
      if (path.endsWith('/join')) return Promise.resolve(json({ error: '角色已被占用' }, 409));
      if (path.endsWith('/view')) return Promise.resolve(json(hostView));
      return Promise.resolve(json([]));
    }));
    render(<App/>);
    await waitFor(() => expect(screen.getByRole('button', { name: '查看角色 / 创建房间' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: '查看角色 / 创建房间' }));
    fireEvent.change(screen.getByRole('textbox', { name: '你的称呼' }), { target: { value: '小林' } });
    fireEvent.click(screen.getByRole('button', { name: '创建并登船' }));
    await waitFor(() => expect(screen.getByRole('button', { name: '选择角色入座' })).toBeTruthy());
    expect(loadSession()).toEqual({ room_id: 'ocean', host_token: 'kept-host', token: undefined, player_id: undefined });
    expect(screen.getByRole('alert').textContent).toContain('主持凭证已保留');
    expect(screen.queryByText('你看见了一封信。')).toBeNull();
  });
});
