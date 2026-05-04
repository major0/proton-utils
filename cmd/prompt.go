package cli

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/term"
)

// UserPrompt prompts the user for input. If password is true, echo is
// disabled while reading.
func UserPrompt(prompt string, password bool) (string, error) {
	var err error
	var input string
	var bytePasswd []byte
	reader := bufio.NewReader(os.Stdin)

	for input == "" {
		fmt.Print(prompt + ": ")
		if password {
			bytePasswd, err = term.ReadPassword(int(os.Stdin.Fd())) //nolint:gosec // standard pattern for term.ReadPassword
			input = string(bytePasswd)
			fmt.Println("")
		} else {
			input, err = reader.ReadString('\n')
			input = strings.TrimRight(input, "\r\n")
		}

		if err != nil {
			return "", err
		}
	}
	slog.Debug("UserPrompt", prompt, "<redacted>")
	return input, nil
}
