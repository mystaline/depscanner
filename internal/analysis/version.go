// Package analysis provides utilities for Go source code analysis, including
// semver comparison and pseudo-version parsing for dependency staleness detection.
package analysis

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PseudoVersion holds the parsed components of a canonical Go pseudo-version.
//
// Examples:
//   - v0.0.0-20260311025516-abcdef123456
//   - v1.1.0-dev.0.20260409024228-2a019f321162
type PseudoVersion struct {
	BaseVersion string    // The semver prefix (e.g. v1.1.0-dev.0)
	Timestamp   time.Time // Normalized commit timestamp
	CommitHash  string    // 12-character abbreviated Git hash
}

// IsPseudoVersion reports whether ver looks like a Go pseudo-version.
// Handles both standard (v0.0.0-YYYYMMDDHHMMSS-hash) and pre-release
// (v1.1.0-dev.0.YYYYMMDDHHMMSS-hash) formats.
func IsPseudoVersion(ver string) bool {
	parts := strings.Split(ver, "-")
	if len(parts) < 3 {
		return false
	}
	hash := parts[len(parts)-1]
	if len(hash) != 12 || !isHex(hash) {
		return false
	}
	// The timestamp is always the last 14 chars of the second-to-last segment.
	// Standard: "20260311025516", pre-release: "dev.0.20260311025516"
	tsPart := parts[len(parts)-2]
	if len(tsPart) >= 14 {
		ts := tsPart[len(tsPart)-14:]
		return isDigits(ts)
	}
	return false
}

// ParsePseudoVersion extracts the timestamp and commit hash from a pseudo-version string.
func ParsePseudoVersion(ver string) (PseudoVersion, error) {
	parts := strings.Split(ver, "-")
	if len(parts) < 3 {
		return PseudoVersion{}, fmt.Errorf("not a pseudo-version: %s", ver)
	}

	hash := parts[len(parts)-1]
	tsPart := parts[len(parts)-2]
	base := strings.Join(parts[:len(parts)-2], "-")

	if len(hash) != 12 || !isHex(hash) {
		return PseudoVersion{}, fmt.Errorf("invalid commit hash in pseudo-version: %s", ver)
	}

	// Extract the 14-digit timestamp from the end of tsPart.
	// Standard: "20260311025516", pre-release: "dev.0.20260311025516"
	if len(tsPart) < 14 {
		return PseudoVersion{}, fmt.Errorf("invalid timestamp in pseudo-version: %s", ver)
	}
	tsStr := tsPart[len(tsPart)-14:]
	if !isDigits(tsStr) {
		return PseudoVersion{}, fmt.Errorf("invalid timestamp in pseudo-version: %s", ver)
	}

	ts, err := time.Parse("20060102150405", tsStr)
	if err != nil {
		return PseudoVersion{}, fmt.Errorf("parse timestamp: %w", err)
	}

	return PseudoVersion{
		BaseVersion: base,
		Timestamp:   ts,
		CommitHash:  hash,
	}, nil
}

// CommitsBehind returns a human-friendly string describing the time delta
// between two versions, used as a fallback for staleness reporting.
func CommitsBehind(current, latest time.Time) string {
	diff := latest.Sub(current)
	switch {
	case diff < time.Hour:
		return "just behind"
	case diff < 24*time.Hour:
		return fmt.Sprintf("~%d hours behind", int(diff.Hours()))
	default:
		return fmt.Sprintf("~%d days behind", int(diff.Hours()/24))
	}
}

// CompareSemver performs a numeric comparison of two semantic version strings.
// Returns -1 if a < b, 0 if a == b, and 1 if a > b.
// It ignores pre-release and metadata suffixes for the numeric comparison.
func CompareSemver(a, b string) int {
	aParts := parseSemverParts(a)
	bParts := parseSemverParts(b)
	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

func parseSemverParts(ver string) [3]int {
	ver = strings.TrimPrefix(ver, "v")
	// Clean up suffixes like +incompatible or -beta
	if idx := strings.IndexAny(ver, "+-"); idx >= 0 {
		ver = ver[:idx]
	}
	parts := strings.SplitN(ver, ".", 3)
	var result [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		result[i], _ = strconv.Atoi(parts[i])
	}
	return result
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
