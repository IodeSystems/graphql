package graphql

import (
	"fmt"
	"sync"
)

// Two-phase selection writing.
//
// The append walker used to resolve and write one field at a time. For
// a thunked resolver — one that starts a goroutine and returns
// func() (interface{}, error) that waits on it — that awaits each
// thunk immediately after its own resolver returns, so goroutines meant
// to overlap run end to end. Wall time is sum(thunk), not max(thunk).
//
// The map-tree executor avoids this not by running anything in parallel
// (dethunkMapWithBreadthFirstTraversal calls thunks serially) but by
// ordering: every resolver at a level is called, each starting its
// goroutine, before any thunk is awaited.
//
// This does the same for bytes. Phase 1 calls every resolver in the
// selection set and keeps the results, thunks un-awaited. Phase 2 walks
// the same fields in plan order writing JSON, awaiting each thunk as it
// arrives. Parallel fetchers, linear writer, no intermediate map.
//
// Field order stays deterministic (selectionPlan.fields is source-
// ordered) and null bubble-up is unchanged: a late thunk failing under
// NonNull rolls the enclosing block back to its saved offset, dropping
// siblings already written, exactly as before.

// pendingField carries one field across the phase boundary.
//
// Deliberately small. ResolveInfo is 216 bytes (it holds a Schema by
// value), and storing one per field made a 100-field selection set
// carry 30KB of scratch — measured as +458% B/op before this was
// pointered. Phase 2 rebuilds the info from the plan instead, which is
// a handful of field copies; the pointer is populated only on the
// extensions path, where the hook may have mutated the struct.
type pendingField struct {
	fp     *fieldPlan
	info   *ResolveInfo
	result interface{}
	// err is a resolver error held until phase 2, where it is panicked
	// inside the field's own recover so it produces the same output it
	// would have from a single-phase walk.
	err  error
	args map[string]interface{}
	// pooled records that args came from argsMapPool and must go back
	// after phase 2 — not after phase 1, because a thunk may close over
	// the map and is not awaited until phase 2.
	pooled bool
}

// writePlannedSelection writes one selection set, resolving every field
// before awaiting any thunk. See the file comment.
func writePlannedSelection(eCtx *executionContext, sp *selectionPlan, source interface{}, parentType *Object, dst []byte) []byte {
	if sp == nil {
		return append(dst, '{', '}')
	}
	if source == nil {
		source = map[string]interface{}{}
	}

	pp := acquirePending(len(sp.fields))
	defer func() {
		for i := range *pp {
			if (*pp)[i].pooled {
				releaseArgsMap((*pp)[i].args)
			}
		}
		releasePending(pp)
	}()

	// Phase 1: resolve everything. No bytes written, no thunk awaited.
	for _, fp := range sp.fields {
		if fp.skipPredicate != nil && !fp.skipPredicate(eCtx.VariableValues) {
			continue
		}
		if fp.fieldDef == nil {
			continue
		}
		*pp = append(*pp, resolveFieldForAppend(eCtx, parentType, source, fp))
	}

	// Phase 2: write in plan order, awaiting thunks as we reach them.
	dst = append(dst, '{')
	wrote := false
	pending := *pp
	for i := range pending {
		pf := &pending[i]
		commaPos := -1
		if wrote {
			commaPos = len(dst)
			dst = append(dst, ',')
		}
		beforeField := len(dst)
		dst = writeResolvedField(eCtx, parentType, source, pf, dst)
		if len(dst) == beforeField {
			if commaPos >= 0 {
				dst = dst[:commaPos]
			}
		} else {
			wrote = true
		}
	}
	return append(dst, '}')
}

// resolveFieldForAppend runs phase 1 for one field: build args, fire the
// extensions hook, call the resolver. Any error is stored rather than
// panicked, because the field's byte offset does not exist yet.
func resolveFieldForAppend(eCtx *executionContext, parentType *Object, source interface{}, fp *fieldPlan) pendingField {
	pf := pendingField{fp: fp}
	info := ResolveInfo{
		FieldName:      fp.fieldName,
		FieldASTs:      fp.fieldASTs,
		ReturnType:     fp.returnType,
		ParentType:     parentType,
		Schema:         eCtx.Schema,
		Fragments:      eCtx.Fragments,
		RootValue:      eCtx.Root,
		Operation:      eCtx.Operation,
		VariableValues: eCtx.VariableValues,
	}

	fieldDef := fp.fieldDef
	if len(fp.args.static) == 0 && len(fp.args.dynamicArgDefs) == 0 {
		pf.args = emptyArgsMap
	} else {
		if eCtx.poolArgs {
			pf.args = acquireArgsMap()
			pf.pooled = true
		} else {
			pf.args = make(map[string]interface{}, len(fp.args.static)+len(fp.args.dynamicArgDefs))
		}
		for k, v := range fp.args.static {
			pf.args[k] = v
		}
		if len(fp.args.dynamicArgDefs) > 0 {
			populateArgumentValues(pf.args, fp.args.dynamicArgDefs, fp.args.dynamicArgASTs, eCtx.VariableValues)
		}
	}

	// ResolveAppend fields own their bytes, so they cannot be split
	// across the phases. Leave them for phase 2 to run whole.
	if fieldDef.ResolveAppend != nil {
		return pf
	}

	resolveFn := fieldDef.Resolve
	if resolveFn == nil {
		resolveFn = DefaultResolveFn
	}

	var resolveFieldFinishFn resolveFieldFinishFuncHandler
	if len(eCtx.Schema.extensions) > 0 {
		infoForExt := info
		extErrs, fn := handleExtensionsResolveFieldDidStart(eCtx.Schema.extensions, eCtx, &infoForExt)
		if len(extErrs) != 0 {
			eCtx.Errors = append(eCtx.Errors, extErrs...)
		}
		resolveFieldFinishFn = fn
		info = infoForExt
		pf.info = &infoForExt
	}

	// A panic from the resolver itself (not a returned error) has to be
	// caught here so the remaining fields still get resolved — dropping
	// out of phase 1 early would serialize the thunks we came to
	// overlap. It is re-raised in phase 2.
	func() {
		defer func() {
			if r := recover(); r != nil {
				pf.err = asFieldError(r)
			}
		}()
		pf.result, pf.err = resolveFn(ResolveParams{
			Source:  source,
			Args:    pf.args,
			Info:    info,
			Context: eCtx.Context,
		})
	}()

	if resolveFieldFinishFn != nil {
		extErrs := resolveFieldFinishFn(pf.result, pf.err)
		if len(extErrs) != 0 {
			eCtx.Errors = append(eCtx.Errors, extErrs...)
		}
	}
	return pf
}

// writeResolvedField runs phase 2 for one field: emit the key, then the
// value, awaiting a thunk if the resolver returned one.
func writeResolvedField(eCtx *executionContext, parentType *Object, source interface{}, pf *pendingField, dst []byte) (out []byte) {
	fp := pf.fp
	pathDepth := len(eCtx.pathBuf)
	eCtx.pathBuf = append(eCtx.pathBuf, pathEntry{key: fp.responseKey})
	keyStart := len(dst)

	out = dst
	defer recoverPlannedField(&out, keyStart, fp, eCtx, pathDepth)

	// Rebuilt rather than carried across the phase boundary; see the
	// pendingField comment. The extensions path stores its own copy
	// because the hook may have changed it.
	var info ResolveInfo
	if pf.info != nil {
		info = *pf.info
	} else {
		info = ResolveInfo{
			FieldName:      fp.fieldName,
			FieldASTs:      fp.fieldASTs,
			ReturnType:     fp.returnType,
			ParentType:     parentType,
			Schema:         eCtx.Schema,
			Fragments:      eCtx.Fragments,
			RootValue:      eCtx.Root,
			Operation:      eCtx.Operation,
			VariableValues: eCtx.VariableValues,
		}
	}

	if fp.fieldDef.ResolveAppend != nil {
		out = append(dst, fp.responseKeyJSON...)
		appended, err := fp.fieldDef.ResolveAppend(ResolveParams{
			Source:  source,
			Args:    pf.args,
			Info:    info,
			Context: eCtx.Context,
		}, out)
		if err != nil {
			panic(err)
		}
		return appended
	}

	if pf.err != nil {
		panic(pf.err)
	}

	out = append(dst, fp.responseKeyJSON...)
	out = writeCompleteValueCatchingError(eCtx, fp.returnType, fp, info, pf.result, out, -1)
	return out
}

// asFieldError normalises a recovered panic value into the error shape
// handleFieldError expects, matching what a resolver-returned error
// would have produced.
func asFieldError(r interface{}) error {
	switch e := r.(type) {
	case error:
		return e
	default:
		return fmt.Errorf("%v", r)
	}
}

// pendingPool recycles pendingField scratch across requests. A free
// list on the execution context only helps within one request, and the
// scratch is allocated per selection set, so a wide query paid for it
// on every request — measured as +92% B/op on a 100-field selection
// set before this was hoisted to a package pool.
var pendingPool = sync.Pool{
	New: func() interface{} {
		s := make([]pendingField, 0, 32)
		return &s
	},
}

func acquirePending(n int) *[]pendingField {
	p := pendingPool.Get().(*[]pendingField)
	if cap(*p) < n {
		*p = make([]pendingField, 0, n)
	} else {
		*p = (*p)[:0]
	}
	return p
}

func releasePending(p *[]pendingField) {
	s := *p
	// Clear so results and args maps are not kept alive by the pool.
	for i := range s {
		s[i] = pendingField{}
	}
	*p = s[:0]
	pendingPool.Put(p)
}
