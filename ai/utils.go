package ai

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

// ContentText extracts and joins text blocks (utils/text.ts).
func ContentText(content Content, separator string) string {
	if content.IsText {
		return content.Text
	}
	parts := make([]string, 0, len(content.Blocks))
	for _, block := range content.Blocks {
		if block.ContentType() == "text" {
			parts = append(parts, block.(TextContent).Text)
		}
	}
	return strings.Join(parts, separator)
}

// GetSystemMessageText renders a system message as a complete prompt: its
// content followed by its sections (insertion order preserved via jsonx
// objects; map iteration here follows sorted order, which TS tests never
// observe because pi only reads section values through ordered helpers).
func GetSystemMessageText(message *SystemMessage) string {
	parts := []string{ContentText(message.Content, "\n")}
	for _, name := range sortedSectionNames(message.Sections) {
		value := message.Sections[name]
		if value != nil {
			parts = append(parts, *value)
		}
	}
	filtered := parts[:0]
	for _, part := range parts {
		if len(part) > 0 {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "\n\n")
}

// RenderSystemMessageUpdate renders a later system message for APIs that
// accept system messages mid-conversation.
func RenderSystemMessageUpdate(message *SystemMessage) string {
	parts := []string{}
	text := ContentText(message.Content, "\n")
	if len(text) > 0 {
		parts = append(parts, text)
	}
	for _, name := range sortedSectionNames(message.Sections) {
		value := message.Sections[name]
		if value == nil {
			parts = append(parts, fmt.Sprintf("Removed system prompt section %q.", name))
		} else {
			parts = append(parts, fmt.Sprintf("Updated system prompt section %q:\n\n%s", name, *value))
		}
	}
	return strings.Join(parts, "\n\n")
}

func sortedSectionNames(sections map[string]*string) []string {
	names := make([]string, 0, len(sections))
	for name := range sections {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// uuidv7 (utils/uuid.ts)
// ---------------------------------------------------------------------------

const maxUUIDv7Timestamp = 0xffffffffffff

var (
	uuidMu          sync.Mutex
	lastOrdinaryTS  int64 = -1
	uuidSequence    *big.Int
	maxUUIDSequence = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 41), big.NewInt(1))
)

// UUIDv7 generates a time-ordered UUIDv7. A supplied timestamp is preserved
// for follower ids; the no-argument form is monotonic (never regresses).
func UUIDv7(timestampMs ...float64) string {
	requested := nowMs()
	if len(timestampMs) > 0 {
		requested = timestampMs[0]
	}
	if requested != float64(int64(requested)) || requested < 0 || requested > maxUUIDv7Timestamp {
		panic(fmt.Sprintf("UUIDv7 timestamp must be an integer between 0 and %d", maxUUIDv7Timestamp))
	}

	uuidMu.Lock()
	defer uuidMu.Unlock()
	effective := int64(requested)
	if len(timestampMs) == 0 {
		if effective < lastOrdinaryTS {
			effective = lastOrdinaryTS
		}
		lastOrdinaryTS = effective
	}

	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	if uuidSequence == nil {
		uuidSequence = new(big.Int).Lsh(big.NewInt(int64(bytes[1])), 32)
		uuidSequence.Or(uuidSequence, new(big.Int).Lsh(big.NewInt(int64(bytes[2])), 24))
		uuidSequence.Or(uuidSequence, new(big.Int).Lsh(big.NewInt(int64(bytes[3])), 16))
		uuidSequence.Or(uuidSequence, new(big.Int).Lsh(big.NewInt(int64(bytes[4])), 8))
		uuidSequence.Or(uuidSequence, big.NewInt(int64(bytes[5])))
	} else {
		if uuidSequence.Cmp(maxUUIDSequence) == 0 {
			panic("UUIDv7 generator sequence exhausted")
		}
		uuidSequence.Add(uuidSequence, big.NewInt(1))
	}

	ts := big.NewInt(effective)
	for index := 5; index >= 0; index-- {
		shift := uint((5 - index) * 8)
		bytes[index] = byte(new(big.Int).And(new(big.Int).Rsh(ts, shift), big.NewInt(0xff)).Int64())
	}
	seq := uuidSequence
	setByte := func(index int, v *big.Int) { bytes[index] = byte(v.Int64()) }
	setByte(6, orConst(new(big.Int).And(new(big.Int).Rsh(seq, 37), big.NewInt(0x0f)), 0x70))
	setByte(7, new(big.Int).And(new(big.Int).Rsh(seq, 29), big.NewInt(0xff)))
	setByte(8, orConst(new(big.Int).And(new(big.Int).Rsh(seq, 23), big.NewInt(0x3f)), 0x80))
	setByte(9, new(big.Int).And(new(big.Int).Rsh(seq, 15), big.NewInt(0xff)))
	setByte(10, new(big.Int).And(new(big.Int).Rsh(seq, 7), big.NewInt(0xff)))
	setByte(11, orConst(new(big.Int).Lsh(new(big.Int).And(seq, big.NewInt(0x7f)), 1), int64(bytes[11]&0x01)))

	hex := make([]string, 16)
	for i, b := range bytes {
		hex[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(hex[0:4], "") + "-" + strings.Join(hex[4:6], "") + "-" +
		strings.Join(hex[6:8], "") + "-" + strings.Join(hex[8:10], "") + "-" + strings.Join(hex[10:], "")
}

func orConst(a *big.Int, c int64) *big.Int { return new(big.Int).Or(a, big.NewInt(c)) }

func nowMs() float64 { return float64(time.Now().UnixMilli()) }

// ---------------------------------------------------------------------------
// diagnostics (utils/diagnostics.ts)
// ---------------------------------------------------------------------------

// FormatThrownValue mirrors formatThrownValue.
func FormatThrownValue(value any) string {
	switch t := value.(type) {
	case error:
		if t.Error() != "" {
			return t.Error()
		}
		return fmt.Sprintf("%T", t)
	case string:
		return t
	default:
		return fmt.Sprint(value)
	}
}

// ExtractDiagnosticError mirrors extractDiagnosticError. Go errors expose no
// name/stack the way JS Errors do; the type name stands in for name.
func ExtractDiagnosticError(err any) DiagnosticErrorInfo {
	if e, ok := err.(error); ok {
		name := fmt.Sprintf("%T", e)
		info := DiagnosticErrorInfo{Name: &name, Message: e.Error()}
		if info.Message == "" {
			info.Message = name
		}
		if coder, ok := err.(interface{ Code() any }); ok {
			code := coder.Code()
			switch code.(type) {
			case string, float64, int:
				info.Code = code
			}
		}
		return info
	}
	name := "ThrownValue"
	return DiagnosticErrorInfo{Name: &name, Message: FormatThrownValue(err)}
}

// CreateAssistantMessageDiagnostic mirrors createAssistantMessageDiagnostic.
func CreateAssistantMessageDiagnostic(typ string, err any, details *jsonx.Obj) AssistantMessageDiagnostic {
	diagErr := ExtractDiagnosticError(err)
	return AssistantMessageDiagnostic{
		Type:      typ,
		Timestamp: nowMs(),
		Error:     &diagErr,
		Details:   details,
	}
}

// AppendAssistantMessageDiagnostic mirrors appendAssistantMessageDiagnostic.
func AppendAssistantMessageDiagnostic(message *AssistantMessage, diagnostic AssistantMessageDiagnostic) {
	message.Diagnostics = append(message.Diagnostics, diagnostic)
}
