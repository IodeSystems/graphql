package graphql_test

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/IodeSystems/graphql-go/v2"
)

// thunkSchema builds n fields whose resolvers each start a goroutine
// that sleeps, then return a thunk waiting on it. This is upstream's
// examples/concurrent-resolvers shape: the parallelism lives in the
// resolver, and the executor only decides when to await.
//
// Awaiting each thunk as its resolver returns costs n*delay.
// Calling every resolver first and awaiting afterwards costs ~delay.
func thunkSchema(t testing.TB, n int, delay time.Duration) (graphql.Schema, string) {
	t.Helper()
	fields := graphql.Fields{}
	var q strings.Builder
	q.WriteString("{")
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("f%d", i)
		fields[name] = &graphql.Field{
			Type: graphql.String,
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				ch := make(chan string, 1)
				go func() {
					time.Sleep(delay)
					ch <- "ok"
				}()
				return func() (interface{}, error) { return <-ch, nil }, nil
			},
		}
		q.WriteString(" " + name)
	}
	q.WriteString(" }")

	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{Name: "Q", Fields: fields}),
	})
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return schema, q.String()
}

// Regression guard. Before two-phase resolution the append walker
// awaited each thunk as its own resolver returned, serializing
// goroutines meant to overlap: 20 fields x 1ms measured 21.8ms here,
// against 1.6ms for Do. If this benchmark climbs back toward
// n*delay, the phase split has been undone.
func BenchmarkThunks20_DoWriter(b *testing.B) {
	schema, query := thunkSchema(b, 20, time.Millisecond)
	p := graphql.Params{Schema: schema, RequestString: query}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := graphql.DoWriter(p, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkThunks20_Do(b *testing.B) {
	schema, query := thunkSchema(b, 20, time.Millisecond)
	p := graphql.Params{Schema: schema, RequestString: query}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := graphql.Do(p); r.HasErrors() {
			b.Fatal(r.Errors)
		}
	}
}

// Correctness, not speed: every thunked field must still be present and
// correct, whichever order they were awaited in.
func TestThunkResultsComplete(t *testing.T) {
	schema, query := thunkSchema(t, 20, time.Microsecond)
	p := graphql.Params{Schema: schema, RequestString: query}
	var sb strings.Builder
	if err := graphql.DoWriter(p, &sb); err != nil {
		t.Fatal(err)
	}
	got := sb.String()
	for i := 0; i < 20; i++ {
		want := fmt.Sprintf(`"f%d":"ok"`, i)
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}
