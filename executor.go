package graphql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/IodeSystems/graphql-go/v2/gqlerrors"
	"github.com/IodeSystems/graphql-go/v2/language/ast"
)

type ExecuteParams struct {
	Schema        Schema
	Root          interface{}
	AST           *ast.Document
	OperationName string
	Args          map[string]interface{}

	// Context may be provided to pass application-specific per-request
	// information to resolve functions.
	Context context.Context

	// RetainArgs disables the args-map pool that the executor uses
	// to recycle ResolveParams.Args across resolver calls. The
	// default — false — acquires the args map from a sync.Pool and
	// returns it after the resolver finishes; resolvers must treat
	// p.Args as borrowed (read freely, do not retain references
	// past the call, do not mutate-then-return). Set true if a
	// resolver stashes p.Args in a struct field, channel, or
	// goroutine that outlives the resolver call. Applies to both
	// ExecutePlan and ExecutePlanAppend.
	RetainArgs bool
}

// Execute runs an operation against a schema. Behavior is unchanged
// from prior releases: it now plans + executes via PlanQuery /
// ExecutePlan internally, but builds a fresh plan per call. Callers
// that issue the same query repeatedly should hold onto the *Plan
// returned by PlanQuery and pass it to ExecutePlan to skip the
// per-call planning work.
func Execute(p ExecuteParams) (result *Result) {
	plan, err := PlanQuery(&p.Schema, p.AST, p.OperationName)
	if err != nil {
		return &Result{Errors: gqlerrors.FormatErrors(err)}
	}
	return ExecutePlan(plan, p)
}

// pathEntry holds one segment of the lazy response path.
// key is non-empty for field names; idx is valid (>= 0) for list indices.
// Stored inline in pathBuf ([]pathEntry) to avoid per-element interface{}
// boxing — the struct values live directly in the slice's backing array.
type pathEntry struct {
	key string // field name, empty for list index
	idx int    // list index, -1 for field name
}

type buildExecutionCtxParams struct {
	Schema        Schema
	Root          interface{}
	AST           *ast.Document
	OperationName string
	Args          map[string]interface{}
	Result        *Result
	Context       context.Context
}

type executionContext struct {
	Schema         Schema
	Fragments      map[string]ast.Definition
	Root           interface{}
	Operation      ast.Definition
	VariableValues map[string]interface{}
	Errors         []gqlerrors.FormattedError
	Context        context.Context

	// plan is set on the ExecutePlan / ExecutePlanAppend paths; it lets
	// abstract fields plan their concrete-type sub-selections lazily at
	// execute time.
	plan *Plan

	// pathBuf is the depth-stack for the response path. Entries are
	// stored inline ([]pathEntry) to avoid per-element interface{}
	// boxing. Both walkers (ExecutePlan and ExecutePlanAppend) push
	// on field/list-element entry and pop on exit. errorPathArray
	// reconstructs the error envelope's `path` from this buffer.
	pathBuf []pathEntry

	// retainArgs mirrors !ExecuteParams.RetainArgs. When true (the
	// default), the executor recycles per-resolver args maps through
	// argsMapPool. When false, every resolver call allocates a fresh
	// args map — required when resolvers retain p.Args references
	// past the resolve call (struct fields, channels, goroutines).
	poolArgs bool

	// thunkCount is incremented every time a resolver-returned func
	// (a thunk) is wrapped by completePlannedValue. The dethunk pass
	// only needs to run when this is > 0; for thunk-free schemas
	// (the dominant shape) we skip the entire tree walk.
	thunkCount int
}

// argsMapPool recycles per-resolver argument maps. Acquired before
// each resolver call (via acquireArgsMap), released after (via
// releaseArgsMap). The pool persists across requests; resolvers must
// treat p.Args as borrowed (read freely, do not retain references
// past the resolver return). Adopters that retain set
// ExecuteParams.RetainArgs to opt out.
var argsMapPool = sync.Pool{
	New: func() interface{} {
		return make(map[string]interface{}, 4)
	},
}

// emptyArgsMap is a shared, never-mutated zero-length map used as
// p.Args for resolvers on arg-less fields. Resolvers must treat
// p.Args as read-only (documented contract), so a singleton is safe
// even on the no-pool path: there are no entries to read or mutate.
// Avoids one map allocation per arg-less resolver call.
var emptyArgsMap = map[string]interface{}{}

// acquireArgsMap returns a cleared args map suitable for one
// resolver call. Always returns a non-nil map; ResolveParams.Args is
// non-nil by historical contract.
func acquireArgsMap() map[string]interface{} {
	return argsMapPool.Get().(map[string]interface{})
}

// releaseArgsMap returns a no-longer-needed args map to the pool,
// clearing its keys first. Safe on a nil map (no-op). Must not be
// called when ExecuteParams.RetainArgs is set: the caller may have
// stashed the map past the resolver return.
func releaseArgsMap(m map[string]interface{}) {
	if m == nil {
		return
	}
	for k := range m {
		delete(m, k)
	}
	argsMapPool.Put(m)
}

// errorPathArray returns the response-path locator for an error
// envelope. Both walkers (ExecutePlan and ExecutePlanAppend) push to
// eCtx.pathBuf on field / list-element entry, so this is the single
// source of truth for `errors[].path`.
func (eCtx *executionContext) errorPathArray() []interface{} {
	if len(eCtx.pathBuf) == 0 {
		return nil
	}
	out := make([]interface{}, len(eCtx.pathBuf))
	for i, e := range eCtx.pathBuf {
		if e.key != "" {
			out[i] = e.key
		} else {
			out[i] = e.idx
		}
	}
	return out
}

func buildExecutionContext(p buildExecutionCtxParams) (*executionContext, error) {
	eCtx := &executionContext{}
	var operation *ast.OperationDefinition
	fragments := map[string]ast.Definition{}

	for _, definition := range p.AST.Definitions {
		switch definition := definition.(type) {
		case *ast.OperationDefinition:
			if (p.OperationName == "") && operation != nil {
				return nil, errors.New("Must provide operation name if query contains multiple operations.")
			}
			if p.OperationName == "" || definition.GetName() != nil && definition.GetName().Value == p.OperationName {
				operation = definition
			}
		case *ast.FragmentDefinition:
			key := ""
			if definition.GetName() != nil && definition.GetName().Value != "" {
				key = definition.GetName().Value
			}
			fragments[key] = definition
		default:
			return nil, fmt.Errorf("GraphQL cannot execute a request containing a %v", definition.GetKind())
		}
	}

	if operation == nil {
		if p.OperationName != "" {
			return nil, fmt.Errorf(`Unknown operation named "%v".`, p.OperationName)
		}
		return nil, fmt.Errorf(`Must provide an operation.`)
	}

	variableValues, err := getVariableValues(p.Schema, operation.GetVariableDefinitions(), p.Args)
	if err != nil {
		return nil, err
	}

	eCtx.Schema = p.Schema
	eCtx.Fragments = fragments
	eCtx.Root = p.Root
	eCtx.Operation = operation
	eCtx.VariableValues = variableValues
	eCtx.Context = p.Context
	return eCtx, nil
}

// Extracts the root type of the operation from the schema.
func getOperationRootType(schema Schema, operation ast.Definition) (*Object, error) {
	if operation == nil {
		return nil, errors.New("Can only execute queries, mutations and subscription")
	}

	switch operation.GetOperation() {
	case ast.OperationTypeQuery:
		return schema.QueryType(), nil
	case ast.OperationTypeMutation:
		mutationType := schema.MutationType()
		if mutationType == nil || mutationType.PrivateName == "" {
			return nil, gqlerrors.NewError(
				"Schema is not configured for mutations",
				[]ast.Node{operation},
				"",
				nil,
				[]int{},
				nil,
			)
		}
		return mutationType, nil
	case ast.OperationTypeSubscription:
		subscriptionType := schema.SubscriptionType()
		if subscriptionType == nil || subscriptionType.PrivateName == "" {
			return nil, gqlerrors.NewError(
				"Schema is not configured for subscriptions",
				[]ast.Node{operation},
				"",
				nil,
				[]int{},
				nil,
			)
		}
		return subscriptionType, nil
	default:
		return nil, gqlerrors.NewError(
			"Can only execute queries, mutations and subscription",
			[]ast.Node{operation},
			"",
			nil,
			[]int{},
			nil,
		)
	}
}

// dethunkQueue is a structure that allows us to execute a classic breadth-first traversal.
type dethunkQueue struct {
	DethunkFuncs []func()
}

func (d *dethunkQueue) push(f func()) {
	d.DethunkFuncs = append(d.DethunkFuncs, f)
}

func (d *dethunkQueue) shift() func() {
	f := d.DethunkFuncs[0]
	d.DethunkFuncs = d.DethunkFuncs[1:]
	return f
}

// dethunkWithBreadthFirstTraversal performs a breadth-first descent of the map, calling any thunks
// in the map values and replacing each thunk with that thunk's return value. This parallels
// the reference graphql-js implementation, which calls Promise.all on thunks at each depth (which
// is an implicit parallel descent).
func dethunkMapWithBreadthFirstTraversal(finalResults map[string]interface{}) {
	dethunkQueue := &dethunkQueue{DethunkFuncs: []func(){}}
	dethunkMapBreadthFirst(finalResults, dethunkQueue)
	for len(dethunkQueue.DethunkFuncs) > 0 {
		f := dethunkQueue.shift()
		f()
	}
}

func dethunkMapBreadthFirst(m map[string]interface{}, dethunkQueue *dethunkQueue) {
	for k, v := range m {
		if f, ok := v.(func() interface{}); ok {
			m[k] = f()
		}
		switch val := m[k].(type) {
		case map[string]interface{}:
			dethunkQueue.push(func() { dethunkMapBreadthFirst(val, dethunkQueue) })
		case []interface{}:
			dethunkQueue.push(func() { dethunkListBreadthFirst(val, dethunkQueue) })
		}
	}
}

func dethunkListBreadthFirst(list []interface{}, dethunkQueue *dethunkQueue) {
	for i, v := range list {
		if f, ok := v.(func() interface{}); ok {
			list[i] = f()
		}
		switch val := list[i].(type) {
		case map[string]interface{}:
			dethunkQueue.push(func() { dethunkMapBreadthFirst(val, dethunkQueue) })
		case []interface{}:
			dethunkQueue.push(func() { dethunkListBreadthFirst(val, dethunkQueue) })
		}
	}
}

// dethunkMapDepthFirst performs a serial descent of the map, calling any thunks
// in the map values and replacing each thunk with that thunk's return value. This is needed
// to conform to the graphql-js reference implementation, which requires serial (depth-first)
// implementations for mutation selects.
func dethunkMapDepthFirst(m map[string]interface{}) {
	for k, v := range m {
		if f, ok := v.(func() interface{}); ok {
			m[k] = f()
		}
		switch val := m[k].(type) {
		case map[string]interface{}:
			dethunkMapDepthFirst(val)
		case []interface{}:
			dethunkListDepthFirst(val)
		}
	}
}

func dethunkListDepthFirst(list []interface{}) {
	for i, v := range list {
		if f, ok := v.(func() interface{}); ok {
			list[i] = f()
		}
		switch val := list[i].(type) {
		case map[string]interface{}:
			dethunkMapDepthFirst(val)
		case []interface{}:
			dethunkListDepthFirst(val)
		}
	}
}

type collectFieldsParams struct {
	ExeContext           *executionContext
	RuntimeType          *Object // previously known as OperationType
	SelectionSet         *ast.SelectionSet
	Fields               map[string][]*ast.Field
	VisitedFragmentNames map[string]bool
}

// Given a selectionSet, adds all of the fields in that selection to
// the passed in map of fields, and returns it at the end.
// CollectFields requires the "runtime type" of an object. For a field which
// returns and Interface or Union type, the "runtime type" will be the actual
// Object type returned by that field.
func collectFields(p collectFieldsParams) (fields map[string][]*ast.Field) {
	// overlying SelectionSet & Fields to fields
	if p.SelectionSet == nil {
		return p.Fields
	}
	fields = p.Fields
	if fields == nil {
		fields = map[string][]*ast.Field{}
	}
	if p.VisitedFragmentNames == nil {
		p.VisitedFragmentNames = map[string]bool{}
	}
	for _, iSelection := range p.SelectionSet.Selections {
		switch selection := iSelection.(type) {
		case *ast.Field:
			if !shouldIncludeNode(p.ExeContext, selection.Directives) {
				continue
			}
			name := getFieldEntryKey(selection)
			if _, ok := fields[name]; !ok {
				fields[name] = []*ast.Field{}
			}
			fields[name] = append(fields[name], selection)
		case *ast.InlineFragment:

			if !shouldIncludeNode(p.ExeContext, selection.Directives) ||
				!doesFragmentConditionMatch(p.ExeContext, selection, p.RuntimeType) {
				continue
			}
			innerParams := collectFieldsParams{
				ExeContext:           p.ExeContext,
				RuntimeType:          p.RuntimeType,
				SelectionSet:         selection.SelectionSet,
				Fields:               fields,
				VisitedFragmentNames: p.VisitedFragmentNames,
			}
			collectFields(innerParams)
		case *ast.FragmentSpread:
			fragName := ""
			if selection.Name != nil {
				fragName = selection.Name.Value
			}
			if visited, ok := p.VisitedFragmentNames[fragName]; (ok && visited) ||
				!shouldIncludeNode(p.ExeContext, selection.Directives) {
				continue
			}
			p.VisitedFragmentNames[fragName] = true
			fragment, hasFragment := p.ExeContext.Fragments[fragName]
			if !hasFragment {
				continue
			}

			if fragment, ok := fragment.(*ast.FragmentDefinition); ok {
				if !doesFragmentConditionMatch(p.ExeContext, fragment, p.RuntimeType) {
					continue
				}
				innerParams := collectFieldsParams{
					ExeContext:           p.ExeContext,
					RuntimeType:          p.RuntimeType,
					SelectionSet:         fragment.GetSelectionSet(),
					Fields:               fields,
					VisitedFragmentNames: p.VisitedFragmentNames,
				}
				collectFields(innerParams)
			}
		}
	}
	return fields
}

// Determines if a field should be included based on the @include and @skip
// directives, where @skip has higher precedence than @include.
func shouldIncludeNode(eCtx *executionContext, directives []*ast.Directive) bool {
	var (
		skipAST, includeAST *ast.Directive
		argValues           map[string]interface{}
	)
	for _, directive := range directives {
		if directive == nil || directive.Name == nil {
			continue
		}
		switch directive.Name.Value {
		case SkipDirective.Name:
			skipAST = directive
		case IncludeDirective.Name:
			includeAST = directive
		}
	}
	// precedence: skipAST > includeAST
	if skipAST != nil {
		argValues = getArgumentValues(SkipDirective.Args, skipAST.Arguments, eCtx.VariableValues)
		if skipIf, ok := argValues["if"].(bool); ok && skipIf {
			return false // excluded selectionSet's fields
		}
	}
	if includeAST != nil {
		argValues = getArgumentValues(IncludeDirective.Args, includeAST.Arguments, eCtx.VariableValues)
		if includeIf, ok := argValues["if"].(bool); ok && !includeIf {
			return false // excluded selectionSet's fields
		}
	}
	return true
}

// Determines if a fragment is applicable to the given type.
func doesFragmentConditionMatch(eCtx *executionContext, fragment ast.Node, ttype *Object) bool {

	switch fragment := fragment.(type) {
	case *ast.FragmentDefinition:
		typeConditionAST := fragment.TypeCondition
		if typeConditionAST == nil {
			return true
		}
		conditionalType, err := typeFromAST(eCtx.Schema, typeConditionAST)
		if err != nil || conditionalType == nil {
			return false
		}
		if conditionalType == ttype {
			return true
		}
		if conditionalType.Name() == ttype.Name() {
			return true
		}
		if conditionalType, ok := conditionalType.(*Interface); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
		if conditionalType, ok := conditionalType.(*Union); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
	case *ast.InlineFragment:
		typeConditionAST := fragment.TypeCondition
		if typeConditionAST == nil {
			return true
		}
		conditionalType, err := typeFromAST(eCtx.Schema, typeConditionAST)
		if err != nil || conditionalType == nil {
			return false
		}
		if conditionalType == ttype {
			return true
		}
		if conditionalType.Name() == ttype.Name() {
			return true
		}
		if conditionalType, ok := conditionalType.(*Interface); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
		if conditionalType, ok := conditionalType.(*Union); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
	}

	return false
}

// Implements the logic to compute the key of a given field’s entry
func getFieldEntryKey(node *ast.Field) string {

	if node.Alias != nil && node.Alias.Value != "" {
		return node.Alias.Value
	}
	if node.Name != nil && node.Name.Value != "" {
		return node.Name.Value
	}
	return ""
}

func handleFieldError(r interface{}, fieldNodes []ast.Node, returnType Output, eCtx *executionContext) {
	err := NewLocatedErrorWithPath(r, fieldNodes, eCtx.errorPathArray())
	// send panic upstream
	if _, ok := returnType.(*NonNull); ok {
		panic(err)
	}
	eCtx.Errors = append(eCtx.Errors, gqlerrors.FormatError(err))
}

// completeLeafValue complete a leaf value (Scalar / Enum) by serializing to a valid value, returning nil if serialization is not possible.
func completeLeafValue(returnType Leaf, result interface{}) interface{} {
	serializedResult := returnType.Serialize(result)
	if isNullish(serializedResult) {
		return nil
	}
	return serializedResult
}

// defaultResolveTypeFn If a resolveType function is not given, then a default resolve behavior is
// used which tests each possible type for the abstract type by calling
// isTypeOf for the object being coerced, returning the first type that matches.
func defaultResolveTypeFn(p ResolveTypeParams, abstractType Abstract) *Object {
	possibleTypes := p.Info.Schema.PossibleTypes(abstractType)
	for _, possibleType := range possibleTypes {
		if possibleType.IsTypeOf == nil {
			continue
		}
		isTypeOfParams := IsTypeOfParams{
			Value:   p.Value,
			Info:    p.Info,
			Context: p.Context,
		}
		if res := possibleType.IsTypeOf(isTypeOfParams); res {
			return possibleType
		}
	}
	return nil
}

// FieldResolver is used in DefaultResolveFn when the the source value implements this interface.
type FieldResolver interface {
	// Resolve resolves the value for the given ResolveParams. It has the same semantics as FieldResolveFn.
	Resolve(p ResolveParams) (interface{}, error)
}

// defaultResolveCache memoizes the struct-field walk done by
// DefaultResolveFn on its first hit for a given (struct type, GraphQL
// field name) pair. Value is the index path to the matching field
// (see resolveDefaultStructField), or nil if no field matched. Skips
// per-call NumField iteration, per-field strings.EqualFold + Tag.Get +
// strings.Split, and the per-field closure allocation in the original
// loop.
//
// reflect.Type identity is stable for a given Go type, so the cache
// never invalidates. Bounded in practice by (distinct source struct
// types × distinct GraphQL field names exercised against them).
var defaultResolveCache sync.Map

type defaultResolveKey struct {
	t reflect.Type
	n string
}

// resolveDefaultStructField runs DefaultResolveFn's struct-field
// matching rules (case-insensitive Go field name, then `json` tag,
// then `graphql` tag — first match wins, scanning in declaration
// order) and returns the index path to the match, suitable for
// fieldByIndexPath. nil means no field on sourceType matches
// fieldName.
//
// Fields promoted from embedded structs are matched using Go's own
// promotion rules: shallower depths win outright, and a tie between
// two branches at the same depth is ambiguous and matches nothing —
// the same answer a plain sourceVal.FieldName selector would give.
// Searching breadth-first is what makes that work; a depth-first walk
// would let a deeply embedded field shadow a shallower one purely by
// declaration order.
func resolveDefaultStructField(sourceType reflect.Type, fieldName string) []int {
	key := defaultResolveKey{t: sourceType, n: fieldName}
	if cached, ok := defaultResolveCache.Load(key); ok {
		path, _ := cached.([]int)
		return path
	}
	path := searchDefaultStructField(sourceType, fieldName)
	defaultResolveCache.Store(key, path)
	return path
}

// structLevel is one breadth-first frontier entry: a struct type and
// the index path that reaches it from the root struct.
type structLevel struct {
	typ  reflect.Type
	path []int
}

// searchDefaultStructField is resolveDefaultStructField's uncached
// body. Kept separate so the cache stores whatever this returns,
// including a nil "no match".
func searchDefaultStructField(sourceType reflect.Type, fieldName string) []int {
	level := []structLevel{{typ: sourceType}}
	// Recursive embeddings (type T struct{ *T }) would otherwise loop
	// forever; a type only needs visiting at its shallowest depth.
	visited := map[reflect.Type]bool{sourceType: true}

	for len(level) > 0 {
		var match []int
		matches := 0
		var next []structLevel

		for _, cur := range level {
			found := -1
			for i := 0; i < cur.typ.NumField(); i++ {
				typeField := cur.typ.Field(i)
				if strings.EqualFold(typeField.Name, fieldName) ||
					tagFirstSegmentEquals(typeField.Tag.Get("json"), fieldName) ||
					tagFirstSegmentEquals(typeField.Tag.Get("graphql"), fieldName) {
					found = i
					break
				}
				if typeField.Anonymous {
					et := typeField.Type
					if et.Kind() == reflect.Ptr {
						et = et.Elem()
					}
					if et.Kind() == reflect.Struct && !visited[et] {
						next = append(next, structLevel{
							typ:  et,
							path: appendIndex(cur.path, i),
						})
					}
				}
			}
			if found >= 0 {
				matches++
				if matches > 1 {
					// Ambiguous at this depth: Go would refuse to
					// promote either, so neither is a match.
					return nil
				}
				match = appendIndex(cur.path, found)
			}
		}
		if matches == 1 {
			return match
		}
		for _, n := range next {
			visited[n.typ] = true
		}
		level = next
	}
	return nil
}

// appendIndex returns path + [i] without aliasing path's backing array
// — sibling frontier entries share a prefix and must not overwrite
// each other.
func appendIndex(path []int, i int) []int {
	out := make([]int, len(path)+1)
	copy(out, path)
	out[len(path)] = i
	return out
}

// fieldByIndexPath walks an index path produced by
// resolveDefaultStructField. Unlike reflect.Value.FieldByIndex it does
// not panic when the path crosses a nil embedded pointer; it returns
// the zero Value, which the caller reports as a null field.
func fieldByIndexPath(v reflect.Value, path []int) reflect.Value {
	for n, i := range path {
		if n > 0 {
			if v.Kind() == reflect.Ptr {
				if v.IsNil() {
					return reflect.Value{}
				}
				v = v.Elem()
			}
		}
		v = v.Field(i)
	}
	return v
}

// tagFirstSegmentEquals reports whether the first comma-separated
// segment of tag equals want. Allocation-free vs. strings.Split.
func tagFirstSegmentEquals(tag, want string) bool {
	if tag == "" {
		return false
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		return tag[:comma] == want
	}
	return tag == want
}

// DefaultResolveFn If a resolve function is not given, then a default resolve behavior is used
// which takes the property of the source object of the same name as the field
// and returns it as the result, or if it's a function, returns the result
// of calling that function.
func DefaultResolveFn(p ResolveParams) (interface{}, error) {
	sourceVal := reflect.ValueOf(p.Source)
	// Check if value implements 'Resolver' interface
	if resolver, ok := sourceVal.Interface().(FieldResolver); ok {
		return resolver.Resolve(p)
	}

	// try to resolve p.Source as a struct
	if sourceVal.IsValid() && sourceVal.Type().Kind() == reflect.Ptr {
		sourceVal = sourceVal.Elem()
	}
	if !sourceVal.IsValid() {
		return nil, nil
	}

	if sourceVal.Type().Kind() == reflect.Struct {
		path := resolveDefaultStructField(sourceVal.Type(), p.Info.FieldName)
		if path == nil {
			return nil, nil
		}
		field := fieldByIndexPath(sourceVal, path)
		if !field.IsValid() {
			// Path crossed a nil embedded pointer.
			return nil, nil
		}
		return field.Interface(), nil
	}

	// try p.Source as a map[string]interface
	if sourceMap, ok := p.Source.(map[string]interface{}); ok {
		property := sourceMap[p.Info.FieldName]
		val := reflect.ValueOf(property)
		if val.IsValid() && val.Type().Kind() == reflect.Func {
			// try type casting the func to the most basic func signature
			// for more complex signatures, user have to define ResolveFn
			if propertyFn, ok := property.(func() interface{}); ok {
				return propertyFn(), nil
			}
		}
		return property, nil
	}

	// Try accessing as map via reflection
	if r := reflect.ValueOf(p.Source); r.Kind() == reflect.Map && r.Type().Key().Kind() == reflect.String {
		// Convert the field name to the map's key type to support custom
		// string-based key types (e.g., type MyKey string).
		key := reflect.ValueOf(p.Info.FieldName).Convert(r.Type().Key())
		val := r.MapIndex(key)
		if val.IsValid() {
			property := val.Interface()
			if val.Type().Kind() == reflect.Func {
				// try type casting the func to the most basic func signature
				// for more complex signatures, user have to define ResolveFn
				if propertyFn, ok := property.(func() interface{}); ok {
					return propertyFn(), nil
				}
			}
			return property, nil
		}
	}

	// last resort, return nil
	return nil, nil
}

// This method looks up the field on the given type definition.
// It has special casing for the two introspection fields, __schema
// and __typename. __typename is special because it can always be
// queried as a field, even in situations where no other fields
// are allowed, like on a Union. __schema could get automatically
// added to the query type, but that would require mutating type
// definitions, which would cause issues.
func getFieldDef(schema Schema, parentType *Object, fieldName string) *FieldDefinition {

	if parentType == nil {
		return nil
	}

	if fieldName == SchemaMetaFieldDef.Name &&
		schema.QueryType() == parentType {
		return SchemaMetaFieldDef
	}
	if fieldName == TypeMetaFieldDef.Name &&
		schema.QueryType() == parentType {
		return TypeMetaFieldDef
	}
	if fieldName == TypeNameMetaFieldDef.Name {
		return TypeNameMetaFieldDef
	}
	return parentType.Fields()[fieldName]
}
