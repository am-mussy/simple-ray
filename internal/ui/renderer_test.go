package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNonTerminalRendererIsASCIIAndAppendOnly(t *testing.T) {
	var output bytes.Buffer
	renderer, err := NewRenderer(&output, Options{
		Terminal: TerminalNever,
		Color:    ColorAlways,
		Unicode:  UnicodeAlways,
	})
	if err != nil {
		t.Fatal(err)
	}

	renderer.Banner("VPNCTL", "Secure server setup")
	renderer.Section("Installing")
	renderer.Event(Event{Step: "Dependencies", State: StateRunning})
	renderer.Event(Event{Step: "Dependencies", State: StateSuccess, Duration: time.Second})
	renderer.Notice(SeverityWarning, "Network is slow")
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	want := "VPNCTL\nSecure server setup\n\nInstalling\n[RUN] Dependencies\n[OK] Dependencies  1s\n[WARNING] Network is slow\n"
	if got != want {
		t.Fatalf("unexpected output\nwant:\n%q\ngot:\n%q", want, got)
	}
	if strings.ContainsAny(got, "\x1b\r╭✓⠋") {
		t.Fatalf("non-terminal output contains terminal control or Unicode: %q", got)
	}
}

func TestTerminalRendererUsesUnicodeAndColor(t *testing.T) {
	var output bytes.Buffer
	renderer, err := NewRenderer(&output, Options{
		Terminal: TerminalAlways,
		Color:    ColorAlways,
		Unicode:  UnicodeAlways,
		Width:    44,
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer.Banner("VPNCTL", "Secure server setup")
	renderer.Event(Event{Step: "Xray", State: StateSuccess})
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, expected := range []string{"╭", "VPNCTL", "✓", ansiGreen} {
		if !strings.Contains(got, expected) {
			t.Fatalf("terminal output does not contain %q: %q", expected, got)
		}
	}
}

func TestNoColorEnvironmentDisablesANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var output bytes.Buffer
	renderer, err := NewRenderer(&output, Options{
		Terminal: TerminalAlways,
		Color:    ColorAuto,
		Unicode:  UnicodeAlways,
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer.Event(Event{Step: "Xray", State: StateFailure})
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b") {
		t.Fatalf("NO_COLOR output contains ANSI: %q", output.String())
	}
	if !strings.Contains(output.String(), "✗ Xray") {
		t.Fatalf("NO_COLOR removed semantic marker: %q", output.String())
	}
}

func TestExplicitColorOverridesNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var output bytes.Buffer
	renderer, err := NewRenderer(&output, Options{
		Terminal: TerminalAlways,
		Color:    ColorAlways,
		Unicode:  UnicodeNever,
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer.Notice(SeverityError, "failed")
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), ansiRed) {
		t.Fatalf("explicit color did not override NO_COLOR: %q", output.String())
	}
}

func TestRunningEventUpdatesOneTerminalLine(t *testing.T) {
	var output bytes.Buffer
	renderer, err := NewRenderer(&output, Options{
		Terminal: TerminalAlways,
		Color:    ColorNever,
		Unicode:  UnicodeNever,
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer.Event(Event{Step: "Download", State: StateRunning, Detail: "1 MB"})
	renderer.Event(Event{Step: "Download", State: StateRunning, Detail: "2 MB"})
	renderer.Event(Event{Step: "Download", State: StateSuccess})
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "\r\x1b[2K") {
		t.Fatalf("running update did not clear the current line: %q", got)
	}
	if !strings.HasSuffix(got, "  OK Download\n") {
		t.Fatalf("completed event did not finalize the line: %q", got)
	}
}

func TestTableSanitizesTerminalEscapesAndControls(t *testing.T) {
	var output bytes.Buffer
	renderer, err := NewRenderer(&output, Options{
		Terminal: TerminalNever,
		Width:    30,
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer.Table(
		[]Column{{Title: "NAME", MinWidth: 4}, {Title: "STATUS", MinWidth: 6}},
		[][]string{{"evil\x1b[31mname\x1b[0m\nnext", "active\x07"}},
	)
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.ContainsAny(got, "\x1b\a\r") {
		t.Fatalf("table contains unsafe controls: %q", got)
	}
	if !strings.Contains(got, "evilname next") {
		t.Fatalf("table did not preserve sanitized content: %q", got)
	}
}

func TestRendererKeepsFirstWriterError(t *testing.T) {
	want := errors.New("write failed")
	renderer, err := NewRenderer(errorWriter{err: want}, Options{Terminal: TerminalNever})
	if err != nil {
		t.Fatal(err)
	}
	renderer.Notice(SeverityInfo, "hello")
	renderer.Notice(SeverityInfo, "again")
	if got := renderer.Close(); !errors.Is(got, want) {
		t.Fatalf("Close error = %v, want %v", got, want)
	}
}

func TestInvalidRendererOptions(t *testing.T) {
	_, err := NewRenderer(&bytes.Buffer{}, Options{Color: ColorMode("sometimes")})
	if err == nil {
		t.Fatal("expected invalid color mode error")
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
