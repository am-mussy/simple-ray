package ui

import (
	"io"
	"time"
)

type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateSuccess State = "success"
	StateWarning State = "warning"
	StateFailure State = "failure"
	StateSkipped State = "skipped"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeveritySuccess Severity = "success"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type ColorMode string

const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

type TerminalMode string

const (
	TerminalAuto   TerminalMode = "auto"
	TerminalAlways TerminalMode = "always"
	TerminalNever  TerminalMode = "never"
)

type UnicodeMode string

const (
	UnicodeAuto   UnicodeMode = "auto"
	UnicodeAlways UnicodeMode = "always"
	UnicodeNever  UnicodeMode = "never"
)

type Event struct {
	Phase    string
	Step     string
	State    State
	Detail   string
	Duration time.Duration
}

type Column struct {
	Title    string
	MinWidth int
	Right    bool
}

type Choice struct {
	Label       string
	Description string
}

type Options struct {
	Terminal TerminalMode
	Color    ColorMode
	Unicode  UnicodeMode
	Width    int
}

type Renderer interface {
	Banner(title, subtitle string)
	Section(title string)
	Event(Event)
	Table(columns []Column, rows [][]string)
	Notice(severity Severity, message string)
	Close() error
}

type Prompter interface {
	Input(label, defaultValue string, validate func(string) error) (string, error)
	Confirm(label string, defaultValue bool) (bool, error)
	Select(label string, choices []Choice, defaultIndex int) (int, error)
}

func NewRenderer(w io.Writer, options Options) (*TextRenderer, error) {
	return newTextRenderer(w, options)
}
