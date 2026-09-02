package graphql_test

import (
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/IodeSystems/graphql-go/v2"
	"github.com/IodeSystems/graphql-go/v2/benchutil"
	"github.com/IodeSystems/graphql-go/v2/language/parser"
	"github.com/IodeSystems/graphql-go/v2/language/source"
)

// BenchmarkPlannedExecute_* compare a cached *Plan re-executed N times
// vs graphql.Do (which parses, validates, plans, and executes every
// call). The cached path skips parse + validate + plan; what's left
// is the work that's inherently per-request.

func BenchmarkPlannedExecute_WideQuery_100_10(b *testing.B) {
	schema := benchutil.WideSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideSchemaQuery(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := graphql.ExecutePlan(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("errors: %v", result.Errors)
		}
	}
}

func BenchmarkUncachedExecute_WideQuery_100_10(b *testing.B) {
	schema := benchutil.WideSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideSchemaQuery(100)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := graphql.Do(graphql.Params{
			Schema:        schema,
			RequestString: query,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("errors: %v", result.Errors)
		}
	}
}

func BenchmarkPlannedExecute_ListQuery_1K(b *testing.B) {
	schema := benchutil.ListSchemaWithXItems(1000)
	query := `query { colors { hex r g b } }`

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := graphql.ExecutePlan(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("errors: %v", result.Errors)
		}
	}
}

func BenchmarkUncachedExecute_ListQuery_1K(b *testing.B) {
	schema := benchutil.ListSchemaWithXItems(1000)
	query := `query { colors { hex r g b } }`

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := graphql.Do(graphql.Params{
			Schema:        schema,
			RequestString: query,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("errors: %v", result.Errors)
		}
	}
}

// BenchmarkPlannedExecute_WideQuery_100_10_Varied demonstrates that
// a single cached *Plan handles arbitrary literal variations — the
// plan binds the field arg to a `$v` variable and the request's Args
// changes per call. No re-parse, no re-validate, no re-plan; just
// per-request arg coercion (the only inherently-dynamic work) plus
// the resolver loop. This is the canonical parametric-query path:
// every real client should look like this.
func BenchmarkPlannedExecute_WideQuery_100_10_Varied(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideArgedSchemaQueryWithVariable(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := graphql.ExecutePlan(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
			Args:   map[string]interface{}{"v": fmt.Sprintf("v-%d", i)},
		})
		if len(result.Errors) > 0 {
			b.Fatalf("errors: %v", result.Errors)
		}
	}
}

// BenchmarkUncachedExecute_WideQuery_100_10_Varied is the comparison:
// same workload, but the caller bakes the literal into the query
// string itself and lets graphql.Do parse + validate + plan + execute
// every single call. This is what naive clients do when they don't
// use GraphQL variables.
func BenchmarkUncachedExecute_WideQuery_100_10_Varied(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		query := benchutil.WideArgedSchemaQueryWithLiteral(100, fmt.Sprintf("v-%d", i))
		result := graphql.Do(graphql.Params{
			Schema:        schema,
			RequestString: query,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("errors: %v", result.Errors)
		}
	}
}

// BenchmarkPlanCache_HotLoop_NativeVars: the canonical fast path —
// the client sends the query once with `$v` variables; the cache
// stores one plan; every iteration is `cache.Get + ExecutePlan` with
// varying Args. This is what real clients should look like.
func BenchmarkPlanCache_HotLoop_NativeVars(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideArgedSchemaQueryWithVariable(100)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pr := cache.Get(&schema, query, "")
		if len(pr.Errors) > 0 {
			b.Fatalf("get: %v", pr.Errors)
		}
		result := graphql.ExecutePlan(pr.Plan, graphql.ExecuteParams{
			Schema: schema,
			Args:   map[string]interface{}{"v": fmt.Sprintf("v-%d", i)},
		})
		if len(result.Errors) > 0 {
			b.Fatalf("execute: %v", result.Errors)
		}
	}
}

// BenchmarkPlanCache_HotLoop_Normalized: the salvage path — the
// client sends literal-baked queries that vary per call. With
// Normalize=true, every iteration parses + normalizes (cheap) and
// hits the same cached plan. Demonstrates the full effect of cache
// + normalization on the worst client behavior.
func BenchmarkPlanCache_HotLoop_Normalized(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		query := benchutil.WideArgedSchemaQueryWithLiteral(100, fmt.Sprintf("v-%d", i))
		pr := cache.Get(&schema, query, "")
		if len(pr.Errors) > 0 {
			b.Fatalf("get: %v", pr.Errors)
		}
		result := graphql.ExecutePlan(pr.Plan, graphql.ExecuteParams{
			Schema: schema,
			Args:   pr.SynthArgs,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("execute: %v", result.Errors)
		}
	}
}

// BenchmarkPlanCache_HotLoop_NoNorm: the worst case — literal-baked
// queries vary per call, but normalization is OFF. Every iteration
// is a fresh parse + validate + plan. The cache only hits when an
// LRU-replayed literal happens to match (vanishingly rare for real
// workloads). This measures what users get if they neither use
// variables nor turn on normalization.
func BenchmarkPlanCache_HotLoop_NoNorm(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		query := benchutil.WideArgedSchemaQueryWithLiteral(100, fmt.Sprintf("v-%d", i))
		pr := cache.Get(&schema, query, "")
		if len(pr.Errors) > 0 {
			b.Fatalf("get: %v", pr.Errors)
		}
		result := graphql.ExecutePlan(pr.Plan, graphql.ExecuteParams{
			Schema: schema,
			Args:   pr.SynthArgs,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("execute: %v", result.Errors)
		}
	}
}

// BenchmarkPlannedExecute_WideQuery_100_10_StaticArg is the static
// counterpart: same plan, same Args every call. Lets us see how
// close the Varied case (per-call arg coercion) gets to the
// fully-static ceiling.
func BenchmarkPlannedExecute_WideQuery_100_10_StaticArg(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideArgedSchemaQueryWithVariable(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}
	args := map[string]interface{}{"v": "static"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := graphql.ExecutePlan(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
			Args:   args,
		})
		if len(result.Errors) > 0 {
			b.Fatalf("errors: %v", result.Errors)
		}
	}
}

// BenchmarkPlannedAppend_* mirror BenchmarkPlannedExecute_* but
// route through ExecutePlanAppend. Each iteration reuses a single
// pooled scratch buffer (length-reset to keep cap), modeling the
// canonical caller pattern: append straight into a per-request
// response buffer that's pooled across calls.

func BenchmarkPlannedAppend_WideQuery_100_10(b *testing.B) {
	schema := benchutil.WideSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideSchemaQuery(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	buf := make([]byte, 0, 64*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
		}, buf)
		if len(specErrs) > 0 {
			b.Fatalf("spec errors: %v", specErrs)
		}
		buf = out
	}
}

func BenchmarkPlannedAppend_ListQuery_1K(b *testing.B) {
	schema := benchutil.ListSchemaWithXItems(1000)
	query := `query { colors { hex r g b } }`

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	buf := make([]byte, 0, 256*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
		}, buf)
		if len(specErrs) > 0 {
			b.Fatalf("spec errors: %v", specErrs)
		}
		buf = out
	}
}

func BenchmarkPlannedAppend_WideQuery_100_10_Varied(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideArgedSchemaQueryWithVariable(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	buf := make([]byte, 0, 64*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
			Args:   map[string]interface{}{"v": fmt.Sprintf("v-%d", i)},
		}, buf)
		if len(specErrs) > 0 {
			b.Fatalf("spec errors: %v", specErrs)
		}
		buf = out
	}
}

func BenchmarkPlannedAppend_WideQuery_100_10_StaticArg(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideArgedSchemaQueryWithVariable(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}
	args := map[string]interface{}{"v": "static"}

	buf := make([]byte, 0, 64*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
			Args:   args,
		}, buf)
		if len(specErrs) > 0 {
			b.Fatalf("spec errors: %v", specErrs)
		}
		buf = out
	}
}

// BenchmarkPlannedAppendResolveAppend_WideQuery_100_10 mirrors
// BenchmarkPlannedAppend_WideQuery_100_10 but every leaf field uses
// ResolveAppend instead of Resolve. Measures the win from skipping
// the Serialize / leafEmitter / boxing chain on every field.
func BenchmarkPlannedAppendResolveAppend_WideQuery_100_10(b *testing.B) {
	schema := benchutil.WideSchemaResolveAppendWithXFieldsAndYItems(100, 10)
	query := benchutil.WideSchemaQuery(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	buf := make([]byte, 0, 64*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
		}, buf)
		if len(specErrs) > 0 {
			b.Fatalf("spec errors: %v", specErrs)
		}
		buf = out
	}
}

// defaultResolveBenchRow exercises DefaultResolveFn against a struct
// source: name-case-insensitive match on F00..F49.
type defaultResolveBenchRow struct {
	F00, F01, F02, F03, F04, F05, F06, F07, F08, F09 string
	F10, F11, F12, F13, F14, F15, F16, F17, F18, F19 string
	F20, F21, F22, F23, F24, F25, F26, F27, F28, F29 string
	F30, F31, F32, F33, F34, F35, F36, F37, F38, F39 string
	F40, F41, F42, F43, F44, F45, F46, F47, F48, F49 string
}

// BenchmarkPlannedAppend_EnumList_1K measures the enum leaf path:
// a list of 1000 items, each emitting a single enum field. Exercises
// the pre-encoded valueToJSON fast path vs. the Serialize+emitter
// fallback. Variation simulates real-world non-uniform enum distribution.
func BenchmarkPlannedAppend_EnumList_1K(b *testing.B) {
	status := graphql.NewEnum(graphql.EnumConfig{
		Name: "Status",
		Values: graphql.EnumValueConfigMap{
			"ACTIVE":   &graphql.EnumValueConfig{Value: "ACTIVE"},
			"INACTIVE": &graphql.EnumValueConfig{Value: "INACTIVE"},
			"PENDING":  &graphql.EnumValueConfig{Value: "PENDING"},
		},
	})
	row := graphql.NewObject(graphql.ObjectConfig{
		Name: "Row",
		Fields: graphql.Fields{
			"status": &graphql.Field{
				Type: graphql.NewNonNull(status),
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return p.Source, nil
				},
			},
		},
	})
	query := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"rows": &graphql.Field{
				Type: graphql.NewList(row),
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					out := make([]string, 1000)
					vals := [3]string{"ACTIVE", "INACTIVE", "PENDING"}
					for i := range out {
						out[i] = vals[i%3]
					}
					return out, nil
				},
			},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: query})
	if err != nil {
		b.Fatalf("schema: %v", err)
	}

	q := "{ rows { status } }"
	src := source.NewSource(&source.Source{Body: []byte(q), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	buf := make([]byte, 0, 32*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
		}, buf)
		if len(specErrs) > 0 {
			b.Fatalf("spec errors: %v", specErrs)
		}
		buf = out
	}
}

// BenchmarkPlannedAppend_DefaultResolve_Struct50 measures the
// DefaultResolveFn path: 50 leaf fields, all using default resolution
// against a struct source. The cache hit case dominates after warmup.
func BenchmarkPlannedAppend_DefaultResolve_Struct50(b *testing.B) {
	rowFields := graphql.Fields{}
	for i := 0; i < 50; i++ {
		rowFields[fmt.Sprintf("f%02d", i)] = &graphql.Field{Type: graphql.String}
	}
	row := graphql.NewObject(graphql.ObjectConfig{Name: "Row", Fields: rowFields})
	query := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"row": &graphql.Field{
				Type: row,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return defaultResolveBenchRow{
						F00: "a", F01: "b", F02: "c", F03: "d", F04: "e",
						F05: "f", F06: "g", F07: "h", F08: "i", F09: "j",
					}, nil
				},
			},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: query})
	if err != nil {
		b.Fatalf("schema: %v", err)
	}

	q := "{ row { "
	for i := 0; i < 50; i++ {
		q += fmt.Sprintf("f%02d ", i)
	}
	q += "} }"

	src := source.NewSource(&source.Source{Body: []byte(q), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		b.Fatalf("plan: %v", err)
	}

	buf := make([]byte, 0, 8*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
			Schema: schema,
			AST:    doc,
		}, buf)
		if len(specErrs) > 0 {
			b.Fatalf("spec errors: %v", specErrs)
		}
		buf = out
	}
}

// BenchmarkPlanQuery_Wide_100 times PlanQuery itself on a wide
// selection set. The PlannedExecute_* benchmarks above build their plan
// once outside the timed loop, so nothing here measured plan-time
// allocation until this existed; PlanQueryAbstractFan is deep rather
// than wide (two fields per selection set), so it could not see
// per-field costs either. That blind spot is why the response-key
// encoding allocated once per field for three months unnoticed.
func BenchmarkPlanQuery_Wide_100(b *testing.B) {
	schema := benchutil.WideSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideSchemaQuery(100)

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "bench"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		b.Fatalf("parse: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := graphql.PlanQuery(&schema, doc, ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDoMarshal_Wide_100 and BenchmarkDoWriter_Wide_100 compare
// the two full-request entry points on the same schema and query:
// what a server does today (Do, then marshal the *Result) against the
// byte path (DoWriter). Both parse, validate, plan and execute every
// call — no plan cache — so this is the honest end-to-end shape, not
// the executor in isolation.
func BenchmarkDoMarshal_Wide_100(b *testing.B) {
	schema := benchutil.WideSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideSchemaQuery(100)
	p := graphql.Params{Schema: schema, RequestString: query}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := json.Marshal(graphql.Do(p))
		if err != nil {
			b.Fatal(err)
		}
		_ = out
	}
}

func BenchmarkDoWriter_Wide_100(b *testing.B) {
	schema := benchutil.WideSchemaWithXFieldsAndYItems(100, 10)
	query := benchutil.WideSchemaQuery(100)
	p := graphql.Params{Schema: schema, RequestString: query}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := graphql.DoWriter(p, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}
