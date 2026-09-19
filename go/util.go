package main

import (
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

type intuitionError struct {
	status int
	msg    string
}

func (e *intuitionError) Error() string { return e.msg }

func errBad(status int, msg string) error {
	return &intuitionError{status: status, msg: msg}
}

func statusOf(err error) int {
	if ie, ok := err.(*intuitionError); ok {
		return ie.status
	}
	return 502
}

func trim(s string) string { return strings.TrimSpace(s) }

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case jsonNumber:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

type jsonNumber interface{ Float64() (float64, error) }

func clip(s string, max int) string {
	s = trim(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func redact(message string) string {
	out := vckPattern.ReplaceAllString(message, "[redacted]")
	out = bearerPattern.ReplaceAllString(out, "Bearer [redacted]")
	if utf8.RuneCountInString(out) <= 500 {
		return out
	}
	return string([]rune(out)[:500])
}

var (
	vckPattern    = regexp.MustCompile(`vck_[A-Za-z0-9_-]+`)
	bearerPattern = regexp.MustCompile(`(?i)Bearer\s+\S+`)
)
