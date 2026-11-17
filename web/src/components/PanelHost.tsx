import { createContext, useContext, useId, useState } from 'react';
import type { ActionSpec, CluesData, GameEvent, Panel, RevealData, SendAction, TurnData, View, VotesData } from '../types';
import type { SpeechControls } from '../hooks/useSpeech';
import { Empty, Icon, JsonData } from './UI';

interface GameContextValue { view: View; events: GameEvent[]; send: SendAction; busy: boolean; speech: SpeechControls; player: boolean }
export const GameContext = createContext<GameContextValue | null>(null);
export function useGame() {
  const value = useContext(GameContext);
  if (!value) throw new Error('GameContext missing');
  return value;
}
export function useGameAction(type: string) {
  const { view, player, busy, send } = useGame();
  const spec = view.allowed_actions?.find(action => action.type === type);
  const allowed = player && !!spec && !view.ended;
  const reason = !player ? '请先选择角色登船。' : view.ended ? '航程已结束，不能再行动。' : !spec ? view.hint || '当前阶段不允许此操作，请等待阶段或回合变化。' : busy ? '上一项操作正在确认。' : '';
  return { spec, allowed, disabled: !allowed || busy, reason, act: (payload?: Record<string, unknown>) => send(type, payload) };
}
function invoke(promise: Promise<void>) { void promise.catch(() => {}); }
export function ReadingPanel() {
  const { view, speech } = useGame();
  const ready = useGameAction('ready');
  const sections = view.me?.sections || [];
  if (!view.me) return <Empty title="这份剧本只属于角色本人">主持人可以管理航程，但不能查看玩家的私密剧本。选择空位加入后即可阅读。</Empty>;
  const me = view.me;
  const readText = sections.map(section => `${section.title}\n${section.body}`).join('\n\n');
  return <div className="reading-panel"><div className="reading-heading"><div><p className="privacy-note"><Icon name="lock" size={14}/>仅你可见</p><h2>{me.name}的剧本</h2></div><button className="button small" disabled={!speech.supported || !readText} title={!speech.supported ? '浏览器不支持语音朗读' : !readText ? '剧本尚未解锁' : '点击后使用本机中文语音朗读'} onClick={() => speech.read(readText, me.tts)}><Icon name="sound" size={17}/>朗读我的剧本</button></div>
    <p className="character-public">{me.public_intro}</p>
    {!sections.length ? <Empty title="你的秘密尚未揭开">剧本将在读本阶段解锁。先确认角色并准备，或等待主持人开始航程。</Empty> : <div className="script-pages">{sections.map((section, index) => <article key={`${section.act}-${index}`} className="script-section"><p className="act-label">第 {section.act} 幕</p><h3>{section.title}</h3><div className="prose">{section.body}</div></article>)}</div>}
    {(me.goals?.length || me.ability || me.performance) ? <details className="role-notes" open><summary>你的目标与表演提示</summary>{!!me.goals?.length && <><h4>角色目标</h4><ul>{me.goals.map((goal, index) => <li key={index}>{goal}</li>)}</ul></>}{me.ability && <><h4>角色能力</h4><p>{me.ability}</p></>}{me.performance && <><h4>表演提示</h4><p>{me.performance}</p></>}<p className="fine-print">{me.can_lie ? '你可以根据角色目标隐瞒或编造证词。' : '请遵守角色规则，如实陈述你知晓的信息。'}</p></details> : null}
    {ready.allowed && <div className="reading-ready"><p>{ready.spec?.desc || '阅读完成后，确认已准备好。'}</p><button className="button primary" disabled={ready.disabled} onClick={() => invoke(ready.act())}><Icon name="check" size={17}/>我已准备好</button></div>}
  </div>;
}
export function CluesPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const clues = (data || view.data.clues || {}) as CluesData;
  const search = useGameAction('search');
  const publish = useGameAction('publish');
  const [inventory, setInventory] = useState<'own' | 'public'>('own');
  const [selected, setSelected] = useState('');
  const locations = clues.locations || [];
  const own = clues.own || [], publicClues = clues.public || [];
  const visible = inventory === 'own' ? own : publicClues;
  const location = locations.find(item => item.id === selected);
  const investigate = () => { if (location) invoke(search.act({ location: location.id })); };
  const exhausted = location ? location.remaining <= 0 : false;
  const noAP = typeof clues.ap === 'number' && clues.ap <= 0;
  const disabledReason = !search.allowed ? search.reason : noAP ? '行动点已用完。等待下一轮调查。' : !location ? '先在甲板上选择一个调查地点。' : exhausted ? '这个地点没有剩余线索。' : '';
  return <div className="clues-panel"><div className="reading-heading"><div><h2>甲板调查</h2><p className="muted">选择地点，沿航船寻找留下的痕迹。</p></div><span className="ap-counter">行动点 <strong>{clues.ap ?? '—'}</strong></span></div>
    <div className="deck-layout"><div className="deck-map"><div className="deck-direction"><span>船首</span><Icon name="compass" size={22}/></div>
      <div className="ship-hull"><div className="deck-spine" aria-hidden="true"/>{locations.length ? <div className="deck-locations" role="group" aria-label="航船调查地点">{locations.map((item, index) => <button key={item.id} className={`deck-location ${selected === item.id ? 'selected' : ''}`} aria-pressed={selected === item.id} onClick={() => setSelected(item.id)}><span className="deck-porthole" aria-hidden="true"/><span className="deck-location-title">{item.name}</span><small>{item.remaining > 0 ? `${item.remaining} 条待发现` : '已查尽'}</small><span className="deck-cabin" aria-hidden="true">{String(index + 1).padStart(2, '0')}</span></button>)}</div> : <div className="deck-locked"><Icon name="lock" size={25}/><p>调查地点尚未开放</p><span>进入搜证阶段后查看可探索的船舱。</span></div>}</div>
      <span className="deck-stern">船尾 · 东方之星</span>
    </div><aside className="location-manifest"><h3>调查地点</h3>{locations.length ? <ul>{locations.map(item => <li key={item.id}><button aria-pressed={selected === item.id} onClick={() => setSelected(item.id)}><span>{item.name}</span><small>{item.remaining > 0 ? `${item.remaining} 条` : '无剩余'}</small></button></li>)}</ul> : <p className="muted">暂无可调查地点。</p>}
      <div className="search-order"><strong>{location?.name || '请选择一个地点'}</strong><p>{disabledReason || '消耗行动点，获取该地点的一条线索。'}</p><button className="button primary" disabled={search.disabled || !location || exhausted || noAP} aria-describedby="search-reason" onClick={investigate}><Icon name="compass" size={17}/>调查此处</button><span id="search-reason" className="sr-only">{disabledReason}</span></div>
    </aside></div>
    <div className="evidence-heading"><h3>线索档案</h3><div role="group" aria-label="切换线索范围" className="segmented"><button aria-pressed={inventory === 'own'} onClick={() => setInventory('own')}>我的线索 {own.length}</button><button aria-pressed={inventory === 'public'} onClick={() => setInventory('public')}>公开线索 {publicClues.length}</button></div></div>
    {!visible.length ? <Empty title={inventory === 'own' ? '手中还没有线索' : '还没有公开的线索'}>{inventory === 'own' ? '在调查阶段选择地点搜证。获得的私密线索由你决定是否公开。' : '玩家公开的线索会出现在这里，所有人都可以阅读。'}</Empty> : <div className="evidence-list">{visible.map(clue => {
      const published = publicClues.some(item => item.id === clue.id) || clue.visibility === 'public';
      return <article className="evidence" key={clue.id}><div className="evidence-head"><span className="evidence-seal"><Icon name="clue" size={22}/></span><div><h4>{clue.title || '未命名线索'}</h4><span>{locations.find(item => item.id === clue.location)?.name || clue.location} · 第 {clue.act} 幕</span></div><small className={published ? 'public-tag' : 'private-tag'}>{published ? '已公开' : '仅你可见'}</small></div><div className="prose">{clue.body}</div>{inventory === 'own' && !published && <div className="evidence-footer"><span>{publish.allowed ? '公开后，所有同行者都能阅读。' : publish.reason}</span><button className="button small" disabled={publish.disabled} title={publish.reason} onClick={() => invoke(publish.act({ clue: clue.id }))}>公开这条线索</button></div>}</article>;
    })}</div>}
  </div>;
}
export function TurnPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const turn = (data || view.data.turn) as TurnData | undefined;
  const pass = useGameAction('pass');
  if (!turn) return <Empty title="尚未开始轮流发言">进入讨论阶段后，这里会显示当前发言者。</Empty>;
  const seat = view.seats.find(item => item.id === turn.pid);
  return <div className="turn-summary"><Icon name="message" size={24}/><div><h3>{turn.pid === view.pid ? '轮到你发言' : `${seat?.char_name || turn.pid}正在发言`}</h3><p>第 {turn.round} / {turn.total_rounds} 轮。发言请使用右侧对话区。</p></div><button className="button small" disabled={pass.disabled} title={pass.reason} onClick={() => invoke(pass.act())}>结束我的发言</button></div>;
}
export function VotesPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const votes = (data || view.data.votes || {}) as VotesData;
  const vote = useGameAction('vote.cast');
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const id = useId();
  return <section className="votes-panel"><h2>最后的指认</h2><p className="muted">根据你的剧本、线索与证词，做出判断。以服务器确认结果为准。</p>
    {!votes.questions?.length ? <Empty title="投票尚未开放">等待讨论结束后，回答本航程的指认问题。</Empty> : votes.questions.map(question => {
      const cast = votes.cast?.[question.id];
      const confirmed = cast !== undefined;
      return <fieldset key={question.id} className="vote-question" disabled={vote.disabled || confirmed}><legend>{question.question}</legend>{question.options.map(option => <label key={option.value} className="vote-option"><input type="radio" name={`${id}-${question.id}`} checked={(cast ?? answers[question.id]) === option.value} onChange={() => setAnswers(previous => ({ ...previous, [question.id]: option.value }))}/>{option.label}</label>)}<div className="vote-submit"><button className="button primary" disabled={vote.disabled || confirmed || !answers[question.id]} onClick={() => invoke(vote.act({ question: question.id, option: answers[question.id] }))}>{confirmed ? '已提交指认' : '确认指认'}</button><span>{confirmed ? '你的答案已记录。' : vote.reason}</span></div></fieldset>;
    })}{!!votes.results?.length && <><h3>投票结果</h3><JsonData data={votes.results}/></>}
  </section>;
}
export function RevealPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const reveal = (data || view.data.reveal || {}) as RevealData;
  if (!reveal.truth && !reveal.text && !reveal.title) return <Empty title="真相仍在雾中">结局将在揭晓阶段公开，当前不会展示未解锁内容。</Empty>;
  return <article className="reveal-panel"><Icon name="anchor" size={38}/><p className="act-label">航程结局</p><h2>{reveal.title || '雾散之后'}</h2>{reveal.text && <div className="prose ending-text">{reveal.text}</div>}{reveal.truth && <section className="truth"><h3>真相</h3><div className="prose">{reveal.truth}</div></section>}{!!reveal.results?.length && <><h3>最终结果</h3><JsonData data={reveal.results}/></>}</article>;
}
export function ReadyPanel() {
  const { view } = useGame();
  const ready = useGameAction('ready');
  return <div className="ready-panel"><h2>登船确认</h2><p>各位角色准备完毕后，航程就可以开始。</p><ul>{view.seats.map(seat => <li key={seat.id}><span>{seat.char_name}</span><span>{!seat.joined ? '等待登船' : seat.ready ? '已准备' : '正在准备'}</span></li>)}</ul><button className="button primary" disabled={ready.disabled} title={ready.reason} onClick={() => invoke(ready.act())}>我已准备好</button><p className="muted">{ready.reason}</p></div>;
}
export type PanelCategory = 'reading' | 'clues' | 'votes' | 'reveal' | 'turn' | 'ready' | 'conversation' | 'generic';
export function panelCategory(panel: Panel): PanelCategory {
  // IDs are opaque. Plugins choose their own descriptor IDs; route by semantic kind/plugin/data_key.
  const keys = [panel.kind, panel.plugin, panel.data_key || ''].map(key => key.toLowerCase().replace(/[._-]/g, ''));
  const aliases: [PanelCategory, string[]][] = [
    ['reading', ['reading', 'read', 'script', 'character', 'characterreading', 'characterbook', 'scriptreading', 'reader']],
    ['clues', ['clues', 'clue', 'search', 'investigation', 'evidence', 'cluesearch']],
    ['votes', ['votes', 'vote', 'voting', 'ballot']],
    ['reveal', ['reveal', 'truth', 'ending', 'endgame', 'results']],
    ['turn', ['turn', 'discussion', 'speech', 'rounds', 'turntaking']],
    ['ready', ['ready', 'lobby', 'seats', 'waiting']],
    ['conversation', ['chat', 'conversation', 'pm', 'privatechat', 'privatemessage', 'messaging']],
  ];
  // Prefer a meaningful kind over a broad plugin name.
  for (const key of keys) for (const [category, names] of aliases) if (names.includes(key)) return category;
  return 'generic';
}
export function PanelHost({ panel }: { panel: Panel }) {
  const { view } = useGame();
  const data = panel.data_key ? view.data[panel.data_key] : undefined;
  switch (panelCategory(panel)) {
    case 'reading': return <ReadingPanel/>;
    case 'clues': return <CluesPanel data={data}/>;
    case 'votes': return <VotesPanel data={data}/>;
    case 'reveal': return <RevealPanel data={data}/>;
    case 'turn': return <TurnPanel data={data}/>;
    case 'ready': return <ReadyPanel/>;
    case 'conversation': return <section><h2>{panel.title}</h2><p className="muted">公开发言与私信位于对话区。私信仅收发双方可见。</p>{data !== undefined && <JsonData data={data}/>}</section>;
    default: return <section className="plugin-panel"><h2>{panel.title || '扩展内容'}</h2><p className="muted">此扩展尚无专用界面，以下保留服务器提供的原始内容。</p><JsonData data={data === undefined ? { panel, data: null } : data}/></section>;
  }
}
const builtInActions = new Set(['ready', 'say', 'pass', 'search', 'publish', 'vote.cast', 'pm.send', 'host.advance']);
export function ExtraActions() {
  const { view } = useGame();
  return <>{view.allowed_actions?.filter(action => !builtInActions.has(action.type)).map(action => <GenericAction key={action.type} spec={action}/>)}</>;
}
function GenericAction({ spec }: { spec: ActionSpec }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const action = useGameAction(spec.type);
  const fields = Object.entries(spec.params || {});
  return <form className="generic-action" onSubmit={event => { event.preventDefault(); invoke(action.act(values)); }}><h3>{spec.desc || spec.type}</h3>{fields.map(([key, description]) => <label className="field" key={key}>{description || key}{spec.options?.length === 0 || !spec.options ? <input value={values[key] || ''} required={!spec.optional} onChange={event => setValues(previous => ({ ...previous, [key]: event.target.value }))}/> : <select required={!spec.optional} value={values[key] || ''} onChange={event => setValues(previous => ({ ...previous, [key]: event.target.value }))}><option value="">请选择</option>{spec.options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>}</label>)}<button className="button" disabled={action.disabled}>执行操作</button></form>;
}
