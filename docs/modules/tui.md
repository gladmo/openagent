# tui

English | [中文](tui.zh.md)

`tui` is the Go port of pi-tui v0.87.1: terminal UI primitives — colors, segmentation, keys, components, a differential renderer, and full-screen management. The port inventory is in [PARITY.md](../../PARITY.md); per-round records in [PORTING.md](../../PORTING.md).

## Colors

- `colors.go` converts OKLCH ↔ sRGB and renders ANSI sequences (`ForegroundAnsi`/`BackgroundAnsi`) per `TerminalColorMode` (256color, truecolor).
- `terminal_colors.go` reads/writes terminal default colors via OSC 10/11.
- `ParseColor` accepts hex, `rgb()`, `oklch()`, and indexed specs; every color converts to sRGB so math never fails.

## Text segmentation

- `grapheme.go` implements UAX#29 grapheme clusters (GB9c/GB11/RI parity). `unicode_data.go` holds generated UCD 16.0.0 tables (GCB/WB/InCB/Extended_Pictographic/RGI emoji/EAW); regenerate with `scripts/gen-unicode-tables.mjs`.
- `word_segmenter.go` implements UAX#29 words. CJK dictionary segmentation (ICU CLDR) is a documented deviation: default stepping is per-character.
- `utils.go` provides `VisibleWidth`, `TruncateToWidth`, `WrapTextWithAnsi`, `SliceByColumn`, and OSC-8 hyperlink tracking.

## Keys and input

- `keys.go` decodes Kitty CSI-u, xterm modifyOtherKeys, and legacy sequences; `keybindings.go` resolves named actions; `stdin_buffer.go` chunks stdin reads.

## Components

Single-line `input` editor, multi-line `editor` (paste markers, kill ring, undo, character jump, sticky-column cursor), `markdown` renderer over the marked-compatible lexer, `latex` renderer, `select_list`/`settings_list`, `image` handling (kitty/sixel/iTerm), `stack`/`box`/`text` layout primitives, `autocomplete` provider integration.

## Screens

- `tui_main_screen.go` renders differentially from a component tree; `tui_alt_screen.go` adds selection, search, scrollbars, and flash management.
- The focus engine tracks overlay restore; the 16ms scheduler batches renders.

## Known limitations

- `native-modifiers`/`native-platform`/`native-module-path` (NAPI) have no Go equivalent; clipboard falls back to OSC52 + system commands.
- CJK dictionary segmentation deviates (per-char UAX#29 by default).
