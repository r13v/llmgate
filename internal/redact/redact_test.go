package redact

import (
	"strings"
	"testing"
)

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		want   string
	}{
		{name: "empty", secret: "", want: "<empty>"},
		{name: "short generic", secret: "abc123", want: "***"},
		{name: "long generic", secret: "plain-token-1234", want: "***1234"},
		{name: "short sk", secret: "sk-abc", want: "sk-[redacted]"},
		{name: "long sk", secret: "sk-test-token-1234567890", want: "sk-...7890"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskSecret(tt.secret); got != tt.want {
				t.Fatalf("MaskSecret(%q) = %q, want %q", tt.secret, got, tt.want)
			}
		})
	}
}

func TestTextRedactsSecretsAndTokenPatterns(t *testing.T) {
	const known = "known-secret-9876"
	const bearer = "bearer-secret-1234"
	const header = "sk-test-token-1234567890"
	const assigned = "assigned-secret-2468"
	const colonAssigned = "colon-secret-1357"
	const jsonAssigned = "json-secret-8642"

	input := strings.Join([]string{
		"known: " + known,
		"Authorization: Bearer " + bearer,
		"x-litellm-api-key: " + header,
		`"x-litellm-api-key":"plain-litellm-key-1122"`,
		"ANTHROPIC_AUTH_TOKEN=" + assigned,
		"ANTHROPIC_AUTH_TOKEN: " + colonAssigned,
		`"ANTHROPIC_AUTH_TOKEN": "` + jsonAssigned + `"`,
		"gateway said token " + header + " was invalid",
	}, "\n")

	got := Text(input, Options{KnownSecrets: []string{known}})
	for _, notWant := range []string{known, bearer, header, "plain-litellm-key-1122", assigned, colonAssigned, jsonAssigned} {
		if strings.Contains(got, notWant) {
			t.Fatalf("redacted text leaked %q in:\n%s", notWant, got)
		}
	}
	for _, want := range []string{
		"known: ***9876",
		"Authorization: Bearer ***1234",
		"x-litellm-api-key: sk-...7890",
		`"x-litellm-api-key":"***1122"`,
		"ANTHROPIC_AUTH_TOKEN=***2468",
		"ANTHROPIC_AUTH_TOKEN: ***1357",
		`"ANTHROPIC_AUTH_TOKEN": "***8642"`,
		"gateway said token sk-...7890 was invalid",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted text missing %q in:\n%s", want, got)
		}
	}
}

func TestTextRedactsShortUnknownSKTokens(t *testing.T) {
	got := Text("gateway said sk-abc was invalid", Options{})
	if strings.Contains(got, "sk-abc") {
		t.Fatalf("short sk token leaked in:\n%s", got)
	}
	if !strings.Contains(got, "sk-[redacted]") {
		t.Fatalf("short sk token was not masked as expected:\n%s", got)
	}
}

func TestTextRedactsGenericCredentialParameters(t *testing.T) {
	input := strings.Join([]string{
		"https://gateway.example.com/v1/models?api_key=plain-api-key-1234&token=query-token-5678#fragment",
		"body api_key=body-api-key-2468",
		"body token=body-token-1122",
		"body token: colon-token-3344",
		`{"access_token":"json-access-token-1357"}`,
		`{"token":"json-token-9753"}`,
		"refresh_token: refresh-token-8642",
	}, "\n")

	got := Text(input, Options{})
	for _, notWant := range []string{
		"plain-api-key-1234",
		"query-token-5678",
		"body-api-key-2468",
		"body-token-1122",
		"colon-token-3344",
		"json-access-token-1357",
		"json-token-9753",
		"refresh-token-8642",
	} {
		if strings.Contains(got, notWant) {
			t.Fatalf("redacted text leaked %q in:\n%s", notWant, got)
		}
	}
	for _, want := range []string{
		"api_key=***1234",
		"token=***5678",
		"api_key=***2468",
		"token=***1122",
		"token: ***3344",
		`"access_token":"***1357"`,
		`"token":"***9753"`,
		"refresh_token: ***8642",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted text missing %q in:\n%s", want, got)
		}
	}
}

func TestShortenHomePathUnix(t *testing.T) {
	tests := []struct {
		name string
		path string
		home string
		want string
	}{
		{name: "exact", path: "/Users/alice", home: "/Users/alice", want: "~"},
		{name: "child", path: "/Users/alice/.claude/settings.json", home: "/Users/alice", want: "~/.claude/settings.json"},
		{name: "trailing home separator", path: "/Users/alice/.zshrc", home: "/Users/alice/", want: "~/.zshrc"},
		{name: "sibling is unchanged", path: "/Users/alice2/.zshrc", home: "/Users/alice", want: "/Users/alice2/.zshrc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortenHomePath(tt.path, tt.home, "linux"); got != tt.want {
				t.Fatalf("ShortenHomePath(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

func TestShortenHomePathWindows(t *testing.T) {
	tests := []struct {
		name string
		path string
		home string
		want string
	}{
		{name: "exact case insensitive", path: `c:\Users\Alice`, home: `C:\Users\Alice`, want: "~"},
		{name: "child with backslashes", path: `C:\Users\Alice\AppData\Roaming`, home: `C:\Users\Alice`, want: `~\AppData\Roaming`},
		{name: "child with slashes", path: `C:/Users/Alice/AppData/Roaming`, home: `C:\Users\Alice`, want: "~/AppData/Roaming"},
		{name: "sibling is unchanged", path: `C:\Users\Alice2\AppData`, home: `C:\Users\Alice`, want: `C:\Users\Alice2\AppData`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortenHomePath(tt.path, tt.home, "windows"); got != tt.want {
				t.Fatalf("ShortenHomePath(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

func TestTextShortensHomePaths(t *testing.T) {
	got := Text(
		`paths: /Users/alice/.claude/settings.json and C:\Users\Alice\AppData`,
		Options{HomeDir: "/Users/alice", GOOS: "linux"},
	)
	if !strings.Contains(got, "paths: ~/.claude/settings.json") {
		t.Fatalf("unix home path was not shortened:\n%s", got)
	}

	got = Text(
		`paths: C:\Users\Alice\AppData and C:\Users\Alice2\AppData`,
		Options{HomeDir: `C:\Users\Alice`, GOOS: "windows"},
	)
	if !strings.Contains(got, `paths: ~\AppData`) {
		t.Fatalf("windows home path was not shortened:\n%s", got)
	}
	if strings.Contains(got, `~2\AppData`) {
		t.Fatalf("sibling path was incorrectly shortened:\n%s", got)
	}
}
