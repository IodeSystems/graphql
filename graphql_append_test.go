package graphql_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/IodeSystems/graphql-go/v2"
)

func appendTestSchema(t testing.TB) graphql.Schema {
	t.Helper()
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"hello": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return "world", nil
					},
				},
				"num": &graphql.Field{
					Type: graphql.Int,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return 42, nil
					},
				},
				"boom": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return nil, errors.New("resolver exploded")
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return schema
}

// DoAppend must agree with json.Marshal(Do(...)) for every request
// shape, including the ones that never reach execution. Field order
// inside data differs by design (append-mode is deterministic, map
// iteration is not), so compare decoded.
func TestDoAppendParityWithDo(t *testing.T) {
	schema := appendTestSchema(t)
	for _, tc := range []struct{ name, query string }{
		{"simple", `{ hello }`},
		{"multi", `{ hello num }`},
		{"field error", `{ hello boom }`},
		{"parse error", `{ hello `},
		{"validation error", `{ nosuchfield }`},
		{"empty request", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := graphql.Params{Schema: schema, RequestString: tc.query}

			want, err := json.Marshal(graphql.Do(p))
			if err != nil {
				t.Fatalf("marshal Do: %v", err)
			}
			got := graphql.DoAppend(p, nil)

			var wantDec, gotDec interface{}
			if err := json.Unmarshal(want, &wantDec); err != nil {
				t.Fatalf("decode Do: %v (%s)", err, want)
			}
			if err := json.Unmarshal(got, &gotDec); err != nil {
				t.Fatalf("decode DoAppend: %v (%s)", err, got)
			}
			if !reflect.DeepEqual(wantDec, gotDec) {
				t.Fatalf("mismatch:\n  Do:       %s\n  DoAppend: %s", want, got)
			}
		})
	}
}

// The no-data envelope should be byte-identical to a marshalled Do
// result, not merely equivalent: nothing reorders when data is null.
func TestDoAppendErrorEnvelopeIsByteIdentical(t *testing.T) {
	schema := appendTestSchema(t)
	for _, q := range []string{`{ hello `, `{ nosuchfield }`, ``} {
		p := graphql.Params{Schema: schema, RequestString: q}
		want, err := json.Marshal(graphql.Do(p))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if got := graphql.DoAppend(p, nil); !bytes.Equal(got, want) {
			t.Errorf("query %q:\n  want %s\n  got  %s", q, want, got)
		}
	}
}

// DoAppend appends; it must not clobber what the caller already has.
func TestDoAppendPreservesPrefix(t *testing.T) {
	schema := appendTestSchema(t)
	prefix := []byte("PREFIX")
	got := graphql.DoAppend(graphql.Params{Schema: schema, RequestString: `{ hello }`}, prefix)
	if !bytes.HasPrefix(got, []byte("PREFIX")) {
		t.Fatalf("prefix lost: %s", got)
	}
	if !bytes.Contains(got, []byte(`"world"`)) {
		t.Fatalf("body missing: %s", got)
	}
}

func TestDoWriterMatchesDoAppend(t *testing.T) {
	schema := appendTestSchema(t)
	for _, q := range []string{`{ hello num }`, `{ hello boom }`, `{ nosuchfield }`} {
		p := graphql.Params{Schema: schema, RequestString: q}
		var buf bytes.Buffer
		if err := graphql.DoWriter(p, &buf); err != nil {
			t.Fatalf("DoWriter: %v", err)
		}
		if want := graphql.DoAppend(p, nil); !bytes.Equal(buf.Bytes(), want) {
			t.Errorf("query %q:\n  DoAppend: %s\n  DoWriter: %s", q, want, buf.Bytes())
		}
	}
}

// The pooled buffer is reused across calls; a stale length or a
// missing reset would leak the previous response into the next one.
func TestDoWriterPoolReuseIsClean(t *testing.T) {
	schema := appendTestSchema(t)
	queries := []string{`{ hello num }`, `{ hello }`, `{ num }`, `{ hello num }`}
	want := make([]string, len(queries))
	for i, q := range queries {
		want[i] = string(graphql.DoAppend(graphql.Params{Schema: schema, RequestString: q}, nil))
	}
	for round := 0; round < 50; round++ {
		for i, q := range queries {
			var buf bytes.Buffer
			if err := graphql.DoWriter(graphql.Params{Schema: schema, RequestString: q}, &buf); err != nil {
				t.Fatalf("DoWriter: %v", err)
			}
			if got := buf.String(); got != want[i] {
				t.Fatalf("round %d query %q:\n  want %s\n  got  %s", round, q, want[i], got)
			}
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// A write failure is the only thing DoWriter reports. GraphQL errors
// live in the body and must not surface as a returned error.
func TestDoWriterReturnsOnlyWriteErrors(t *testing.T) {
	schema := appendTestSchema(t)

	var buf bytes.Buffer
	if err := graphql.DoWriter(graphql.Params{Schema: schema, RequestString: `{ nosuchfield }`}, &buf); err != nil {
		t.Fatalf("validation failure must not be a returned error, got %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"errors"`)) {
		t.Fatalf("validation errors missing from body: %s", buf.Bytes())
	}

	if err := graphql.DoWriter(graphql.Params{Schema: schema, RequestString: `{ hello }`}, failWriter{}); err == nil {
		t.Fatal("expected the writer's error")
	}
}
