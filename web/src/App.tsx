import { useEffect, useState } from 'react';
import { api, errorText, joinedSession, loadSession, storeSession } from './api';
import type { Session } from './types';
import { useSpeech } from './hooks/useSpeech';
import GameBoard from './components/GameBoard';
import Library, { type CreateSettings } from './components/Library';
import { ErrorBanner, ErrorBoundary, Icon, Modal } from './components/UI';

type Page = 'library' | 'lobby' | 'game';
function HarborApp() {
  const [session, setSession] = useState<Session | null>(loadSession);
  const [page, setPage] = useState<Page>(() => loadSession() ? 'game' : 'library');
  const [notice, setNotice] = useState('');
  const [forget, setForget] = useState(false);
  const speech = useSpeech();
  const navigate = (next: Page) => { speech.stop(); setPage(next); };
  useEffect(() => { document.title = page === 'game' ? '航程调查 · 雾港 · 剧本杀' : page === 'lobby' ? '候船大厅 · 雾港 · 剧本杀' : '雾港 · 剧本杀'; }, [page]);
  const save = (next: Session | null) => {
    setSession(next);
    if (!storeSession(next)) setNotice(next ? '浏览器无法保存登船凭证。当前仍可游戏，但刷新后可能无法恢复，请勿关闭页面。' : '浏览器无法清除保存的凭证，请检查本机存储设置。');
  };
  const join = async (room: string, character: string, name: string) => {
    const joined = await api.join(room, character, name);
    speech.stop(); save(joinedSession(session, joined)); setPage('game');
  };
  const create = async ({ script, ai, llm, character, name }: CreateSettings) => {
    const created = await api.create(script.meta.id, ai, llm);
    let next: Session = { room_id: created.room_id, host_token: created.host_token };
    // Persist the host receipt before joining: a failed join must not strand the room's host.
    save(next);
    if (character) {
      try { next = joinedSession(next, await api.join(created.room_id, character, name)); save(next); }
      catch (cause) { setNotice(`房间已创建，主持凭证已保留，但角色登记失败：${errorText(cause)}。可在登船名单中重新选择角色。`); }
    }
    speech.stop(); setPage('game');
  };
  return <div className="harbor-app"><a className="skip-link" href="#main">跳到主要内容</a>
    <header className="site-header"><button className="brand" onClick={() => navigate('library')} aria-label="雾港 · 剧本杀，返回剧本档案"><span className="brand-symbol"><Icon name="anchor" size={25}/></span><span>雾港 <b>·</b> 剧本杀</span></button>
      <nav aria-label="主要导航"><button aria-current={page === 'library' ? 'page' : undefined} onClick={() => navigate('library')}>剧本档案</button><button aria-current={page === 'lobby' ? 'page' : undefined} onClick={() => navigate('lobby')}>候船大厅</button>{session && <button aria-current={page === 'game' ? 'page' : undefined} onClick={() => navigate('game')}>我的航程</button>}</nav>
      <div className="speech-toolbar"><button className={`speech-toggle ${speech.enabled ? 'enabled' : ''}`} aria-pressed={speech.enabled} disabled={!speech.supported} onClick={speech.toggle} title={!speech.supported ? '浏览器不支持 Web Speech，仍可使用文字阅读' : speech.enabled ? '关闭自动朗读并停止播放' : '点击授权浏览器朗读后续语音事件'}><Icon name={speech.enabled ? 'sound' : 'mute'} size={18}/><span>{!speech.supported ? '语音不可用' : speech.enabled ? '朗读已开启' : '开启语音朗读'}</span></button>{speech.supported && <label className="speech-rate">语速 <select aria-label="朗读语速" value={speech.rate} onChange={event => speech.changeRate(Number(event.target.value))}><option value={0.75}>0.75×</option><option value={1}>1×</option><option value={1.25}>1.25×</option><option value={1.5}>1.5×</option></select></label>}{speech.speaking && <button className="text-button stop-speech" onClick={speech.stop}>停止当前朗读</button>}</div>
    </header>
    {notice && <div className="global-notice"><ErrorBanner message={notice} dismiss={() => setNotice('')}/></div>}
    {!speech.supported && <p className="speech-explanation" role="status">此浏览器不支持 Web Speech 语音朗读。所有内容仍可通过文字阅读，不会生成或模拟语音。</p>}
    {speech.error && <div className="global-notice"><ErrorBanner message={speech.error}/></div>}
    {speech.enabled && !speech.error && <p className="speech-explanation" role="status">{speech.speaking ? '正在使用浏览器语音朗读。' : `自动朗读已开启 · ${speech.voiceCount ? `本机有 ${speech.voiceCount} 个中文语音` : '正在等待浏览器加载中文语音'}`} 历史记录不会自动重播。</p>}
    {page === 'game' && session ? <GameBoard key={`${session.room_id}:${session.token || 'host'}`} session={session} speech={speech} onJoin={join} onForget={() => setForget(true)}/> : <Library page={page === 'lobby' ? 'lobby' : 'library'} session={session} resume={() => navigate('game')} onCreate={create} onJoin={join}/>}
    {forget && <Modal title="清除本机登船凭证？" close={() => setForget(false)}><p>这只会清除当前浏览器保存的玩家与主持凭证，不会退出后端房间或释放角色空位。清除后可能无法找回该角色或主持权限。</p><div className="modal-actions"><button className="button" onClick={() => setForget(false)}>保留凭证</button><button className="button danger" onClick={() => { speech.stop(); save(null); setForget(false); setPage('lobby'); }}>确认清除</button></div></Modal>}
  </div>;
}
export default function App() { return <ErrorBoundary><HarborApp/></ErrorBoundary>; }
