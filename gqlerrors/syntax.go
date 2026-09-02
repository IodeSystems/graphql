package gqlerrors

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/IodeSystems/graphql-go/v2/language/ast"
	"github.com/IodeSystems/graphql-go/v2/language/location"
	"github.com/IodeSystems/graphql-go/v2/language/source"
)

func NewSyntaxError(s *source.Source, position int, description string) *Error {
	l := location.GetLocation(s, position)
	return NewError(
		fmt.Sprintf("Syntax Error %s (%d:%d) %s\n\n%s", s.Name, l.Line, l.Column, description, highlightSourceAtLocation(s, l)),
		[]ast.Node{},
		"",
		s,
		[]int{position},
		nil,
	)
}

// printCharCode here is slightly different from lexer.printCharCode()
func printCharCode(code rune) string {
	// print as ASCII for printable range
	if code >= 0x0020 {
		return fmt.Sprintf(`%c`, code)
	}
	// Otherwise print the escaped form. e.g. `"\\u0007"`
	return fmt.Sprintf(`\u%04X`, code)
}
func printLine(str string) string {
	// Fast path: nothing to escape, which is every ordinary source line.
	// Avoids building the string rune by rune for the common case.
	needsEscape := false
	for _, runeValue := range str {
		if runeValue < 0x0020 {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return str
	}
	var b strings.Builder
	b.Grow(len(str))
	for _, runeValue := range str {
		b.WriteString(printCharCode(runeValue))
	}
	return b.String()
}

// lineSplitRE splits a source body into lines. Package-level so it is
// compiled once rather than on every syntax error — error construction
// is on the request path for any malformed query.
var lineSplitRE = regexp.MustCompile("\r\n|[\n\r]")

func highlightSourceAtLocation(s *source.Source, l location.SourceLocation) string {
	line := l.Line
	prevLineNum := fmt.Sprintf("%d", (line - 1))
	lineNum := fmt.Sprintf("%d", line)
	nextLineNum := fmt.Sprintf("%d", (line + 1))
	padLen := len(nextLineNum)
	lines := lineSplitRE.Split(string(s.Body), -1)
	var highlight strings.Builder
	if line >= 2 {
		fmt.Fprintf(&highlight, "%s: %s\n", lpad(padLen, prevLineNum), printLine(lines[line-2]))
	}
	fmt.Fprintf(&highlight, "%s: %s\n", lpad(padLen, lineNum), printLine(lines[line-1]))
	// The caret indent is as wide as the error's column, so on a
	// single-line document it is as wide as the document. Building it by
	// repeated concatenation reallocated and copied the whole prefix each
	// time — quadratic in the column number, which made one syntax error
	// on a long minified query burn seconds of CPU.
	if indent := 1 + padLen + l.Column; indent > 0 {
		highlight.WriteString(strings.Repeat(" ", indent))
	}
	highlight.WriteString("^\n")
	if line < len(lines) {
		fmt.Fprintf(&highlight, "%s: %s\n", lpad(padLen, nextLineNum), printLine(lines[line]))
	}
	return highlight.String()
}

func lpad(l int, s string) string {
	if n := l - len(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}
