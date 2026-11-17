import { useEffect, useRef, useState } from 'react';
import type { GameEvent } from '../types';
import { useGame, useGameAction } from './PanelHost';
import { Empty, Icon, JsonData } from './UI';

const eventNames: Record<string, string> = {
  'room.created': '航程创建', 'player.join': '乘客登船', 'player.ready': '确认准备',
  'phase.enter': '进入新阶段', 'phase.exit': '阶段结束', 'phase.deadline': '阶段时限',
  'game.end': '航程结束', 'clue.found': '发现线索', 'clue.publish': '公开线索',
  'clue.published': '公开线索', 'vote.cast': '提交指认', 'turn.start': '发言回合',
  'turn.pass': '结束发言', 'say': '公开发言', 'speech': '公开发言',
  'pm.send': '私信', 'pm.sent': '私信', 'tts': '语音朗读',
};
export function eventText(event: GameEvent) {
  const data = event.payload || {};
  for (const key of ['text', 'body', 'message']) if (typeof data[key] === 'string') return data[key] as string;
  if (event.type === 'player.join') return `${data.name || data.char_name || '一位乘客'}已登船。`;
  if (event.type === 'phase.enter') return String(data.title || data.phase || '进入新阶段。');
  if (event.type === 'game.end') return '本次航程已结束，真相与结局已解锁。';
  return '';
}
function isTTS(event: GameEvent) { return event.type === 'tts' || event.type.endsWith('.tts'); }
function isPM(event: GameEvent) { return /(^pm[.]|private|whisper)/i.test(event.type); }
function isDiscussion(event: GameEvent) {
  return event.public && (!!eventText(event) || /say|speech|chat|message/.test(event.type));
}
function timeLabel(at: string) {
  const date = new Date(at);
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
}
export default function Conversation() {
  const { view, events, speech } = useGame();
  const [tab, setTab] = useState<'public' | 'private' | 'log'>('public');
  const [text, setText] = useState('');
  const [target, setTarget] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const list = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);
  const say = useGameAction('say'), pm = useGameAction('pm.send');
  const action = tab === 'private' ? pm : say;
  const recipients = view.seats.filter(seat => seat.joined && seat.id !== view.pid);
  const options = pm.spec?.options?.length ? pm.spec.options : recipients.map(seat => ({ value: seat.id, label: seat.char_name }));
  const recipient = options.some(option => option.value === target) ? target : options[0]?.value || '';
  const visible = events.filter(event => {
    if (tab === 'log') return true;
    if (tab === 'private') return !event.public && (isPM(event) || isTTS(event));
    if (!isDiscussion(event)) return false;
    // Some plugins emit both say and tts; others only emit tts. Never hide the only textual copy.
    return !isTTS(event) || !events.some(other => other.public && !isTTS(other) &&
      other.actor === event.actor && Math.abs(other.seq - event.seq) <= 3 && eventText(other) === eventText(event));
  });
  useEffect(() => { if (list.current && stickToBottom.current) list.current.scrollTop = list.current.scrollHeight; }, [visible.length, tab]);
  const send = async () => {
    if (!text.trim() || action.disabled || submitting || (tab === 'private' && !recipient)) return;
    setSubmitting(true);
    try {
      await action.act(tab === 'private' ? { to: recipient, text: text.trim() } : { text: text.trim() });
      setText(''); stickToBottom.current = true;
    } catch { /* Keep the draft. The room-level alert displays the server error. */ }
    finally { setSubmitting(false); }
  };
  return <aside className="conversation" aria-labelledby="conversation-title">
    <div className="conversation-heading"><div><Icon name="message"/><h2 id="conversation-title">同行者的证词</h2></div><span className={speech.enabled ? 'sound-status active' : 'sound-status'} title={speech.enabled ? '自动朗读已开启' : '自动朗读关闭'}><Icon name={speech.enabled ? 'sound' : 'mute'} size={16}/></span></div>
    <div className="conversation-tabs" role="tablist" aria-label="对话频道" onKeyDown={event => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      const tabs = ['public', 'private', 'log'] as const;
      const current = tabs.indexOf(tab);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? 2 : (current + (event.key === 'ArrowRight' ? 1 : -1) + 3) % 3;
      setTab(tabs[next]); stickToBottom.current = true;
      event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
    }}>{(['public', 'private', 'log'] as const).map(key => <button key={key} id={`chat-tab-${key}`} role="tab" tabIndex={tab === key ? 0 : -1} aria-controls="chat-feed" aria-selected={tab === key} onClick={() => { setTab(key); stickToBottom.current = true; }}>{key === 'public' ? '公开讨论' : key === 'private' ? '私信' : '航程记录'}</button>)}</div>
    <div className="message-feed" id="chat-feed" role="tabpanel" aria-labelledby={`chat-tab-${tab}`} tabIndex={0} ref={list} onScroll={() => { if (list.current) stickToBottom.current = list.current.scrollHeight - list.current.scrollTop - list.current.clientHeight < 64; }}>
      {!visible.length ? <Empty title={tab === 'private' ? '尚无私信' : tab === 'log' ? '航程记录正在载入' : '还没有人打破沉默'}>{tab === 'private' ? '选择一位同行者，发送仅双方可见的私信。' : '发言与阶段变化将在连接后出现在这里。'}</Empty> : visible.map(event => {
        const seat = view.seats.find(item => item.id === event.actor);
        const own = event.actor === view.pid;
        const body = eventText(event);
        return <article key={event.seq} className={`message ${own ? 'mine' : ''} ${!event.public ? 'private' : ''}`}><div className="message-meta"><strong>{seat?.char_name || (event.actor === 'host' ? '主持人' : event.actor || '航程通知')}</strong>{own && <span>你</span>}<time dateTime={event.at}>{timeLabel(event.at)}</time></div>
          {tab === 'log' && <span className="event-name">{eventNames[event.type] || event.type}</span>}
          {!event.public && <p className="private-indicator"><Icon name="lock" size={12}/>仅相关玩家可见{typeof event.payload?.to === 'string' && ` · 发给 ${view.seats.find(item => item.id === event.payload?.to)?.char_name || event.payload.to}`}</p>}
          {body ? <p className="message-body">{body}</p> : <details className="event-detail"><summary>{eventNames[event.type] || event.type}</summary><JsonData data={event.payload}/></details>}
        </article>;
      })}
    </div>
    {tab !== 'log' && <form className="message-compose" onSubmit={event => { event.preventDefault(); void send(); }}>
      {tab === 'private' && <label className="recipient">发给<select value={recipient} onChange={event => setTarget(event.target.value)} disabled={pm.disabled || !options.length}><option value="" disabled>选择同行者</option>{options.map(option => <option value={option.value} key={option.value}>{option.label}</option>)}</select></label>}
      <label className="sr-only" htmlFor="speech-draft">{tab === 'private' ? '私信内容' : '公开发言内容'}</label><textarea id="speech-draft" value={text} maxLength={4000} onChange={event => setText(event.target.value)} placeholder={tab === 'private' ? '写下私信，只有收发双方可见…' : '你在这艘船上看见了什么？'} disabled={action.disabled || submitting} aria-describedby="compose-hint" onKeyDown={event => { if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) { event.preventDefault(); void send(); } }}/>
      <div className="compose-footer"><small>{text.length}/4000</small><button className="button primary small" disabled={action.disabled || submitting || !text.trim() || (tab === 'private' && !recipient)}><Icon name="send" size={16}/>{submitting ? '发送中…' : tab === 'private' ? '发送私信' : '发表证词'}</button></div><p id="compose-hint" className="compose-hint">{action.reason || (tab === 'private' ? '私信仅收发双方可见。' : 'Ctrl / ⌘ + Enter 发送；Enter 换行。')}</p>
    </form>}
  </aside>;
}
