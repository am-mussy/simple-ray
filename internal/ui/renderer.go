package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

type TextRenderer struct {
	w        io.Writer
	mu       sync.Mutex
	terminal bool
	color    bool
	unicode  bool
	width    int
	active   bool
	spinner  int
	err      error
}

func newTextRenderer(w io.Writer, options Options) (*TextRenderer, error) {
	if w == nil {
		return nil, errors.New("ui writer is required")
	}
	if options.Terminal == "" {
		options.Terminal = TerminalAuto
	}
	if options.Color == "" {
		options.Color = ColorAuto
	}
	if options.Unicode == "" {
		options.Unicode = UnicodeAuto
	}
	if !validTerminalMode(options.Terminal) {
		return nil, fmt.Errorf("invalid terminal mode %q", options.Terminal)
	}
	if !validColorMode(options.Color) {
		return nil, fmt.Errorf("invalid color mode %q", options.Color)
	}
	if !validUnicodeMode(options.Unicode) {
		return nil, fmt.Errorf("invalid unicode mode %q", options.Unicode)
	}

	terminal := options.Terminal == TerminalAlways || options.Terminal == TerminalAuto && isTerminal(w)
	width := options.Width
	if width <= 0 {
		width = terminalWidth()
	}
	color := terminal && options.Color != ColorNever
	if options.Color == ColorAuto && (os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb") {
		color = false
	}
	unicodeOutput := terminal && options.Unicode != UnicodeNever
	if options.Unicode == UnicodeAuto {
		unicodeOutput = unicodeOutput && supportsUTF8()
	}

	return &TextRenderer{
		w:        w,
		terminal: terminal,
		color:    color,
		unicode:  unicodeOutput,
		width:    width,
	}, nil
}

func validTerminalMode(mode TerminalMode) bool {
	return mode == TerminalAuto || mode == TerminalAlways || mode == TerminalNever
}

func validColorMode(mode ColorMode) bool {
	return mode == ColorAuto || mode == ColorAlways || mode == ColorNever
}

func validUnicodeMode(mode UnicodeMode) bool {
	return mode == UnicodeAuto || mode == UnicodeAlways || mode == UnicodeNever
}

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalWidth() int {
	if value, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && value >= 20 && value <= 1000 {
		return value
	}
	return 80
}

func supportsUTF8() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	locale := strings.ToUpper(os.Getenv("LC_ALL") + " " + os.Getenv("LC_CTYPE") + " " + os.Getenv("LANG"))
	return strings.Contains(locale, "UTF-8") || strings.Contains(locale, "UTF8")
}

func (r *TextRenderer) Banner(title, subtitle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishActive()
	title = safeText(title)
	subtitle = safeText(subtitle)
	if !r.terminal || r.width < 34 {
		r.writeLine(title)
		if subtitle != "" {
			r.writeLine(subtitle)
		}
		return
	}
	boxWidth := min(r.width, 46)
	if r.unicode {
		r.writeLine("╭" + strings.Repeat("─", boxWidth-2) + "╮")
		r.writeLine("│" + center(title, boxWidth-2) + "│")
		if subtitle != "" {
			r.writeLine("│" + center(subtitle, boxWidth-2) + "│")
		}
		r.writeLine("╰" + strings.Repeat("─", boxWidth-2) + "╯")
	} else {
		r.writeLine("+" + strings.Repeat("-", boxWidth-2) + "+")
		r.writeLine("|" + center(title, boxWidth-2) + "|")
		if subtitle != "" {
			r.writeLine("|" + center(subtitle, boxWidth-2) + "|")
		}
		r.writeLine("+" + strings.Repeat("-", boxWidth-2) + "+")
	}
}

func (r *TextRenderer) Section(title string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishActive()
	r.writeLine("")
	r.writeLine(r.style(safeText(title), ansiCyan))
}

func (r *TextRenderer) Event(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event.Phase = safeText(event.Phase)
	event.Step = safeText(event.Step)
	event.Detail = safeText(event.Detail)
	if event.Step == "" {
		event.Step = event.Phase
	}
	line := r.eventLine(event)
	if event.State == StateRunning && r.terminal {
		if r.active {
			r.writeRaw("\r\x1b[2K")
		}
		r.writeRaw(line)
		r.active = true
		return
	}
	r.finishActive()
	r.writeLine(line)
}

func (r *TextRenderer) eventLine(event Event) string {
	symbol, color := r.stateStyle(event.State)
	detail := ""
	if event.Detail != "" {
		detail = "  " + event.Detail
	}
	if event.Duration > 0 && event.State != StateRunning {
		detail += "  " + event.Duration.Round(100_000_000).String()
	}
	if !r.terminal {
		return fmt.Sprintf("[%s] %s%s", symbol, event.Step, detail)
	}
	return "  " + r.style(symbol, color) + " " + event.Step + r.style(detail, ansiDim)
}

func (r *TextRenderer) stateStyle(state State) (string, string) {
	if !r.terminal {
		switch state {
		case StatePending:
			return "WAIT", ""
		case StateRunning:
			return "RUN", ""
		case StateSuccess:
			return "OK", ""
		case StateWarning:
			return "WARN", ""
		case StateFailure:
			return "ERROR", ""
		case StateSkipped:
			return "SKIP", ""
		default:
			return "INFO", ""
		}
	}
	if r.unicode {
		switch state {
		case StatePending:
			return "·", ansiDim
		case StateRunning:
			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			frame := frames[r.spinner%len(frames)]
			r.spinner++
			return frame, ansiCyan
		case StateSuccess:
			return "✓", ansiGreen
		case StateWarning:
			return "!", ansiYellow
		case StateFailure:
			return "✗", ansiRed
		case StateSkipped:
			return "–", ansiDim
		default:
			return "·", ansiDim
		}
	}
	switch state {
	case StatePending:
		return ".", ansiDim
	case StateRunning:
		return "...", ansiCyan
	case StateSuccess:
		return "OK", ansiGreen
	case StateWarning:
		return "WARN", ansiYellow
	case StateFailure:
		return "ERROR", ansiRed
	case StateSkipped:
		return "SKIP", ansiDim
	default:
		return "INFO", ansiDim
	}
}

func (r *TextRenderer) Table(columns []Column, rows [][]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishActive()
	if len(columns) == 0 {
		return
	}
	cleanRows := make([][]string, len(rows))
	for i, row := range rows {
		cleanRows[i] = make([]string, len(columns))
		for j := range columns {
			if j < len(row) {
				cleanRows[i][j] = safeText(row[j])
			}
		}
	}
	cleanColumns := make([]Column, len(columns))
	copy(cleanColumns, columns)
	for i := range cleanColumns {
		cleanColumns[i].Title = safeText(cleanColumns[i].Title)
	}
	widths := tableWidths(cleanColumns, cleanRows, r.width)
	r.writeTableRow(cleanColumns, widths, columnTitles(cleanColumns))
	if r.terminal {
		separator := make([]string, len(widths))
		for i, width := range widths {
			separator[i] = strings.Repeat("-", width)
		}
		r.writeLine(strings.Join(separator, "  "))
	}
	for _, row := range cleanRows {
		r.writeTableRow(cleanColumns, widths, row)
	}
}

func (r *TextRenderer) writeTableRow(columns []Column, widths []int, cells []string) {
	parts := make([]string, len(columns))
	for i, column := range columns {
		cell := ""
		if i < len(cells) {
			cell = truncate(cells[i], widths[i])
		}
		if column.Right {
			parts[i] = padLeft(cell, widths[i])
		} else {
			parts[i] = padRight(cell, widths[i])
		}
	}
	r.writeLine(strings.TrimRight(strings.Join(parts, "  "), " "))
}

func (r *TextRenderer) Notice(severity Severity, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishActive()
	message = safeText(message)
	state := StatePending
	switch severity {
	case SeveritySuccess:
		state = StateSuccess
	case SeverityWarning:
		state = StateWarning
	case SeverityError:
		state = StateFailure
	}
	symbol, color := r.stateStyle(state)
	if r.terminal {
		r.writeLine(r.style(symbol, color) + " " + message)
		return
	}
	label := strings.ToUpper(string(severity))
	if label == "" {
		label = "INFO"
	}
	r.writeLine("[" + label + "] " + message)
}

func (r *TextRenderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishActive()
	return r.err
}

func (r *TextRenderer) finishActive() {
	if !r.active {
		return
	}
	r.writeRaw("\n")
	r.active = false
}

func (r *TextRenderer) style(value, code string) string {
	if !r.color || value == "" {
		return value
	}
	return code + value + ansiReset
}

func (r *TextRenderer) writeLine(value string) {
	r.writeRaw(value + "\n")
}

func (r *TextRenderer) writeRaw(value string) {
	if r.err != nil {
		return
	}
	_, r.err = io.WriteString(r.w, value)
}

func tableWidths(columns []Column, rows [][]string, maxWidth int) []int {
	widths := make([]int, len(columns))
	for i, column := range columns {
		widths[i] = max(displayWidth(column.Title), max(column.MinWidth, 1))
	}
	for _, row := range rows {
		for i := range columns {
			if i < len(row) {
				widths[i] = max(widths[i], displayWidth(row[i]))
			}
		}
	}
	available := max(maxWidth-2*(len(widths)-1), len(widths))
	for sum(widths) > available {
		widest := -1
		for i, width := range widths {
			minimum := max(columns[i].MinWidth, 4)
			if width > minimum && (widest == -1 || width > widths[widest]) {
				widest = i
			}
		}
		if widest == -1 {
			break
		}
		widths[widest]--
	}
	return widths
}

func columnTitles(columns []Column) []string {
	titles := make([]string, len(columns))
	for i, column := range columns {
		titles[i] = column.Title
	}
	return titles
}

func safeText(value string) string {
	value = stripEscapeSequences(value)
	var builder strings.Builder
	for _, char := range value {
		if char == '\n' || char == '\r' || char == '\t' {
			builder.WriteByte(' ')
			continue
		}
		if unicode.IsControl(char) {
			continue
		}
		builder.WriteRune(char)
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func stripEscapeSequences(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value); {
		if value[i] != 0x1b {
			builder.WriteByte(value[i])
			i++
			continue
		}
		i++
		if i >= len(value) {
			break
		}
		switch value[i] {
		case '[':
			i++
			for i < len(value) {
				final := value[i]
				i++
				if final >= 0x40 && final <= 0x7e {
					break
				}
			}
		case ']':
			i++
			for i < len(value) {
				if value[i] == 0x07 {
					i++
					break
				}
				if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			i++
		}
	}
	return builder.String()
}

func center(value string, width int) string {
	value = truncate(value, width)
	left := max((width-displayWidth(value))/2, 0)
	return strings.Repeat(" ", left) + padRight(value, width-left)
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(value) <= width {
		return value
	}
	suffix := "..."
	if width <= len(suffix) {
		return strings.Repeat(".", width)
	}
	limit := width - len(suffix)
	var builder strings.Builder
	used := 0
	for _, char := range value {
		charWidth := runeWidth(char)
		if used+charWidth > limit {
			break
		}
		builder.WriteRune(char)
		used += charWidth
	}
	return builder.String() + suffix
}

func padRight(value string, width int) string {
	return value + strings.Repeat(" ", max(width-displayWidth(value), 0))
}

func padLeft(value string, width int) string {
	return strings.Repeat(" ", max(width-displayWidth(value), 0)) + value
}

func displayWidth(value string) int {
	width := 0
	for len(value) > 0 {
		char, size := utf8.DecodeRuneInString(value)
		if char == utf8.RuneError && size == 1 {
			width++
			value = value[1:]
			continue
		}
		width += runeWidth(char)
		value = value[size:]
	}
	return width
}

func runeWidth(char rune) int {
	if unicode.Is(unicode.Mn, char) || unicode.Is(unicode.Me, char) {
		return 0
	}
	if char >= 0x1100 && (char <= 0x115f || char >= 0x2e80 && char <= 0xa4cf || char >= 0xac00 && char <= 0xd7a3 || char >= 0xf900 && char <= 0xfaff || char >= 0xfe10 && char <= 0xfe6f || char >= 0xff00 && char <= 0xff60 || char >= 0x1f300 && char <= 0x1faff) {
		return 2
	}
	return 1
}

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}
