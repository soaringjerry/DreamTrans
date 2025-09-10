import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

export default function MarkdownView({ text }: { text: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        a(props: any) {
          const {node, ...rest} = props as any
          return <a {...rest} target="_blank" rel="noreferrer noopener" />
        },
        code(props: any) {
          const {inline, className, children, ...rest} = props as any
          return inline ? (
            <code className={className} {...rest}>{children}</code>
          ) : (
            <pre className={className} {...rest}><code>{children}</code></pre>
          )
        }
      }}
    >
      {text || ''}
    </ReactMarkdown>
  )
}
