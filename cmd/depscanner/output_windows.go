//go:build windows
// +build windows

package main

import (
	"fmt"
	"io"
)

// WindowsFormatter outputs without ANSI color codes for Windows compatibility.
type WindowsFormatter struct{}

// NewOutputFormatter creates the appropriate formatter for the platform.
func NewOutputFormatter() OutputFormatter {
	return NewWindowsFormatter()
}

// NewWindowsFormatter creates a formatter without color support.
func NewWindowsFormatter() OutputFormatter {
	return &WindowsFormatter{}
}

func (f *WindowsFormatter) ColorGreen() string {
	return ""
}

func (f *WindowsFormatter) ColorRed() string {
	return ""
}

func (f *WindowsFormatter) ColorYellow() string {
	return ""
}

func (f *WindowsFormatter) ColorBlue() string {
	return ""
}

func (f *WindowsFormatter) ColorBold() string {
	return ""
}

func (f *WindowsFormatter) ColorReset() string {
	return ""
}

func (f *WindowsFormatter) Printf(format string, v ...interface{}) {
	fmt.Printf(format, v...)
}

func (f *WindowsFormatter) Fprintf(w io.Writer, format string, v ...interface{}) {
	fmt.Fprintf(w, format, v...)
}

func (f *WindowsFormatter) Fprintln(w io.Writer, v ...interface{}) {
	fmt.Fprintln(w, v...)
}

func (f *WindowsFormatter) Println(v ...interface{}) {
	fmt.Println(v...)
}
