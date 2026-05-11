//go:build !windows
// +build !windows

package main

import (
	"fmt"
	"io"
)

// UnixFormatter outputs with ANSI color codes.
type UnixFormatter struct{}

// NewOutputFormatter creates the appropriate formatter for the platform.
func NewOutputFormatter() OutputFormatter {
	return NewUnixFormatter()
}

// NewUnixFormatter creates a formatter with ANSI color support.
func NewUnixFormatter() OutputFormatter {
	return &UnixFormatter{}
}

func (f *UnixFormatter) ColorGreen() string {
	return "\033[32m"
}

func (f *UnixFormatter) ColorRed() string {
	return "\033[31m"
}

func (f *UnixFormatter) ColorYellow() string {
	return "\033[33m"
}

func (f *UnixFormatter) ColorBlue() string {
	return "\033[34m"
}

func (f *UnixFormatter) ColorBold() string {
	return "\033[1m"
}

func (f *UnixFormatter) ColorReset() string {
	return "\033[0m"
}

func (f *UnixFormatter) Printf(format string, v ...interface{}) {
	fmt.Printf(format, v...)
}

func (f *UnixFormatter) Fprintf(w io.Writer, format string, v ...interface{}) {
	fmt.Fprintf(w, format, v...)
}

func (f *UnixFormatter) Fprintln(w io.Writer, v ...interface{}) {
	fmt.Fprintln(w, v...)
}

func (f *UnixFormatter) Println(v ...interface{}) {
	fmt.Println(v...)
}
