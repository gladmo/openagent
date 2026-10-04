// Dumps marked token trees as JSON for Go golden tests.
import { Marked, Tokenizer } from "/tmp/md-golden/package/lib/marked.esm.js";

const STRICT_STRIKETHROUGH_REGEX = /^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)/;
class StrictStrikethroughTokenizer extends Tokenizer {
	del(src) {
		const match = STRICT_STRIKETHROUGH_REGEX.exec(src);
		if (!match) return undefined;
		const text = match[2];
		return { type: "del", raw: match[0], text, tokens: this.lexer.inlineTokens(text) };
	}
}
const parser = new Marked();
parser.setOptions({ tokenizer: new StrictStrikethroughTokenizer() });

// Latex extensions (inline + block) ported from markdown.ts
function isEscaped(source, index) {
	let backslashes = 0;
	for (let position = index - 1; position >= 0 && source[position] === "\\"; position--) backslashes++;
	return backslashes % 2 === 1;
}
function findClosingDelimiter(source, closing, start) {
	let index = source.indexOf(closing, start);
	while (index >= 0 && isEscaped(source, index)) index = source.indexOf(closing, index + closing.length);
	return index;
}
function looksLikePendingDollarMath(source) {
	return /\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]/.test(source);
}
function tokenizeInlineLatex(source) {
	let opening = "", closing = "";
	if (source.startsWith("$$")) { opening = "$$"; closing = "$$"; }
	else if (source.startsWith("\\(")) { opening = "\\("; closing = "\\)"; }
	else if (source.startsWith("\\[")) { opening = "\\["; closing = "\\]"; }
	else if (source.startsWith("$") && !/^\$\s/.test(source)) { opening = "$"; closing = "$"; }
	else return undefined;
	const closingIndex = findClosingDelimiter(source, closing, opening.length);
	if (closingIndex >= 0 && opening === "$" && (
		(/\s$/.test(source.slice(opening.length, closingIndex))) ||
		(/^\d/.test(source.slice(closingIndex + 1))) ||
		(/^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_\s])?$/.test(source.slice(opening.length, closingIndex)) &&
			/^[A-Za-z_][A-Za-z0-9_]*/.test(source.slice(closingIndex + 1))) ||
		source.slice(opening.length, closingIndex).includes("`")
	)) return undefined;
	if (closingIndex < 0) {
		const pendingSource = source.slice(opening.length);
		if (opening.startsWith("\\") || looksLikePendingDollarMath(pendingSource)) {
			return { type: "latex", raw: source, text: pendingSource, pending: true };
		}
		return undefined;
	}
	const text = source.slice(opening.length, closingIndex);
	if (!text || text.includes("\n")) return undefined;
	const raw = source.slice(0, closingIndex + closing.length);
	return { type: "latex", raw, text };
}
function tokenizeBlockLatex(source) {
	const dollarMatch = /^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*?)\$\$[ \t]*(?:\n|$)/.exec(source);
	if (dollarMatch?.[1]) return { type: "latexBlock", raw: dollarMatch[0], text: dollarMatch[1].trim() };
	const bracketMatch = /^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*?)\\\][ \t]*(?:\n|$)/.exec(source);
	if (bracketMatch?.[1]) return { type: "latexBlock", raw: bracketMatch[0], text: bracketMatch[1].trim() };
	const pendingBracket = /^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*)$/.exec(source);
	if (pendingBracket) return { type: "latexBlock", raw: pendingBracket[0], text: pendingBracket[1], pending: true };
	const pendingDollar = /^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*)$/.exec(source);
	if (pendingDollar?.[1] && looksLikePendingDollarMath(pendingDollar[1])) {
		return { type: "latexBlock", raw: pendingDollar[0], text: pendingDollar[1], pending: true };
	}
	return undefined;
}
parser.use({ extensions: [
	{ name: "latexBlock", level: "block",
		start(source) { const match = /(?:^|\n) {0,3}(?:\$\$|\\\[)/.exec(source); return match ? match.index + (match[0].startsWith("\n") ? 1 : 0) : undefined; },
		tokenizer: tokenizeBlockLatex },
	{ name: "latex", level: "inline",
		start(source) { const indices = [source.indexOf("$"), source.indexOf("\\("), source.indexOf("\\[")].filter((i) => i >= 0); return indices.length > 0 ? Math.min(...indices) : undefined; },
		tokenizer: tokenizeInlineLatex },
] });

const input = process.argv[2] ?? "";
const tokens = parser.lexer(input);
const strip = (t) => {
	if (Array.isArray(t)) return t.map(strip);
	if (!t || typeof t !== "object") return t;
	const out = {};
	for (const key of ["type", "raw", "text", "depth", "lang", "ordered", "start", "loose", "task", "checked", "href", "pending", "align", "items", "header", "rows", "tokens"]) {
		if (t[key] !== undefined) out[key] = key === "items" ? t[key].map(strip) : (key === "header" || key === "rows" || key === "tokens" || key === "align") ? strip(t[key]) : t[key];
	}
	return out;
};
console.log(JSON.stringify(strip(tokens), null, 1));
