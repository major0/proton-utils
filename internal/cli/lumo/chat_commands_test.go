package lumoCmd

import "testing"

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantCmd string
		wantOK  bool
	}{
		{"exit", "/exit", "exit", true},
		{"help", "/help", "help", true},
		{"unknown", "/foo", "foo", true},
		{"with args", "/help topic", "help", true},
		{"non-slash", "hello world", "", false},
		{"empty", "", "", false},
		{"just slash", "/", "", true},
		{"whitespace prefix", "  /exit", "exit", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, ok := ParseSlashCommand(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if cmd != tt.wantCmd {
				t.Fatalf("cmd=%q, want %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestClassifyCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want SlashCommand
	}{
		{"exit", CmdExit},
		{"help", CmdHelp},
		{"foo", CmdUnknown},
		{"", CmdUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			got := ClassifyCommand(tt.cmd)
			if got != tt.want {
				t.Fatalf("ClassifyCommand(%q)=%d, want %d", tt.cmd, got, tt.want)
			}
		})
	}
}
