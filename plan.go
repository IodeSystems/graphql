package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"sync"

	"github.com/IodeSystems/graphql-go/gqlerrors"
	"github.com/IodeSystems/graphql-go/language/ast"
)

// Plan is a precomputed execution shape for a (schema, document,
// operationName) triple. Same triple → same Plan; cache + reuse.
//
// PlanQuery does once-per-query work that Execute otherwise repeats
// every request: identify the operation, collect fragments, resolve
// every selection's *FieldDefinition, walk the selection tree to
// build per-level field lists, pre-coerce literal arguments, and
// pre-compute @include/@skip predicates that don't reference
// variables. ExecutePlan walks the plan and only does work that's
// inherently per-request: variable substitution, abstract type
// runtime resolution, resolver invocation, and result materialization.
//
// Plans are bound to the *Schema pointer they were planned against.
// If the schema is rebuilt, plans become stale (the *Schema pointer
// changes) and callers should re-plan.
type Plan struct {
	schema     *Schema
	operation  *ast.OperationDefinition
	fragments  map[string]ast.Definition
	rootType   *Object
	root       *selectionPlan
	isMutation bool

	// abstractMu guards lazy population of fieldPlan.abstractAlternatives,
	// which happens at execute time (concurrently across fields) the
	// first time each concrete type is encountered for an abstract field.
	abstractMu sync.Mutex
}

// selectionPlan is a pre-collected, source-ordered list of fields to
// emit for one selection set under a known parent runtime type. The
// plan tree mirrors the document, with one selectionPlan per object-
// returning field's sub-selection.
type selectionPlan struct {
	parentType *Object
	fields     []*fieldPlan

	// keysOnce guards the lazy build of every field's responseKeyJSON.
	// Those bytes are read only by the append-mode walkers, so building
	// them at plan time charged ExecutePlan and Do callers for a
	// feature they never touch. Plans are cached and shared across
	// concurrent requests, hence Once rather than a plain nil check.
	keysOnce sync.Once
}

// ensureResponseKeys builds the response-key JSON for this selection
// set if it has not been built yet. Called from the append walker on
// entry to a selection set; the map-tree walker never calls it, and so
// never pays for it.
func (sp *selectionPlan) ensureResponseKeys() {
	sp.keysOnce.Do(func() { fillResponseKeyJSON(sp) })
}

// fieldPlan is one entry in a selectionPlan: enough to resolve, run,
// and complete a single field without re-walking the schema or the
// AST. Sub-selections are pre-planned for object returns; abstract
// returns lazily plan per-concrete-type at execute time (cached on
// abstractAlternatives).
type fieldPlan struct {
	responseKey string
	fieldName   string
	fieldDef    *FieldDefinition
	fieldASTs   []*ast.Field // [0] is the canonical AST for arg lookup; full slice flows into ResolveInfo
	args        argPlan
	returnType  Output

	// skipPredicate evaluates the field's combined @skip / @include
	// directives against request variables. nil ⇒ always include
	// (constant-true at plan time, the common case).
	skipPredicate func(map[string]interface{}) bool

	// sub is set when returnType (after unwrapping NonNull and List)
	// resolves to a single concrete *Object; abstractAlternatives is
	// set when it resolves to an Interface or Union; both nil for
	// leaf fields (Scalar/Enum) and for fields whose sub-selection
	// the planner couldn't analyse (e.g. Object returns whose own
	// sub-selection contained inputs we don't yet handle — falls
	// back to runtime collectFields).
	sub                  *selectionPlan
	abstractAlternatives map[*Object]*selectionPlan

	// responseKeyJSON is the pre-encoded JSON object key for this
	// field, including the leading quote, escaped key bytes, closing
	// quote, and trailing colon. Ready to drop between fields in the
	// append-mode walker. Example: []byte(`"hello":`).
	responseKeyJSON []byte

	// leafEmitter is non-nil when returnType (after unwrapping NonNull
	// + List) is a Scalar carrying an AppendJSON hook, or any Enum
	// (the planner builds an emitter that wraps Enum.Serialize +
	// appendJSONString). Called with the raw resolver result; writes
	// the JSON form (or "null" when Serialize would yield nil). nil
	// for object/abstract returns and for custom scalars without
	// AppendJSON, in which case the walker falls back to
	// Serialize + json.Marshal.
	leafEmitter AppendJSONFn

	// leafType caches the unwrapped leaf type for the
	// AppendJSON-less scalar fallback path. nil when the field
	// returns a composite (Object / Interface / Union) or when
	// leafEmitter is set (the walker uses leafEmitter directly).
	leafType Leaf

	// listElemType is the reflect.Type of the concrete Go element
	// type for list-returning fields, populated lazily on first
	// execution. Enables typed iteration (int/String/bool/float64)
	// without reflect.Value.Interface() boxing. nil for non-list
	// fields or when the element type is unknown.
	listElemType reflect.Type
}

// argPlan separates per-arg coercion into a plan-time slice
// (literals + schema defaults — same per request, coerce once) and a
// per-request slice (any arg whose AST references a `$variable`).
// Mixed fields (some literal, some variable) get partial pre-coercion:
// only the variable-bearing args run through populateArgumentValues at
// execute time; the literal subset is copied verbatim from static.
type argPlan struct {
	// static holds the coerced literal / default subset. Read-only;
	// the executor copies before handing to the resolver so the
	// resolver can mutate without aliasing the plan. Empty when every
	// arg references a variable.
	static map[string]interface{}

	// dynamicArgDefs / dynamicArgASTs are the variable-bearing args
	// (parallel slices). Empty when every arg is a literal/default.
	dynamicArgDefs []*Argument
	dynamicArgASTs []*ast.Argument
}

// PlanQuery walks the document, picks the named operation (or the
// only one), pre-resolves fragments + the entire selection tree, and
// returns a Plan ready to be executed against the same schema.
//
// Errors are spec-aligned: missing operation name, unknown operation
// name, ambiguous operation, and document containing a non-operation
// non-fragment definition all surface here. Field-level errors (e.g.
// "no such field on type X") that the existing executor surfaces as
// dispatch-time errors continue to surface there — PlanQuery only
// fails on document-level errors.
func PlanQuery(schema *Schema, doc *ast.Document, operationName string) (*Plan, error) {
	if schema == nil {
		return nil, errors.New("graphql: PlanQuery: schema is nil")
	}
	if doc == nil {
		return nil, errors.New("graphql: PlanQuery: document is nil")
	}

	var operation *ast.OperationDefinition
	fragments := map[string]ast.Definition{}
	for _, definition := range doc.Definitions {
		switch d := definition.(type) {
		case *ast.OperationDefinition:
			if operationName == "" && operation != nil {
				return nil, errors.New("Must provide operation name if query contains multiple operations.")
			}
			if operationName == "" || (d.GetName() != nil && d.GetName().Value == operationName) {
				operation = d
			}
		case *ast.FragmentDefinition:
			key := ""
			if d.GetName() != nil && d.GetName().Value != "" {
				key = d.GetName().Value
			}
			fragments[key] = d
		default:
			return nil, fmt.Errorf("GraphQL cannot execute a request containing a %v", definition.GetKind())
		}
	}
	if operation == nil {
		if operationName != "" {
			return nil, fmt.Errorf(`Unknown operation named "%v".`, operationName)
		}
		return nil, errors.New("Must provide an operation.")
	}

	rootType, err := getOperationRootType(*schema, operation)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		schema:     schema,
		operation:  operation,
		fragments:  fragments,
		rootType:   rootType,
		isMutation: operation.GetOperation() == ast.OperationTypeMutation,
	}
	plan.root = plan.planSelectionSet(rootType, operation.GetSelectionSet(), nil)
	return plan, nil
}

// planSelectionSet pre-collects the fields under one selection-set
// for a known parent type, recursing into sub-selections. Two-phase:
//
//  1. collectInto walks the selection set + fragments, grouping
//     fields by responseKey and merging their ASTs.
//  2. For each grouped field, planFieldChildren walks the union of
//     all merged ASTs' sub-selections — ensuring fragments that name
//     the same response key contribute their sub-fields. Without
//     this, `... { x { a } } ... { x { b } }` would only see one
//     fragment's `x.{...}`.
//
// visitedFragmentNames is threaded along to avoid infinite recursion
// in mutually-referencing fragments — same shape as the runtime
// collectFields uses.
func (p *Plan) planSelectionSet(parentType *Object, selectionSet *ast.SelectionSet, visitedFragmentNames map[string]bool) *selectionPlan {
	if selectionSet == nil {
		return nil
	}
	if visitedFragmentNames == nil {
		visitedFragmentNames = map[string]bool{}
	}
	sp := &selectionPlan{parentType: parentType}
	keyed := map[string]int{}
	p.collectInto(parentType, selectionSet, visitedFragmentNames, sp, keyed, nil)
	if len(sp.fields) == 0 {
		return nil
	}
	// Phase 2: plan sub-selections for each merged field group.
	for _, fp := range sp.fields {
		if fp.fieldDef == nil {
			continue
		}
		p.planMergedFieldChildren(fp)
	}
	return sp
}

// planMergedFieldChildren walks every AST in fp.fieldASTs to merge
// their sub-selections into a single sub-plan (or per-concrete-type
// abstract alternatives). Mirrors completeObjectValue's loop over
// fieldASTs, but at plan time so the executor can use the result
// directly.
func (p *Plan) planMergedFieldChildren(fp *fieldPlan) {
	// Object returns resolve to a single concrete type, so plan their
	// sub-selection eagerly.
	if obj, ok := unwrapNamedType(fp.returnType).(*Object); ok {
		fp.sub = p.planMergedSelectionsForType(obj, fp.fieldASTs)
		return
	}
	// Abstract returns (Interface / Union) are planned lazily, per
	// concrete type, the first time that type is actually encountered at
	// execute time (see Plan.abstractAlternative). Eagerly expanding
	// every possible type here is O(possibleTypes) per abstract field;
	// because each expanded type's sub-selection can contain further
	// abstract fields, it compounds to O(possibleTypes ^ nesting-depth)
	// — which makes deeply-nested polymorphic queries on large schemas
	// take minutes to plan, even though a single request only ever
	// resolves one concrete type per level.
}

// abstractAlternative returns the planned sub-selection for an abstract
// field's runtime concrete type, planning (and caching) it on first use.
// Concurrency-safe: ExecutePlan resolves fields concurrently, so several
// goroutines may reach the same abstract field at once.
func (p *Plan) abstractAlternative(fp *fieldPlan, runtimeType *Object) *selectionPlan {
	p.abstractMu.Lock()
	defer p.abstractMu.Unlock()
	if fp.abstractAlternatives == nil {
		fp.abstractAlternatives = map[*Object]*selectionPlan{}
	}
	if sub, ok := fp.abstractAlternatives[runtimeType]; ok {
		return sub
	}
	sub := p.planMergedSelectionsForType(runtimeType, fp.fieldASTs)
	fp.abstractAlternatives[runtimeType] = sub
	return sub
}

// planMergedSelectionsForType collects the union of every AST's
// SelectionSet under one concrete parent type, returning a
// selectionPlan that mirrors what completeObjectValue's runtime
// collectFields loop would produce.
func (p *Plan) planMergedSelectionsForType(parentType *Object, fieldASTs []*ast.Field) *selectionPlan {
	sp := &selectionPlan{parentType: parentType}
	keyed := map[string]int{}
	visited := map[string]bool{}
	for _, f := range fieldASTs {
		if f == nil || f.SelectionSet == nil {
			continue
		}
		p.collectInto(parentType, f.SelectionSet, visited, sp, keyed, nil)
	}
	if len(sp.fields) == 0 {
		return nil
	}
	for _, fp := range sp.fields {
		if fp.fieldDef == nil {
			continue
		}
		p.planMergedFieldChildren(fp)
	}
	return sp
}

// collectInto mirrors executor.collectFields: walks selections,
// follows fragment spreads + inline fragments, evaluates @include /
// @skip directives at plan time when constant. Per-field
// skipPredicates carry the dynamic part forward to ExecutePlan.
//
// parentPred carries variable-driven @skip / @include from any
// enclosing inline fragment or fragment spread. It is AND-composed
// with each field's own predicate when a new fieldPlan is created so
// that fragment-level gates are honored at execute time.
//
// keyed maps responseKey → index in sp.fields so repeat selections
// of the same response key merge their fieldASTs (matches
// collectFields's `fields[name] = append(fields[name], selection)`).
func (p *Plan) collectInto(parentType *Object, selectionSet *ast.SelectionSet, visitedFragmentNames map[string]bool, sp *selectionPlan, keyed map[string]int, parentPred func(map[string]interface{}) bool) {
	for _, iSelection := range selectionSet.Selections {
		switch sel := iSelection.(type) {
		case *ast.Field:
			pred, alwaysSkip := planDirectives(sel.Directives)
			if alwaysSkip {
				continue
			}
			responseKey := getFieldEntryKey(sel)
			if idx, ok := keyed[responseKey]; ok {
				// Merge with an earlier same-key field (sub-selection
				// merging happens at execute time via collectFields on
				// the sub-selection — for now we just keep all ASTs and
				// let the runtime path stitch sub-selections; the
				// plan-time precompute conservatively re-plans the
				// first AST's sub-selection, which is correct because
				// validation rules guarantee mergeable selections refer
				// to the same field).
				sp.fields[idx].fieldASTs = append(sp.fields[idx].fieldASTs, sel)
				continue
			}
			fieldName := ""
			if sel.Name != nil {
				fieldName = sel.Name.Value
			}
			fieldDef := getFieldDef(*p.schema, parentType, fieldName)
			if fieldDef == nil {
				// Unknown field: keep it in the plan with a nil
				// fieldDef so ExecutePlan can mirror the
				// hasNoFieldDefs branch (skip the response key).
			}
			fp := &fieldPlan{
				responseKey:   responseKey,
				fieldName:     fieldName,
				fieldDef:      fieldDef,
				fieldASTs:     []*ast.Field{sel},
				skipPredicate: andPredicates(parentPred, pred),
			}
			if fieldDef != nil {
				fp.returnType = fieldDef.Type
				fp.args = planArguments(fieldDef.Args, sel.Arguments)
				fp.leafEmitter, fp.leafType = pickLeafEmitter(fp.returnType)
			}
			keyed[responseKey] = len(sp.fields)
			sp.fields = append(sp.fields, fp)

		case *ast.InlineFragment:
			pred, alwaysSkip := planDirectives(sel.Directives)
			if alwaysSkip {
				continue
			}
			if !planFragmentMatches(*p.schema, sel.TypeCondition, parentType) {
				continue
			}
			if sel.SelectionSet != nil {
				p.collectInto(parentType, sel.SelectionSet, visitedFragmentNames, sp, keyed, andPredicates(parentPred, pred))
			}

		case *ast.FragmentSpread:
			pred, alwaysSkip := planDirectives(sel.Directives)
			if alwaysSkip {
				continue
			}
			fragName := ""
			if sel.Name != nil {
				fragName = sel.Name.Value
			}
			if visitedFragmentNames[fragName] {
				continue
			}
			frag, ok := p.fragments[fragName]
			if !ok {
				continue
			}
			fragDef, ok := frag.(*ast.FragmentDefinition)
			if !ok {
				continue
			}
			visitedFragmentNames[fragName] = true
			if !planFragmentMatches(*p.schema, fragDef.TypeCondition, parentType) {
				continue
			}
			if fragDef.GetSelectionSet() != nil {
				p.collectInto(parentType, fragDef.GetSelectionSet(), visitedFragmentNames, sp, keyed, andPredicates(parentPred, pred))
			}
		}
	}
}

// andPredicates returns a predicate that is true only when both inputs
// are true. nil is treated as the constant-true predicate, so the
// common "no enclosing gate" / "no field-level directive" cases avoid
// allocating a closure.
func andPredicates(a, b func(map[string]interface{}) bool) func(map[string]interface{}) bool {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(vars map[string]interface{}) bool {
		if !a(vars) {
			return false
		}
		return b(vars)
	}
}

// unwrapNamedType peels NonNull and List wrappers to expose the
// underlying named type (Object / Interface / Union / Scalar / Enum).
func unwrapNamedType(t Output) Output {
	for {
		switch tt := t.(type) {
		case *NonNull:
			t = tt.OfType.(Output)
		case *List:
			t = tt.OfType.(Output)
		default:
			return tt
		}
	}
}

// fillResponseKeyJSON pre-builds the JSON object keys (`"key":`) for
// every field in sp, as slices of ONE backing array.
//
// These bytes are read only by the append-mode walkers. Building them
// per field cost one allocation per field on every plan, paid by
// ExecutePlan and Execute callers who never look at them; a slab makes
// it one allocation per selection set instead.
//
// The slab is sized exactly by jsonStringEncodedLen, so the append
// calls below cannot grow it and invalidate slices already handed out.
// Field names are spec-validated identifiers, but aliases are
// arbitrary strings, so the bytes still route through the full JSON
// string escaper.
func fillResponseKeyJSON(sp *selectionPlan) {
	total := 0
	for _, fp := range sp.fields {
		total += jsonStringEncodedLen(fp.responseKey) + 1 // + ':'
	}
	slab := make([]byte, 0, total)
	for _, fp := range sp.fields {
		start := len(slab)
		slab = appendJSONString(slab, fp.responseKey)
		slab = append(slab, ':')
		fp.responseKeyJSON = slab[start:len(slab):len(slab)]
	}
}

// pickLeafEmitter inspects the unwrapped leaf type of a return type
// and returns the AppendJSON emitter the planned-append walker
// should use for it, plus the Leaf reference for the
// AppendJSON-less fallback path. Returns (nil, nil) for composite
// returns (Object / Interface / Union); returns (emitter, leaf) for
// scalars with AppendJSON and for all enums; returns (nil, scalar)
// for scalars without AppendJSON so the walker can call Serialize
// directly and json.Marshal the result.
func pickLeafEmitter(out Output) (AppendJSONFn, Leaf) {
	leaf := unwrapNamedType(out)
	switch t := leaf.(type) {
	case *Scalar:
		if fn := t.scalarConfig.AppendJSON; fn != nil {
			return fn, t
		}
		return nil, t
	case *Enum:
		return makeEnumAppendJSON(t), t
	}
	return nil, nil
}

// makeEnumAppendJSON returns an emitter that JSON-quotes the
// already-serialized enum name. The walker's writeCompleteLeafValue
// calls Enum.Serialize before invoking the emitter, so this gets a
// post-Serialize string (or some non-string the walker already
// rejected as nil — defensive null fallback).
func makeEnumAppendJSON(en *Enum) AppendJSONFn {
	_ = en
	return func(dst []byte, value interface{}) []byte {
		if s, ok := value.(string); ok {
			return appendJSONString(dst, s)
		}
		return append(dst, "null"...)
	}
}

// planArguments walks argDefs and classifies each arg per request:
// variable-bearing args defer to populateArgumentValues; literal args
// (and args with no AST entry that fall back to argDef.DefaultValue)
// are coerced once here and stored in argPlan.static. Mixed fields
// see a partial pre-coercion — only the dynamic subset runs at
// execute time.
func planArguments(argDefs []*Argument, argASTs []*ast.Argument) argPlan {
	if len(argDefs) == 0 && len(argASTs) == 0 {
		return argPlan{}
	}
	var static map[string]interface{}
	var dynDefs []*Argument
	var dynASTs []*ast.Argument
	for _, argDef := range argDefs {
		var argAST *ast.Argument
		for _, a := range argASTs {
			if a != nil && a.Name != nil && a.Name.Value == argDef.PrivateName {
				argAST = a
				break
			}
		}
		if argAST != nil && valueHasVariables(argAST.Value) {
			dynDefs = append(dynDefs, argDef)
			dynASTs = append(dynASTs, argAST)
			continue
		}
		var value ast.Value
		if argAST != nil {
			value = argAST.Value
		}
		tmp := valueFromAST(value, argDef.Type, nil)
		if isNullish(tmp) {
			tmp = argDef.DefaultValue
		}
		if isNullish(tmp) {
			continue
		}
		if static == nil {
			static = map[string]interface{}{}
		}
		static[argDef.PrivateName] = tmp
	}
	return argPlan{
		static:         static,
		dynamicArgDefs: dynDefs,
		dynamicArgASTs: dynASTs,
	}
}

// astHasVariables walks the argument AST tree looking for any
// ast.Variable node. Returns true on the first hit.
func astHasVariables(argASTs []*ast.Argument) bool {
	for _, a := range argASTs {
		if a == nil {
			continue
		}
		if valueHasVariables(a.Value) {
			return true
		}
	}
	return false
}

func valueHasVariables(v ast.Value) bool {
	switch n := v.(type) {
	case nil:
		return false
	case *ast.Variable:
		return true
	case *ast.ListValue:
		for _, item := range n.Values {
			if valueHasVariables(item) {
				return true
			}
		}
	case *ast.ObjectValue:
		for _, f := range n.Fields {
			if f != nil && valueHasVariables(f.Value) {
				return true
			}
		}
	}
	return false
}

// planDirectives evaluates @include and @skip directives at plan
// time when their `if` argument is a literal; returns a
// skipPredicate (nil if always-include) and an alwaysSkip flag (true
// if literal evaluation produced a definitive skip).
func planDirectives(directives []*ast.Directive) (pred func(map[string]interface{}) bool, alwaysSkip bool) {
	var skipDir, includeDir *ast.Directive
	for _, d := range directives {
		if d == nil || d.Name == nil {
			continue
		}
		switch d.Name.Value {
		case SkipDirective.Name:
			skipDir = d
		case IncludeDirective.Name:
			includeDir = d
		}
	}
	if skipDir == nil && includeDir == nil {
		return nil, false
	}
	// Evaluate constants where possible; surface a runtime predicate
	// for the variable-driven cases.
	var skipDyn, includeDyn *ast.Directive
	if skipDir != nil {
		if astHasVariables(skipDir.Arguments) {
			skipDyn = skipDir
		} else {
			vals := getArgumentValues(SkipDirective.Args, skipDir.Arguments, nil)
			if v, ok := vals["if"].(bool); ok && v {
				return nil, true
			}
		}
	}
	if includeDir != nil {
		if astHasVariables(includeDir.Arguments) {
			includeDyn = includeDir
		} else {
			vals := getArgumentValues(IncludeDirective.Args, includeDir.Arguments, nil)
			if v, ok := vals["if"].(bool); ok && !v {
				return nil, true
			}
		}
	}
	if skipDyn == nil && includeDyn == nil {
		return nil, false
	}
	return func(vars map[string]interface{}) bool {
		if skipDyn != nil {
			vals := getArgumentValues(SkipDirective.Args, skipDyn.Arguments, vars)
			if v, ok := vals["if"].(bool); ok && v {
				return false // excluded
			}
		}
		if includeDyn != nil {
			vals := getArgumentValues(IncludeDirective.Args, includeDyn.Arguments, vars)
			if v, ok := vals["if"].(bool); ok && !v {
				return false // excluded
			}
		}
		return true
	}, false
}

// planFragmentMatches mirrors doesFragmentConditionMatch: a missing
// type condition matches anything; otherwise the condition resolves
// against the schema and must equal — or, for abstract types,
// include — the runtime parent type.
func planFragmentMatches(schema Schema, typeConditionAST *ast.Named, runtime *Object) bool {
	if typeConditionAST == nil {
		return true
	}
	conditionalType, err := typeFromAST(schema, typeConditionAST)
	if err != nil {
		return false
	}
	if conditionalType == runtime {
		return true
	}
	if conditionalType.Name() == runtime.Name() {
		return true
	}
	switch ct := conditionalType.(type) {
	case *Interface:
		return schema.IsPossibleType(ct, runtime)
	case *Union:
		return schema.IsPossibleType(ct, runtime)
	}
	return false
}

// ExecutePlan runs a planned operation. Args / Root / Context still
// flow in per-request; everything else (selection shape, field
// resolution, literal args, directive predicates, sub-plans) is
// taken from the plan.
//
// The walker mirrors executor.executeOperation → executeFields →
// resolveField → completeValue, but skips collectFields and
// getFieldDef on the hot path. Per-field arguments come from the
// argPlan: static (no variables) bypasses getArgumentValues entirely.
func ExecutePlan(plan *Plan, p ExecuteParams) (result *Result) {
	if plan == nil {
		return &Result{Errors: gqlerrors.FormatErrors(errors.New("graphql: ExecutePlan: plan is nil"))}
	}
	ctx := p.Context
	if ctx == nil {
		ctx = context.Background()
	}

	extErrs, executionFinishFn := handleExtensionsExecutionDidStart(&p)
	if len(extErrs) != 0 {
		return &Result{Errors: extErrs}
	}
	defer func() {
		extErrs := executionFinishFn(result)
		if len(extErrs) != 0 {
			result.Errors = append(result.Errors, extErrs...)
		}
		addExtensionResults(&p, result)
	}()

	resultChannel := make(chan *Result, 2)
	go func() {
		out := &Result{}
		defer func() {
			if err := recover(); err != nil {
				if e, ok := err.(error); ok {
					out.Errors = append(out.Errors, gqlerrors.FormatError(e))
				} else {
					out.Errors = append(out.Errors, gqlerrors.FormatError(fmt.Errorf("%v", err)))
				}
			}
			resultChannel <- out
		}()

		// Plan is bound to plan.schema (sub-plans, abstractAlternatives,
		// and field defs were resolved against it). Use that same schema
		// here so variable coercion and abstract-type resolution stay
		// consistent with what the plan was built against — p.Schema is
		// ignored to avoid silent drift if the caller passes a rebuilt
		// schema with the same shape but different *Object pointers.
		execSchema := *plan.schema
		variableValues, err := getVariableValues(execSchema, plan.operation.GetVariableDefinitions(), p.Args)
		if err != nil {
			out.Errors = append(out.Errors, gqlerrors.FormatError(err))
			return
		}

		eCtx := &executionContext{
			Schema:         execSchema,
			Fragments:      plan.fragments,
			Root:           p.Root,
			Operation:      plan.operation,
			VariableValues: variableValues,
			Context:        ctx,
			poolArgs:       !p.RetainArgs,
			plan:           plan,
		}

		data := executePlannedSelection(eCtx, plan.root, p.Root, plan.rootType)
		// Mutations run serially with each field's result
		// dethunked depth-first; queries run all then dethunk
		// breadth-first. Skip the entire dethunk pass when no
		// resolver returned a func() — the dominant case in
		// production schemas — saving the full tree walk plus
		// the per-node closure pushes the BFS dethunker would do.
		if eCtx.thunkCount > 0 {
			if plan.isMutation {
				dethunkMapDepthFirst(data)
			} else {
				dethunkMapWithBreadthFirstTraversal(data)
			}
		}
		out.Data = data
		out.Errors = append(out.Errors, eCtx.Errors...)
	}()

	select {
	case <-ctx.Done():
		r := &Result{}
		r.Errors = append(r.Errors, gqlerrors.FormatError(ctx.Err()))
		return r
	case r := <-resultChannel:
		return r
	}
}

// executePlannedSelection runs one selection plan against a parent
// source value, returning the assembled response map. Walks fields
// in source order (sp.fields is built that way at plan time).
//
// Mutation vs. query traversal is handled at the top level in
// ExecutePlan via dethunkMapDepthFirst / dethunkMapWithBreadthFirstTraversal,
// so this walker is the same for both.
func executePlannedSelection(eCtx *executionContext, sp *selectionPlan, source interface{}, parentType *Object) map[string]interface{} {
	if sp == nil {
		return map[string]interface{}{}
	}
	if source == nil {
		source = map[string]interface{}{}
	}
	finalResults := make(map[string]interface{}, len(sp.fields))
	for _, fp := range sp.fields {
		if fp.skipPredicate != nil && !fp.skipPredicate(eCtx.VariableValues) {
			continue
		}
		if fp.fieldDef == nil {
			// Mirrors executeSubFields' hasNoFieldDefs branch: silently
			// skip unknown fields. Validation should have rejected
			// these but we match runtime behavior for safety.
			continue
		}
		// Push this field's response key for error-path reconstruction
		// (handleFieldError → errorPathArray reads eCtx.pathBuf).
		// Same pattern as writePlannedField in the append-mode walker.
		pathDepth := len(eCtx.pathBuf)
		eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{key: fp.responseKey})
		resolved, ok := resolvePlannedField(eCtx, parentType, source, fp)
		eCtx.pathBuf = eCtx.pathBuf[:pathDepth]
		if !ok {
			continue
		}
		finalResults[fp.responseKey] = resolved
	}
	return finalResults
}

// resolvePlannedField mirrors resolveField but uses the plan's
// pre-resolved fieldDef + pre-coerced static args + pre-decided
// returnType. Pure-literal arguments skip getArgumentValues entirely;
// variable-bearing args fall through to the existing per-request
// coercion.
func resolvePlannedField(eCtx *executionContext, parentType *Object, source interface{}, fp *fieldPlan) (result interface{}, ok bool) {
	var returnType Output
	defer func() {
		if r := recover(); r != nil {
			handleFieldError(r, FieldASTsToNodeASTs(fp.fieldASTs), returnType, eCtx)
			ok = true
		}
	}()
	fieldDef := fp.fieldDef
	returnType = fp.returnType
	resolveFn := fieldDef.Resolve
	if resolveFn == nil {
		resolveFn = DefaultResolveFn
	}

	// Resolvers expect a non-nil Args map (the existing resolveField
	// path always passes the result of getArgumentValues, which is
	// never nil even when empty). Match that contract.
	//
	// The ExecutePlan path does NOT pool args — resolvers may return
	// thunks that close over p.Args, and those thunks are dethunked
	// later (breadth-first), long after this function returns. The
	// append-mode walker dethunks synchronously and can safely pool;
	// see writePlannedField.
	//
	// Fast path for arg-less fields: hand back a shared empty map.
	// Resolvers must treat p.Args as read-only (documented contract);
	// thunks may close over it but len==0 means there's nothing to read,
	// so the singleton is safe even on the no-pool path. Saves one map
	// alloc per resolved field on schemas without arguments — the
	// dominant shape for list-of-leaves workloads.
	var args map[string]interface{}
	if len(fp.args.static) == 0 && len(fp.args.dynamicArgDefs) == 0 {
		args = emptyArgsMap
	} else {
		args = make(map[string]interface{}, len(fp.args.static)+len(fp.args.dynamicArgDefs))
		for k, v := range fp.args.static {
			args[k] = v
		}
		if len(fp.args.dynamicArgDefs) > 0 {
			populateArgumentValues(args, fp.args.dynamicArgDefs, fp.args.dynamicArgASTs, eCtx.VariableValues)
		}
	}

	info := ResolveInfo{
		FieldName:      fp.fieldName,
		FieldASTs:      fp.fieldASTs,
		ReturnType:     returnType,
		ParentType:     parentType,
		Schema:         eCtx.Schema,
		Fragments:      eCtx.Fragments,
		RootValue:      eCtx.Root,
		Operation:      eCtx.Operation,
		VariableValues: eCtx.VariableValues,
	}

	// Extensions allocate a per-field map + closure even when none are
	// registered. Skip entirely on the common no-extensions schema —
	// saves ~22% of allocs per resolved field on hot paths.
	var resolveFieldFinishFn resolveFieldFinishFuncHandler
	if len(eCtx.Schema.extensions) > 0 {
		var extErrs []gqlerrors.FormattedError
		extErrs, resolveFieldFinishFn = handleExtensionsResolveFieldDidStart(eCtx.Schema.extensions, eCtx, &info)
		if len(extErrs) != 0 {
			eCtx.Errors = append(eCtx.Errors, extErrs...)
		}
	}

	var resolveFnError error
	result, resolveFnError = resolveFn(ResolveParams{
		Source:  source,
		Args:    args,
		Info:    info,
		Context: eCtx.Context,
	})

	if resolveFieldFinishFn != nil {
		extErrs := resolveFieldFinishFn(result, resolveFnError)
		if len(extErrs) != 0 {
			eCtx.Errors = append(eCtx.Errors, extErrs...)
		}
	}
	if resolveFnError != nil {
		panic(resolveFnError)
	}

	completed := completePlannedValueCatchingError(eCtx, returnType, fp, info, result)
	return completed, true
}

func completePlannedValueCatchingError(eCtx *executionContext, returnType Type, fp *fieldPlan, info ResolveInfo, result interface{}) (completed interface{}) {
	defer func() {
		if r := recover(); r != nil {
			handleFieldError(r, FieldASTsToNodeASTs(fp.fieldASTs), returnType, eCtx)
		}
	}()
	if rt, ok := returnType.(*NonNull); ok {
		return completePlannedValue(eCtx, rt, fp, info, result)
	}
	return completePlannedValue(eCtx, returnType, fp, info, result)
}

func completePlannedValue(eCtx *executionContext, returnType Type, fp *fieldPlan, info ResolveInfo, result interface{}) interface{} {
	resultVal := reflect.ValueOf(result)
	if resultVal.IsValid() && resultVal.Kind() == reflect.Func {
		eCtx.thunkCount++
		// Snapshot the pathBuf at thunk-creation time. Thunks are
		// dethunked after the walker has already unwound past this
		// field's push/pop, so without a snapshot, errors raised
		// during dethunk land at the wrong response path. The walker
		// dethunks single-threaded (depth-first for mutations,
		// breadth-first for queries) so swap/restore on eCtx.pathBuf
		// is safe — no concurrent reads to race with.
		pathSnap := append([]pathEntry(nil), eCtx.pathBuf...)
		return func() interface{} {
			savedPath := eCtx.pathBuf
			eCtx.pathBuf = pathSnap
			defer func() { eCtx.pathBuf = savedPath }()
			return completePlannedThunkValueCatchingError(eCtx, returnType, fp, info, result)
		}
	}
	if rt, ok := returnType.(*NonNull); ok {
		completed := completePlannedValue(eCtx, rt.OfType, fp, info, result)
		if completed == nil {
			err := NewLocatedErrorWithPath(
				fmt.Sprintf("Cannot return null for non-nullable field %v.%v.", info.ParentType, info.FieldName),
				FieldASTsToNodeASTs(fp.fieldASTs),
				eCtx.errorPathArray(),
			)
			panic(gqlerrors.FormatError(err))
		}
		return completed
	}
	if isNullish(result) {
		return nil
	}
	if rt, ok := returnType.(*List); ok {
		return completePlannedListValue(eCtx, rt, fp, info, result)
	}
	if rt, ok := returnType.(*Scalar); ok {
		return completeLeafValue(rt, result)
	}
	if rt, ok := returnType.(*Enum); ok {
		return completeLeafValue(rt, result)
	}
	if rt, ok := returnType.(*Union); ok {
		return completePlannedAbstractValue(eCtx, rt, fp, info, result)
	}
	if rt, ok := returnType.(*Interface); ok {
		return completePlannedAbstractValue(eCtx, rt, fp, info, result)
	}
	if rt, ok := returnType.(*Object); ok {
		return completePlannedObjectValue(eCtx, rt, fp, info, result)
	}
	err := invariantf(false, `Cannot complete value of unexpected type "%v."`, returnType)
	if err != nil {
		panic(gqlerrors.FormatError(err))
	}
	return nil
}

func completePlannedThunkValueCatchingError(eCtx *executionContext, returnType Type, fp *fieldPlan, info ResolveInfo, result interface{}) (completed interface{}) {
	defer func() {
		if r := recover(); r != nil {
			handleFieldError(r, FieldASTsToNodeASTs(fp.fieldASTs), returnType, eCtx)
		}
	}()
	propertyFn, ok := result.(func() (interface{}, error))
	if !ok {
		err := gqlerrors.NewFormattedError("Error resolving func. Expected `func() (interface{}, error)` signature")
		panic(gqlerrors.FormatError(err))
	}
	fnResult, err := propertyFn()
	if err != nil {
		panic(gqlerrors.FormatError(err))
	}
	result = fnResult
	if rt, ok := returnType.(*NonNull); ok {
		return completePlannedValue(eCtx, rt, fp, info, result)
	}
	return completePlannedValue(eCtx, returnType, fp, info, result)
}

func completePlannedListValue(eCtx *executionContext, returnType *List, fp *fieldPlan, info ResolveInfo, result interface{}) interface{} {
	resultVal := reflect.ValueOf(result)
	if resultVal.Kind() == reflect.Ptr {
		resultVal = resultVal.Elem()
	}
	parentTypeName := ""
	if info.ParentType != nil {
		parentTypeName = info.ParentType.Name()
	}
	err := invariantf(
		resultVal.IsValid() && isIterable(result),
		"User Error: expected iterable, but did not find one "+
			"for field %v.%v.", parentTypeName, info.FieldName)
	if err != nil {
		panic(gqlerrors.FormatError(err))
	}
	itemType := returnType.OfType
	completedResults := make([]interface{}, 0, resultVal.Len())
	for i := 0; i < resultVal.Len(); i++ {
		val := resultVal.Index(i).Interface()
		// Push the list index onto pathBuf for any error-path
		// reporting under this item; pop after the item completes.
		itemDepth := len(eCtx.pathBuf)
		eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{idx: i})
		completedItem := completePlannedValueCatchingError(eCtx, itemType, fp, info, val)
		eCtx.pathBuf = eCtx.pathBuf[:itemDepth]
		completedResults = append(completedResults, completedItem)
	}
	return completedResults
}

func completePlannedObjectValue(eCtx *executionContext, returnType *Object, fp *fieldPlan, info ResolveInfo, result interface{}) interface{} {
	if returnType.IsTypeOf != nil {
		p := IsTypeOfParams{Value: result, Info: info, Context: eCtx.Context}
		if !returnType.IsTypeOf(p) {
			panic(gqlerrors.NewFormattedError(
				fmt.Sprintf(`Expected value of type "%v" but got: %T.`, returnType, result),
			))
		}
	}
	if fp.sub != nil {
		return executePlannedSelection(eCtx, fp.sub, result, returnType)
	}
	// Fallback: planner didn't precompute (e.g. selection set was
	// empty per validation, which shouldn't reach here for object
	// types). Surface no-data with a defensive empty map.
	return map[string]interface{}{}
}

func completePlannedAbstractValue(eCtx *executionContext, returnType Abstract, fp *fieldPlan, info ResolveInfo, result interface{}) interface{} {
	var runtimeType *Object
	rtParams := ResolveTypeParams{Value: result, Info: info, Context: eCtx.Context}
	if u, ok := returnType.(*Union); ok && u.ResolveType != nil {
		runtimeType = u.ResolveType(rtParams)
	} else if i, ok := returnType.(*Interface); ok && i.ResolveType != nil {
		runtimeType = i.ResolveType(rtParams)
	} else {
		runtimeType = defaultResolveTypeFn(rtParams, returnType)
	}
	if err := invariantf(runtimeType != nil,
		`Abstract type %v must resolve to an Object type at runtime `+
			`for field %v.%v with value "%v", received "%v".`,
		returnType, info.ParentType, info.FieldName, result, runtimeType,
	); err != nil {
		panic(err)
	}
	if !eCtx.Schema.IsPossibleType(returnType, runtimeType) {
		panic(gqlerrors.NewFormattedError(
			fmt.Sprintf(`Runtime Object type "%v" is not a possible type for "%v".`, runtimeType, returnType),
		))
	}
	// Plan (and cache) the concrete type's sub-selection on first use.
	// Abstract alternatives are planned lazily so that only the types a
	// request actually resolves are ever planned — see
	// planMergedFieldChildren.
	if eCtx.plan != nil {
		if sub := eCtx.plan.abstractAlternative(fp, runtimeType); sub != nil {
			return executePlannedSelection(eCtx, sub, result, runtimeType)
		}
	}
	// The concrete type contributes no selectable fields (e.g. only
	// inline fragments on other types matched). Surface an empty object.
	return map[string]interface{}{}
}

// ExecutePlanAppend runs a planned operation, appending the GraphQL
// HTTP-spec response body (`{"data":<data>}` or
// `{"data":<data>,"errors":[...]}`) to dst and returning the
// extended slice. The second return is spec-level errors that
// occurred before data assembly began (e.g. variable coercion
// failures); when non-empty the caller decides whether to surface
// them as a separate envelope or fold them into a fresh response.
// Field-level errors are written into the response bytes via the
// `errors` array.
//
// Thunk concurrency is preserved: every resolver in a selection set is
// called — each starting its goroutine — before any thunk is awaited,
// which is the same ordering ExecutePlan's breadth-first dethunk pass
// provides. Mutations run serially in source order regardless, which
// matches the spec's mutation ordering.
func ExecutePlanAppend(plan *Plan, p ExecuteParams, dst []byte) ([]byte, []gqlerrors.FormattedError) {
	if plan == nil {
		return dst, gqlerrors.FormatErrors(errors.New("graphql: ExecutePlanAppend: plan is nil"))
	}
	ctx := p.Context
	if ctx == nil {
		ctx = context.Background()
	}

	execSchema := *plan.schema
	variableValues, err := getVariableValues(execSchema, plan.operation.GetVariableDefinitions(), p.Args)
	if err != nil {
		return dst, gqlerrors.FormatErrors(err)
	}

	eCtx := &executionContext{
		Schema:         execSchema,
		Fragments:      plan.fragments,
		Root:           p.Root,
		Operation:      plan.operation,
		VariableValues: variableValues,
		Context:        ctx,
		poolArgs:       !p.RetainArgs,
		plan:           plan,
	}

	dst = append(dst, `{"data":`...)
	dataStart := len(dst)
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Unabsorbed bubble from a non-null root field: data
				// becomes null per the spec. The error has already been
				// recorded into eCtx.Errors by the inner handlers (or
				// will be after this absorb).
				dst = dst[:dataStart]
				if e, ok := r.(error); ok {
					eCtx.Errors = append(eCtx.Errors, gqlerrors.FormatError(e))
				} else {
					eCtx.Errors = append(eCtx.Errors, gqlerrors.FormatError(fmt.Errorf("%v", r)))
				}
				dst = append(dst, "null"...)
			}
		}()
		dst = writePlannedSelection(eCtx, plan.root, p.Root, plan.rootType, dst)
	}()

	if len(eCtx.Errors) > 0 {
		dst = append(dst, `,"errors":`...)
		errs, marshalErr := json.Marshal(eCtx.Errors)
		if marshalErr != nil {
			dst = append(dst, "[]"...)
		} else {
			dst = append(dst, errs...)
		}
	}
	dst = append(dst, '}')
	return dst, nil
}

// writeCompleteValueCatchingError mirrors completePlannedValueCatchingError
// for append-mode. On panic it rolls dst back to entry length and
// forwards via handleFieldError; nullable returnType triggers a
// `null` emission here (and an error recorded), NonNull returnType
// re-panics for the parent to absorb.
//
// pathEntry is the eCtx.pathBuf depth captured before any push that the
// caller wants this absorber to clean up (e.g. the per-item index
// pushed by writeCompleteListValue). Pass -1 from sites that did not
// push their own path key; the field-level pop is then left to
// writePlannedField's recoverPlannedField.
func writeCompleteValueCatchingError(eCtx *executionContext, returnType Type, fp *fieldPlan, info ResolveInfo, result interface{}, dst []byte, pathEntry int) (out []byte) {
	out = dst
	entryLen := len(dst)
	defer recoverCompleteValue(&out, entryLen, fp, returnType, eCtx, pathEntry)
	out = writeCompleteValue(eCtx, returnType, fp, info, result, out, false)
	return out
}

// recoverPlannedField absorbs a panic from writePlannedField's
// resolver / completion path. Rolls *out back to keyStart, forwards
// the panic value to handleFieldError, and (for nullable returnType
// only — NonNull paths re-panic from handleFieldError) emits
// `"responseKey":null`. Lifted out of a closure so the defer record
// can be open-coded onto the stack.
//
// Pops the pathBuf depth-stack to pathEntry on the normal-return path
// (r == nil). On the panic path, the pop is deferred so errorPathArray
// can read the full path before it's truncated.
func recoverPlannedField(out *[]byte, keyStart int, fp *fieldPlan, eCtx *executionContext, pathEntry int) {
	r := recover()
	if r == nil {
		eCtx.pathBuf = eCtx.pathBuf[:pathEntry]
		return
	}
	defer func() { eCtx.pathBuf = eCtx.pathBuf[:pathEntry] }()
	*out = (*out)[:keyStart]
	handleFieldError(r, FieldASTsToNodeASTs(fp.fieldASTs), fp.returnType, eCtx)
	*out = append(*out, fp.responseKeyJSON...)
	*out = append(*out, "null"...)
}

// recoverCompleteValue is the writeCompleteValueCatchingError
// counterpart to recoverPlannedField: rolls *out back to entryLen,
// records the error (or re-panics for NonNull), and emits `null` at
// the absorption point. Named-function defer keeps the record on
// the stack.
//
// Pops the pathBuf depth-stack to pathEntry on the normal-return path
// (r == nil). On the panic path, the pop is deferred so errorPathArray
// can read the full path before it's truncated.
func recoverCompleteValue(out *[]byte, entryLen int, fp *fieldPlan, returnType Type, eCtx *executionContext, pathEntry int) {
	r := recover()
	if r == nil {
		if pathEntry >= 0 {
			eCtx.pathBuf = eCtx.pathBuf[:pathEntry]
		}
		return
	}
	if pathEntry >= 0 {
		defer func() { eCtx.pathBuf = eCtx.pathBuf[:pathEntry] }()
	}
	*out = (*out)[:entryLen]
	handleFieldError(r, FieldASTsToNodeASTs(fp.fieldASTs), returnType, eCtx)
	*out = append(*out, "null"...)
}

// writeCompleteValue mirrors completePlannedValue for append-mode.
// nonNull is true when the immediate wrapper was a *NonNull (set
// when we strip a NonNull layer and recurse). Leaf writes use the
// flag to distinguish "Serialize returned nil under NonNull"
// (panic) from "Serialize returned nil under nullable" (emit
// "null").
func writeCompleteValue(eCtx *executionContext, returnType Type, fp *fieldPlan, info ResolveInfo, result interface{}, dst []byte, nonNull bool) []byte {
	rv := reflect.ValueOf(result)
	if rv.IsValid() && rv.Kind() == reflect.Func {
		propertyFn, ok := result.(func() (interface{}, error))
		if !ok {
			err := gqlerrors.NewFormattedError("Error resolving func. Expected `func() (interface{}, error)` signature")
			panic(gqlerrors.FormatError(err))
		}
		fnResult, fnErr := propertyFn()
		if fnErr != nil {
			panic(gqlerrors.FormatError(fnErr))
		}
		result = fnResult
	}

	if rt, ok := returnType.(*NonNull); ok {
		if isNullish(result) {
			err := NewLocatedErrorWithPath(
				fmt.Sprintf("Cannot return null for non-nullable field %v.%v.", info.ParentType, info.FieldName),
				FieldASTsToNodeASTs(fp.fieldASTs),
				eCtx.errorPathArray(),
			)
			panic(gqlerrors.FormatError(err))
		}
		return writeCompleteValue(eCtx, rt.OfType, fp, info, result, dst, true)
	}

	if isNullish(result) {
		return append(dst, "null"...)
	}

	switch rt := returnType.(type) {
	case *List:
		return writeCompleteListValue(eCtx, rt, fp, info, result, dst)
	case *Scalar:
		return writeCompleteLeafValue(eCtx, rt, fp, info, result, dst, nonNull)
	case *Enum:
		return writeCompleteLeafValue(eCtx, rt, fp, info, result, dst, nonNull)
	case *Object:
		return writeCompleteObjectValue(eCtx, rt, fp, info, result, dst)
	case *Interface:
		return writeCompleteAbstractValue(eCtx, rt, fp, info, result, dst)
	case *Union:
		return writeCompleteAbstractValue(eCtx, rt, fp, info, result, dst)
	}
	if err := invariantf(false, `Cannot complete value of unexpected type "%v."`, returnType); err != nil {
		panic(gqlerrors.FormatError(err))
	}
	return dst
}

// writeCompleteLeafValue serializes result via returnType.Serialize
// and emits the JSON form via fp.leafEmitter (or json.Marshal
// fallback when no emitter is registered). Post-Serialize nil is
// the spec's "Serialize failed" case: under NonNull it panics, else
// "null" is emitted.
//
// Built-in scalars take a fast path that bypasses Serialize entirely
// when the resolver returned the canonical Go type (string for
// String/ID, int for Int, float64 for Float, bool for Boolean). The
// generic Serialize+leafEmitter path would otherwise coerce the same
// value twice (e.g. coerceString runs fmt.Sprintf on a plain string,
// allocating a new string identical to the input). On a type-assertion
// miss or a value the canonical path would null-out (Int overflow,
// NaN/Inf float), control falls through to the generic path so the
// existing spec-compliant behavior is preserved.
func writeCompleteLeafValue(eCtx *executionContext, returnType Leaf, fp *fieldPlan, info ResolveInfo, result interface{}, dst []byte, nonNull bool) []byte {
	switch t := fp.leafType.(type) {
	case *Scalar:
		switch t {
		case String, ID:
			if s, ok := result.(string); ok {
				return appendJSONString(dst, s)
			}
		case Boolean:
			if b, ok := result.(bool); ok {
				if b {
					return append(dst, "true"...)
				}
				return append(dst, "false"...)
			}
		case Int:
			if n, ok := result.(int); ok && n >= math.MinInt32 && n <= math.MaxInt32 {
				return strconv.AppendInt(dst, int64(n), 10)
			}
		case Float:
			if f, ok := result.(float64); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
				abs := math.Abs(f)
				fmtByte := byte('f')
				if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
					fmtByte = 'e'
				}
				return strconv.AppendFloat(dst, f, fmtByte, -1, 64)
			}
		}
	case *Enum:
		// Enum fast path: plan-time pre-encoded JSON bytes per value.
		// Skips Enum.Serialize (reflect + valuesLookup) and
		// appendJSONString (no escaping needed for enum names by spec).
		// Falls through to the Serialize path for ptr-to-value (handled
		// by Serialize's reflect-Indirect) and for unknown values
		// (Serialize returns nil → null or non-null error below).
		if pre, ok := t.valueToJSON[result]; ok {
			return append(dst, pre...)
		}
	}

	serialized := returnType.Serialize(result)
	if isNullish(serialized) {
		if nonNull {
			err := NewLocatedErrorWithPath(
				fmt.Sprintf("Cannot return null for non-nullable field %v.%v.", info.ParentType, info.FieldName),
				FieldASTsToNodeASTs(fp.fieldASTs),
				eCtx.errorPathArray(),
			)
			panic(gqlerrors.FormatError(err))
		}
		return append(dst, "null"...)
	}
	if fp.leafEmitter != nil {
		return fp.leafEmitter(dst, serialized)
	}
	bs, mErr := json.Marshal(serialized)
	if mErr != nil {
		if nonNull {
			err := NewLocatedErrorWithPath(
				fmt.Sprintf("Cannot return null for non-nullable field %v.%v.", info.ParentType, info.FieldName),
				FieldASTsToNodeASTs(fp.fieldASTs),
				eCtx.errorPathArray(),
			)
			panic(gqlerrors.FormatError(err))
		}
		return append(dst, "null"...)
	}
	return append(dst, bs...)
}

// writeCompleteListValue mirrors completePlannedListValue. Each item
// completes via writeCompleteValueCatchingError, so nullable items
// can absorb item-level non-null violations while non-null items
// propagate the bubble up to the list's containing field.
//
// When fp.listElemType is known and is a primitive (string, int,
// bool, float64), the walker iterates with typed accessors to avoid
// reflect.Value.Interface() boxing. Unknown or composite element
// types fall back to the generic reflect path.
func writeCompleteListValue(eCtx *executionContext, returnType *List, fp *fieldPlan, info ResolveInfo, result interface{}, dst []byte) []byte {
	resultVal := reflect.ValueOf(result)
	if resultVal.Kind() == reflect.Ptr {
		resultVal = resultVal.Elem()
	}
	parentTypeName := ""
	if info.ParentType != nil {
		parentTypeName = info.ParentType.Name()
	}
	if err := invariantf(
		resultVal.IsValid() && isIterable(result),
		"User Error: expected iterable, but did not find one "+
			"for field %v.%v.", parentTypeName, info.FieldName); err != nil {
		panic(gqlerrors.FormatError(err))
	}
	itemType := returnType.OfType
	n := resultVal.Len()

	// Capture element type on first execution for future calls.
	if fp.listElemType == nil && n > 0 {
		fp.listElemType = resultVal.Type().Elem()
	}

	// Typed fast paths for primitive element types.
	if fp.listElemType != nil {
		switch fp.listElemType.Kind() {
		case reflect.String:
			dst = append(dst, '[')
			for i := 0; i < n; i++ {
				if i > 0 {
					dst = append(dst, ',')
				}
				val := resultVal.Index(i).String()
				itemDepth := len(eCtx.pathBuf)
				eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{idx: i})
				dst = writeCompleteValueCatchingError(eCtx, itemType, fp, info, val, dst, itemDepth)
			}
			return append(dst, ']')
		case reflect.Int:
			dst = append(dst, '[')
			for i := 0; i < n; i++ {
				if i > 0 {
					dst = append(dst, ',')
				}
				val := int(resultVal.Index(i).Int())
				itemDepth := len(eCtx.pathBuf)
				eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{idx: i})
				dst = writeCompleteValueCatchingError(eCtx, itemType, fp, info, val, dst, itemDepth)
			}
			return append(dst, ']')
		case reflect.Bool:
			dst = append(dst, '[')
			for i := 0; i < n; i++ {
				if i > 0 {
					dst = append(dst, ',')
				}
				val := resultVal.Index(i).Bool()
				itemDepth := len(eCtx.pathBuf)
				eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{idx: i})
				dst = writeCompleteValueCatchingError(eCtx, itemType, fp, info, val, dst, itemDepth)
			}
			return append(dst, ']')
		case reflect.Float64:
			dst = append(dst, '[')
			for i := 0; i < n; i++ {
				if i > 0 {
					dst = append(dst, ',')
				}
				val := resultVal.Index(i).Float()
				itemDepth := len(eCtx.pathBuf)
				eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{idx: i})
				dst = writeCompleteValueCatchingError(eCtx, itemType, fp, info, val, dst, itemDepth)
			}
			return append(dst, ']')
		}
	}

	// Generic fallback: unknown element type or composite (struct, etc.).
	dst = append(dst, '[')
	for i := 0; i < n; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		val := resultVal.Index(i).Interface()
		itemDepth := len(eCtx.pathBuf)
		eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{idx: i})
		dst = writeCompleteValueCatchingError(eCtx, itemType, fp, info, val, dst, itemDepth)
	}
	return append(dst, ']')
}

func writeCompleteObjectValue(eCtx *executionContext, returnType *Object, fp *fieldPlan, info ResolveInfo, result interface{}, dst []byte) []byte {
	if returnType.IsTypeOf != nil {
		p := IsTypeOfParams{Value: result, Info: info, Context: eCtx.Context}
		if !returnType.IsTypeOf(p) {
			panic(gqlerrors.NewFormattedError(
				fmt.Sprintf(`Expected value of type "%v" but got: %T.`, returnType, result),
			))
		}
	}
	if fp.sub != nil {
		return writePlannedSelection(eCtx, fp.sub, result, returnType, dst)
	}
	return append(dst, '{', '}')
}

func writeCompleteAbstractValue(eCtx *executionContext, returnType Abstract, fp *fieldPlan, info ResolveInfo, result interface{}, dst []byte) []byte {
	var runtimeType *Object
	rtParams := ResolveTypeParams{Value: result, Info: info, Context: eCtx.Context}
	if u, ok := returnType.(*Union); ok && u.ResolveType != nil {
		runtimeType = u.ResolveType(rtParams)
	} else if i, ok := returnType.(*Interface); ok && i.ResolveType != nil {
		runtimeType = i.ResolveType(rtParams)
	} else {
		runtimeType = defaultResolveTypeFn(rtParams, returnType)
	}
	if err := invariantf(runtimeType != nil,
		`Abstract type %v must resolve to an Object type at runtime `+
			`for field %v.%v with value "%v", received "%v".`,
		returnType, info.ParentType, info.FieldName, result, runtimeType,
	); err != nil {
		panic(err)
	}
	if !eCtx.Schema.IsPossibleType(returnType, runtimeType) {
		panic(gqlerrors.NewFormattedError(
			fmt.Sprintf(`Runtime Object type "%v" is not a possible type for "%v".`, runtimeType, returnType),
		))
	}
	// Plan (and cache) the concrete type's sub-selection on first use —
	// same lazy path as the map-tree walker, see planMergedFieldChildren.
	if eCtx.plan != nil {
		if sub := eCtx.plan.abstractAlternative(fp, runtimeType); sub != nil {
			return writePlannedSelection(eCtx, sub, result, runtimeType, dst)
		}
	}
	return append(dst, '{', '}')
}
