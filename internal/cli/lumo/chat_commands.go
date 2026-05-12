package lumoCmd

import "strings"

// SlashCommand identifies a recognized slash command.
type SlashCommand int

const (
	// CmdNone indicates the input is not a slash command.
	CmdNone SlashCommand = iota
	// CmdExit indicates the /exit command.
	CmdExit
	// CmdHelp indicates the /help command.
	CmdHelp
	// CmdWebSearch indicates the /websearch command.
	CmdWebSearch
	// CmdUnknown indicates an unrecognized slash command.
	CmdUnknown
)

// ParseSlashCommand parses a slash command from user input. Returns the
// command name, remaining args, and whether the input is a slash command.
// Returns ok=false for non-slash input.
func ParseSlashCommand(input string) (cmd string, args string, ok bool) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return "", "", false
	}

	// Split into command and args at first space.
	rest := input[1:] // strip leading /
	parts := strings.SplitN(rest, " ", 2)
	cmd = strings.ToLower(parts[0])
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return cmd, args, true
}

// ClassifyCommand maps a parsed command name to a SlashCommand constant.
func ClassifyCommand(cmd string) SlashCommand {
	switch cmd {
	case "exit":
		return CmdExit
	case "help":
		return CmdHelp
	case "websearch":
		return CmdWebSearch
	default:
		return CmdUnknown
	}
}

// HelpText returns the help message for available slash commands.
func HelpText() string {
	return `Available commands:
  /help                  Show this help message
  /websearch enable      Enable web search for this session
  /websearch disable     Disable web search for this session
  /exit                  Exit the chat`
}
