package tui

// Port of autocomplete.ts: the combined slash-command + file-path
// autocomplete provider. fd-based fuzzy search spawns the fd binary like
// the reference; direct directory reads use os.ReadDir.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gladmo/openagent/abort"
)

// AutocompleteItem is one completion candidate.
type AutocompleteItem struct {
	Value       string
	Label       string
	Description string
}

// SlashCommand is a command with optional argument completions.
type SlashCommand struct {
	Name        string
	Description string
	// ArgumentHint joins into the description ("hint — desc").
	ArgumentHint string
	// GetArgumentCompletions returns argument items, or nil when none.
	GetArgumentCompletions func(argumentPrefix string) []AutocompleteItem
}

// AutocompleteSuggestions carries the items and the matched prefix.
type AutocompleteSuggestions struct {
	Items  []AutocompleteItem
	Prefix string
}

// AutocompleteQueryOptions carry abort and force flags.
type AutocompleteQueryOptions struct {
	Signal *abort.Signal
	Force  bool
}

// AutocompleteResult is the post-completion state.
type AutocompleteResult struct {
	Lines      []string
	CursorLine int
	CursorCol  int
}

// AutocompleteProvider supplies suggestions for a cursor position.
type AutocompleteProvider interface {
	// TriggerCharacters naturally trigger this provider at token boundaries.
	TriggerCharacters() string
	// GetSuggestions returns suggestions, or nil when none.
	GetSuggestions(lines []string, cursorLine, cursorCol int, options AutocompleteQueryOptions) *AutocompleteSuggestions
	// ApplyCompletion applies an item at the prefix.
	ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) AutocompleteResult
	// ShouldTriggerFileCompletion gates explicit Tab completion.
	ShouldTriggerFileCompletion(lines []string, cursorLine, cursorCol int) bool
}

// slashCommandOrItem is the union the combined provider accepts.
type slashCommandOrItem struct {
	command *SlashCommand
	item    AutocompleteItem
}

func commandName(entry slashCommandOrItem) string {
	if entry.command != nil {
		return entry.command.Name
	}
	return entry.item.Value
}

var autocompletePathDelimiters = map[rune]bool{' ': true, '\t': true, '"': true, '\'': true, '=': true}

// Opening wrappers that may precede a path in prose, mapped to closers.
var autocompletePathWrappers = map[byte]byte{'(': ')', '[': ']', '{': '}', '<': '>', '`': '`'}

// jsWhitespaceClass mirrors JS \s (Go RE2 \s lacks U+00A0/U+3000/etc).
const jsWhitespaceClass = "[\t\n\v\f\r \u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]"

// cjkPunctClass covers the reference's CJK punctuation (explicit list plus
// the Script_Extensions branch).
const cjkPunctClass = "[，．：；！？（）［］｛｝“”‘’…—。、〃〈〉《》「」『』【】〔〕〖〗〘〙〚〛〜〝〞〟‧﹔﹕﹖﹗！：；？]"

var autocompleteTokenStartPattern = regexp.MustCompile(`(?:^|` + cjkPunctClass + "|" + jsWhitespaceClass + `)$`)

func autocompleteToDisplayPath(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

func autocompleteEscapeRegex(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '.', '*', '+', '?', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func autocompleteBuildFdPathQuery(query string) string {
	normalized := autocompleteToDisplayPath(query)
	if !strings.Contains(normalized, "/") {
		return normalized
	}

	hasTrailingSeparator := strings.HasSuffix(normalized, "/")
	trimmed := strings.Trim(normalized, "/")
	if trimmed == "" {
		return normalized
	}

	separatorPattern := "[\\\\/]"
	var segments []string
	for _, segment := range strings.Split(trimmed, "/") {
		if segment != "" {
			segments = append(segments, autocompleteEscapeRegex(segment))
		}
	}
	if len(segments) == 0 {
		return normalized
	}

	pattern := strings.Join(segments, separatorPattern)
	if hasTrailingSeparator {
		pattern += separatorPattern
	}
	return pattern
}

func autocompleteFindLastDelimiter(text string) int {
	lastDelimiter := -1
	index := 0
	for _, character := range text {
		index += len(string(character))
		if autocompletePathDelimiters[character] || AutocompleteSeparatorRune(character) {
			lastDelimiter = index - 1
		}
	}
	return lastDelimiter
}

// stripLeadingWrappers strips opening wrappers before a path ("(~/Dev" ->
// "~/Dev") but keeps a wrapper when its closer appears later.
func autocompleteStripLeadingWrappers(token string) string {
	result := token
	for len(result) > 0 {
		closer, ok := autocompletePathWrappers[result[0]]
		if !ok || strings.IndexByte(result[1:], closer) >= 0 {
			break
		}
		result = result[1:]
	}
	return result
}

func autocompleteFindUnclosedQuoteStart(text string) int {
	inQuotes := false
	quoteStart := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteStart = i
			}
		}
	}
	if inQuotes {
		return quoteStart
	}
	return -1
}

func autocompleteIsTokenStart(text string, index int) bool {
	start := index
	for start > 0 {
		if _, ok := autocompletePathWrappers[text[start-1]]; !ok {
			break
		}
		start--
	}
	if start == 0 {
		return true
	}
	if autocompletePathDelimiters[rune(text[start-1])] {
		return true
	}
	return autocompleteTokenStartPattern.MatchString(text[:start])
}

func autocompleteExtractQuotedPrefix(text string) string {
	quoteStart := autocompleteFindUnclosedQuoteStart(text)
	if quoteStart == -1 {
		return ""
	}

	if quoteStart > 0 && text[quoteStart-1] == '@' {
		if !autocompleteIsTokenStart(text, quoteStart-1) {
			return ""
		}
		return text[quoteStart-1:]
	}

	if !autocompleteIsTokenStart(text, quoteStart) {
		return ""
	}
	return text[quoteStart:]
}

type autocompletePathPrefixInfo struct {
	rawPrefix      string
	isAtPrefix     bool
	isQuotedPrefix bool
}

func autocompleteParsePathPrefix(prefix string) autocompletePathPrefixInfo {
	switch {
	case strings.HasPrefix(prefix, "@\""):
		return autocompletePathPrefixInfo{rawPrefix: prefix[2:], isAtPrefix: true, isQuotedPrefix: true}
	case strings.HasPrefix(prefix, "\""):
		return autocompletePathPrefixInfo{rawPrefix: prefix[1:], isAtPrefix: false, isQuotedPrefix: true}
	case strings.HasPrefix(prefix, "@"):
		return autocompletePathPrefixInfo{rawPrefix: prefix[1:], isAtPrefix: true, isQuotedPrefix: false}
	}
	return autocompletePathPrefixInfo{rawPrefix: prefix}
}

func autocompleteBuildCompletionValue(path string, isDirectory, isAtPrefix, isQuotedPrefix bool) string {
	needsQuotes := isQuotedPrefix
	if !needsQuotes {
		for _, r := range path {
			if AutocompleteSeparatorRune(r) {
				needsQuotes = true
				break
			}
		}
	}
	prefix := ""
	if isAtPrefix {
		prefix = "@"
	}

	if !needsQuotes {
		return prefix + path
	}
	return prefix + "\"" + path + "\""
}

type autocompleteWalkEntry struct {
	path        string
	isDirectory bool
}

// walkDirectoryWithFd runs fd like the reference (fast, .gitignore-aware).
func walkDirectoryWithFd(baseDir, fdPath, query string, maxResults int, signal *abort.Signal, maxDepth int) []autocompleteWalkEntry {
	args := []string{
		"--base-directory", baseDir,
		"--max-results", itoa(maxResults),
		"--type", "f",
		"--type", "d",
		"--follow",
		"--hidden",
		"--exclude", ".git",
		"--exclude", ".git/*",
		"--exclude", ".git/**",
	}
	if maxDepth > 0 {
		args = append(args, "--max-depth", itoa(maxDepth))
	}
	if strings.Contains(autocompleteToDisplayPath(query), "/") {
		args = append(args, "--full-path")
	}
	if query != "" {
		args = append(args, autocompleteBuildFdPathQuery(query))
	}

	if signal.Aborted() {
		return nil
	}

	ctx := context.Background()
	if done := signal.Done(); done != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	cmd := exec.CommandContext(ctx, fdPath, args...)
	stdout, err := cmd.Output()
	if err != nil || signal.Aborted() || len(stdout) == 0 {
		return nil
	}

	var results []autocompleteWalkEntry
	for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
		if line == "" {
			continue
		}
		displayLine := autocompleteToDisplayPath(line)
		hasTrailingSeparator := strings.HasSuffix(displayLine, "/")
		normalizedPath := strings.TrimSuffix(displayLine, "/")
		if normalizedPath == ".git" || strings.HasPrefix(normalizedPath, ".git/") || strings.Contains(normalizedPath, "/.git/") {
			continue
		}
		results = append(results, autocompleteWalkEntry{path: displayLine, isDirectory: hasTrailingSeparator})
	}
	return results
}

// CombinedAutocompleteProvider handles slash commands and file paths.
type CombinedAutocompleteProvider struct {
	commands []slashCommandOrItem
	basePath string
	fdPath   string
}

// NewCombinedAutocompleteProvider builds the provider; fdPath may be empty
// to disable fuzzy search.
func NewCombinedAutocompleteProvider(commands []SlashCommand, items []AutocompleteItem, basePath, fdPath string) *CombinedAutocompleteProvider {
	provider := &CombinedAutocompleteProvider{basePath: basePath, fdPath: fdPath}
	for i := range commands {
		provider.commands = append(provider.commands, slashCommandOrItem{command: &commands[i]})
	}
	for _, item := range items {
		provider.commands = append(provider.commands, slashCommandOrItem{item: item})
	}
	return provider
}

// TriggerCharacters lists natural trigger characters.
func (p *CombinedAutocompleteProvider) TriggerCharacters() string { return "/@.~" }

type autocompleteCommandItem struct {
	name        string
	label       string
	description string
}

// GetSuggestions returns suggestions at the cursor, or nil.
func (p *CombinedAutocompleteProvider) GetSuggestions(lines []string, cursorLine, cursorCol int, options AutocompleteQueryOptions) *AutocompleteSuggestions {
	currentLine := ""
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	if cursorCol > len(currentLine) {
		cursorCol = len(currentLine)
	}
	textBeforeCursor := currentLine[:cursorCol]

	if atPrefix := p.extractAtPrefix(textBeforeCursor); atPrefix != "" {
		info := autocompleteParsePathPrefix(atPrefix)
		suggestions := p.getFuzzyFileSuggestions(info.rawPrefix, info.isQuotedPrefix, options.Signal)
		if len(suggestions) == 0 {
			return nil
		}
		return &AutocompleteSuggestions{Items: suggestions, Prefix: atPrefix}
	}

	if !options.Force && strings.HasPrefix(textBeforeCursor, "/") {
		spaceIndex := strings.IndexByte(textBeforeCursor, ' ')

		if spaceIndex == -1 {
			prefix := textBeforeCursor[1:]
			var commandItems []autocompleteCommandItem
			for _, entry := range p.commands {
				name := commandName(entry)
				hint := ""
				desc := ""
				if entry.command != nil {
					hint = entry.command.ArgumentHint
					desc = entry.command.Description
				} else {
					desc = entry.item.Description
				}
				fullDesc := desc
				if hint != "" {
					if desc != "" {
						fullDesc = hint + " — " + desc
					} else {
						fullDesc = hint
					}
				}
				commandItems = append(commandItems, autocompleteCommandItem{name: name, label: name, description: fullDesc})
			}

			bareName := func(item autocompleteCommandItem) string {
				return strings.TrimPrefix(item.name, "skill:")
			}
			bareNameMatches := FuzzyFilter(commandItems, prefix, bareName)
			bareNameMatchSet := map[string]bool{}
			for _, item := range bareNameMatches {
				bareNameMatchSet[item.name] = true
			}
			var fullNameOnly []autocompleteCommandItem
			for _, item := range commandItems {
				if strings.HasPrefix(item.name, "skill:") && !bareNameMatchSet[item.name] {
					fullNameOnly = append(fullNameOnly, item)
				}
			}
			fullNameOnlyMatches := FuzzyFilter(fullNameOnly, prefix, func(item autocompleteCommandItem) string { return item.name })

			var filtered []AutocompleteItem
			for _, item := range append(bareNameMatches, fullNameOnlyMatches...) {
				entry := AutocompleteItem{Value: item.name, Label: item.label}
				if item.description != "" {
					entry.Description = item.description
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				return nil
			}
			return &AutocompleteSuggestions{Items: filtered, Prefix: textBeforeCursor}
		}

		commandNameText := textBeforeCursor[1:spaceIndex]
		argumentText := textBeforeCursor[spaceIndex+1:]

		var command *SlashCommand
		for i := range p.commands {
			if commandName(p.commands[i]) == commandNameText && p.commands[i].command != nil {
				command = p.commands[i].command
				break
			}
		}
		if command == nil || command.GetArgumentCompletions == nil {
			return nil
		}

		argumentSuggestions := command.GetArgumentCompletions(argumentText)
		if len(argumentSuggestions) == 0 {
			return nil
		}
		return &AutocompleteSuggestions{Items: argumentSuggestions, Prefix: argumentText}
	}

	pathMatch := p.extractPathPrefix(textBeforeCursor, options.Force)
	if pathMatch == nil {
		return nil
	}

	suggestions := p.getFileSuggestions(*pathMatch)
	if len(suggestions) == 0 {
		return nil
	}
	return &AutocompleteSuggestions{Items: suggestions, Prefix: *pathMatch}
}

// ApplyCompletion applies an item, returning the new state.
func (p *CombinedAutocompleteProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) AutocompleteResult {
	currentLine := ""
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	if cursorCol > len(currentLine) {
		cursorCol = len(currentLine)
	}
	// Cursor movement while the completion list is open can leave the
	// cursor before the recorded prefix; clamp instead of panicking.
	prefixLen := len(prefix)
	if prefixLen > cursorCol {
		prefixLen = cursorCol
	}
	beforePrefix := currentLine[:cursorCol-prefixLen]
	afterCursor := currentLine[cursorCol:]
	isQuotedPrefix := strings.HasPrefix(prefix, "\"") || strings.HasPrefix(prefix, "@\"")
	hasLeadingQuoteAfterCursor := strings.HasPrefix(afterCursor, "\"")
	hasTrailingQuoteInItem := strings.HasSuffix(item.Value, "\"")
	adjustedAfterCursor := afterCursor
	if isQuotedPrefix && hasTrailingQuoteInItem && hasLeadingQuoteAfterCursor {
		adjustedAfterCursor = afterCursor[1:]
	}

	replaceLine := func(newLine string, cursorCol int) AutocompleteResult {
		newLines := append([]string(nil), lines...)
		newLines[cursorLine] = newLine
		return AutocompleteResult{Lines: newLines, CursorLine: cursorLine, CursorCol: cursorCol}
	}

	// Slash command: prefix starts with "/" at line start without further
	// path separators.
	isSlashCommand := strings.HasPrefix(prefix, "/") && strings.TrimSpace(beforePrefix) == "" && !strings.Contains(prefix[1:], "/")
	if isSlashCommand {
		newLine := beforePrefix + "/" + item.Value + " " + adjustedAfterCursor
		return replaceLine(newLine, len(beforePrefix)+len(item.Value)+2)
	}

	// File attachment: prefix starts with "@".
	if strings.HasPrefix(prefix, "@") {
		isDirectory := strings.HasSuffix(item.Label, "/")
		suffix := " "
		if isDirectory {
			suffix = ""
		}
		newLine := beforePrefix + item.Value + suffix + adjustedAfterCursor
		cursorOffset := len(item.Value)
		if isDirectory && hasTrailingQuoteInItem {
			cursorOffset = len(item.Value) - 1
		}
		return replaceLine(newLine, len(beforePrefix)+cursorOffset+len(suffix))
	}

	// Command-argument context: text contains "/command ".
	textBeforeCursor := currentLine[:cursorCol]
	if strings.Contains(textBeforeCursor, "/") && strings.Contains(textBeforeCursor, " ") {
		newLine := beforePrefix + item.Value + adjustedAfterCursor
		isDirectory := strings.HasSuffix(item.Label, "/")
		cursorOffset := len(item.Value)
		if isDirectory && hasTrailingQuoteInItem {
			cursorOffset = len(item.Value) - 1
		}
		return replaceLine(newLine, len(beforePrefix)+cursorOffset)
	}

	// Plain file path.
	newLine := beforePrefix + item.Value + adjustedAfterCursor
	isDirectory := strings.HasSuffix(item.Label, "/")
	cursorOffset := len(item.Value)
	if isDirectory && hasTrailingQuoteInItem {
		cursorOffset = len(item.Value) - 1
	}
	return replaceLine(newLine, len(beforePrefix)+cursorOffset)
}

// extractAtPrefix extracts the @-prefix for fuzzy file suggestions.
func (p *CombinedAutocompleteProvider) extractAtPrefix(text string) string {
	if quotedPrefix := autocompleteExtractQuotedPrefix(text); strings.HasPrefix(quotedPrefix, "@\"") {
		return quotedPrefix
	}

	lastDelimiterIndex := autocompleteFindLastDelimiter(text)
	tokenText := text
	if lastDelimiterIndex != -1 {
		tokenText = text[lastDelimiterIndex+1:]
	}
	token := autocompleteStripLeadingWrappers(tokenText)
	if strings.HasPrefix(token, "@") {
		return token
	}
	return ""
}

// extractPathPrefix extracts a path-like prefix; nil when none.
func (p *CombinedAutocompleteProvider) extractPathPrefix(text string, forceExtract bool) *string {
	if quotedPrefix := autocompleteExtractQuotedPrefix(text); quotedPrefix != "" {
		return &quotedPrefix
	}

	lastDelimiterIndex := autocompleteFindLastDelimiter(text)
	pathText := text
	if lastDelimiterIndex != -1 {
		pathText = text[lastDelimiterIndex+1:]
	}
	pathPrefix := autocompleteStripLeadingWrappers(pathText)

	if forceExtract {
		return &pathPrefix
	}

	if strings.Contains(pathPrefix, "/") || strings.HasPrefix(pathPrefix, ".") || strings.HasPrefix(pathPrefix, "~/") {
		return &pathPrefix
	}

	if pathPrefix == "" && text != "" && autocompleteTokenStartPattern.MatchString(text) {
		return &pathPrefix
	}

	return nil
}

// expandHomePath expands ~/ to the actual home directory.
func (p *CombinedAutocompleteProvider) expandHomePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		expanded := filepath.Join(home, path[2:])
		if strings.HasSuffix(path, "/") && !strings.HasSuffix(expanded, "/") {
			return expanded + "/"
		}
		return expanded
	}
	if path == "~" {
		return home
	}
	return path
}

type autocompleteScopedQuery struct {
	baseDir     string
	query       string
	displayBase string
}

func (p *CombinedAutocompleteProvider) resolveScopedFuzzyQuery(rawQuery string) *autocompleteScopedQuery {
	normalizedQuery := autocompleteToDisplayPath(rawQuery)
	slashIndex := strings.LastIndexByte(normalizedQuery, '/')
	if slashIndex == -1 {
		return nil
	}

	displayBase := normalizedQuery[:slashIndex+1]
	query := normalizedQuery[slashIndex+1:]

	var baseDir string
	switch {
	case strings.HasPrefix(displayBase, "~/"):
		baseDir = p.expandHomePath(displayBase)
	case strings.HasPrefix(displayBase, "/"):
		baseDir = displayBase
	default:
		baseDir = filepath.Join(p.basePath, displayBase)
	}

	info, err := os.Stat(baseDir)
	if err != nil || !info.IsDir() {
		return nil
	}
	return &autocompleteScopedQuery{baseDir: baseDir, query: query, displayBase: displayBase}
}

func (p *CombinedAutocompleteProvider) scopedPathForDisplay(displayBase, relativePath string) string {
	normalized := autocompleteToDisplayPath(relativePath)
	if displayBase == "/" {
		return "/" + normalized
	}
	return autocompleteToDisplayPath(displayBase) + normalized
}

// getFileSuggestions lists directory entries matching the prefix.
func (p *CombinedAutocompleteProvider) getFileSuggestions(prefix string) []AutocompleteItem {
	info := autocompleteParsePathPrefix(prefix)
	expandedPrefix := info.rawPrefix

	if strings.HasPrefix(expandedPrefix, "~") {
		expandedPrefix = p.expandHomePath(expandedPrefix)
	}

	isRootPrefix := info.rawPrefix == "" || info.rawPrefix == "./" || info.rawPrefix == "../" ||
		info.rawPrefix == "~" || info.rawPrefix == "~/" || info.rawPrefix == "/" ||
		(info.isAtPrefix && info.rawPrefix == "")

	var searchDir, searchPrefix string
	switch {
	case isRootPrefix || strings.HasSuffix(info.rawPrefix, "/"):
		if strings.HasPrefix(info.rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = expandedPrefix
		} else {
			searchDir = filepath.Join(p.basePath, expandedPrefix)
		}
		searchPrefix = ""
	default:
		dir := filepath.Dir(expandedPrefix)
		file := filepath.Base(expandedPrefix)
		if strings.HasPrefix(info.rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = dir
		} else {
			searchDir = filepath.Join(p.basePath, dir)
		}
		searchPrefix = file
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var suggestions []AutocompleteItem
	for _, entry := range entries {
		if !strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(searchPrefix)) {
			continue
		}

		isDirectory := entry.IsDir()
		if !isDirectory && entry.Type()&os.ModeSymlink != 0 {
			if stat, err := os.Stat(filepath.Join(searchDir, entry.Name())); err == nil {
				isDirectory = stat.IsDir()
			}
		}

		name := entry.Name()
		displayPrefix := info.rawPrefix
		var relativePath string
		switch {
		case strings.HasSuffix(displayPrefix, "/"):
			relativePath = displayPrefix + name
		case strings.ContainsAny(displayPrefix, "/\\"):
			if strings.HasPrefix(displayPrefix, "~/") {
				homeRelativeDir := displayPrefix[2:]
				dir := filepath.Dir(homeRelativeDir)
				if dir == "." {
					relativePath = "~/" + name
				} else {
					relativePath = "~/" + filepath.Join(dir, name)
				}
			} else if strings.HasPrefix(displayPrefix, "/") {
				dir := filepath.Dir(displayPrefix)
				if dir == "/" {
					relativePath = "/" + name
				} else {
					relativePath = dir + "/" + name
				}
			} else {
				relativePath = filepath.Join(filepath.Dir(displayPrefix), name)
				// path.Join normalizes away ./ prefix, preserve it.
				if strings.HasPrefix(displayPrefix, "./") && !strings.HasPrefix(relativePath, "./") {
					relativePath = "./" + relativePath
				}
			}
		default:
			if strings.HasPrefix(displayPrefix, "~") {
				relativePath = "~/" + name
			} else {
				relativePath = name
			}
		}

		relativePath = autocompleteToDisplayPath(relativePath)
		pathValue := relativePath
		if isDirectory {
			pathValue += "/"
		}
		value := autocompleteBuildCompletionValue(pathValue, isDirectory, info.isAtPrefix, info.isQuotedPrefix)
		label := name
		if isDirectory {
			label += "/"
		}
		suggestions = append(suggestions, AutocompleteItem{Value: value, Label: label})
	}

	// Directories first, then alphabetically.
	sort.SliceStable(suggestions, func(a, b int) bool {
		aIsDir := strings.HasSuffix(suggestions[a].Label, "/")
		bIsDir := strings.HasSuffix(suggestions[b].Label, "/")
		if aIsDir != bIsDir {
			return aIsDir
		}
		return suggestions[a].Label < suggestions[b].Label
	})
	return suggestions
}

// scoreEntry scores an fd entry against the query.
func (p *CombinedAutocompleteProvider) scoreEntry(filePath, query string, isDirectory bool) int {
	fileName := filepath.Base(filePath)
	lowerFileName := strings.ToLower(fileName)
	lowerQuery := strings.ToLower(query)

	score := 0
	switch {
	case lowerFileName == lowerQuery:
		score = 100
	case strings.HasPrefix(lowerFileName, lowerQuery):
		score = 80
	case strings.Contains(lowerFileName, lowerQuery):
		score = 50
	case strings.Contains(strings.ToLower(filePath), lowerQuery):
		score = 30
	}
	if isDirectory && score > 0 {
		score += 10
	}
	return score
}

// getFuzzyFileSuggestions runs the two-pass fd fuzzy search for @-prefixes.
func (p *CombinedAutocompleteProvider) getFuzzyFileSuggestions(query string, isQuotedPrefix bool, signal *abort.Signal) []AutocompleteItem {
	if p.fdPath == "" || signal.Aborted() {
		return nil
	}

	scopedQuery := p.resolveScopedFuzzyQuery(query)
	fdBaseDir := p.basePath
	fdQuery := query
	if scopedQuery != nil {
		fdBaseDir = scopedQuery.baseDir
		fdQuery = scopedQuery.query
	}
	baseDirEntries := walkDirectoryWithFd(fdBaseDir, p.fdPath, fdQuery, 100, signal, 1)
	recursiveEntries := walkDirectoryWithFd(fdBaseDir, p.fdPath, fdQuery, 100, signal, 0)
	seenPaths := map[string]bool{}
	for _, entry := range baseDirEntries {
		seenPaths[entry.path] = true
	}
	var entries []autocompleteWalkEntry
	entries = append(entries, baseDirEntries...)
	for _, entry := range recursiveEntries {
		if seenPaths[entry.path] {
			continue
		}
		seenPaths[entry.path] = true
		entries = append(entries, entry)
	}
	if signal.Aborted() {
		return nil
	}

	type scoredEntry struct {
		autocompleteWalkEntry
		score int
	}
	var scoredEntries []scoredEntry
	for _, entry := range entries {
		score := 1
		if fdQuery != "" {
			score = p.scoreEntry(entry.path, fdQuery, entry.isDirectory)
		}
		if score > 0 {
			scoredEntries = append(scoredEntries, scoredEntry{autocompleteWalkEntry: entry, score: score})
		}
	}

	sort.SliceStable(scoredEntries, func(a, b int) bool {
		if a := scoredEntries[a].score - scoredEntries[b].score; a != 0 {
			return scoredEntries[a].score > scoredEntries[b].score
		}
		aDepth := len(nonEmptySegments(autocompleteToDisplayPath(scoredEntries[a].path)))
		bDepth := len(nonEmptySegments(autocompleteToDisplayPath(scoredEntries[b].path)))
		if aDepth != bDepth {
			return aDepth < bDepth
		}
		if len(scoredEntries[a].path) != len(scoredEntries[b].path) {
			return len(scoredEntries[a].path) < len(scoredEntries[b].path)
		}
		return scoredEntries[a].path < scoredEntries[b].path
	})
	if len(scoredEntries) > 20 {
		scoredEntries = scoredEntries[:20]
	}

	var suggestions []AutocompleteItem
	for _, entry := range scoredEntries {
		pathWithoutSlash := entry.path
		if entry.isDirectory {
			pathWithoutSlash = entry.path[:len(entry.path)-1]
		}
		displayPath := pathWithoutSlash
		if scopedQuery != nil {
			displayPath = p.scopedPathForDisplay(scopedQuery.displayBase, pathWithoutSlash)
		}
		entryName := filepath.Base(pathWithoutSlash)
		completionPath := displayPath
		if entry.isDirectory {
			completionPath += "/"
		}
		value := autocompleteBuildCompletionValue(completionPath, entry.isDirectory, true, isQuotedPrefix)
		label := entryName
		if entry.isDirectory {
			label += "/"
		}
		suggestions = append(suggestions, AutocompleteItem{Value: value, Label: label, Description: displayPath})
	}
	return suggestions
}

func nonEmptySegments(path string) []string {
	var segments []string
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// ShouldTriggerFileCompletion gates Tab-triggered file completion.
func (p *CombinedAutocompleteProvider) ShouldTriggerFileCompletion(lines []string, cursorLine, cursorCol int) bool {
	currentLine := ""
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	if cursorCol > len(currentLine) {
		cursorCol = len(currentLine)
	}
	textBeforeCursor := currentLine[:cursorCol]

	trimmed := strings.TrimSpace(textBeforeCursor)
	if strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ") {
		return false
	}
	return true
}
