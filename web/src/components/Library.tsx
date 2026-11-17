import { useEffect, useState } from 'react';
import { api, errorText } from '../api';
import type { RoomInfo, ScriptEntry, Session } from '../types';
import { Empty, ErrorBanner, Icon, Modal, ShipIllustration } from './UI';

export interface CreateSettings { script: ScriptEntry; ai: string[]; llm: 'mock' | 'qoder'; character: string; name: string }
interface Props {
  page: 'library' | 'lobby'; session: Session | null;
  resume: () => void; onCreate: (settings: CreateSettings) => Promise<void>;
  onJoin: (room: string, character: string, name: string) => Promise<void>;
}
function phaseLabel(room: RoomInfo) { return typeof room.phase === 'string' ? room.phase : room.phase?.title || '等待登船'; }
export default function Library({ page, session, resume, onCreate, onJoin }: Props) {
  const [scripts, setScripts] = useState<ScriptEntry[]>([]);
  const [rooms, setRooms] = useState<RoomInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [revision, setRevision] = useState(0);
  const [script, setScript] = useState<ScriptEntry | null>(null);
  const [room, setRoom] = useState<RoomInfo | null>(null);
  const [character, setCharacter] = useState('');
  const [name, setName] = useState('');
  const [ai, setAi] = useState<string[]>([]);
  const [llm, setLlm] = useState<'mock' | 'qoder'>('mock');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState('');
  const [roomFilter, setRoomFilter] = useState('open');
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    Promise.allSettled([api.scripts(controller.signal), api.rooms(controller.signal)]).then(([catalog, lobby]) => {
      if (controller.signal.aborted) return;
      if (catalog.status === 'fulfilled') setScripts(catalog.value || []);
      if (lobby.status === 'fulfilled') setRooms(lobby.value || []);
      const failures = [catalog, lobby].filter(result => result.status === 'rejected') as PromiseRejectedResult[];
      if (failures.length) setError(`大厅数据未完整载入：${errorText(failures[0].reason)} 请确认后端已启动后重试。`);
      setLoading(false);
    });
    return () => controller.abort();
  }, [revision]);
  const openScript = (entry: ScriptEntry) => {
    setRoom(null); setScript(entry); setCharacter(entry.characters[0]?.id || '');
    setAi([]); setFormError('');
  };
  const openRoom = (entry: RoomInfo) => {
    setScript(null); setRoom(entry); setFormError('');
    setCharacter(entry.seats?.find(seat => !seat.joined)?.id || '');
  };
  const close = () => { if (!busy) { setScript(null); setRoom(null); } };
  const submit = async () => {
    if (busy) return;
    setFormError(''); setBusy(true);
    try {
      if (script) await onCreate({ script, ai, llm, character, name: name.trim() });
      else if (room) await onJoin(room.id, character, name.trim());
      setScript(null); setRoom(null);
    } catch (cause) { setFormError(errorText(cause)); }
    finally { setBusy(false); }
  };
  const modalCharacters = script?.characters || room?.seats?.map(seat => ({
    id: seat.id, name: seat.char_name, public_intro: seat.public_intro, tts: {},
  })) || [];
  const selectedCharacter = modalCharacters.find(item => item.id === character);
  const filteredRooms = rooms.filter(entry => roomFilter === 'all' || (!entry.ended && entry.seats?.some(seat => !seat.joined)));
  return <main id="main" className="library-main">
    <section className="voyage-hero" aria-labelledby="voyage-title">
      <div className="hero-copy"><p className="voyage-caption"><Icon name="anchor" size={17}/> 午夜航程 · 海上悬疑</p>
        <h1 id="voyage-title">东方之星</h1><p className="hero-subtitle">雾起之后，<br/>每个人都有一段未说完的故事。</p>
        <p className="hero-description">登船，读懂你的角色。沿着甲板寻找线索，<br className="desktop-break"/>在彼此的证词之间，拼出真相。</p>
        <a className="button brass" href={page === 'library' ? '#scripts' : '#rooms'}><Icon name="compass" size={18}/>{page === 'library' ? '选择剧本，准备登船' : '查看正在候船的房间'}</a>
      </div>
      <div className="hero-ship"><div className="hero-time"><span>午夜</span><strong>00:00</strong><span>雾中航行</span></div><ShipIllustration/><p>一艘船，数份证词，一个真相。</p></div>
      <span className="hero-coordinate" aria-hidden="true">港口不见了，故事才刚开始。</span>
    </section>
    {session && <div className="resume-strip"><div><Icon name="anchor"/><span>你的登船凭证仍在本机保存 <strong>房间 {session.room_id}</strong></span></div><button className="button small" onClick={resume}>返回我的房间</button></div>}
    <div className="library-content"><ErrorBanner message={error} retry={() => setRevision(value => value + 1)}/>
      {page === 'library' ? <section id="scripts" aria-labelledby="scripts-title">
        <div className="section-heading"><div><h2 id="scripts-title">剧本档案</h2><p>从一份角色开始，走进另一段人生。</p></div><span className="muted">{loading ? '正在载入…' : `${scripts.length} 部可用剧本`}</span></div>
        {loading && !scripts.length ? <div className="loading-state" role="status">正在向港务室查询剧本…</div> : !scripts.length ? <Empty title="尚无可用剧本">确认后端已加载剧本，然后点击重试刷新目录。</Empty> : <div className="script-catalog">{scripts.map((entry, index) => <article className="script-entry" key={entry.meta.id}>
          <div className="script-cover"><Icon name="anchor" size={32}/><span>{entry.meta.title}</span><div className="cover-line"/><small>雾港剧本档案</small></div>
          <div className="script-summary"><div className="script-title-row"><h3>{entry.meta.title}</h3>{index === 0 && <span className="stamp">待启航</span>}</div>
            <p>{entry.meta.intro || '剧本简介尚未提供，登船前可先查看公开角色。'}</p>
            <div className="cast-preview">{entry.characters?.slice(0, 5).map(item => <span key={item.id}>{item.name}</span>)}{entry.characters?.length > 5 && <span>等 {entry.characters.length} 位</span>}</div>
            <div className="script-footer"><span><Icon name="people" size={17}/>{entry.meta.players} 人剧本</span><button className="button primary" onClick={() => openScript(entry)}>查看角色 / 创建房间</button></div>
          </div>
        </article>)}</div>}
        <div className="how-to-board"><h3>登船须知</h3><ol><li><strong>选择角色</strong><span>公开介绍可提前查看，私密剧本仅自己可见。</span></li><li><strong>读本与调查</strong><span>按阶段解锁内容，行动点决定你能调查的地点。</span></li><li><strong>讨论与指认</strong><span>等待自己的发言回合，在投票时给出你的答案。</span></li></ol></div>
      </section> : <section id="rooms" aria-labelledby="rooms-title">
        <div className="section-heading"><div><h2 id="rooms-title">候船大厅</h2><p>加入一个空位，和同行者开启航程。</p></div><button className="button small" onClick={() => setRevision(value => value + 1)} disabled={loading}><Icon name="refresh" size={16}/>刷新房间</button></div>
        <div className="room-filters" role="group" aria-label="筛选房间"><button aria-pressed={roomFilter === 'open'} onClick={() => setRoomFilter('open')}>有空位的航程</button><button aria-pressed={roomFilter === 'all'} onClick={() => setRoomFilter('all')}>全部房间</button></div>
        {loading && !rooms.length ? <div className="loading-state" role="status">正在查询候船名单…</div> : !filteredRooms.length ? <Empty title="目前没有可加入的房间">到剧本档案创建一个房间，邀请朋友或安排 AI 同行。</Empty> : <div className="room-list">{filteredRooms.map(entry => {
          const open = !entry.ended && entry.seats?.some(seat => !seat.joined);
          return <article className="room-row" key={entry.id}><div className="room-symbol"><Icon name="anchor" size={25}/></div><div className="room-main"><h3>{entry.title}</h3><p>房间 {entry.id} <span>{phaseLabel(entry)}</span></p></div><div className="room-seats"><strong>{entry.seats?.filter(seat => seat.joined).length || 0} / {entry.seats?.length || 0}</strong><span>{entry.ended ? '航程已结束' : '已登船'}</span></div><button className="button" disabled={!open} title={!open ? '该房间已满或已结束' : undefined} onClick={() => openRoom(entry)}>{open ? '选择空位' : entry.ended ? '已结束' : '已满员'}</button></article>;
        })}</div>}
      </section>}
      <footer className="library-footer"><Icon name="anchor" size={17}/><span>雾港 · 剧本杀</span><span>语音由浏览器提供。点击开启后才会朗读，不会自动播放。</span></footer>
    </div>
    {(script || room) && <Modal title={script ? `登船登记 · ${script.meta.title}` : `加入航程 · ${room?.title}`} close={close}>
      <form className="boarding-form" onSubmit={event => { event.preventDefault(); void submit(); }}>
        <ErrorBanner message={formError}/><p className="muted">以下仅展示公开角色介绍，秘密将在加入后按阶段解锁。</p>
        <fieldset disabled={busy}><legend>选择你的角色</legend><div className="character-choice">{modalCharacters.map(item => {
          const taken = room?.seats?.find(seat => seat.id === item.id)?.joined;
          return <label key={item.id} className={character === item.id ? 'selected' : ''}><input type="radio" name="character" value={item.id} checked={character === item.id} disabled={taken} onChange={() => { setCharacter(item.id); setAi(previous => previous.filter(id => id !== item.id)); }}/><span>{item.name}{taken && <small>已登船</small>}</span></label>;
        })}{script && <label className={!character ? 'selected' : ''}><input type="radio" name="character" checked={!character} onChange={() => setCharacter('')}/><span>仅主持，不扮演角色</span></label>}</div>
        {selectedCharacter && <div className="character-intro"><h3>{selectedCharacter.name}</h3><p>{selectedCharacter.public_intro}</p></div>}
        {character && <label className="field">你的称呼<input value={name} onChange={event => setName(event.target.value)} required maxLength={40} autoComplete="nickname" placeholder="同行者该如何称呼你？"/></label>}
        {script && <><div className="field"><span>安排 AI 同行（其余空位留给真人）</span><div className="ai-choices">{script.characters.filter(item => item.id !== character).map(item => <label key={item.id}><input type="checkbox" checked={ai.includes(item.id)} onChange={event => setAi(previous => event.target.checked ? [...previous, item.id] : previous.filter(id => id !== item.id))}/>{item.name}</label>)}</div></div>
          <label className="field">AI 模式<select value={llm} onChange={event => setLlm(event.target.value as 'mock' | 'qoder')}><option value="mock">Mock · 确定性测试模式</option><option value="qoder">Qoder · 模型驱动（需后端配置）</option></select></label>
          {script.meta.rules && <details className="rules"><summary>阅读游戏规则</summary><p>{script.meta.rules}</p></details>}
          {(script.meta.credits || script.meta.license_note) && <p className="fine-print">{script.meta.credits} {script.meta.license_note}</p>}
        </>}
        </fieldset><div className="modal-actions"><button type="button" className="button" onClick={close} disabled={busy}>取消</button><button className="button primary" disabled={busy || (!!room && !character)}>{busy ? '正在登记…' : script ? '创建并登船' : '确认加入'}</button></div>
      </form>
    </Modal>}
  </main>;
}
