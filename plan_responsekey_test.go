package graphql

import (
	"fmt"
	"math/rand"
	"testing"
)

// jsonStringEncodedLen sizes the slab that fillResponseKeyJSON builds
// every response key into. If it ever under-reports, appendJSONString
// grows the slab mid-build and every slice handed out before that
// point silently points at a stale array. Pin the two together.
func TestJSONStringEncodedLenMatchesAppend(t *testing.T) {
	cases := []string{
		"", "a", "hello", "hello_world", "__typename",
		`"`, `\`, "\n", "\r", "\t", "\b", "\f",
		"\x00", "\x01", "\x1f", "\x7f",
		"a\"b\\c\nd", "  spaced  ", "&amp;", "<script>",
		"héllo", "日本語", "🎉", "é",
		" ", " ", "a b c",
		"\xe2", "\xe2\x80", "a\xe2\x80", // truncated U+2028 prefixes
		"\xff\xfe", "a\xffb",
	}
	for i := 0; i < 2000; i++ {
		b := make([]byte, rand.Intn(24))
		for j := range b {
			b[j] = byte(rand.Intn(256))
		}
		cases = append(cases, string(b))
	}
	for _, s := range cases {
		want := len(appendJSONString(nil, s))
		if got := jsonStringEncodedLen(s); got != want {
			t.Fatalf("jsonStringEncodedLen(%q) = %d, want %d", s, got, want)
		}
	}
}

// The slab is one array shared by every field. Each field must get the
// exact bytes the old per-field encoder produced, and slicing must not
// let one field's append bleed into its neighbour.
func TestFillResponseKeyJSONMatchesPerFieldEncoding(t *testing.T) {
	keys := []string{"a", "alias", `weird"key`, "with\nnewline", "日本語", " ", ""}
	sp := &selectionPlan{}
	for _, k := range keys {
		sp.fields = append(sp.fields, &fieldPlan{responseKey: k})
	}
	fillResponseKeyJSON(sp)

	for i, fp := range sp.fields {
		want := append(appendJSONString(nil, keys[i]), ':')
		if string(fp.responseKeyJSON) != string(want) {
			t.Errorf("field %d (%q): got %q, want %q", i, keys[i], fp.responseKeyJSON, want)
		}
	}

	// Appending to one field's slice must not overwrite the next one:
	// the three-index slice caps each at its own length.
	for i, fp := range sp.fields {
		before := make([]string, len(sp.fields))
		for j, o := range sp.fields {
			before[j] = string(o.responseKeyJSON)
		}
		_ = append(fp.responseKeyJSON, 'X')
		for j, o := range sp.fields {
			if string(o.responseKeyJSON) != before[j] {
				t.Fatalf("append to field %d corrupted field %d: %q -> %q",
					i, j, before[j], o.responseKeyJSON)
			}
		}
	}
}

// A selection set with no fields never reaches fillResponseKeyJSON in
// the planner, but the helper must not panic if it ever does.
func TestFillResponseKeyJSONEmpty(t *testing.T) {
	sp := &selectionPlan{}
	fillResponseKeyJSON(sp)
	if len(sp.fields) != 0 {
		t.Fatal("unexpected fields")
	}
}

func TestFillResponseKeyJSONManyFields(t *testing.T) {
	sp := &selectionPlan{}
	for i := 0; i < 500; i++ {
		sp.fields = append(sp.fields, &fieldPlan{responseKey: fmt.Sprintf("field_%d", i)})
	}
	fillResponseKeyJSON(sp)
	for i, fp := range sp.fields {
		want := fmt.Sprintf("%q:", fmt.Sprintf("field_%d", i))
		if string(fp.responseKeyJSON) != want {
			t.Fatalf("field %d: got %q, want %q", i, fp.responseKeyJSON, want)
		}
	}
}
