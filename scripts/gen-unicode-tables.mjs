#!/usr/bin/env node
// Generates tui/unicode_data.go from UCD 16.0.0 data files (the Unicode
// version of Go 1.27's stdlib tables and of Node 26's ICU, which the
// TypeScript reference runs on). Not a gate; run manually when regenerating:
//
//   node scripts/gen-unicode-tables.mjs <ucd-dir> <out-file>
//
// <ucd-dir> must contain: GraphemeBreakProperty.txt, DerivedCoreProperties.txt,
// emoji-data.txt, emoji-sequences.txt, emoji-zwj-sequences.txt.
// Downloads: https://www.unicode.org/Public/16.0.0/ucd/... and
// https://www.unicode.org/Public/emoji/16.0/... (emoji-sequences/-zwj).

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";

const [ucdDir, outFile] = process.argv.slice(2);
if (!ucdDir || !outFile) {
	console.error("usage: node scripts/gen-unicode-tables.mjs <ucd-dir> <out-file>");
	process.exit(1);
}

const read = (name) => readFileSync(`${ucdDir}/${name}`, "utf8");

function parseRanges(text, propertyName) {
	// Parses "XXXX..YYYY ; prop" / "XXXX ; prop" lines for a property whose
	// value is its own token (PropList-style binary properties).
	const ranges = [];
	for (const line of text.split("\n")) {
		if (line.startsWith("#") || line.trim() === "") continue;
		const match = /^([0-9A-F]+)(?:\.\.([0-9A-F]+))?\s*;\s*([^\s#]+)/.exec(line);
		if (!match || match[3] !== propertyName) continue;
		ranges.push([parseInt(match[1], 16), parseInt(match[2] ?? match[1], 16)]);
	}
	return ranges;
}

function parseInCB(text, value) {
	// DerivedCoreProperties-style lines with a value field: "XXXX ; InCB; Linker".
	const ranges = [];
	for (const line of text.split("\n")) {
		if (line.startsWith("#") || line.trim() === "") continue;
		const match = /^([0-9A-F]+)(?:\.\.([0-9A-F]+))?\s*;\s*InCB;\s*(\S+)/.exec(line);
		if (!match || match[3] !== value) continue;
		ranges.push([parseInt(match[1], 16), parseInt(match[2] ?? match[1], 16)]);
	}
	return ranges;
}

function parseSequences(text, sequenceType) {
	// "XXXX YYYY...; Type ; comment" -> decoded rune arrays; ranges expand.
	const sequences = [];
	for (const line of text.split("\n")) {
		if (line.startsWith("#") || line.trim() === "") continue;
		const semicolon1 = line.indexOf(";");
		const semicolon2 = line.indexOf(";", semicolon1 + 1);
		const fieldsPart = line.slice(0, semicolon1).trim();
		const type = line.slice(semicolon1 + 1, semicolon2 < 0 ? undefined : semicolon2).trim();
		if (type !== sequenceType) continue;
		const fields = fieldsPart.split(/\s+/);
		const runes = [];
		for (const field of fields) {
			const range = field.split("..");
			if (range.length === 2) {
				const start = parseInt(range[0], 16);
				const end = parseInt(range[1], 16);
				if (runes.length !== 0) throw new Error(`range not in first field: ${line}`);
				// Ranges only appear as singleton-field Basic_Emoji entries; a
				// range plus more fields never occurs in these files.
				for (let cp = start; cp <= end; cp++) sequences.push([cp]);
				runes.length = 0;
				break;
			}
			runes.push(parseInt(field, 16));
		}
		if (runes.length > 0) sequences.push(runes);
	}
	return sequences;
}

function goRune(r) {
	const hex = r.toString(16);
	return r <= 0xffff ? "\\u" + hex.padStart(4, "0") : "\\U" + hex.padStart(8, "0");
}

function goSequence(runes) {
	return runes.map(goRune).join("");
}

function emitRangeTable(name, ranges) {
	if (ranges.length === 0) return `var ${name} = [][2]rune{}\n`;
	const lines = [`var ${name} = [][2]rune{`];
	for (let i = 0; i < ranges.length; i++) {
		const [start, end] = ranges[i];
		lines.push(`\t{0x${start.toString(16)}, 0x${end.toString(16)}},`);
	}
	lines.push("}");
	return lines.join("\n") + "\n";
}

function emitStringSet(name, sequences) {
	const encoded = sequences.map((runes) => JSON.stringify(goSequence(runes)));
	encoded.sort();
	const lines = [`var ${name} = map[string]struct{}{`];
	for (const value of encoded) lines.push(`\t${value}: {},`);
	lines.push("}");
	return lines.join("\n") + "\n";
}

const gbp = read("GraphemeBreakProperty.txt");
const wbp = read("WordBreakProperty.txt");
const dcp = read("DerivedCoreProperties.txt");
const emojiData = read("emoji-data.txt");
const emojiSequences = read("emoji-sequences.txt");
const emojiZwj = read("emoji-zwj-sequences.txt");

const sources = [
	["GraphemeBreakProperty.txt", gbp],
	["WordBreakProperty.txt", wbp],
	["DerivedCoreProperties.txt", dcp],
	["emoji-data.txt", emojiData],
	["emoji-sequences.txt", emojiSequences],
	["emoji-zwj-sequences.txt", emojiZwj],
];

const gcbClasses = ["CR", "LF", "Control", "Extend", "ZWJ", "Regional_Indicator", "Prepend", "SpacingMark", "L", "V", "T", "LV", "LVT"];
const parts = [
	"package tui\n",
	"\n",
	"// Code generated from UCD 16.0.0 data files; DO NOT EDIT. Regenerate with\n",
	"// scripts/gen-unicode-tables.mjs (see that file for the download sources).\n",
	"// Unicode 16.0.0 matches the reference runtimes' tables: Node >= 22.19's ICU\n",
	"// and Go's unicode stdlib package.\n",
	"\n",
];
for (const cls of gcbClasses) parts.push(emitRangeTable(`gcb${cls.replaceAll("_", "")}`, parseRanges(gbp, cls)));
const wbClasses = ["CR", "LF", "Newline", "Extend", "ZWJ", "Regional_Indicator", "Format", "Katakana", "Hebrew_Letter", "ALetter", "Single_Quote", "Double_Quote", "MidNumLet", "MidLetter", "MidNum", "Numeric", "ExtendNumLet", "WSegSpace"];
for (const cls of wbClasses) parts.push(emitRangeTable(`wbt${cls.replaceAll("_", "")}`, parseRanges(wbp, cls)));
parts.push(emitRangeTable("propDefaultIgnorable", parseRanges(dcp, "Default_Ignorable_Code_Point")));
parts.push(emitRangeTable("propExtendedPictographic", parseRanges(emojiData, "Extended_Pictographic")));
parts.push(emitRangeTable("propEmojiPresentation", parseRanges(emojiData, "Emoji_Presentation")));
parts.push(emitRangeTable("propEmojiModifier", parseRanges(emojiData, "Emoji_Modifier")));
parts.push(emitRangeTable("propEmojiModifierBase", parseRanges(emojiData, "Emoji_Modifier_Base")));
parts.push(emitRangeTable("incbLinker", parseInCB(dcp, "Linker")));
parts.push(emitRangeTable("incbConsonant", parseInCB(dcp, "Consonant")));
parts.push(emitRangeTable("incbExtend", parseInCB(dcp, "Extend")));
parts.push(emitStringSet("rgiBasicEmoji", parseSequences(emojiSequences, "Basic_Emoji")));
parts.push(emitStringSet("rgiFlagSequences", parseSequences(emojiSequences, "RGI_Emoji_Flag_Sequence")));
parts.push(emitStringSet("rgiTagSequences", parseSequences(emojiSequences, "RGI_Emoji_Tag_Sequence")));
parts.push(emitStringSet("rgiModifierSequences", parseSequences(emojiSequences, "RGI_Emoji_Modifier_Sequence")));
parts.push(emitStringSet("rgiZwjSequences", parseSequences(emojiZwj, "RGI_Emoji_ZWJ_Sequence")));

parts.push(`// unicodeSourceDigests pins the generator inputs.\nvar unicodeSourceDigests = map[string]string{\n`);
for (const [name, text] of sources) {
	parts.push(`\t"${name}": "${createHash("sha256").update(text).digest("hex")}",\n`);
}
parts.push("}\n");

writeFileSync(outFile, parts.join(""));
console.log(`wrote ${outFile}`);
for (const cls of gcbClasses) console.log(` gcb${cls.replaceAll("_", "")}: ${parseRanges(gbp, cls).length} ranges`);
console.log(` basic ${rgiCount(emojiSequences, "Basic_Emoji")}, flags ${rgiCount(emojiSequences, "RGI_Emoji_Flag_Sequence")}, tags ${rgiCount(emojiSequences, "RGI_Emoji_Tag_Sequence")}, modifiers ${rgiCount(emojiSequences, "RGI_Emoji_Modifier_Sequence")}, zwj ${rgiCount(emojiZwj, "RGI_Emoji_ZWJ_Sequence")}, incbExtend ${parseInCB(dcp, "Extend").length}`);

function rgiCount(text, type) {
	return parseSequences(text, type).length;
}
