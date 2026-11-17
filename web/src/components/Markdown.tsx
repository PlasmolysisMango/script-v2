import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

export function Markdown({ children, className }: { children?: string; className?: string }) {
  if (!children) return null;
  return <div className={`prose markdown ${className || ''}`.trim()}>
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      skipHtml
      components={{
        a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noreferrer noopener"/>,
        img: ({ node: _node, ...props }) => <img {...props} loading="lazy" alt={props.alt || ''}/>,
        table: ({ node: _node, ...props }) => <div className="markdown-table"><table {...props}/></div>,
      }}
    >{children}</ReactMarkdown>
  </div>;
}

export function stripMarkdown(text: string): string {
  return text
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}#{1,6}\s+/gm, '')
    .replace(/^\s{0,3}>\s?/gm, '')
    .replace(/^\s*[-*+]\s+/gm, '')
    .replace(/`{1,3}/g, '')
    .replace(/[*_~]{1,3}/g, '')
    .replace(/^\s*\|.*\|\s*$/gm, '')
    .replace(/^\s*[-: ]+\|[-: |]+\s*$/gm, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}
