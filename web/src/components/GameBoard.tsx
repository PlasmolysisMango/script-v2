import { useEffect, useState } from 'react';
import type { Session, View } from '../types';
import type { SpeechControls } from '../hooks/useSpeech';
import { useRoom } from '../hooks/useRoom';
import { errorText } from '../api';
import Conversation from './Conversation';
import { CluesPanel, ExtraActions, GameContext, PanelHost, ReadingPanel, RevealPanel, TurnPanel, VotesPanel, panelCategory, useGame, useGameAction } from './PanelHost';
import { Empty, ErrorBanner, Icon, Modal } from './UI';

interface Props { session: Session; speech: SpeechControls; onJoin: (room: string, character: string, name: string) => Promise<void>; onForget: () => void }
function Deadline({ value }: { value?: string }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer); }, []);
  const deadline = value ? new Date(value) : null;
  if (!deadline || deadline.getFullYear() < 2000 || Number.isNaN(deadline.getTime())) return null;
  const seconds = Math.max(0, Math.ceil((deadline.getTime() - now) / 1000));
  return <p className={`phase-deadline ${seconds < 60 ? 'urgent' : ''}`}><span>阶段剩余</span><strong>{seconds ? `${Math.floor(seconds / 60).toString().padStart(2, '0')}:${(seconds % 60).toString().padStart(2, '0')}` : '等待阶段更新'}</strong></p>;
}
function SeatSidebar({ hostAllowed, onAdvance, onChoose, onForget }: { hostAllowed: boolean; onAdvance: () => void; onChoose: () => void; onForget: () => void }) {
  const { view, player, busy } = useGame();
  const ready = useGameAction('ready');
  const turn = view.data.turn;
  return <aside className="seat-sidebar" aria-label="航程与座位"><div className="phase-plaque"><Icon name="flag" size={20}/><span>当前航程</span><h2>{view.phase.title || view.phase.type}</h2><p>第 {view.phase.act} 幕 · 阶段 {view.phase.index + 1}</p><Deadline value={view.phase.deadline}/></div>
    <div className="seat-heading"><h3>登船名单</h3><span>{view.seats.filter(seat => seat.joined).length}/{view.seats.length}</span></div><ol className="seat-list">{view.seats.map(seat => <li key={seat.id} className={`${seat.id === turn?.pid ? 'current-turn' : ''} ${seat.id === view.pid ? 'your-seat' : ''}`}><div className="seat-avatar" aria-hidden="true">{seat.char_name.slice(0, 1)}</div><div className="seat-details"><strong>{seat.char_name}{seat.id === view.pid && <small>你</small>}</strong><span>{seat.joined ? `${seat.name || '已登船'}${seat.kind === 'ai' ? ' · AI' : ''}` : '等待登船'}</span></div><span className="seat-status">{seat.id === turn?.pid ? '发言中' : seat.ready ? '已准备' : seat.joined ? '已入座' : '空位'}</span></li>)}</ol>
    {!player && !view.ended && <button className="button sidebar-button" onClick={onChoose}>选择角色登船</button>}
    {ready.allowed && <button className="button brass sidebar-button" disabled={ready.disabled} onClick={() => { void ready.act().catch(() => {}); }}><Icon name="check" size={17}/>我已准备好</button>}
    <div className="sidebar-rules"><Icon name="lock" size={15}/><p>只读服务器授权给你的内容。未解锁的剧本与线索不会预载。</p></div>
    <div className="host-console"><h3>主持人操作</h3><button className="button sidebar-button" disabled={!hostAllowed || busy || view.ended} onClick={onAdvance}><Icon name="flag" size={16}/>推进下一阶段</button><p>{view.ended ? '本次航程已结束。' : hostAllowed ? '推进前请确认所有玩家已完成当前阶段。' : '仅持有主持凭证且服务器允许时可推进。'}</p></div>
    <button className="forget-session" onClick={onForget}>清除本机登船凭证</button>
  </aside>;
}
function BoardWorkspace({ view }: { view: View }) {
  const { player } = useGame();
  const [selected, setSelected] = useState('reading');
  const reading = view.panels?.find(panel => panelCategory(panel) === 'reading');
  const clues = view.panels?.find(panel => panelCategory(panel) === 'clues');
  const otherPanels = (view.panels || []).filter(panel => panel !== reading && panel !== clues && !(panelCategory(panel) === 'turn' && view.data.turn));
  const tabs = [
    { id: 'reading', title: '我的剧本', icon: 'book', render: reading ? <PanelHost panel={reading}/> : <ReadingPanel/> },
    { id: 'clues', title: '甲板与线索', icon: 'compass', render: clues ? <PanelHost panel={clues}/> : <CluesPanel/> },
    ...otherPanels.map(panel => ({ id: `plugin:${panel.id}`, title: panel.title || '扩展内容', icon: panelCategory(panel) === 'votes' ? 'flag' : 'clue', render: <PanelHost panel={panel}/> })),
    ...(!otherPanels.some(panel => panelCategory(panel) === 'votes') && (view.data.votes || view.allowed_actions?.some(action => action.type === 'vote.cast')) ? [{ id: 'votes', title: '投票指认', icon: 'flag', render: <VotesPanel/> }] : []),
    ...(!otherPanels.some(panel => panelCategory(panel) === 'reveal') && view.data.reveal ? [{ id: 'reveal', title: '真相与结局', icon: 'anchor', render: <RevealPanel/> }] : []),
  ];
  const selectedTab = tabs.find(tab => tab.id === selected) || tabs[0];
  useEffect(() => {
    const type = view.phase.type.toLowerCase();
    const category = /reveal|ending|end/.test(type) ? 'reveal' : /vote/.test(type) ? 'votes' : /search|investigation/.test(type) ? 'clues' : 'reading';
    const panel = view.panels?.find(item => panelCategory(item) === category);
    setSelected(category === 'reading' || category === 'clues' ? category : panel ? `plugin:${panel.id}` : category);
  }, [view.phase.id]);
  return <section className="board-workspace" aria-label="角色阅读与调查"><div className="workspace-tabs" role="tablist" aria-label="航程内容" onKeyDown={event => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const current = tabs.findIndex(tab => tab.id === selectedTab.id);
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    setSelected(tabs[next].id);
    event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
  }}>{tabs.map((tab, index) => <button key={tab.id} id={`workspace-tab-${index}`} role="tab" tabIndex={selectedTab.id === tab.id ? 0 : -1} aria-controls="workspace-content" aria-selected={selectedTab.id === tab.id} onClick={() => setSelected(tab.id)}><Icon name={tab.icon} size={18}/>{tab.title}</button>)}</div>
    {view.data.turn && <TurnPanel/>}
    <div className="phase-hint" role="status"><span className={`turn-lamp ${view.data.turn?.pid === view.pid ? 'yours' : ''}`}/><p>{view.ended ? '航程已结束。你仍可回顾已解锁的剧本、线索与记录。' : view.hint || (view.data.turn?.pid === view.pid ? '轮到你发言，在对话区发表证词或结束回合。' : !player ? '你正在以主持人身份查看航程，可选择空位扮演角色。' : view.allowed_actions?.length ? '请根据当前阶段与可用操作行动。' : '当前没有可执行的角色动作，请等待其他玩家或主持人推进。')}</p></div>
    <div id="workspace-content" className="workspace-content" role="tabpanel" tabIndex={0} aria-labelledby={`workspace-tab-${tabs.indexOf(selectedTab)}`}>{selectedTab.render}<ExtraActions/></div>
  </section>;
}
export default function GameBoard({ session, speech, onJoin, onForget }: Props) {
  const room = useRoom(session, speech.onEvent);
  const [choose, setChoose] = useState(false);
  const [advance, setAdvance] = useState(false);
  const [character, setCharacter] = useState('');
  const [name, setName] = useState('');
  const [joining, setJoining] = useState(false);
  const [joinError, setJoinError] = useState('');
  const labels = { connecting: '正在连接', live: '实时连接', reconnecting: '重连中', offline: '未连接' };
  const view = room.view;
  if (!view) return <main id="main" className="room-loading"><div className="loading-room-symbol"><Icon name="anchor" size={50}/></div><h1>正在登上航船</h1><p>房间 {session.room_id} · {labels[room.connection]}</p><ErrorBanner message={room.error} retry={room.retry}/>{room.connection === 'offline' && <p>若凭证已失效，请返回大厅；清除本机凭证不会释放后端座位。</p>}<button className="text-button" onClick={onForget}>清除本机凭证</button></main>;
  const available = view.seats.filter(seat => !seat.joined);
  const selectedCharacter = available.some(seat => seat.id === character) ? character : available[0]?.id || '';
  return <GameContext.Provider value={{ view, events: room.events, send: room.send, busy: room.busy, speech, player: !!session.token }}>
    <main id="main" className="game-main"><div className="game-topline"><div><Icon name="anchor" size={23}/><h1>{view.meta.title}</h1><span className="room-number">房间 {view.room_id}</span></div><div className={`connection-state ${room.connection}`} role="status"><span/>{labels[room.connection]}{room.connection !== 'live' && <button className="text-button" onClick={room.retry}>重新连接</button>}</div></div>
      <ErrorBanner message={room.error} retry={room.retry} dismiss={room.clearError}/>
      <div className="game-layout"><SeatSidebar hostAllowed={!!session.host_token && room.hostAllowed} onChoose={() => { setJoinError(''); setChoose(true); }} onAdvance={() => setAdvance(true)} onForget={onForget}/><BoardWorkspace view={view}/><Conversation/></div>
    </main>
    {choose && <Modal title="选择空位登船" close={() => { if (!joining) setChoose(false); }}><ErrorBanner message={joinError}/>{!available.length ? <Empty title="船上已经满员">你仍可使用主持人权限管理当前航程。</Empty> : <form className="boarding-form" onSubmit={async event => {
      event.preventDefault(); setJoining(true); setJoinError('');
      try { await onJoin(view.room_id, selectedCharacter, name.trim()); setChoose(false); }
      catch (cause) { setJoinError(errorText(cause)); }
      finally { setJoining(false); }
    }}><label className="field">空位角色<select value={selectedCharacter} onChange={event => setCharacter(event.target.value)} disabled={joining}>{available.map(seat => <option key={seat.id} value={seat.id}>{seat.char_name}</option>)}</select></label><div className="character-intro"><p>{available.find(seat => seat.id === selectedCharacter)?.public_intro}</p></div><label className="field">你的称呼<input value={name} onChange={event => setName(event.target.value)} required maxLength={40} disabled={joining} autoComplete="nickname"/></label><div className="modal-actions"><button className="button primary" disabled={joining || !selectedCharacter}>{joining ? '正在登记…' : '确认登船'}</button></div></form>}</Modal>}
    {advance && <Modal title="推进下一阶段？" close={() => { if (!room.busy) setAdvance(false); }}><p>当前阶段：<strong>{view.phase.title}</strong>。请确认所有玩家完成读本、调查或讨论。推进可能结束当前回合。</p><p className="muted">此操作单独使用主持人凭证，不会改变你的玩家身份。</p><div className="modal-actions"><button className="button" disabled={room.busy} onClick={() => setAdvance(false)}>留在当前阶段</button><button className="button primary" disabled={room.busy || !room.hostAllowed} onClick={async () => { try { await room.send('host.advance', undefined, true); setAdvance(false); } catch { setAdvance(false); } }}>{room.busy ? '正在推进…' : '确认推进'}</button></div></Modal>}
  </GameContext.Provider>;
}
