package render

import (
	"fmt"
	"strings"
)

// EscapeTOONValue quotes and escapes a string value for TOON tabular output if needed.
func EscapeTOONValue(s string) string {
	s = strings.TrimSpace(s)
	// If empty, quote it
	if s == "" {
		return `""`
	}
	// Check if quoting is needed: contains commas, quotes, newlines, colons, or starts/ends with special characters
	needsQuotes := strings.ContainsAny(s, ",\"\n\r\t{}[]:") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ")
	if !needsQuotes {
		return s
	}
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	escaped = strings.ReplaceAll(escaped, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "\r", "\\r")
	return `"` + escaped + `"`
}

// FormatTOONTable formats a named tabular collection into the Token-Oriented Object Notation (TOON)
// specification (e.g. `items[N]{col1,col2}:`).
func FormatTOONTable(name string, headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s[%d]{%s}:\n", name, len(rows), strings.Join(headers, ",")))
	for _, row := range rows {
		escapedRow := make([]string, len(row))
		for i, val := range row {
			escapedRow[i] = EscapeTOONValue(val)
		}
		b.WriteString(fmt.Sprintf("  %s\n", strings.Join(escapedRow, ",")))
	}
	return b.String()
}

// FormatTOONObject formats a single named object with key-value pairs in TOON format.
func FormatTOONObject(name string, fields [][2]string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s:\n", name))
	for _, pair := range fields {
		val := strings.ReplaceAll(pair[1], "\n", " ")
		b.WriteString(fmt.Sprintf("  %s: %s\n", pair[0], val))
	}
	return b.String()
}
