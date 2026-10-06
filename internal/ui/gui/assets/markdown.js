// Marked tokenizes Markdown; only this allowlist builds DOM. Transcript HTML is
// never parsed as markup, images never fetch, and links require a user action.
import { marked } from "./vendor/marked.mjs";
import { api, h, fail, ask } from "./core.js";

// Decode entities in text tokens only. The parser receives a single entity, never
// transcript markup; its result is appended as a text node by h().
const entities = new DOMParser();
const decode = (text) => String(text || "").replace(/&(?:#\d+|#x[\da-f]+|[a-z][\da-z]+);/gi,
  (entity) => entities.parseFromString(entity, "text/html").body.textContent);

function nodes(tokens, depth = 0) {
  if (depth > 64) return ["[Nested content omitted]"];
  return (tokens || []).flatMap((t) => {
    const children = () => nodes(t.tokens, depth + 1);
    switch (t.type) {
      case "space": case "def": return [];
      case "paragraph": return h("p", {}, children());
      case "heading": return h("h" + Math.min(6, t.depth + 2), {}, children());
      case "strong": case "em": case "del": return h(t.type, {}, children());
      case "blockquote": return h("blockquote", {}, children());
      case "br": case "hr": return h(t.type);
      case "code": return h("pre", {}, h("code", {}, t.text));
      case "codespan": return h("code", {}, t.text);
      case "list": return h(t.ordered ? "ol" : "ul", t.ordered ? { start: t.start } : {},
        t.items.map((item) => h("li", {}, nodes(item.tokens, depth + 1))));
      case "checkbox": return h("span", { class: "md-check", "aria-label": t.checked ? "Completed" : "Not completed" }, t.checked ? "☑ " : "☐ ");
      case "table": return h("div", { class: "md-table", tabindex: "0", role: "region", "aria-label": "Table" }, h("table", {},
        h("thead", {}, h("tr", {}, t.header.map((cell) => h("th", { scope: "col" }, nodes(cell.tokens, depth + 1))))),
        h("tbody", {}, t.rows.map((row) => h("tr", {}, row.map((cell) => h("td", {}, nodes(cell.tokens, depth + 1))))))));
      case "link": {
        const href = decode(t.href);
        let url;
        try { url = new URL(href); } catch { return children(); }
        if (!["https:", "http:"].includes(url.protocol)) return children();
        return h("a", { href: url.href, title: url.href, rel: "noopener noreferrer", onclick: async (ev) => {
          ev.preventDefault();
          if (await ask({ title: "Open this link in your browser?", body: url.href, ok: "Open link" })) api("OpenConversationLink", url.href).catch(fail);
        } }, children());
      }
      case "image": return h("span", { class: "md-image" }, "[Image: ", decode(t.text) || "image", "]");
      case "html": return t.raw; // Show literal HTML, never execute it.
      case "text": return t.tokens ? children() : decode(t.text);
      case "escape": return t.text;
      default: return t.raw || t.text || "";
    }
  });
}

export function markdown(text) {
  try { return h("div", { class: "markdown" }, nodes(marked.lexer(text || "", { gfm: true }))); }
  catch { return h("div", { class: "markdown plain" }, text); }
}
