// Package cli provides the Cobra command tree for the codegraph binary.
// Each verb lives in its own file; this file contains shared UI helpers.
package cli

import (
	"fmt"
	"os"
	"strings"
)

// isTTY returns true when os.Stdout is a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// printSuccess prints a styled success line to stdout.
func printSuccess(msg string) {
	if isTTY() {
		fmt.Println("\x1b[32m✓\x1b[0m " + msg)
	} else {
		fmt.Println("ok: " + msg)
	}
}

// printInfo prints a styled info line to stdout.
func printInfo(msg string) {
	if isTTY() {
		fmt.Println("\x1b[34mi\x1b[0m " + msg)
	} else {
		fmt.Println("info: " + msg)
	}
}

// printWarn prints a styled warning to stdout.
func printWarn(msg string) {
	if isTTY() {
		fmt.Println("\x1b[33m!\x1b[0m " + msg)
	} else {
		fmt.Println("warn: " + msg)
	}
}

// printError prints a styled error to stderr.
func printError(msg string) {
	if isTTY() {
		fmt.Fprintln(os.Stderr, "\x1b[31m✗\x1b[0m "+msg)
	} else {
		fmt.Fprintln(os.Stderr, "error: "+msg)
	}
}

// printBold prints a bold header line (only if TTY).
func printBold(msg string) {
	if isTTY() {
		fmt.Println("\x1b[1m" + msg + "\x1b[0m")
	} else {
		fmt.Println(msg)
	}
}

// printDim prints a dim text line (only if TTY).
func printDim(msg string) {
	if isTTY() {
		fmt.Println("\x1b[2m" + msg + "\x1b[0m")
	} else {
		fmt.Println(msg)
	}
}

// dim returns a dim-styled version of s when on a TTY.
func dim(s string) string {
	if isTTY() {
		return "\x1b[2m" + s + "\x1b[0m"
	}
	return s
}

// cyan returns a cyan-styled version of s when on a TTY.
func cyan(s string) string {
	if isTTY() {
		return "\x1b[36m" + s + "\x1b[0m"
	}
	return s
}

// bold returns a bold-styled version of s when on a TTY.
func bold(s string) string {
	if isTTY() {
		return "\x1b[1m" + s + "\x1b[0m"
	}
	return s
}

// formatDuration converts milliseconds to a human-readable string.
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	sec := float64(ms) / 1000.0
	if sec < 60 {
		return fmt.Sprintf("%.1fs", sec)
	}
	min := int(sec) / 60
	rem := sec - float64(min*60)
	return fmt.Sprintf("%dm %.0fs", min, rem)
}

// formatNumber formats an integer with comma separators.
func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
