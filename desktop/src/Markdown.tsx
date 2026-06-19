import DOMPurify from "dompurify";
import { marked } from "marked";

marked.setOptions({ breaks: true, gfm: true });
export default function Markdown({ children }: { children: string }) {
  const html = DOMPurify.sanitize(marked.parse(children || "") as string);
  return <div className="markdown" dangerouslySetInnerHTML={{ __html: html }} />;
}
