package gqlerrors_test

import (
	"strings"
	"testing"
	"time"

	"github.com/IodeSystems/graphql-go/v2/language/parser"
)

// Syntax-error formatting must stay linear in the error's column.
//
// highlightSourceAtLocation builds a caret indent as wide as the error's
// column. On a single-line document — the shape of every minified query —
// that column is the document length, so building the indent by repeated
// string concatenation was quadratic: an 80KB malformed query cost ~2.7s
// of CPU, reachable by any client since parsing runs on untrusted input.
//
// Quadratic growth quadruples the time per doubling of input. This asserts
// well under that, with enough headroom to absorb a slow or loaded machine
// while still failing loudly if the quadratic behavior returns.
func TestSyntaxErrorFormattingIsNotQuadratic(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	parseMalformed := func(n int) time.Duration {
		// One long line, syntax error at the far end: no nesting involved.
		q := "{ " + strings.Repeat("a ", n) + "!"
		start := time.Now()
		if _, err := parser.Parse(parser.ParseParams{Source: q}); err == nil {
			t.Fatal("expected a syntax error")
		}
		return time.Since(start)
	}

	// Warm caches and package-level regexes so the small sample is not
	// paying one-time setup that the large one avoids.
	parseMalformed(1000)

	const small, factor = 10000, 4
	dSmall := parseMalformed(small)
	dLarge := parseMalformed(small * factor)

	// Linear would be ~4x, quadratic ~16x. Fail above 8x: comfortably
	// clear of linear-with-noise, comfortably under quadratic.
	if dSmall <= 0 {
		return // clock too coarse to compare; nothing to assert
	}
	if ratio := float64(dLarge) / float64(dSmall); ratio > 8 {
		t.Errorf("scaling %.1fx for a %dx larger document (%v -> %v); "+
			"expected roughly linear, quadratic formatting has likely returned",
			ratio, factor, dSmall, dLarge)
	}
}

// A malformed query of a size a client could plausibly send must not cost
// seconds of CPU. Before the fix this took ~2.7s.
func TestSyntaxErrorOnLargeQueryIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	q := "{ " + strings.Repeat("a ", 40000) + "!"
	start := time.Now()
	if _, err := parser.Parse(parser.ParseParams{Source: q}); err == nil {
		t.Fatal("expected a syntax error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("formatting one syntax error on an 80KB query took %v", elapsed)
	}
}
