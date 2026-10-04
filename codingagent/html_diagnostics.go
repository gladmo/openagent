package codingagent

// Port of utils/html.ts and core/diagnostics.ts: HTML entity decoding and
// the resource diagnostic/collision shapes.

import (
	"fmt"
	"strconv"
	"strings"
)

// decodeCodePoint validates and renders one code point.
func decodeCodePoint(codePoint int64) (string, bool) {
	if codePoint < 0 || codePoint > 0x10ffff {
		return "", false
	}
	return string(rune(codePoint)), true
}

// DecodeHtmlEntity decodes one entity body ("amp", "#x27", "#39", ...).
func DecodeHtmlEntity(entity string) (string, bool) {
	switch entity {
	case "amp":
		return "&", true
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "quot":
		return `"`, true
	case "apos":
		return "'", true
	}

	if strings.HasPrefix(entity, "#x") || strings.HasPrefix(entity, "#X") {
		value, err := strconv.ParseInt(entity[2:], 16, 64)
		if err != nil {
			return "", false
		}
		return decodeCodePoint(value)
	}
	if strings.HasPrefix(entity, "#") {
		value, err := strconv.ParseInt(entity[1:], 10, 64)
		if err != nil {
			return "", false
		}
		return decodeCodePoint(value)
	}
	return "", false
}

// DecodedHtmlEntity is one decoded entity with its source length.
type DecodedHtmlEntity struct {
	Text   string
	Length int
}

// DecodeHtmlEntityAtHtml decodes the entity starting at html[index] (which
// must be '&'); nil when no valid entity fits within 16 characters.
func DecodeHtmlEntityAtHtml(html string, index int) *DecodedHtmlEntity {
	semicolonIndex := strings.IndexByte(html[index+1:], ';')
	if semicolonIndex == -1 || semicolonIndex+1 > 16 {
		return nil
	}
	semicolonIndex += index + 1

	entity := html[index+1 : semicolonIndex]
	decoded, ok := DecodeHtmlEntity(entity)
	if !ok {
		return nil
	}
	return &DecodedHtmlEntity{Text: decoded, Length: semicolonIndex - index + 1}
}

// ============================================================================
// diagnostics.ts
// ============================================================================

// ResourceCollisionType names the colliding resource kind.
type ResourceCollisionType string

const (
	CollisionExtension ResourceCollisionType = "extension"
	CollisionSkill     ResourceCollisionType = "skill"
	CollisionPrompt    ResourceCollisionType = "prompt"
	CollisionTheme     ResourceCollisionType = "theme"
)

// ResourceCollision reports a name collision between two resources.
type ResourceCollision struct {
	ResourceType ResourceCollisionType
	Name         string
	WinnerPath   string
	LoserPath    string
	WinnerSource string
	LoserSource  string
}

// ResourceDiagnostic reports one resource loading issue.
type ResourceDiagnostic struct {
	Type      string // "warning" | "error" | "collision"
	Message   string
	Path      string
	Collision *ResourceCollision
}

// FormatResourceDiagnostic renders the diagnostic for logs.
func FormatResourceDiagnostic(diagnostic *ResourceDiagnostic) string {
	if diagnostic.Collision != nil {
		collision := diagnostic.Collision
		return fmt.Sprintf("%s collision %q: winner %s, loser %s", collision.ResourceType, collision.Name, collision.WinnerPath, collision.LoserPath)
	}
	if diagnostic.Path != "" {
		return fmt.Sprintf("%s: %s (%s)", diagnostic.Type, diagnostic.Message, diagnostic.Path)
	}
	return fmt.Sprintf("%s: %s", diagnostic.Type, diagnostic.Message)
}
