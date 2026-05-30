package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Option[T any] struct {
	Label string
	Value T
}

type Prompter struct {
	reader *bufio.Reader
}

func NewPrompter(r io.Reader) *Prompter {
	return &Prompter{reader: bufio.NewReader(r)}
}

func Select[T any](p *Prompter, message string, options []Option[T]) (T, error) {
	var zero T

	if len(options) == 0 {
		return zero, fmt.Errorf("no options provided")
	}

	fmt.Println(message)
	fmt.Println()

	for i, opt := range options {
		fmt.Printf("  [%d] %s\n", i+1, opt.Label)
	}

	fmt.Println()

	for {
		input := p.prompt(fmt.Sprintf("Select [1-%d]: ", len(options)), "")

		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(options) {
			fmt.Printf("Please enter a number between 1 and %d\n", len(options))
			continue
		}

		return options[choice-1].Value, nil
	}
}

func (p *Prompter) Confirm(message string) (bool, error) {
	input := p.prompt(message+" [y/N]: ", "N")
	return strings.EqualFold(input, "y"), nil
}

func (p *Prompter) Input(message, defaultVal string) (string, error) {
	var msg string
	if defaultVal != "" {
		msg = fmt.Sprintf("%s [%s]: ", message, defaultVal)
	} else {
		msg = message + ": "
	}

	return p.prompt(msg, defaultVal), nil
}

func (p *Prompter) prompt(message, defaultVal string) string {
	fmt.Print(message)

	input, _ := p.reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return defaultVal
	}

	return input
}
