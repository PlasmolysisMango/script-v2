import { createContext, useContext, useId, useState } from 'react';
import type {
  ActionSpec, CardsData, Clue, CluesData, DiceData, GameEvent, HostData, MediaData, Panel, PointsData,
  RevealData, SendAction, TurnData, View, VotesData,
} from '../types';
import type { SpeechControls } from '../hooks/useSpeech';
import { Empty, Icon, JsonData } from './UI';
import { Markdown, stripMarkdown } from './Markdown';

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
  const reason = !player ? '请先选择角色加入。' : view.ended ? '本局已结束，不能再行动。' : !spec ? view.hint || '当前阶段不允许此操作，请等待阶段或回合变化。' : busy ? '上一项操作正在确认。' : '';
  return { spec, allowed, disabled: !allowed || busy, reason, act: (payload?: Record<string, unknown>) => send(type, payload) };
}
function invoke(promise: Promise<void>) { void promise.catch(() => {}); }
function seatName(view: View, id?: string): string {
  if (!id) return '—';
  if (id === 'host') return '主持人';
  const seat = view.seats.find(item => item.id === id);
  return seat ? `${seat.char_name}${seat.name ? `（${seat.name}）` : ''}` : id;
}
export function ReadingPanel() {
  const { view, speech } = useGame();
  const ready = useGameAction('ready');
  const sections = view.me?.sections || [];
  if (!view.me) return <Empty title="这份剧本只属于角色本人">主持人可以管理流程，但不能查看玩家的私密剧本。选择空位加入后即可阅读。</Empty>;
  const me = view.me;
  const readText = sections.map(section => `${section.title}\n${stripMarkdown(section.body)}`).join('\n\n');
  return <div className="reading-panel"><div className="reading-heading"><div><p className="privacy-note"><Icon name="lock" size={14}/>仅你可见</p><h2>{me.name}的剧本</h2></div><button className="button small" disabled={!speech.supported || !readText} title={!speech.supported ? '浏览器不支持语音朗读' : !readText ? '剧本尚未解锁' : '点击后使用本机中文语音朗读'} onClick={() => speech.read(readText, me.tts)}><Icon name="sound" size={17}/>朗读我的剧本</button></div>
    <p className="character-public">{me.public_intro}</p>
    {(me.points !== undefined || me.letter || me.floor) && <div className="character-tags">{me.points !== undefined && <span className="pill">初始积分 {me.points}</span>}{me.letter && <span className="pill">身份字母 {me.letter}</span>}{me.floor && <span className="pill">楼层 {me.floor}</span>}</div>}
    {!sections.length ? <Empty title="你的秘密尚未揭开">剧本将在读本阶段解锁。先确认角色并准备，或等待主持人开始。</Empty> : <div className="script-pages">{sections.map((section, index) => <article key={`${section.act}-${index}`} className="script-section"><p className="act-label">第 {section.act} 幕</p><h3>{section.title}</h3><Markdown>{section.body}</Markdown></article>)}</div>}
    {(me.goals?.length || me.ability || me.performance) ? <details className="role-notes" open><summary>你的目标与表演提示</summary>{!!me.goals?.length && <><h4>角色目标</h4><ul>{me.goals.map((goal, index) => <li key={index}>{goal}</li>)}</ul></>}{me.ability && <><h4>角色能力</h4><p>{me.ability}</p></>}{me.performance && <><h4>表演提示</h4><p>{me.performance}</p></>}<p className="fine-print">{me.can_lie ? '你可以根据角色目标隐瞒或编造证词。' : '请遵守角色规则，如实陈述你知晓的信息。'}</p></details> : null}
    {me.skill && <section className="skill-note"><h3>角色技能 · {me.skill.name}</h3><p>{me.skill.text}</p>{me.skill.cost ? <p className="fine-print">消耗 {me.skill.cost} 点</p> : null}</section>}
    {ready.allowed && <div className="reading-ready"><p>{ready.spec?.desc || '阅读完成后，确认已准备好。'}</p><button className="button primary" disabled={ready.disabled} onClick={() => invoke(ready.act())}><Icon name="check" size={17}/>我已准备好</button></div>}
  </div>;
}

function ClueCard({ clue, view, onPublish, canPublish, publishReason }: { clue: Clue; view: View; onPublish: () => void; canPublish: boolean; publishReason: string }) {
  const published = clue.visibility === 'public' || false;
  const location = clue.location ? view.data.clues?.locations?.find(item => item.id === clue.location)?.name || clue.location : '';
  return <article className="evidence"><div className="evidence-head"><span className="evidence-seal"><Icon name="clue" size={22}/></span><div><h4>{clue.title || '未命名线索'}{clue.number ? ` · ${clue.number}` : ''}</h4><span>{[location, clue.kind, `第 ${clue.act} 幕`].filter(Boolean).join(' · ')}</span></div><small className={published ? 'public-tag' : 'private-tag'}>{published ? '已公开' : '仅你可见'}</small></div>
    {clue.image_url && <img className="clue-image" src={clue.image_url} alt={clue.title || '线索图片'} loading="lazy"/>}
    <Markdown>{clue.body}</Markdown>
    {clue.keywords?.length ? <p className="fine-print">关键词：{clue.keywords.join('、')}</p> : null}
    {!published && <div className="evidence-footer"><span>{canPublish ? '公开后，所有同行者都能阅读。' : publishReason}</span><button className="button small" disabled={!canPublish} title={publishReason} onClick={onPublish}>公开这条线索</button></div>}
  </article>;
}

export function CluesPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const clues = (data || view.data.clues || {}) as CluesData;
  const search = useGameAction('search');
  const publish = useGameAction('publish');
  const [inventory, setInventory] = useState<'own' | 'public'>('own');
  const [selected, setSelected] = useState('');
  const [kind, setKind] = useState('');
  const locations = clues.locations || [];
  const own = clues.own || [], publicClues = clues.public || [];
  const visible = inventory === 'own' ? own : publicClues;
  const mode = clues.mode || (clues.pool !== undefined || clues.kinds ? 'pool' : 'location');
  const location = locations.find(item => item.id === selected);
  const pool = clues.pool || [];
  const poolItem = pool.find(item => item.id === selected);
  const kinds = Object.entries(clues.kinds || {}).filter(([, count]) => count > 0);
  const picks = clues.picks_left ?? clues.quota ?? clues.ap ?? 0;
  const myTurn = !clues.current || clues.current === view.pid;
  const exhausted = mode === 'location' ? (location ? location.remaining <= 0 : false) : picks <= 0 || pool.length === 0;
  const disabledReason = !search.allowed ? search.reason : !myTurn ? `等待 ${seatName(view, clues.current)} 完成调查。` : exhausted ? (picks <= 0 ? '本阶段调查次数已用完。' : '线索池已经抽空。') : mode === 'pool' && !poolItem && !kind ? '可直接随机抽取，或选择类别 / 线索。' : '';
  const investigate = () => {
    if (mode === 'location') { if (location) invoke(search.act({ location: location.id })); return; }
    const payload: Record<string, unknown> = {};
    if (poolItem && selected) payload.clue = poolItem.id;
    else if (kind) payload.kind = kind;
    invoke(search.act(payload));
  };
  const canInvestigate = search.allowed && myTurn && !exhausted && (mode === 'location' ? !!location : true);
  const heading = view.meta.id === 'eastern-star' ? '甲板调查' : '调查线索';
  return <div className="clues-panel"><div className="reading-heading"><div><h2>{heading}</h2><p className="muted">{mode === 'pool' ? '按顺位调查线索池；可选择线索、按类别抽取，或随机抽取。' : '选择地点，寻找留下的痕迹。'}</p></div><span className="ap-counter">剩余调查 <strong>{picks}</strong></span></div>
    {mode === 'pool' ? <div className="pool-layout">
      <div className="pool-main"><div className="pool-status"><span>线索池 {pool.length} 条</span>{clues.current && <span className={myTurn ? 'mine' : ''}>{myTurn ? '轮到你了' : `当前：${seatName(view, clues.current)}`}</span>}{clues.done && <span>本阶段已结束</span>}</div>
        {kinds.length > 0 && <div className="kind-row" role="group" aria-label="线索类别">{kinds.map(([name, count]) => <button key={name} className={`kind-chip ${kind === name ? 'selected' : ''}`} aria-pressed={kind === name} onClick={() => { setKind(kind === name ? '' : name); setSelected(''); }}>{name}<small>{count}</small></button>)}</div>}
        {pool.length ? <ul className="pool-list">{pool.map(item => <li key={item.id}><button aria-pressed={selected === item.id} onClick={() => { setSelected(item.id); setKind(''); }}><span className="pool-number">{item.number ? String(item.number).padStart(2, '0') : '—'}</span><span>{item.title || item.id}</span>{item.kind && <small>{item.kind}</small>}</button></li>)}</ul> : <Empty title="线索池已空">等待主持人推进或分配剩余线索。</Empty>}
      </div>
      <aside className="location-manifest"><h3>{poolItem ? poolItem.title : kind ? `类别：${kind}` : '随机调查'}</h3><p>{disabledReason || (poolItem ? '确认调查这条线索。' : kind ? '按类别随机抽取一条线索。' : '不知道选什么时，直接从池中随机抽取。')}</p><button className="button primary" disabled={!canInvestigate} onClick={investigate}><Icon name="compass" size={17}/>{poolItem ? '调查此线索' : kind ? '按类别抽取' : '随机抽取'}</button>{selected && <button className="text-button" onClick={() => { setSelected(''); setKind(''); }}>清除选择</button>}</aside>
    </div> : <div className="deck-layout"><div className="deck-map">{view.meta.id === 'eastern-star' && <div className="deck-direction"><span>船首</span><Icon name="compass" size={22}/></div>}
      <div className={view.meta.id === 'eastern-star' ? 'ship-hull' : 'plain-locations'}>{view.meta.id === 'eastern-star' && <div className="deck-spine" aria-hidden="true"/>}{locations.length ? <div className={view.meta.id === 'eastern-star' ? 'deck-locations' : 'location-grid'} role="group" aria-label="调查地点">{locations.map((item, index) => <button key={item.id} className={`${view.meta.id === 'eastern-star' ? 'deck-location' : 'location-card'} ${selected === item.id ? 'selected' : ''}`} aria-pressed={selected === item.id} onClick={() => setSelected(item.id)}>{view.meta.id === 'eastern-star' && <span className="deck-porthole" aria-hidden="true"/>}<span className="deck-location-title">{item.name}</span><small>{item.remaining > 0 ? `${item.remaining} 条待发现` : '已查尽'}</small>{view.meta.id === 'eastern-star' && <span className="deck-cabin" aria-hidden="true">{String(index + 1).padStart(2, '0')}</span>}</button>)}</div> : <div className="deck-locked"><Icon name="lock" size={25}/><p>调查地点尚未开放</p><span>进入搜证阶段后查看可探索的地点。</span></div>}</div>
      {view.meta.id === 'eastern-star' && <span className="deck-stern">船尾 · 东方之星</span>}
    </div><aside className="location-manifest"><h3>调查地点</h3>{locations.length ? <ul>{locations.map(item => <li key={item.id}><button aria-pressed={selected === item.id} onClick={() => setSelected(item.id)}><span>{item.name}</span><small>{item.remaining > 0 ? `${item.remaining} 条` : '无剩余'}</small></button></li>)}</ul> : <p className="muted">暂无可调查地点。</p>}
      <div className="search-order"><strong>{location?.name || '请选择一个地点'}</strong><p>{disabledReason || '消耗行动点，获取该地点的一条线索。'}</p><button className="button primary" disabled={!canInvestigate || exhausted} aria-describedby="search-reason" onClick={investigate}><Icon name="compass" size={17}/>调查此处</button><span id="search-reason" className="sr-only">{disabledReason}</span></div>
    </aside></div>}
    <div className="evidence-heading"><h3>线索档案</h3><div role="group" aria-label="切换线索范围" className="segmented"><button aria-pressed={inventory === 'own'} onClick={() => setInventory('own')}>我的线索 {own.length}</button><button aria-pressed={inventory === 'public'} onClick={() => setInventory('public')}>公开线索 {publicClues.length}</button></div></div>
    {!!clues.items?.length && <p className="item-row"><strong>道具：</strong>{clues.items.join('、')}</p>}
    {!visible.length ? <Empty title={inventory === 'own' ? '手中还没有线索' : '还没有公开的线索'}>{inventory === 'own' ? '在调查阶段获取线索。获得的私密线索由你决定是否公开。' : '玩家公开的线索会出现在这里，所有人都可以阅读。'}</Empty> : <div className="evidence-list">{visible.map(clue => <ClueCard key={clue.id} clue={clue} view={view} canPublish={publish.allowed && inventory === 'own'} publishReason={publish.allowed ? '' : publish.reason} onPublish={() => invoke(publish.act({ clue: clue.id }))}/>)}</div>}
  </div>;
}

export function TurnPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const turn = (data || view.data.turn) as TurnData | undefined;
  const pass = useGameAction('pass');
  if (!turn) return <Empty title="尚未开始轮流发言">进入讨论阶段后，这里会显示当前发言者。</Empty>;
  return <div className="turn-summary"><Icon name="message" size={24}/><div><h3>{turn.pid === view.pid ? '轮到你发言' : `${seatName(view, turn.pid)}正在发言`}</h3><p>第 {turn.round} / {turn.total_rounds} 轮。发言请使用右侧对话区。</p></div><button className="button small" disabled={pass.disabled} title={pass.reason} onClick={() => invoke(pass.act())}>结束我的发言</button></div>;
}

function ResultsList({ results }: { results?: { question: string; winner: string; counts?: Record<string, number>; correct?: boolean }[] }) {
  if (!results?.length) return null;
  return <ul className="result-list">{results.map((result, index) => <li key={`${result.question}-${index}`}><strong>{result.question}</strong><span>最多票：{result.winner || '—'}{result.correct !== undefined && <em className={result.correct ? 'right' : 'wrong'}>{result.correct ? '指认正确' : '指认错误'}</em>}</span>{result.counts && <small>{Object.entries(result.counts).map(([option, count]) => `${option} × ${count}`).join(' · ')}</small>}</li>)}</ul>;
}

export function VotesPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const votes = (data || view.data.votes || {}) as VotesData;
  const vote = useGameAction('vote.cast');
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const id = useId();
  return <section className="votes-panel"><h2>指认与投票</h2><p className="muted">根据你的剧本、线索与证词，做出判断。以服务器确认结果为准。</p>
    {!votes.questions?.length ? <Empty title="投票尚未开放">等待讨论结束后，回答本阶段的指认问题。</Empty> : votes.questions.map(question => {
      const cast = votes.cast?.[question.id];
      const confirmed = cast !== undefined;
      return <fieldset key={question.id} className="vote-question" disabled={vote.disabled || confirmed}><legend>{question.question}</legend>{question.options.map(option => <label key={option.value} className="vote-option"><input type="radio" name={`${id}-${question.id}`} checked={(cast ?? answers[question.id]) === option.value} onChange={() => setAnswers(previous => ({ ...previous, [question.id]: option.value }))}/>{option.label}</label>)}<div className="vote-submit"><button className="button primary" disabled={vote.disabled || confirmed || !answers[question.id]} onClick={() => invoke(vote.act({ question: question.id, option: answers[question.id] }))}>{confirmed ? '已提交指认' : '确认指认'}</button><span>{confirmed ? '你的答案已记录。' : vote.reason}</span></div></fieldset>;
    })}{(votes.results?.length || votes.history?.length) ? <><h3>投票结果</h3><ResultsList results={votes.results?.length ? votes.results : votes.history}/></> : null}
  </section>;
}

export function RevealPanel({ data }: { data?: unknown }) {
  const { view } = useGame();
  const reveal = (data || view.data.reveal || {}) as RevealData;
  if (!reveal.truth && !reveal.text && !reveal.title) return <Empty title="真相仍在雾中">结局将在揭晓阶段公开，当前不会展示未解锁内容。</Empty>;
  return <article className="reveal-panel"><Icon name="anchor" size={38}/><p className="act-label">结局</p><h2>{reveal.title || '谜底揭晓'}</h2>{reveal.text && <Markdown className="ending-text">{reveal.text}</Markdown>}{reveal.truth && <section className="truth"><h3>真相</h3><Markdown>{reveal.truth}</Markdown></section>}{!!reveal.results?.length && <><h3>投票结果</h3><ResultsList results={reveal.results}/></>}</article>;
}

export function ReadyPanel() {
  const { view } = useGame();
  const ready = useGameAction('ready');
  return <div className="ready-panel"><h2>准备确认</h2><p>各位角色准备完毕后，剧本就可以开始。</p><ul>{view.seats.map(seat => <li key={seat.id}><span>{seat.char_name}</span><span>{!seat.joined ? '等待加入' : seat.ready ? '已准备' : '正在准备'}</span></li>)}</ul><button className="button primary" disabled={ready.disabled} title={ready.reason} onClick={() => invoke(ready.act())}>我已准备好</button><p className="muted">{ready.reason}</p></div>;
}

export function PointsPanel({ data }: { data?: unknown }) {
  const { view, send, busy } = useGame();
  const pointsData = (data || view.data.points || {}) as PointsData;
  const spec = view.allowed_actions?.find(action => action.type === 'points.skill');
  const targetField = spec?.fields?.find(field => field.key === 'target');
  const [target, setTarget] = useState('');
  const skills = pointsData.skills || [];
  const rank = pointsData.rank || [];
  const throne = pointsData.throne ? seatName(view, pointsData.throne) : '';
  const label = pointsData.label || '积分';
  return <section className="points-panel"><div className="points-hero"><span>{label}</span><strong>{pointsData.points ?? view.me?.points ?? 0}</strong>{pointsData.settled && rank.length > 0 && <small>{rank[0]?.id === view.pid ? '当前排名第一' : `排名第 ${rank.findIndex(item => item.id === view.pid) + 1 || '—'}`}</small>}</div>
    {throne && <p className="points-throne"><Icon name="flag" size={16}/>王座 / 当前最高：{throne}</p>}
    {skills.length > 0 && <div className="skill-list"><h3>角色技能</h3>{targetField?.options?.length ? <label className="field">技能目标<select value={target} onChange={event => setTarget(event.target.value)}><option value="">请选择目标</option>{targetField.options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label> : null}{skills.map(skill => <article key={skill.id} className={`skill-card ${skill.enabled ? '' : 'disabled'}`}><div><h4>{skill.name}{skill.cost ? <small>消耗 {skill.cost}</small> : null}</h4><p>{skill.text || skill.effect}</p></div><button className="button small" disabled={!spec || !skill.enabled || busy || (targetField?.required && !target)} onClick={() => invoke(send('points.skill', target ? { skill: skill.id, target } : { skill: skill.id }))}>使用技能</button></article>)}</div>}
    {rank.length > 0 && <div className="rank-table"><h3>公开排名</h3><ol>{rank.map((entry, index) => <li key={entry.id} className={entry.id === view.pid ? 'you' : ''}><span>{index + 1}</span><span>{seatName(view, entry.id)}</span><strong>{entry.points}</strong></li>)}</ol></div>}
    {!!pointsData.host?.length && <div className="rank-table host-only"><h3>主持人视角 · 全员数值</h3><ol>{pointsData.host.map(entry => <li key={entry.id}><span>{seatName(view, entry.id)}</span><strong>{entry.points}</strong></li>)}</ol></div>}
  </section>;
}

export function DicePanel({ data }: { data?: unknown }) {
  const { view, busy } = useGame();
  const dice = (data || view.data.dice || {}) as DiceData;
  const ask = useGameAction('dice.ask');
  const answer = useGameAction('dice.answer');
  const [text, setText] = useState('');
  const rolls = Object.entries(dice.rolls || {});
  if (!dice.round && !dice.history?.length) return <Empty title="骰子尚未开始">进入骰子阶段后，这里会显示每轮点数、提问与回答。</Empty>;
  return <section className="dice-panel"><div className="dice-head"><div><p className="act-label">骰子问答</p><h2>第 {dice.round} / {dice.total} 轮</h2></div><span className="pill">{dice.sides || 6} 面骰</span></div>
    <div className="dice-rolls">{rolls.map(([pid, value]) => <div key={pid} className={value === Math.max(...rolls.map(([, v]) => v)) ? 'high' : ''}><span>{seatName(view, pid)}</span><strong>{value}</strong></div>)}</div>
    <div className="dice-pair"><span>提问者：{seatName(view, dice.asker)}</span><span>回答者：{seatName(view, dice.answerer)}</span></div>
    {dice.question && <div className="dice-question"><h3>问题</h3><p>{dice.question}</p>{dice.answer ? <><h3>回答</h3><p>{dice.answer}</p></> : null}</div>}
    {answer.allowed && <div className="dice-answer"><label className="field">你的回答<textarea value={text} onChange={event => setText(event.target.value)} rows={4} placeholder="必须包含你的必答内容（若问题命中关键词）"/></label>{!!dice.keywords && Object.keys(dice.keywords).length > 0 && <div className="keyword-hint"><strong>必答内容提示</strong>{Object.entries(dice.keywords).map(([trigger, detail]) => <p key={trigger}><span>{trigger}</span>{detail}</p>)}</div>}<button className="button primary" disabled={answer.disabled || !text.trim() || busy} onClick={() => { invoke(answer.act({ text: text.trim() })); setText(''); }}>提交回答</button></div>}
    {ask.allowed && <div className="dice-ask"><h3>选择你的提问</h3><div className="question-cards">{ask.spec?.options?.map(option => <button key={option.value} className="question-card" disabled={ask.disabled || busy} onClick={() => invoke(ask.act({ question: option.value }))}>{option.label}</button>)}</div></div>}
    {!!dice.history?.length && <details className="dice-history"><summary>已完成 {dice.history.length} 轮</summary><ol>{dice.history.map((round, index) => <li key={index}><strong>第 {index + 1} 轮</strong><span>{seatName(view, round.asker)} → {seatName(view, round.answerer)}</span><p>{round.question}</p><p className="muted">{round.answer}</p></li>)}</ol></details>}
  </section>;
}

const CARD_COLOURS = ['红', '蓝', '绿', '黄'];

export function CardsPanel({ data }: { data?: unknown }) {
  const { view, send, busy } = useGame();
  const cards = (data || view.data.cards || {}) as CardsData;
  const playSpec = view.allowed_actions?.find(action => action.type === 'card.play');
  const drawSpec = view.allowed_actions?.find(action => action.type === 'card.draw');
  const passSpec = view.allowed_actions?.find(action => action.type === 'card.pass');
  const [selected, setSelected] = useState('');
  const [colour, setColour] = useState('');
  const [target, setTarget] = useState('');
  const hand = cards.hand || [];
  const card = hand.find(item => item.id === selected);
  const playable = new Set((cards.playable || []).map(item => item.id));
  const targetOptions = (playSpec?.fields?.find(field => field.key === 'target')?.options) || view.seats.filter(seat => seat.id !== view.pid).map(seat => ({ value: seat.id, label: seat.char_name }));
  const colourOptions = (playSpec?.fields?.find(field => field.key === 'color')?.options?.map(option => option.value)) || CARD_COLOURS;
  const needsColour = card?.kind === 'recolor';
  const needsTarget = card?.kind === 'swap' || card?.kind === 'inspect';
  const playCard = () => {
    if (!card) return;
    const payload: Record<string, unknown> = { card: card.id };
    if (needsColour && colour) payload.color = colour;
    if (needsTarget && target) payload.target = target;
    invoke(send('card.play', payload));
    setSelected('');
  };
  return <section className="cards-panel"><div className="cards-head"><div><p className="act-label">卡牌调查</p><h2>出牌与线索</h2></div><div className="cards-meta"><span className="colour-chip" data-colour={cards.color}>{cards.color || '未指定颜色'}</span><span>当前：{seatName(view, cards.current)}</span><span>已完成 {cards.finished}</span></div></div>
    {cards.persona && <p className="pill">你的身份字母：{cards.persona}</p>}
    {!!cards.words?.length && <div className="word-row"><strong>未知词</strong>{cards.words.map((word, index) => <span key={`${word}-${index}`}>{word}</span>)}</div>}
    {cards.top && <p className="muted">公共出牌区：{cards.top}</p>}
    <div className="hand-grid">{hand.length ? hand.map(item => <button key={item.id} className={`hand-card ${playable.has(item.id) ? 'playable' : ''} ${selected === item.id ? 'selected' : ''}`} aria-pressed={selected === item.id} onClick={() => setSelected(item.id)}><span className="card-colour" data-colour={item.color}>{item.color || '黑'}</span><strong>{item.title || item.kind}{item.number ? ` ${item.number}` : ''}</strong>{item.text && <small>{item.text}</small>}{playable.has(item.id) && <em>可出</em>}</button>) : <Empty title="手中没有牌">本局可能已结束，或你尚未加入牌局。</Empty>}</div>
    {(playSpec || drawSpec || passSpec) && <div className="card-controls">{selected && <div className="card-options">{needsColour && <label className="field">指定颜色<select value={colour} onChange={event => setColour(event.target.value)}><option value="">请选择</option>{colourOptions.map(name => <option key={name} value={name}>{name}</option>)}</select></label>}{needsTarget && <label className="field">指定目标<select value={target} onChange={event => setTarget(event.target.value)}><option value="">请选择</option>{targetOptions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>}</div>}<button className="button primary" disabled={!playSpec || !card || busy || (needsColour && !colour) || (needsTarget && !target)} onClick={playCard}>出牌</button>{drawSpec && <button className="button" disabled={busy} onClick={() => invoke(send('card.draw'))}>抽牌</button>}{passSpec && <button className="button" disabled={busy} onClick={() => invoke(send('card.pass'))}>过牌</button>}</div>}
    {cards.score_note && <p className="score-note">{cards.score_note}</p>}
    {!!cards.host_hands && <div className="host-hands"><h3>主持人视角 · 全员手牌</h3>{Object.entries(cards.host_hands).map(([pid, list]) => <details key={pid}><summary>{seatName(view, pid)}（{list.length} 张）</summary><p>{list.map(item => `${item.color || '黑'}·${item.title || item.kind}`).join('、')}</p></details>)}</div>}
    {!!cards.ranks?.length && <p className="muted">出牌名次：{cards.ranks.map(id => seatName(view, id)).join(' → ')}</p>}
  </section>;
}

export function OpeningPanel() {
  const { view } = useGame();
  const ready = useGameAction('ready');
  return <section className="opening-panel"><p className="act-label">开场</p><h2>{view.phase.title}</h2><p className="muted">{view.meta.intro}</p><p>请阅读公开信息与角色本。主持人会宣布开场并推进到下一阶段。</p>{ready.allowed && <button className="button primary" disabled={ready.disabled} onClick={() => invoke(ready.act())}>我已准备好</button>}</section>;
}

export function MediaBanner() {
  const { view } = useGame();
  const media = view.data.media as MediaData | undefined;
  if (!media?.audio_url) return null;
  return <section className="media-banner"><div><Icon name="sound" size={20}/><div><strong>{media.audio_label || '配套音频'}</strong><p>由你决定何时播放，不会自动开始。</p></div></div><audio controls preload="none" src={media.audio_url}>您的浏览器不支持音频播放。</audio></section>;
}

export function HostPanel({ data }: { data?: unknown }) {
  const { view, send, busy } = useGame();
  const host = (data || view.data.host || {}) as HostData;
  const clues = view.data.clues as CluesData | undefined;
  const [narration, setNarration] = useState('');
  const [target, setTarget] = useState('');
  const [amount, setAmount] = useState('');
  const [clue, setClue] = useState('');
  const [order, setOrder] = useState('');
  const [publicReveal, setPublicReveal] = useState(false);
  const actions = new Set((view.allowed_actions || []).filter(action => action.type.startsWith('host.') || action.type === 'dice.roll').map(action => action.type));
  const hostSend = (type: string, payload?: Record<string, unknown>) => invoke(send(type, payload, true));
  const clueOptions = clues?.pool || [];
  const targetOptions = view.seats.filter(seat => seat.joined);
  return <section className="host-panel"><p className="privacy-note"><Icon name="lock" size={14}/>仅主持人可见</p><h2>{host.title || view.phase.title}</h2>{host.hint && <p className="muted">{host.hint}</p>}
    {host.dm_script ? <details className="dm-script" open><summary>DM 手稿</summary><Markdown>{host.dm_script}</Markdown></details> : <p className="muted">本阶段没有额外 DM 手稿。</p>}
    {!!host.facts?.length && <details className="host-facts" open><summary>真相速查</summary><ul>{host.facts.map(fact => <li key={fact.id}>{fact.text}</li>)}</ul></details>}
    {!!host.vote_ids?.length && <p className="fine-print">本阶段关联投票：{host.vote_ids.join('、')}</p>}
    <div className="host-actions"><h3>主持人操作</h3>
      {actions.has('host.narrate') && <div className="host-action"><label className="field">旁白<textarea rows={2} value={narration} onChange={event => setNarration(event.target.value)} placeholder="向所有玩家公开的旁白"/></label><button className="button small" disabled={busy || !narration.trim()} onClick={() => { hostSend('host.narrate', { text: narration.trim() }); setNarration(''); }}>发送旁白</button></div>}
      {actions.has('host.points') && <div className="host-action inline"><label className="field">对象<select value={target} onChange={event => setTarget(event.target.value)}><option value="">请选择</option>{targetOptions.map(seat => <option key={seat.id} value={seat.id}>{seat.char_name}</option>)}</select></label><label className="field">增减积分<input type="number" value={amount} onChange={event => setAmount(event.target.value)}/></label><button className="button small" disabled={busy || !target || !amount} onClick={() => { hostSend('host.points', { target, delta: Number(amount) }); setAmount(''); }}>调整积分</button></div>}
      {actions.has('host.grant_clue') && <div className="host-action inline"><label className="field">对象<select value={target} onChange={event => setTarget(event.target.value)}><option value="">请选择</option>{targetOptions.map(seat => <option key={seat.id} value={seat.id}>{seat.char_name}</option>)}</select></label><label className="field">线索 ID<input list="host-clue-ids" value={clue} onChange={event => setClue(event.target.value)}/><datalist id="host-clue-ids">{clueOptions.map(item => <option key={item.id} value={item.id}>{item.title}</option>)}</datalist></label><button className="button small" disabled={busy || !target || !clue} onClick={() => { hostSend('host.grant_clue', { target, clue }); setClue(''); }}>发放线索</button></div>}
      {actions.has('host.show_clues') && <div className="host-action inline"><label className="field">对象<select value={target} onChange={event => setTarget(event.target.value)}><option value="">请选择</option>{targetOptions.map(seat => <option key={seat.id} value={seat.id}>{seat.char_name}</option>)}</select></label><label className="check-field"><input type="checkbox" checked={publicReveal} onChange={event => setPublicReveal(event.target.checked)}/>同时公开</label><button className="button small" disabled={busy || !target} onClick={() => hostSend('host.show_clues', { target, public: publicReveal })}>展示线索</button></div>}
      {actions.has('host.extra_pick') && <div className="host-action inline"><label className="field">对象<select value={target} onChange={event => setTarget(event.target.value)}><option value="">请选择</option>{targetOptions.map(seat => <option key={seat.id} value={seat.id}>{seat.char_name}</option>)}</select></label><label className="field">次数<input type="number" value={amount} onChange={event => setAmount(event.target.value)}/></label><button className="button small" disabled={busy || !target || !amount} onClick={() => { hostSend('host.extra_pick', { target, count: Number(amount) }); setAmount(''); }}>追加调查</button></div>}
      {actions.has('host.order') && <div className="host-action inline"><label className="field">调查顺位<input value={order} onChange={event => setOrder(event.target.value)} placeholder="玩家ID 用逗号分隔"/></label><button className="button small" disabled={busy || !order.trim()} onClick={() => hostSend('host.order', { order: order.split(',').map(item => item.trim()).filter(Boolean) })}>调整顺位</button></div>}
      {actions.has('host.settle') && <div className="host-action"><button className="button small" disabled={busy} onClick={() => hostSend('host.settle')}>公开积分结算</button></div>}
      {actions.has('host.throne') && <div className="host-action inline"><label className="field">王座 / 最高者<select value={target} onChange={event => setTarget(event.target.value)}><option value="">请选择</option>{targetOptions.map(seat => <option key={seat.id} value={seat.id}>{seat.char_name}</option>)}</select></label><button className="button small" disabled={busy || !target} onClick={() => hostSend('host.throne', { target })}>指定</button></div>}
      {actions.has('dice.roll') && <div className="host-action"><button className="button small" disabled={busy} onClick={() => hostSend('dice.roll')}>手动结算本轮骰子</button></div>}
      {actions.has('host.advance') && <div className="host-action"><button className="button primary" disabled={busy} onClick={() => hostSend('host.advance')}>推进下一阶段</button></div>}
    </div>
  </section>;
}

export type PanelCategory = 'reading' | 'clues' | 'votes' | 'reveal' | 'turn' | 'ready' | 'conversation' | 'points' | 'dice' | 'cards' | 'host' | 'opening' | 'generic';
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
    ['points', ['points', 'score', 'favor', 'skill']],
    ['dice', ['dice', 'diceroll']],
    ['cards', ['cards', 'card', 'cardgame', 'hand']],
    ['host', ['host', 'dm', 'hostnotes', 'dmscript', 'narration']],
    ['opening', ['opening', 'prologue', 'intro']],
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
    case 'points': return <PointsPanel data={data}/>;
    case 'dice': return <DicePanel data={data}/>;
    case 'cards': return <CardsPanel data={data}/>;
    case 'host': return <HostPanel data={data}/>;
    case 'opening': return <OpeningPanel/>;
    case 'conversation': return <section><h2>{panel.title}</h2><p className="muted">公开发言与私信位于对话区。私信仅收发双方可见。</p>{data !== undefined && <JsonData data={data}/>}</section>;
    default: return <section className="plugin-panel"><h2>{panel.title || '扩展内容'}</h2><p className="muted">此扩展尚无专用界面，以下保留服务器提供的原始内容。</p><JsonData data={data === undefined ? { panel, data: null } : data}/></section>;
  }
}
const builtInActions = new Set([
  'ready', 'say', 'pass', 'search', 'publish', 'vote.cast', 'pm.send', 'host.advance',
  'dice.ask', 'dice.answer', 'dice.roll', 'card.play', 'card.draw', 'card.pass', 'points.skill',
  'host.narrate', 'host.points', 'host.settle', 'host.throne', 'host.grant_clue', 'host.show_clues', 'host.extra_pick', 'host.order',
]);
export function ExtraActions() {
  const { view } = useGame();
  return <>{view.allowed_actions?.filter(action => !builtInActions.has(action.type)).map(action => <GenericAction key={action.type} spec={action}/>)}</>;
}
function GenericAction({ spec }: { spec: ActionSpec }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const action = useGameAction(spec.type);
  const fields = spec.fields?.length ? spec.fields : Object.entries(spec.params || {}).map(([key, description]) => ({ key, label: description || key, kind: spec.options?.length ? 'select' : 'text', options: spec.options, required: !spec.optional, placeholder: '' }));
  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    const payload: Record<string, unknown> = {};
    for (const field of fields) {
      const raw = values[field.key];
      if (raw === undefined || raw === '') continue;
      payload[field.key] = field.kind === 'number' ? Number(raw) : raw;
    }
    invoke(action.act(payload));
  };
  return <form className="generic-action" onSubmit={submit}><h3>{spec.desc || spec.type}</h3>{fields.map(field => <label className="field" key={field.key}>{field.label || field.key}{field.kind === 'select' && field.options?.length ? <select required={field.required} value={values[field.key] || ''} onChange={event => setValues(previous => ({ ...previous, [field.key]: event.target.value }))}><option value="">{field.placeholder || '请选择'}</option>{field.options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select> : <input type={field.kind === 'number' ? 'number' : 'text'} required={field.required} placeholder={field.placeholder} value={values[field.key] || ''} onChange={event => setValues(previous => ({ ...previous, [field.key]: event.target.value }))}/>}</label>)}<button className="button" disabled={action.disabled}>执行操作</button></form>;
}
