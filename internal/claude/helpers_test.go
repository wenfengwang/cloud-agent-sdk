package claude

import (
	"testing"
)

func TestShellOutput(t *testing.T) {
	for _, tc := range []struct {
		text    string
		isError bool
		output  string
		code    int // -1: no exit code
	}{
		{"one", false, "one", 0},
		{"Exit code 3\ntwo", true, "two", 3},
		{"Exit code 1", true, "", 1},
		{"The user doesn't want to proceed with this tool use.", true, "The user doesn't want to proceed with this tool use.", -1},
	} {
		got := shellOutput(tc.text, tc.isError)
		if got.Output != tc.output {
			t.Errorf("shellOutput(%q).Output = %q, want %q", tc.text, got.Output, tc.output)
		}
		switch {
		case tc.code < 0 && got.ExitCode != nil:
			t.Errorf("shellOutput(%q).ExitCode = %d, want nil", tc.text, *got.ExitCode)
		case tc.code >= 0 && (got.ExitCode == nil || *got.ExitCode != tc.code):
			t.Errorf("shellOutput(%q).ExitCode = %v, want %d", tc.text, got.ExitCode, tc.code)
		}
	}
}

func TestMCPName(t *testing.T) {
	for _, tc := range []struct {
		raw, server, tool string
		ok                bool
	}{
		{"mcp__github__create_issue", "github", "create_issue", true},
		{"mcp__a__b__c", "a", "b__c", true},
		{"mcp__github", "", "", false},
		{"Bash", "", "", false},
	} {
		server, tool, ok := mcpName(tc.raw)
		if server != tc.server || tool != tc.tool || ok != tc.ok {
			t.Errorf("mcpName(%q) = %q, %q, %v", tc.raw, server, tool, ok)
		}
	}
}

func TestEncodeWorkDir(t *testing.T) {
	got := EncodeWorkDir("/private/tmp/claude-probe.Y6aw_dC/work")
	if want := "-private-tmp-claude-probe-Y6aw-dC-work"; got != want {
		t.Errorf("EncodeWorkDir = %q, want %q", got, want)
	}
}
