package codingagent

// Tests for git URL parsing, HTML entity decoding, and resource
// diagnostics (ported from git.test.ts and html.test.ts surfaces).

import (
	"strings"
	"testing"
)

func TestParseGitUrlHttps(t *testing.T) {
	// The reference requires a git: prefix or scheme for bare host/path.
	source := ParseGitUrl("git:github.com/user/repo")
	if source == nil {
		t.Fatal("nil")
	}
	if source.Repo != "https://github.com/user/repo" {
		t.Fatalf("repo = %q", source.Repo)
	}
	if source.Host != "github.com" || source.Path != "user/repo" {
		t.Fatalf("host/path = %q/%q", source.Host, source.Path)
	}
	if source.Ref != "" || source.Pinned {
		t.Fatalf("ref = %q pinned = %v", source.Ref, source.Pinned)
	}
}

func TestParseGitUrlWithRef(t *testing.T) {
	source := ParseGitUrl("git:github.com/user/repo@v1.2.3")
	if source == nil {
		t.Fatal("nil")
	}
	if source.Ref != "v1.2.3" {
		t.Fatalf("ref = %q", source.Ref)
	}
	if !source.Pinned {
		t.Fatal("not pinned")
	}
	if source.Path != "user/repo" {
		t.Fatalf("path = %q", source.Path)
	}
}

func TestParseGitUrlScpLike(t *testing.T) {
	// git@ is the scp-like form: hasGitPrefix is false but the hosted
	// candidate starts with git@ which the scheme test... the reference's
	// regex only allows :// schemes, so bare git@ needs the git: prefix.
	source := ParseGitUrl("git:git@github.com:user/repo.git")
	if source == nil {
		t.Fatal("nil")
	}
	if source.Host != "github.com" {
		t.Fatalf("host = %q", source.Host)
	}
	if source.Path != "user/repo" {
		t.Fatalf("path = %q", source.Path)
	}
}

func TestParseGitUrlGitPrefix(t *testing.T) {
	source := ParseGitUrl("git:github.com/user/repo")
	if source == nil {
		t.Fatal("nil")
	}
	if source.Path != "user/repo" {
		t.Fatalf("path = %q", source.Path)
	}
}

func TestParseGitUrlFullHttps(t *testing.T) {
	source := ParseGitUrl("https://github.com/user/repo.git")
	if source == nil {
		t.Fatal("nil")
	}
	if source.Repo != "https://github.com/user/repo.git" {
		t.Fatalf("repo = %q", source.Repo)
	}
	if source.Path != "user/repo" {
		t.Fatalf("path = %q", source.Path)
	}
}

func TestParseGitUrlNegative(t *testing.T) {
	for _, invalid := range []string{
		"",
		"just-a-name",
		"npm:package",
	} {
		if source := ParseGitUrl(invalid); source != nil {
			t.Errorf("ParseGitUrl(%q) = %+v, want nil", invalid, source)
		}
	}
}

func TestParseGitUrlUnsafePaths(t *testing.T) {
	for _, unsafe := range []string{
		"github.com/../traversal",
		"github.com/user/..",
	} {
		// Traversal in the ref-split path yields either nil or a clean
		// path; the reference rejects outright.
		if source := ParseGitUrl(unsafe); source != nil && strings.Contains(source.Path, "..") {
			t.Errorf("unsafe path accepted: %+v", source)
		}
	}
}

func TestDecodeHtmlEntityNamed(t *testing.T) {
	cases := map[string]string{
		"amp":  "&",
		"lt":   "<",
		"gt":   ">",
		"quot": "\"",
		"apos": "'",
	}
	for entity, want := range cases {
		if got, ok := DecodeHtmlEntity(entity); !ok || got != want {
			t.Errorf("DecodeHtmlEntity(%q) = %q %v, want %q", entity, got, ok, want)
		}
	}
}

func TestDecodeHtmlEntityNumeric(t *testing.T) {
	if got, ok := DecodeHtmlEntity("#x27"); !ok || got != "'" {
		t.Errorf("hex = %q %v", got, ok)
	}
	if got, ok := DecodeHtmlEntity("#39"); !ok || got != "'" {
		t.Errorf("dec = %q %v", got, ok)
	}
	if got, ok := DecodeHtmlEntity("#x1F600"); !ok || got != "\U0001F600" {
		t.Errorf("emoji = %q %v", got, ok)
	}
	// Out of range / invalid.
	if _, ok := DecodeHtmlEntity("#x1100000"); ok {
		t.Error("out of range accepted")
	}
	if _, ok := DecodeHtmlEntity("unknown"); ok {
		t.Error("unknown accepted")
	}
}

func TestDecodeHtmlEntityAtHtml(t *testing.T) {
	decoded := DecodeHtmlEntityAtHtml("a&amp;b", 1)
	if decoded == nil || decoded.Text != "&" || decoded.Length != 5 {
		t.Fatalf("decoded = %+v", decoded)
	}

	// No semicolon.
	if DecodeHtmlEntityAtHtml("a&nope", 1) != nil {
		t.Fatal("unterminated accepted")
	}

	// Beyond 16 chars.
	if DecodeHtmlEntityAtHtml("a&"+strings.Repeat("x", 20)+";", 1) != nil {
		t.Fatal("overlong accepted")
	}
}

func TestResourceDiagnosticFormatting(t *testing.T) {
	collision := &ResourceDiagnostic{
		Type: "collision",
		Path: "/loser",
		Collision: &ResourceCollision{
			ResourceType: CollisionSkill,
			Name:         "review",
			WinnerPath:   "/winner",
			LoserPath:    "/loser",
		},
	}
	formatted := FormatResourceDiagnostic(collision)
	if !strings.Contains(formatted, "skill collision") || !strings.Contains(formatted, "winner /winner") {
		t.Fatalf("collision = %q", formatted)
	}

	warning := &ResourceDiagnostic{Type: "warning", Message: "bad thing", Path: "/p"}
	if got := FormatResourceDiagnostic(warning); !strings.Contains(got, "warning: bad thing (/p)") {
		t.Fatalf("warning = %q", got)
	}

	bare := &ResourceDiagnostic{Type: "error", Message: "failed"}
	if got := FormatResourceDiagnostic(bare); got != "error: failed" {
		t.Fatalf("bare = %q", got)
	}
}
