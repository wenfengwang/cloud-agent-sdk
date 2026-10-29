"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { CopyButton } from "@/components/copy-button";

/** Agent text as GFM markdown. Raw HTML is never rendered. */
export function Markdown({ text }: { text: string }) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          // Agents link local files too; only web links are clickable.
          a: ({ href, children }) =>
            href && /^(https?:|mailto:)/i.test(href) ? (
              <a href={href} target="_blank" rel="noopener noreferrer">
                {children}
              </a>
            ) : (
              <span className="font-medium" title={href}>
                {children}
              </span>
            ),
          pre: ({ children }) => {
            const code = extractText(children);
            return (
              <div className="group/code relative">
                <pre>{children}</pre>
                <CopyButton text={code} className="absolute top-1.5 right-1.5 opacity-0 group-hover/code:opacity-100" />
              </div>
            );
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

function extractText(node: React.ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(extractText).join("");
  if (node && typeof node === "object" && "props" in node) {
    return extractText((node as { props: { children?: React.ReactNode } }).props.children);
  }
  return "";
}
