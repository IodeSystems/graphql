package graphql_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/IodeSystems/graphql-go/v2"
	"github.com/IodeSystems/graphql-go/v2/language/parser"
	"github.com/IodeSystems/graphql-go/v2/language/source"
)

// runParity runs both ExecutePlan and ExecutePlanAppend and asserts
// they produce equivalent JSON-decoded responses. Map iteration
// order makes byte-equality unreliable for the marshaled-Result
// path, so we compare canonical decoded form.
func runParity(t *testing.T, schema graphql.Schema, query, opName string, args map[string]interface{}, root interface{}) {
	t.Helper()

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "test"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, opName)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	mapResult := graphql.ExecutePlan(plan, graphql.ExecuteParams{
		Schema:        schema,
		AST:           doc,
		OperationName: opName,
		Args:          args,
		Root:          root,
		Context:       context.Background(),
	})

	mapBytes, err := json.Marshal(envelope(mapResult))
	if err != nil {
		t.Fatalf("marshal map result: %v", err)
	}

	// Append-mode: info.Path is nil, depth-stack (pathBuf) used for
	// error path reconstruction.
	appendBytes, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
		Schema:        schema,
		AST:           doc,
		OperationName: opName,
		Args:          args,
		Root:          root,
		Context:       context.Background(),
	}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("append spec errors: %v", specErrs)
	}

	var mapDecoded, appendDecoded interface{}
	if err := json.Unmarshal(mapBytes, &mapDecoded); err != nil {
		t.Fatalf("decode map: %v\nbytes: %s", err, mapBytes)
	}
	if err := json.Unmarshal(appendBytes, &appendDecoded); err != nil {
		t.Fatalf("decode append: %v\nbytes: %s", err, appendBytes)
	}
	if !reflect.DeepEqual(mapDecoded, appendDecoded) {
		t.Fatalf("parity mismatch:\n  ExecutePlan:       %s\n  ExecutePlanAppend: %s", mapBytes, appendBytes)
	}
}

// envelope wraps a *graphql.Result so it marshals to the same shape
// ExecutePlanAppend emits: `{"data":...,"errors":[...]}` with errors
// omitted when empty.
func envelope(r *graphql.Result) map[string]interface{} {
	out := map[string]interface{}{"data": r.Data}
	if len(r.Errors) > 0 {
		out["errors"] = r.Errors
	}
	return out
}

func TestAppendParity_Scalars(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"i":     &graphql.Field{Type: graphql.Int, Resolve: func(graphql.ResolveParams) (interface{}, error) { return 42, nil }},
				"f":     &graphql.Field{Type: graphql.Float, Resolve: func(graphql.ResolveParams) (interface{}, error) { return 3.14, nil }},
				"s":     &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "hi \"world\"\n<tag>&", nil }},
				"b":     &graphql.Field{Type: graphql.Boolean, Resolve: func(graphql.ResolveParams) (interface{}, error) { return true, nil }},
				"id":    &graphql.Field{Type: graphql.ID, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "abc-123", nil }},
				"sNull": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return nil, nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ i f s b id sNull }`, "", nil, nil)
}

func TestAppendParity_NestedObjects(t *testing.T) {
	address := graphql.NewObject(graphql.ObjectConfig{
		Name: "Address",
		Fields: graphql.Fields{
			"city": &graphql.Field{Type: graphql.String},
			"zip":  &graphql.Field{Type: graphql.String},
		},
	})
	person := graphql.NewObject(graphql.ObjectConfig{
		Name: "Person",
		Fields: graphql.Fields{
			"name":    &graphql.Field{Type: graphql.String},
			"address": &graphql.Field{Type: address},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"me": &graphql.Field{
					Type: person,
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return map[string]interface{}{
							"name": "Ada",
							"address": map[string]interface{}{
								"city": "Westminster",
								"zip":  "SW1",
							},
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ me { name address { city zip } } }`, "", nil, nil)
}

func TestAppendParity_Lists(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"nums": &graphql.Field{
					Type:    graphql.NewList(graphql.Int),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []int{1, 2, 3, 4, 5}, nil },
				},
				"strs": &graphql.Field{
					Type:    graphql.NewList(graphql.String),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []string{"a", "b", "c"}, nil },
				},
				"empty": &graphql.Field{
					Type:    graphql.NewList(graphql.Int),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []int{}, nil },
				},
				"nullableItems": &graphql.Field{
					Type:    graphql.NewList(graphql.Int),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []interface{}{1, nil, 3}, nil },
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ nums strs empty nullableItems }`, "", nil, nil)
}

func TestAppendParity_NonNullBubble(t *testing.T) {
	// Resolver returns nil for a NonNull field — error bubbles to the
	// nullable parent (`me`), which becomes null and an error is
	// recorded.
	person := graphql.NewObject(graphql.ObjectConfig{
		Name: "Person",
		Fields: graphql.Fields{
			"name":     &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"nickname": &graphql.Field{Type: graphql.String},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"me": &graphql.Field{
					Type: person,
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return map[string]interface{}{
							"name":     nil,
							"nickname": "x",
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ me { name nickname } }`, "", nil, nil)
}

func TestAppendParity_NonNullListItem(t *testing.T) {
	// List-of-NonNull item null → list itself nulls, bubbles to the
	// nullable list field.
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"nums": &graphql.Field{
					Type:    graphql.NewList(graphql.NewNonNull(graphql.Int)),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []interface{}{1, 2, nil, 4}, nil },
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ nums }`, "", nil, nil)
}

func TestAppendParity_ResolverError(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"ok":  &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "ok", nil }},
				"bad": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return nil, errors.New("boom") }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ ok bad }`, "", nil, nil)
}

func TestAppendParity_Directives(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"a": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "A", nil }},
				"b": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "B", nil }},
				"c": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "C", nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ a b @skip(if: true) c @include(if: false) }`, "", nil, nil)
	runParity(t, schema, `query Q($s: Boolean!) { a b @skip(if: $s) }`, "Q", map[string]interface{}{"s": true}, nil)
	runParity(t, schema, `query Q($s: Boolean!) { a b @skip(if: $s) }`, "Q", map[string]interface{}{"s": false}, nil)
}

func TestAppendParity_Enum(t *testing.T) {
	colorEnum := graphql.NewEnum(graphql.EnumConfig{
		Name: "Color",
		Values: graphql.EnumValueConfigMap{
			"RED":   &graphql.EnumValueConfig{Value: "r"},
			"GREEN": &graphql.EnumValueConfig{Value: "g"},
			"BLUE":  &graphql.EnumValueConfig{Value: "b"},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"c":  &graphql.Field{Type: colorEnum, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "g", nil }},
				"cn": &graphql.Field{Type: colorEnum, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "unknown", nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ c cn }`, "", nil, nil)
}

func TestAppendParity_Interface(t *testing.T) {
	namedIface := graphql.NewInterface(graphql.InterfaceConfig{
		Name: "Named",
		Fields: graphql.Fields{
			"name": &graphql.Field{Type: graphql.String},
		},
		ResolveType: func(p graphql.ResolveTypeParams) *graphql.Object {
			m := p.Value.(map[string]interface{})
			if m["__t"] == "dog" {
				return p.Info.Schema.Type("Dog").(*graphql.Object)
			}
			return p.Info.Schema.Type("Cat").(*graphql.Object)
		},
	})
	dog := graphql.NewObject(graphql.ObjectConfig{
		Name:       "Dog",
		Interfaces: []*graphql.Interface{namedIface},
		Fields: graphql.Fields{
			"name":  &graphql.Field{Type: graphql.String},
			"barks": &graphql.Field{Type: graphql.Boolean},
		},
	})
	cat := graphql.NewObject(graphql.ObjectConfig{
		Name:       "Cat",
		Interfaces: []*graphql.Interface{namedIface},
		Fields: graphql.Fields{
			"name":  &graphql.Field{Type: graphql.String},
			"meows": &graphql.Field{Type: graphql.Boolean},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"pets": &graphql.Field{
					Type: graphql.NewList(namedIface),
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return []interface{}{
							map[string]interface{}{"__t": "dog", "name": "Rex", "barks": true},
							map[string]interface{}{"__t": "cat", "name": "Mia", "meows": true},
						}, nil
					},
				},
			},
		}),
		Types: []graphql.Type{dog, cat},
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ pets { name ... on Dog { barks } ... on Cat { meows } } }`, "", nil, nil)
}

// TestAppendParity_Union covers the append-mode walker's abstract
// branch for Unions specifically: sub-selections are planned lazily
// on first sight of a concrete type, so both walkers must agree.
func TestAppendParity_Union(t *testing.T) {
	dog := graphql.NewObject(graphql.ObjectConfig{
		Name: "UDog",
		Fields: graphql.Fields{
			"name":  &graphql.Field{Type: graphql.String},
			"barks": &graphql.Field{Type: graphql.Boolean},
		},
	})
	cat := graphql.NewObject(graphql.ObjectConfig{
		Name: "UCat",
		Fields: graphql.Fields{
			"name":  &graphql.Field{Type: graphql.String},
			"meows": &graphql.Field{Type: graphql.Boolean},
		},
	})
	pet := graphql.NewUnion(graphql.UnionConfig{
		Name:  "UPet",
		Types: []*graphql.Object{dog, cat},
		ResolveType: func(p graphql.ResolveTypeParams) *graphql.Object {
			if p.Value.(map[string]interface{})["__t"] == "dog" {
				return dog
			}
			return cat
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"pets": &graphql.Field{
					Type: graphql.NewList(pet),
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return []interface{}{
							map[string]interface{}{"__t": "dog", "name": "Rex", "barks": true},
							map[string]interface{}{"__t": "cat", "name": "Mia", "meows": true},
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ pets { ... on UDog { name barks } ... on UCat { name meows } } }`, "", nil, nil)
}

func TestAppendParity_Variables(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"echo": &graphql.Field{
					Type: graphql.String,
					Args: graphql.FieldConfigArgument{
						"v": &graphql.ArgumentConfig{Type: graphql.String},
					},
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return p.Args["v"], nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `query Q($v: String) { echo(v: $v) }`, "Q", map[string]interface{}{"v": "hello"}, nil)
}

// TestAppendByteShape spot-checks the literal output bytes for a
// trivial query — guards against regressions in the JSON envelope
// layout that JSON-decode parity wouldn't catch (e.g. trailing
// comma, double-quoted keys).
func TestAppendByteShape(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"a": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "1", nil }},
				"b": &graphql.Field{Type: graphql.Int, Resolve: func(graphql.ResolveParams) (interface{}, error) { return 2, nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ a b }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")
	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	want := `{"data":{"a":"1","b":2}}`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if !strings.HasPrefix(string(got), `{"data":`) || !strings.HasSuffix(string(got), `}`) {
		t.Fatalf("envelope shape wrong: %s", got)
	}
}

// TestAppendInfoPath_ErrorLocation confirms that error envelopes
// contain the correct `path` array reconstructed from the internal
// depth-stack (pathBuf).
func TestAppendInfoPath_ErrorLocation(t *testing.T) {
	thing := graphql.NewObject(graphql.ObjectConfig{
		Name: "Thing",
		Fields: graphql.Fields{
			"name": &graphql.Field{
				Type: graphql.NewNonNull(graphql.String),
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if s, ok := p.Source.(map[string]interface{}); ok {
						return s["name"], nil
					}
					return nil, errors.New("bad source")
				},
			},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"things": &graphql.Field{
					Type: graphql.NewList(thing),
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return []map[string]interface{}{
							{"name": "ok"},
							{"name": nil},
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ things { name } }`, "", nil, nil)
}

// TestAppendResolveAppend_Scalar exercises a field whose
// ResolveAppend writes a JSON scalar directly. Output must match a
// Resolve-based equivalent.
func TestAppendResolveAppend_Scalar(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"raw": &graphql.Field{
					Type: graphql.String,
					ResolveAppend: func(p graphql.ResolveParams, dst []byte) ([]byte, error) {
						return append(dst, `"hello ☃"`...), nil
					},
				},
				"resolved": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return "hello ☃", nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ raw resolved }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")
	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	want := `{"data":{"raw":"hello ☃","resolved":"hello ☃"}}`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// TestAppendResolveAppend_ObjectSubtree confirms ResolveAppend can
// emit a full nested object literal, bypassing the planned sub-walk.
// The implementer is on the hook for selection-set correctness.
func TestAppendResolveAppend_ObjectSubtree(t *testing.T) {
	point := graphql.NewObject(graphql.ObjectConfig{
		Name: "Point",
		Fields: graphql.Fields{
			"x": &graphql.Field{Type: graphql.Int},
			"y": &graphql.Field{Type: graphql.Int},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"p": &graphql.Field{
					Type: point,
					ResolveAppend: func(p graphql.ResolveParams, dst []byte) ([]byte, error) {
						return append(dst, `{"x":1,"y":2}`...), nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ p { x y } }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")
	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	want := `{"data":{"p":{"x":1,"y":2}}}`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// TestAppendResolveAppend_Error confirms a ResolveAppend error rolls
// the field's bytes back and emits "null" with an errors entry, same
// as the Resolve path.
func TestAppendResolveAppend_Error(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"good": &graphql.Field{
					Type: graphql.String,
					ResolveAppend: func(p graphql.ResolveParams, dst []byte) ([]byte, error) {
						return append(dst, `"ok"`...), nil
					},
				},
				"bad": &graphql.Field{
					Type: graphql.String,
					ResolveAppend: func(p graphql.ResolveParams, dst []byte) ([]byte, error) {
						return dst, errors.New("boom")
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ good bad }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")
	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode: %v\nbytes: %s", err, got)
	}
	data := decoded["data"].(map[string]interface{})
	if data["good"] != "ok" {
		t.Errorf("good = %v; want ok", data["good"])
	}
	if data["bad"] != nil {
		t.Errorf("bad = %v; want nil", data["bad"])
	}
	errs, _ := decoded["errors"].([]interface{})
	if len(errs) == 0 {
		t.Errorf("expected errors entry; bytes: %s", got)
	}
}

// TestAppendResolveAppend_NonNullError confirms ResolveAppend error
// on a NonNull field bubbles up to the nearest nullable parent.
func TestAppendResolveAppend_NonNullError(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"v": &graphql.Field{
					Type: graphql.NewNonNull(graphql.String),
					ResolveAppend: func(p graphql.ResolveParams, dst []byte) ([]byte, error) {
						return dst, errors.New("nope")
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ v }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")
	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	if !strings.Contains(string(got), `"data":null`) {
		t.Errorf("expected data:null for unabsorbed NonNull bubble; got %s", got)
	}
}

// TestAppendPartialLiteralArgs confirms a field whose arg list mixes
// literals and variables sees both at execute time: the literal
// pre-coerced at plan time and the variable resolved per request.
func TestAppendPartialLiteralArgs(t *testing.T) {
	var captured map[string]interface{}
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"users": &graphql.Field{
					Type: graphql.String,
					Args: graphql.FieldConfigArgument{
						"first":  &graphql.ArgumentConfig{Type: graphql.Int},
						"after":  &graphql.ArgumentConfig{Type: graphql.String},
						"prefix": &graphql.ArgumentConfig{Type: graphql.String, DefaultValue: "PFX"},
					},
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						captured = map[string]interface{}{}
						for k, v := range p.Args {
							captured[k] = v
						}
						return "ok", nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	query := `query Q($c: String) { users(first: 10, after: $c) }`
	src := source.NewSource(&source.Source{Body: []byte(query), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")

	_, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
		Schema: schema,
		AST:    doc,
		Args:   map[string]interface{}{"c": "abc"},
	}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	if got, want := captured["first"], 10; got != want {
		t.Errorf("first = %v; want %v", got, want)
	}
	if got, want := captured["after"], "abc"; got != want {
		t.Errorf("after = %v; want %v", got, want)
	}
	if got, want := captured["prefix"], "PFX"; got != want {
		t.Errorf("prefix (default) = %v; want %v", got, want)
	}
}

// TestAppendArgsPool_NonNilArgs confirms p.Args is non-nil under
// the pooled default and that RetainArgs=true opt-out also works.
// The pooled map must be a fresh-or-recycled empty map; the
// resolver must see exactly the args it was passed.
func TestAppendArgsPool_NonNilArgs(t *testing.T) {
	var seen map[string]interface{}
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"v": &graphql.Field{
					Type: graphql.String,
					Args: graphql.FieldConfigArgument{
						"a": &graphql.ArgumentConfig{Type: graphql.String},
					},
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						seen = p.Args
						if p.Args == nil {
							return nil, errors.New("p.Args is nil")
						}
						return p.Args["a"], nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ v(a: "hi") }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")

	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	if !strings.Contains(string(got), `"v":"hi"`) {
		t.Fatalf("default mode: want v=hi in %s", got)
	}
	// seen was captured before release; the resolver should not
	// inspect it post-return, but we can confirm the pool returned
	// a non-nil map.
	if seen == nil {
		t.Fatal("resolver received nil args")
	}

	// Opt-out path: RetainArgs=true skips the pool. Args still
	// non-nil and resolver still works.
	seen = nil
	got2, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc, RetainArgs: true}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("retain spec errors: %v", specErrs)
	}
	if !strings.Contains(string(got2), `"v":"hi"`) {
		t.Fatalf("RetainArgs mode: want v=hi in %s", got2)
	}
	if seen == nil {
		t.Fatal("resolver received nil args (RetainArgs)")
	}
}

// Thunked resolvers must produce the same values under the append
// walker as under the map-tree one. Concurrency itself is covered by
// BenchmarkThunks20_DoWriter; this is the value check, including a
// thunk that hands off through a goroutine.
func TestAppendThunkedResolvers(t *testing.T) {
	fooType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Foo",
		Fields: graphql.Fields{
			"name": &graphql.Field{Type: graphql.String},
		},
	})
	barType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Bar",
		Fields: graphql.Fields{
			"name": &graphql.Field{Type: graphql.String},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"foo": &graphql.Field{
					Type: fooType,
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return func() (interface{}, error) {
							return map[string]interface{}{"name": "Foo's name"}, nil
						}, nil
					},
				},
				"bar": &graphql.Field{
					Type: barType,
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						ch := make(chan map[string]interface{}, 1)
						go func() {
							ch <- map[string]interface{}{"name": "Bar's name"}
						}()
						return func() (interface{}, error) { return <-ch, nil }, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ foo { name } bar { name } }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")

	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
		Schema: schema,
		AST:    doc,
	}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode: %v\nbytes: %s", err, got)
	}
	data, _ := decoded["data"].(map[string]interface{})
	foo, _ := data["foo"].(map[string]interface{})
	bar, _ := data["bar"].(map[string]interface{})
	if foo["name"] != "Foo's name" {
		t.Fatalf("foo.name = %v; want %q", foo["name"], "Foo's name")
	}
	if bar["name"] != "Bar's name" {
		t.Fatalf("bar.name = %v; want %q", bar["name"], "Bar's name")
	}

	// Cross-check against the map-tree executor.
	mapResult := graphql.ExecutePlan(plan, graphql.ExecuteParams{Schema: schema, AST: doc})
	got2, err := json.Marshal(envelope(mapResult))
	if err != nil {
		t.Fatalf("marshal map result: %v", err)
	}
	var decoded2 map[string]interface{}
	if err := json.Unmarshal(got2, &decoded2); err != nil {
		t.Fatalf("map decode: %v\nbytes: %s", err, got2)
	}
	if !reflect.DeepEqual(decoded, decoded2) {
		t.Fatalf("thunk parity:\n  append: %s\n  map:    %s", got, got2)
	}
}
