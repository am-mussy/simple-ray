package ui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestInputRetriesAndUsesDefault(t *testing.T) {
	input := strings.NewReader("bad name\n\n")
	var output bytes.Buffer
	prompter := NewPrompter(input, &output, true)
	value, err := prompter.Input("Name", "vpn", func(value string) error {
		if strings.Contains(value, " ") {
			return fmt.Errorf("spaces are not allowed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if value != "vpn" {
		t.Fatalf("value = %q, want vpn", value)
	}
	if !strings.Contains(output.String(), "! spaces are not allowed") {
		t.Fatalf("validation error is missing: %q", output.String())
	}
}

func TestConfirmDefaultsAndRetries(t *testing.T) {
	input := strings.NewReader("maybe\ny\n")
	var output bytes.Buffer
	prompter := NewPrompter(input, &output, true)
	confirmed, err := prompter.Confirm("Install now?", false)
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("expected confirmation")
	}
	if !strings.Contains(output.String(), "Enter y or n") {
		t.Fatalf("retry hint is missing: %q", output.String())
	}
}

func TestSelectAcceptsDefaultAndNumber(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "default", input: "\n", want: 0},
		{name: "number", input: "2\n", want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			prompter := NewPrompter(strings.NewReader(test.input), &output, true)
			got, err := prompter.Select("Choose setup", []Choice{
				{Label: "Recommended", Description: "Secure defaults"},
				{Label: "Advanced", Description: "Choose settings"},
			}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("choice = %d, want %d", got, test.want)
			}
		})
	}
}

func TestPrompterRejectsNonInteractiveUse(t *testing.T) {
	prompter := NewPrompter(strings.NewReader("y\n"), &bytes.Buffer{}, false)
	_, err := prompter.Confirm("Delete?", false)
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("error = %v, want ErrNonInteractive", err)
	}
}

func TestEOFIsCancellation(t *testing.T) {
	prompter := NewPrompter(strings.NewReader(""), &bytes.Buffer{}, true)
	_, err := prompter.Input("Name", "vpn", nil)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("error = %v, want ErrCancelled", err)
	}
}

func TestPromptSanitizesLabelsAndValidationErrors(t *testing.T) {
	input := strings.NewReader("bad\ngood\n")
	var output bytes.Buffer
	prompter := NewPrompter(input, &output, true)
	_, err := prompter.Input("Name\x1b[31m", "", func(value string) error {
		if value == "bad" {
			return fmt.Errorf("invalid\x1b]0;owned\a value")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "\a") {
		t.Fatalf("prompt contains terminal escape: %q", output.String())
	}
}

func TestInputAcceptsBracketedPaste(t *testing.T) {
	input := strings.NewReader("\x1b[200~mussy\x1b[201~\n")
	prompter := NewPrompter(input, &bytes.Buffer{}, true)
	value, err := prompter.Input("Name", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if value != "mussy" {
		t.Fatalf("value = %q", value)
	}
}
