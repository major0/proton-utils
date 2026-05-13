package lumoCmd

import (
	"strings"

	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/major0/proton-utils/api/lumo"
)

// LogFormatOptions controls FormatLog output.
type LogFormatOptions struct {
	Timestamps bool // prefix each message with formatted CreateTime
	Color      bool // emit ANSI color codes on sender names
}

// FormatLog renders messages as a readable log. Each message is
// separated by a blank line. The decrypt callback returns the
// plaintext content and a success flag; when ok is false, FormatLog
// renders "[message decryption failed]" as the content.
//
// Returns the formatted string and the count of decryption failures.
func FormatLog(messages []lumo.Message, opts LogFormatOptions, decrypt func(lumo.Message) (string, bool)) (string, int) {
	if len(messages) == 0 {
		return "", 0
	}

	var b strings.Builder
	failures := 0

	for i, msg := range messages {
		content, ok := decrypt(msg)
		if !ok {
			content = "[message decryption failed]"
			failures++
		}

		// Determine sender label.
		var label string
		switch msg.Role {
		case lumo.WireRoleUser:
			label = "You"
		case lumo.WireRoleAssistant:
			label = "Lumo"
		default:
			label = "?"
		}

		// Apply color to sender label.
		if opts.Color {
			switch msg.Role {
			case lumo.WireRoleUser:
				label = "\x1b[34m" + label + "\x1b[0m"
			case lumo.WireRoleAssistant:
				label = "\x1b[95m" + label + "\x1b[0m"
			}
		}

		// Build the line.
		if opts.Timestamps {
			b.WriteString(cli.FormatISO(msg.CreateTime))
			b.WriteByte(' ')
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(content)

		// Separate consecutive messages with a blank line.
		if i < len(messages)-1 {
			b.WriteString("\n\n")
		}
	}

	return b.String(), failures
}
