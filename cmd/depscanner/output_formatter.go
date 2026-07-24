package main

import (
	"fmt"
	"io"
)

// OutputFormatter provides color-aware output formatting.
// ANSI codes work on all platforms — Windows support is enabled via init_windows.go.
type OutputFormatter interface {
	ColorGreen() string
	ColorRed() string
	ColorYellow() string
	ColorBlue() string
	ColorBold() string
	ColorReset() string
	Printf(format string, v ...interface{})
	Fprintf(w io.Writer, format string, v ...interface{})
	Fprintln(w io.Writer, v ...interface{})
	Println(v ...interface{})
}

type ansiFormatter struct{}

func NewOutputFormatter() OutputFormatter { return &ansiFormatter{} }

func (f *ansiFormatter) ColorGreen() string  { return "\033[32m" }
func (f *ansiFormatter) ColorRed() string    { return "\033[31m" }
func (f *ansiFormatter) ColorYellow() string { return "\033[33m" }
func (f *ansiFormatter) ColorBlue() string   { return "\033[34m" }
func (f *ansiFormatter) ColorBold() string   { return "\033[1m" }
func (f *ansiFormatter) ColorReset() string  { return "\033[0m" }

func (f *ansiFormatter) Printf(format string, v ...interface{}) { fmt.Printf(format, v...) }
func (f *ansiFormatter) Fprintf(w io.Writer, format string, v ...interface{}) {
	fmt.Fprintf(w, format, v...)
}
func (f *ansiFormatter) Fprintln(w io.Writer, v ...interface{}) { fmt.Fprintln(w, v...) }
func (f *ansiFormatter) Println(v ...interface{})               { fmt.Println(v...) }
