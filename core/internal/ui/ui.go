package ui

import "os"

// ANSI escape sequences. All combined (bold+colour where applicable) to avoid
// stacking resets incorrectly on terminals that handle them independently.
const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[1;31m"
	ansiGreen   = "\x1b[1;32m"
	ansiYellow  = "\x1b[1;33m"
	ansiBlue    = "\x1b[1;34m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
	ansiDimCyan = "\x1b[2;36m"
)

// IsColorEnabled reports whether f supports ANSI colour output.
// Respects NO_COLOR (https://no-color.org/) and TERM=dumb.
func IsColorEnabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func paint(code, s string, on bool) string {
	if !on || s == "" {
		return s
	}
	return code + s + ansiReset
}

// Colour helpers — each accepts an `on` bool so callers can pass the colour
// flag directly without branching. When on=false they are no-ops.
func Bold(s string, on bool) string    { return paint(ansiBold, s, on) }
func Dim(s string, on bool) string     { return paint(ansiDim, s, on) }
func Red(s string, on bool) string     { return paint(ansiRed, s, on) }
func Green(s string, on bool) string   { return paint(ansiGreen, s, on) }
func Yellow(s string, on bool) string  { return paint(ansiYellow, s, on) }
func Blue(s string, on bool) string    { return paint(ansiBlue, s, on) }
func Magenta(s string, on bool) string { return paint(ansiMagenta, s, on) }
func Cyan(s string, on bool) string    { return paint(ansiCyan, s, on) }
func DimCyan(s string, on bool) string { return paint(ansiDimCyan, s, on) }
