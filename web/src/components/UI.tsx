import { Component, useEffect, useId, useRef, type ErrorInfo, type ReactNode } from 'react';

const paths: Record<string, ReactNode> = {
  anchor: <><circle cx="12" cy="5" r="2"/><path d="M12 7v14M7 11h10M4 15c0 8 16 8 16 0M4 15l-2 2m18-2 2 2"/></>,
  book: <><path d="M12 5v15M3 4c3-1 6-1 9 1 3-2 6-2 9-1v15c-3-1-6-1-9 1-3-2-6-2-9-1z"/></>,
  compass: <><circle cx="12" cy="12" r="9"/><path d="m16 8-3 5-5 3 3-5z"/></>,
  sound: <><path d="M4 9v6h4l5 4V5L8 9zM17 8c2 2 2 6 0 8m3-11c4 4 4 10 0 14"/></>,
  mute: <><path d="M4 9v6h4l5 4V5L8 9zM17 9l5 6m0-6-5 6"/></>,
  people: <><circle cx="9" cy="7" r="3"/><path d="M3 21v-3c0-6 12-6 12 0v3M17 4c4 0 4 6 0 6m1 4c4 0 4 4 4 7"/></>,
  clue: <><path d="M6 3h9l4 4v14H6zM15 3v5h4M9 12h7m-7 4h5"/></>,
  message: <path d="M3 4h18v13H9l-6 4zM7 8h10M7 12h7"/>,
  lock: <><rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V6a4 4 0 0 1 8 0v4M12 14v3"/></>,
  check: <path d="m5 12 4 4L19 6"/>,
  close: <path d="m6 6 12 12M6 18 18 6"/>,
  refresh: <><path d="M20 8a9 9 0 1 0 1 8M20 3v6h-6"/></>,
  send: <path d="m3 3 18 9-18 9 4-9zm4 9h14"/>,
  flag: <path d="M5 22V3h14l-3 5 3 5H5"/>,
};
export function Icon({ name, size = 20 }: { name: string; size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name] || paths.compass}</svg>;
}
export function ErrorBanner({ message, retry, dismiss }: { message: string; retry?: () => void; dismiss?: () => void }) {
  if (!message) return null;
  return <div className="error-banner" role="alert"><div><strong>暂时无法完成</strong><p>{message}</p></div>
    {retry && <button className="button small" onClick={retry}><Icon name="refresh" size={16}/>重试</button>}
    {dismiss && <button className="icon-button" aria-label="关闭提示" onClick={dismiss}><Icon name="close"/></button>}
  </div>;
}
export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return <div className="empty-state"><Icon name="compass" size={30}/><h3>{title}</h3>{children && <p>{children}</p>}</div>;
}
export function Modal({ title, children, close }: { title: string; children: ReactNode; close: () => void }) {
  const id = useId();
  const ref = useRef<HTMLDivElement>(null);
  const closeRef = useRef(close); closeRef.current = close;
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    const first = ref.current?.querySelector<HTMLElement>('button, input, select, textarea, [tabindex="0"]');
    first?.focus();
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); closeRef.current(); }
      if (event.key !== 'Tab') return;
      const elements = Array.from(ref.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]') || []);
      const firstElement = elements[0], lastElement = elements.at(-1);
      if (!firstElement) { event.preventDefault(); return; }
      if (event.shiftKey && (document.activeElement === firstElement || !ref.current?.contains(document.activeElement))) { event.preventDefault(); lastElement?.focus(); }
      else if (!event.shiftKey && document.activeElement === lastElement) { event.preventDefault(); firstElement.focus(); }
    };
    document.addEventListener('keydown', keydown);
    return () => { document.body.style.overflow = overflow; document.removeEventListener('keydown', keydown); previous?.focus(); };
  }, []);
  return <div className="modal-backdrop" onMouseDown={event => { if (event.target === event.currentTarget) close(); }}>
    <div className="modal" role="dialog" aria-modal="true" aria-labelledby={id} ref={ref}>
      <div className="modal-heading"><h2 id={id}>{title}</h2><button className="icon-button" aria-label="关闭窗口" onClick={close}><Icon name="close"/></button></div>
      {children}
    </div>
  </div>;
}
export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidCatch(_error: Error, _info: ErrorInfo) { /* Do not log private script data or credentials. */ }
  render() {
    if (this.state.failed) return <main className="fatal-error"><h1>界面未能显示</h1><p>房间凭证仍保存在本机。请重新加载页面；如仍失败，请检查后端返回的数据格式。</p><button className="button primary" onClick={() => window.location.reload()}>重新加载</button></main>;
    return this.props.children;
  }
}
export function JsonData({ data }: { data: unknown }) {
  return <pre className="json-data" tabIndex={0}>{JSON.stringify(data ?? {}, null, 2)}</pre>;
}
export function ShipIllustration() {
  return <svg className="ship-illustration" viewBox="0 0 780 280" fill="none" aria-hidden="true">
    <g stroke="currentColor" strokeWidth="1.3">
      <path d="M35 186h682l-48 58H113L35 186Zm88 0v-38h514v38M166 148v-39h373l42 39M227 109V77h214l31 32M292 77V48h56v29M357 77V38h42v39M163 150l-16 35M626 151l-16 35"/>
      <path d="M283 48h74M348 38h60M487 109v-28M487 81h53l-17 11h-36M188 128h326M143 166h471M96 207h568M389 38V14"/>
      {Array.from({ length: 21 }, (_, index) => <circle key={index} cx={145 + index * 23} cy={221} r="3"/>)}
      {Array.from({ length: 16 }, (_, index) => <path key={index} d={`M${176 + index * 22} 155v15`}/>)}
      <path d="M4 255c40-10 40 10 80 0s40 10 80 0 40 10 80 0 40 10 80 0 40 10 80 0 40 10 80 0 40 10 80 0 40 10 80 0 40 10 80 0M42 272h158m31 0h90m33 0h337" opacity=".45"/>
    </g>
    <text x="486" y="207" fill="currentColor" fontSize="11" fontFamily="Songti SC, serif" letterSpacing="4">东方之星</text>
  </svg>;
}
