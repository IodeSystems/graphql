package graphql

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/IodeSystems/graphql-go/gqlerrors"
	"github.com/IodeSystems/graphql-go/language/parser"
	"github.com/IodeSystems/graphql-go/language/source"
)

// responseBufPool backs DoWriter. Append-mode's speed comes from
// reusing a buffer that has already grown to response size; a fresh
// make([]byte, ...) per request would give most of that back. Callers
// who own their own buffer should use DoAppend and skip the pool.
var responseBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, 4096)
		return &b
	},
}

// maxPooledResponseBuf caps what goes back into responseBufPool. One
// pathological response should not pin megabytes for the process
// lifetime.
const maxPooledResponseBuf = 1 << 20

// DoWriter runs a GraphQL request and writes the complete response
// body to w. This is the entry point servers want: the response is
// bytes, and building it as bytes skips the map[string]interface{}
// result tree and the marshal pass that Do requires.
//
// The returned error is the write error from w, nothing else. GraphQL
// errors — parse, validation, and field errors alike — are part of
// the response body, exactly as they are for a marshalled Do result.
// A request that fails validation still produces a complete, valid
// body and a nil error. Callers needing programmatic access to those
// errors want Do.
//
// Nothing is written until the body is complete, so w never sees a
// partial response — one Write call, always.
//
// That is an implementation choice, not a property of GraphQL. Null
// propagation is retroactive: a non-null field failing turns its
// nearest nullable ancestor into null, so no subtree can be committed
// until it finishes. This walker handles that by rolling one buffer
// back to a saved offset, which means holding the whole body. A
// streaming version could flush each nullable subtree as it completes,
// but only for schemas selecting no non-null root field, since an
// unabsorbed bubble replaces the entire data value with null.
func DoWriter(p Params, w io.Writer) error {
	bufp := responseBufPool.Get().(*[]byte)
	buf := DoAppend(p, (*bufp)[:0])

	_, err := w.Write(buf)

	if cap(buf) <= maxPooledResponseBuf {
		*bufp = buf
		responseBufPool.Put(bufp)
	}
	return err
}

// DoAppend runs a GraphQL request and appends the complete response
// body to dst, returning the extended slice. It is the allocation
// floor of this package: pass a buffer you keep across requests
// (buf[:0]) and a steady-state request allocates nothing for the
// response itself.
//
// DoWriter wraps this with a pooled buffer and is the better default
// unless you are already managing buffers.
//
// The body is always complete and valid, including for parse and
// validation failures, which produce {"data":null,"errors":[...]} —
// the same shape json.Marshal gives for the *Result that Do returns
// in those cases.
func DoAppend(p Params, dst []byte) []byte {
	src := source.NewSource(&source.Source{
		Body: []byte(p.RequestString),
		Name: "GraphQL request",
	})

	if extErrs := handleExtensionsInits(&p); len(extErrs) != 0 {
		return appendErrorEnvelope(dst, extErrs)
	}

	extErrs, parseFinishFn := handleExtensionsParseDidStart(&p)
	if len(extErrs) != 0 {
		return appendErrorEnvelope(dst, extErrs)
	}

	AST, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		extErrs = parseFinishFn(err)
		extErrs = append(extErrs, gqlerrors.FormatErrors(err)...)
		return appendErrorEnvelope(dst, extErrs)
	}

	if extErrs = parseFinishFn(err); len(extErrs) != 0 {
		return appendErrorEnvelope(dst, extErrs)
	}

	extErrs, validationFinishFn := handleExtensionsValidationDidStart(&p)
	if len(extErrs) != 0 {
		return appendErrorEnvelope(dst, extErrs)
	}

	validationResult := ValidateDocument(&p.Schema, AST, nil)
	if !validationResult.IsValid {
		extErrs = validationFinishFn(validationResult.Errors)
		extErrs = append(extErrs, validationResult.Errors...)
		return appendErrorEnvelope(dst, extErrs)
	}

	if extErrs = validationFinishFn(validationResult.Errors); len(extErrs) != 0 {
		return appendErrorEnvelope(dst, extErrs)
	}

	ep := ExecuteParams{
		Schema:        p.Schema,
		Root:          p.RootObject,
		AST:           AST,
		OperationName: p.OperationName,
		Args:          p.VariableValues,
		Context:       p.Context,
	}

	plan, err := PlanQuery(&ep.Schema, ep.AST, ep.OperationName)
	if err != nil {
		return appendErrorEnvelope(dst, gqlerrors.FormatErrors(err))
	}

	out, preErrs := ExecutePlanAppend(plan, ep, dst)
	if len(preErrs) != 0 {
		// ExecutePlanAppend returned before data assembly began (e.g.
		// variable coercion failed) and left dst untouched, so the
		// envelope is still ours to write.
		return appendErrorEnvelope(dst, preErrs)
	}
	return out
}

// appendErrorEnvelope writes the no-data response shape. Result.Data
// carries no omitempty, so json.Marshal of a Do failure emits an
// explicit null here; match it rather than omitting the key.
func appendErrorEnvelope(dst []byte, errs []gqlerrors.FormattedError) []byte {
	dst = append(dst, `{"data":null,"errors":`...)
	b, err := json.Marshal(errs)
	if err != nil {
		dst = append(dst, "[]"...)
	} else {
		dst = append(dst, b...)
	}
	return append(dst, '}')
}
