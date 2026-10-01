package hottydoc

// CSS styles every document this package makes. It sets text in the
// terminal's rows (SPEC §8: --hotty-cell-h is one row) so estimates hold,
// and takes its colours from the variables a program's own stylesheet may
// define (--ink, --dim, --rule, --accent, --good, --warn, --bad, --sans),
// else from the host's (--hotty-fg), else from neuroplastio's dark palette.
//
// Documents are sized by their content (r=auto), so the page is as tall as
// what is in it: never height: 100%, which would measure the viewport.
const CSS = `
:root {
  --doc-ink: var(--ink, var(--hotty-fg, #d7d7e0));
  --doc-paper: var(--paper, var(--hotty-bg, #12121a));
  --doc-dim: var(--dim, #8b90a0);
  --doc-rule: var(--rule, #23232e);
  --doc-accent: var(--accent, #9d90ff);
  --doc-good: var(--good, #7fd4a0);
  --doc-warn: var(--warn, #e8c872);
  --doc-bad: var(--bad, #f07a7a);
  --doc-row: var(--hotty-cell-h, 18px);
  --doc-col: var(--hotty-cell-w, 9px);
  --doc-mono: var(--hotty-font, ui-monospace, monospace);
  --doc-sans: var(--sans, "Inter", "IBM Plex Sans", system-ui, sans-serif);
  --doc-card: rgba(255, 255, 255, .035);
}
html, body { height: auto; margin: 0; }
body { background: var(--doc-paper); }
main.doc {
  display: block; box-sizing: border-box;
  padding: 0 var(--doc-col) var(--doc-row);
  font: 14px/var(--doc-row) var(--doc-sans); color: var(--doc-ink);
  overflow-wrap: break-word;
}
main.doc > * { margin: var(--doc-row) 0 0; }
main.doc > *:first-child { margin-top: 0; }
main.doc p, main.doc ul, main.doc ol, main.doc blockquote, main.doc pre, main.doc table, main.doc dl { margin-bottom: 0; }

main.doc h1, main.doc h2, main.doc h3, main.doc h4, main.doc h5, main.doc h6 { margin-bottom: 0; font-weight: 650; }
main.doc h1 { font-size: 22px; line-height: calc(2 * var(--doc-row)); box-shadow: inset 0 -1px 0 var(--doc-rule); }
main.doc h2 { font-size: 17px; line-height: calc(1.5 * var(--doc-row)); color: var(--doc-ink); }
main.doc h3 { font-size: 15px; line-height: var(--doc-row); color: var(--doc-accent); }
main.doc h4, main.doc h5, main.doc h6 { font-size: 14px; line-height: var(--doc-row); color: var(--doc-dim); }

main.doc a { color: var(--doc-accent); text-decoration: underline; text-underline-offset: 2px; }
main.doc a:hover { color: var(--doc-ink); }
main.doc strong, main.doc b { font-weight: 650; }
main.doc code, main.doc kbd, main.doc samp, main.doc tt {
  font-family: var(--doc-mono); font-size: .92em; color: #7fd4d4;
  background: rgba(127, 212, 212, .08); border-radius: 4px; padding: 0 3px;
}
main.doc pre {
  font: 1rem/var(--doc-row) var(--doc-mono); white-space: pre-wrap; overflow-wrap: anywhere;
  padding: calc(var(--doc-row) / 2) calc(2 * var(--doc-col));
  background: var(--doc-card); border-radius: 6px; box-shadow: inset 0 0 0 1px var(--doc-rule);
}
main.doc pre code { font-size: inherit; color: inherit; background: none; padding: 0; }
main.doc > pre.after { margin-top: 0; padding-top: 0; border-top-left-radius: 0; border-top-right-radius: 0; box-shadow: inset 1px 0 0 var(--doc-rule), inset -1px 0 0 var(--doc-rule), inset 0 -1px 0 var(--doc-rule); }
main.doc > pre.before { padding-bottom: 0; border-bottom-left-radius: 0; border-bottom-right-radius: 0; box-shadow: inset 1px 0 0 var(--doc-rule), inset -1px 0 0 var(--doc-rule), inset 0 1px 0 var(--doc-rule); }
main.doc > pre.before.after { box-shadow: inset 1px 0 0 var(--doc-rule), inset -1px 0 0 var(--doc-rule); }
main.doc pre .lang { float: right; color: var(--doc-dim); font: 11px/var(--doc-row) var(--doc-sans); letter-spacing: .08em; text-transform: uppercase; }
main.doc pre .kw { color: var(--doc-accent); } main.doc pre .str { color: var(--doc-good); }
main.doc pre .com { color: var(--doc-dim); font-style: italic; } main.doc pre .num { color: var(--doc-warn); }
main.doc pre .fn { color: #8fa8ff; } main.doc pre .typ { color: #7fd4d4; }

main.doc ul, main.doc ol { padding-left: calc(3 * var(--doc-col)); }
main.doc li > p { margin: 0; }
main.doc li.task { list-style: none; }
main.doc li.task .box { display: inline-block; width: calc(2 * var(--doc-col)); margin-left: calc(-2 * var(--doc-col)); color: var(--doc-dim); }
main.doc li.task.done .box { color: var(--doc-good); }
main.doc li.task.done { color: var(--doc-dim); }
main.doc blockquote { padding: 0 0 0 calc(2 * var(--doc-col)); box-shadow: inset 3px 0 0 var(--doc-accent); color: var(--doc-ink); margin-left: 0; margin-right: 0; }
main.doc blockquote > * { margin: 0; }
main.doc blockquote > * + * { margin-top: var(--doc-row); }
main.doc blockquote .callout { display: block; color: var(--doc-accent); font-weight: 650; font-size: 12px; letter-spacing: .06em; }
main.doc blockquote.warning, main.doc blockquote.caution { box-shadow: inset 3px 0 0 var(--doc-warn); }
main.doc blockquote.warning .callout, main.doc blockquote.caution .callout { color: var(--doc-warn); }
main.doc hr { border: 0; height: var(--doc-row); background: linear-gradient(var(--doc-rule), var(--doc-rule)) center / 100% 1px no-repeat; }
main.doc img { display: block; max-width: 100%; max-height: calc(30 * var(--doc-row)); object-fit: contain; }
main.doc .alt { color: var(--doc-dim); font-style: italic; }
main.doc del, main.doc s { color: var(--doc-dim); }

main.doc table { border-collapse: collapse; font-variant-numeric: tabular-nums; }
main.doc th, main.doc td { height: var(--doc-row); line-height: var(--doc-row); padding: 0 var(--doc-col); text-align: left; vertical-align: top; }
main.doc th { color: var(--doc-dim); font-weight: 600; font-size: 12px; box-shadow: inset 0 -1px 0 var(--doc-rule); }
main.doc tbody tr:nth-child(even) td { background: var(--doc-rule); }
main.doc td.num, main.doc th.num, main.doc td[align="right"], main.doc th[align="right"] { text-align: right; }
main.doc td[align="center"], main.doc th[align="center"] { text-align: center; }

main.doc table.csv { table-layout: fixed; width: 100%; }
main.doc table.csv td, main.doc table.csv th { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
main.doc table.csv tbody tr:nth-child(even) td { background: none; }
main.doc table.csv tr.shade td { background: var(--doc-rule); }
main.doc .more { color: var(--doc-dim); }

main.doc .json { font: 1rem/var(--doc-row) var(--doc-mono); white-space: pre-wrap; overflow-wrap: anywhere; padding-left: calc(2 * var(--doc-col)); }
main.doc .json .in { padding-left: calc(2 * var(--doc-col)); }
main.doc .json details > summary { list-style: none; cursor: pointer; position: relative; }
main.doc .json details > summary::-webkit-details-marker { display: none; }
main.doc .json details > summary::before { content: "▾"; position: absolute; left: calc(-1.6 * var(--doc-col)); color: var(--doc-dim); }
main.doc .json details:not([open]) > summary::before { content: "▸"; }
main.doc .json details:not([open]) > summary .open { display: none; }
main.doc .json details[open] > summary .shut { display: none; }
main.doc .json .k { color: #7fd4d4; } main.doc .json .s { color: var(--doc-good); }
main.doc .json .n { color: var(--doc-warn); } main.doc .json .l { color: var(--doc-accent); }
main.doc .json .p { color: var(--doc-dim); }

main.doc pre.text { font: 1rem/var(--doc-row) var(--doc-mono); padding: 0; background: none; box-shadow: none; border-radius: 0; }
main.doc > pre.text + pre.text { margin-top: 0; }
`

// ImageCSS is the stylesheet of an image's document (Image): the image
// fills its surface, which has a fixed size.
const ImageCSS = `
html, body, main.doc { height: 100%; }
main.doc { padding: 0; }
main.doc img.image { width: 100%; height: 100%; max-height: none; object-fit: contain; object-position: left center; }
`
