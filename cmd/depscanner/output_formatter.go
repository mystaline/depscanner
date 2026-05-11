package main

import (
	"io"
)


// OutputFormatter provides platform-specific output formatting (colors on Unix, plain on Windows).
type OutputFormatter interface {
	// ColorGreen returns green colored text (or plain on Windows)
	ColorGreen() string
	// ColorRed returns red colored text (or plain on Windows)
	ColorRed() string
	// ColorYellow returns yellow colored text (or plain on Windows)
	ColorYellow() string
	// ColorReset returns reset code (or empty on Windows)
	ColorReset() string
	// ColorBlue returns blue colored text (or plain on Windows)
	ColorBlue() string
	// ColorBold returns bold code (or empty on Windows)
	ColorBold() string
	// Printf prints formatted text to stdout
	Printf(format string, v ...interface{})
	// Fprintf prints formatted text to writer
	Fprintf(w io.Writer, format string, v ...interface{})
	// Fprintln prints formatted text to writer with newline
	Fprintln(w io.Writer, v ...interface{})
	// Println prints text with newline
	Println(v ...interface{})
}

