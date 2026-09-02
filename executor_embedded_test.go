package graphql_test

import (
	"testing"

	"github.com/IodeSystems/graphql-go/v2"
)

// DefaultResolveFn resolves fields promoted from embedded structs, using
// Go's own promotion rules. These tests pin the rules that a plain
// selector expression would follow: shallower depths win, same-depth
// ties are ambiguous and resolve to nothing, and a nil embedded pointer
// yields null rather than a panic.

type embLeaf struct {
	Leaf string `json:"leaf"`
	Deep string
}

type embMid struct {
	embLeaf
	Mid string
}

type embRoot struct {
	embMid
	Own string
}

// shadowRoot declares Deep itself, shadowing embLeaf.Deep two levels down.
type shadowRoot struct {
	embMid
	Deep string
}

type ambA struct{ Dup string }
type ambB struct{ Dup string }

// ambRoot embeds two structs that both declare Dup at the same depth.
type ambRoot struct {
	ambA
	ambB
}

type ptrRoot struct {
	*embLeaf
	Own string
}

// selfRoot embeds itself by pointer — the frontier walk must terminate.
type selfRoot struct {
	*selfRoot
	Own string
}

func embSchema(t *testing.T, objFields graphql.Fields, resolve graphql.FieldResolveFn) graphql.Schema {
	t.Helper()
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"o": &graphql.Field{
					Type: graphql.NewObject(graphql.ObjectConfig{
						Name:   "O",
						Fields: objFields,
					}),
					Resolve: resolve,
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func embQuery(t *testing.T, schema graphql.Schema, query string) map[string]interface{} {
	t.Helper()
	r := graphql.Do(graphql.Params{Schema: schema, RequestString: query})
	if len(r.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", r.Errors)
	}
	return r.Data.(map[string]interface{})["o"].(map[string]interface{})
}

func TestDefaultResolveFn_EmbeddedPromotion(t *testing.T) {
	schema := embSchema(t, graphql.Fields{
		"own":  &graphql.Field{Type: graphql.String},
		"mid":  &graphql.Field{Type: graphql.String},
		"leaf": &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return embRoot{
			embMid: embMid{embLeaf: embLeaf{Leaf: "L"}, Mid: "M"},
			Own:    "O",
		}, nil
	})
	got := embQuery(t, schema, `{ o { own mid leaf } }`)
	for field, want := range map[string]string{"own": "O", "mid": "M", "leaf": "L"} {
		if got[field] != want {
			t.Errorf("%s = %v, want %q", field, got[field], want)
		}
	}
}

// The `json` tag on an embedded field is honoured through promotion —
// embLeaf.Leaf is tagged `json:"leaf"`, matched above by tag as well as
// by name. Here the GraphQL field name only matches the tag.
func TestDefaultResolveFn_EmbeddedTagMatch(t *testing.T) {
	type tagged struct {
		Value string `graphql:"renamed"`
	}
	type root struct{ tagged }
	schema := embSchema(t, graphql.Fields{
		"renamed": &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return root{tagged{Value: "V"}}, nil
	})
	if got := embQuery(t, schema, `{ o { renamed } }`); got["renamed"] != "V" {
		t.Errorf("renamed = %v, want \"V\"", got["renamed"])
	}
}

func TestDefaultResolveFn_EmbeddedShallowerWins(t *testing.T) {
	schema := embSchema(t, graphql.Fields{
		"deep": &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return shadowRoot{
			embMid: embMid{embLeaf: embLeaf{Deep: "embedded"}},
			Deep:   "outer",
		}, nil
	})
	if got := embQuery(t, schema, `{ o { deep } }`); got["deep"] != "outer" {
		t.Errorf("deep = %v, want \"outer\" (the shallower field)", got["deep"])
	}
}

func TestDefaultResolveFn_EmbeddedAmbiguousResolvesToNull(t *testing.T) {
	schema := embSchema(t, graphql.Fields{
		"dup": &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return ambRoot{ambA{Dup: "a"}, ambB{Dup: "b"}}, nil
	})
	// Go would reject `v.Dup` as an ambiguous selector; neither branch
	// wins, so the field is null rather than an arbitrary pick.
	if got := embQuery(t, schema, `{ o { dup } }`); got["dup"] != nil {
		t.Errorf("dup = %v, want nil (ambiguous promotion)", got["dup"])
	}
}

func TestDefaultResolveFn_EmbeddedPointer(t *testing.T) {
	schema := embSchema(t, graphql.Fields{
		"leaf": &graphql.Field{Type: graphql.String},
		"own":  &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return ptrRoot{embLeaf: &embLeaf{Leaf: "L"}, Own: "O"}, nil
	})
	got := embQuery(t, schema, `{ o { leaf own } }`)
	if got["leaf"] != "L" || got["own"] != "O" {
		t.Errorf("got %v, want leaf=L own=O", got)
	}
}

func TestDefaultResolveFn_EmbeddedNilPointerIsNull(t *testing.T) {
	schema := embSchema(t, graphql.Fields{
		"leaf": &graphql.Field{Type: graphql.String},
		"own":  &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return ptrRoot{Own: "O"}, nil
	})
	got := embQuery(t, schema, `{ o { leaf own } }`)
	if got["leaf"] != nil {
		t.Errorf("leaf = %v, want nil (nil embedded pointer)", got["leaf"])
	}
	if got["own"] != "O" {
		t.Errorf("own = %v, want \"O\"", got["own"])
	}
}

func TestDefaultResolveFn_EmbeddedSelfReferentialTerminates(t *testing.T) {
	schema := embSchema(t, graphql.Fields{
		"own":     &graphql.Field{Type: graphql.String},
		"missing": &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return selfRoot{Own: "O"}, nil
	})
	// "missing" matches nothing, so the walk must exhaust the frontier
	// rather than recurse through selfRoot forever.
	got := embQuery(t, schema, `{ o { own missing } }`)
	if got["own"] != "O" || got["missing"] != nil {
		t.Errorf("got %v, want own=O missing=nil", got)
	}
}

// A non-embedded struct field must not be searched: only anonymous
// fields promote.
func TestDefaultResolveFn_NamedStructFieldDoesNotPromote(t *testing.T) {
	type inner struct{ Hidden string }
	type root struct {
		Inner inner
	}
	schema := embSchema(t, graphql.Fields{
		"hidden": &graphql.Field{Type: graphql.String},
	}, func(graphql.ResolveParams) (interface{}, error) {
		return root{inner{Hidden: "H"}}, nil
	})
	if got := embQuery(t, schema, `{ o { hidden } }`); got["hidden"] != nil {
		t.Errorf("hidden = %v, want nil (named field must not promote)", got["hidden"])
	}
}
