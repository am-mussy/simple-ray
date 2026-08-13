package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrNonInteractive = errors.New("interactive input is unavailable")
	ErrCancelled      = errors.New("input cancelled")
)

type LinePrompter struct {
	reader      *bufio.Reader
	writer      io.Writer
	interactive bool
	mu          sync.Mutex
}

func NewPrompter(reader io.Reader, writer io.Writer, interactive bool) *LinePrompter {
	return &LinePrompter{
		reader:      bufio.NewReader(reader),
		writer:      writer,
		interactive: interactive,
	}
}

func (p *LinePrompter) Input(label, defaultValue string, validate func(string) error) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.interactive {
		return "", ErrNonInteractive
	}
	for {
		prompt := safeText(label)
		if defaultValue != "" {
			prompt += " [" + safeText(defaultValue) + "]"
		}
		value, err := p.readLine(prompt + ": ")
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			value = defaultValue
		}
		if validate != nil {
			if err := validate(value); err != nil {
				if _, writeErr := fmt.Fprintf(p.writer, "! %s\n", safeText(err.Error())); writeErr != nil {
					return "", writeErr
				}
				continue
			}
		}
		return value, nil
	}
}

func (p *LinePrompter) Confirm(label string, defaultValue bool) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.interactive {
		return false, ErrNonInteractive
	}
	suffix := " [y/N]: "
	if defaultValue {
		suffix = " [Y/n]: "
	}
	for {
		value, err := p.readLine(safeText(label) + suffix)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "":
			return defaultValue, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			if _, err := io.WriteString(p.writer, "! Enter y or n.\n"); err != nil {
				return false, err
			}
		}
	}
}

func (p *LinePrompter) Select(label string, choices []Choice, defaultIndex int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.interactive {
		return 0, ErrNonInteractive
	}
	if len(choices) == 0 {
		return 0, errors.New("at least one choice is required")
	}
	if defaultIndex < 0 || defaultIndex >= len(choices) {
		return 0, errors.New("default choice is out of range")
	}
	if _, err := fmt.Fprintln(p.writer, safeText(label)); err != nil {
		return 0, err
	}
	for i, choice := range choices {
		marker := " "
		if i == defaultIndex {
			marker = ">"
		}
		line := fmt.Sprintf("%s %d. %s", marker, i+1, safeText(choice.Label))
		if choice.Description != "" {
			line += "  " + safeText(choice.Description)
		}
		if _, err := fmt.Fprintln(p.writer, line); err != nil {
			return 0, err
		}
	}
	for {
		value, err := p.readLine(fmt.Sprintf("Choose [%d]: ", defaultIndex+1))
		if err != nil {
			return 0, err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return defaultIndex, nil
		}
		index, conversionErr := strconv.Atoi(value)
		if conversionErr == nil && index >= 1 && index <= len(choices) {
			return index - 1, nil
		}
		if _, err := fmt.Fprintf(p.writer, "! Enter a number from 1 to %d.\n", len(choices)); err != nil {
			return 0, err
		}
	}
}

func (p *LinePrompter) readLine(prompt string) (string, error) {
	if _, err := io.WriteString(p.writer, prompt); err != nil {
		return "", err
	}
	value, err := p.reader.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return "", ErrCancelled
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r"), nil
}
