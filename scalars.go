package graphql

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/IodeSystems/graphql-go/v2/language/ast"
)

// As per the GraphQL Spec, Integers are only treated as valid when a valid
// 32-bit signed integer, providing the broadest support across platforms.
//
// n.b. JavaScript's integers are safe between -(2^53 - 1) and 2^53 - 1 because
// they are internally represented as IEEE 754 doubles.
func coerceInt(value interface{}) interface{} {
	switch value := value.(type) {
	case bool:
		if value == true {
			return 1
		}
		return 0
	case *bool:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case int:
		if value < int(math.MinInt32) || value > int(math.MaxInt32) {
			return nil
		}
		return value
	case *int:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case int8:
		return int(value)
	case *int8:
		if value == nil {
			return nil
		}
		return int(*value)
	case int16:
		return int(value)
	case *int16:
		if value == nil {
			return nil
		}
		return int(*value)
	case int32:
		return int(value)
	case *int32:
		if value == nil {
			return nil
		}
		return int(*value)
	case int64:
		if value < int64(math.MinInt32) || value > int64(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *int64:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case uint:
		if value > math.MaxInt32 {
			return nil
		}
		return int(value)
	case *uint:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case uint8:
		return int(value)
	case *uint8:
		if value == nil {
			return nil
		}
		return int(*value)
	case uint16:
		return int(value)
	case *uint16:
		if value == nil {
			return nil
		}
		return int(*value)
	case uint32:
		if value > uint32(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *uint32:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case uint64:
		if value > uint64(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *uint64:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case float32:
		if value < float32(math.MinInt32) || value > float32(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *float32:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case float64:
		if value < float64(math.MinInt32) || value > float64(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *float64:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	case string:
		val, err := strconv.ParseFloat(value, 0)
		if err != nil {
			return nil
		}
		return coerceInt(val)
	case *string:
		if value == nil {
			return nil
		}
		return coerceInt(*value)
	}

	// If the value cannot be transformed into an int, return nil instead of '0'
	// to denote 'no integer found'
	return nil
}

// Int is the GraphQL Integer type definition.
var Int = NewScalar(ScalarConfig{
	Name: "Int",
	Description: "The `Int` scalar type represents non-fractional signed whole numeric " +
		"values. Int can represent values between -(2^31) and 2^31 - 1. ",
	Serialize:  coerceInt,
	ParseValue: coerceInt,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.IntValue:
			if intValue, err := strconv.Atoi(valueAST.Value); err == nil {
				return intValue
			}
		}
		return nil
	},
	AppendJSON: appendIntJSON,
})

func coerceFloat(value interface{}) interface{} {
	switch value := value.(type) {
	case bool:
		if value == true {
			return 1.0
		}
		return 0.0
	case *bool:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case int:
		return float64(value)
	case *int:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case int8:
		return float64(value)
	case *int8:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case int16:
		return float64(value)
	case *int16:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case int32:
		return float64(value)
	case *int32:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case int64:
		return float64(value)
	case *int64:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case uint:
		return float64(value)
	case *uint:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case uint8:
		return float64(value)
	case *uint8:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case uint16:
		return float64(value)
	case *uint16:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case uint32:
		return float64(value)
	case *uint32:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case uint64:
		return float64(value)
	case *uint64:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case float32:
		return value
	case *float32:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case float64:
		return value
	case *float64:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	case string:
		val, err := strconv.ParseFloat(value, 0)
		if err != nil {
			return nil
		}
		return val
	case *string:
		if value == nil {
			return nil
		}
		return coerceFloat(*value)
	}

	// If the value cannot be transformed into an float, return nil instead of '0.0'
	// to denote 'no float found'
	return nil
}

// Float is the GraphQL float type definition.
var Float = NewScalar(ScalarConfig{
	Name: "Float",
	Description: "The `Float` scalar type represents signed double-precision fractional " +
		"values as specified by " +
		"[IEEE 754](http://en.wikipedia.org/wiki/IEEE_floating_point). ",
	Serialize:  coerceFloat,
	ParseValue: coerceFloat,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.FloatValue:
			if floatValue, err := strconv.ParseFloat(valueAST.Value, 64); err == nil {
				return floatValue
			}
		case *ast.IntValue:
			if floatValue, err := strconv.ParseFloat(valueAST.Value, 64); err == nil {
				return floatValue
			}
		}
		return nil
	},
	AppendJSON: appendFloatJSON,
})

func coerceString(value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return v
	case *string:
		if v == nil {
			return nil
		}
		return *v
	}
	return fmt.Sprintf("%v", value)
}

// String is the GraphQL string type definition
var String = NewScalar(ScalarConfig{
	Name: "String",
	Description: "The `String` scalar type represents textual data, represented as UTF-8 " +
		"character sequences. The String type is most often used by GraphQL to " +
		"represent free-form human-readable text.",
	Serialize:  coerceString,
	ParseValue: coerceString,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			return valueAST.Value
		}
		return nil
	},
	AppendJSON: appendStringJSON,
})

func coerceBool(value interface{}) interface{} {
	switch value := value.(type) {
	case bool:
		return value
	case *bool:
		if value == nil {
			return nil
		}
		return *value
	case string:
		switch value {
		case "", "false":
			return false
		}
		return true
	case *string:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case float64:
		if value != 0 {
			return true
		}
		return false
	case *float64:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case float32:
		if value != 0 {
			return true
		}
		return false
	case *float32:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case int:
		if value != 0 {
			return true
		}
		return false
	case *int:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case int8:
		if value != 0 {
			return true
		}
		return false
	case *int8:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case int16:
		if value != 0 {
			return true
		}
		return false
	case *int16:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case int32:
		if value != 0 {
			return true
		}
		return false
	case *int32:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case int64:
		if value != 0 {
			return true
		}
		return false
	case *int64:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case uint:
		if value != 0 {
			return true
		}
		return false
	case *uint:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case uint8:
		if value != 0 {
			return true
		}
		return false
	case *uint8:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case uint16:
		if value != 0 {
			return true
		}
		return false
	case *uint16:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case uint32:
		if value != 0 {
			return true
		}
		return false
	case *uint32:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	case uint64:
		if value != 0 {
			return true
		}
		return false
	case *uint64:
		if value == nil {
			return nil
		}
		return coerceBool(*value)
	}
	return false
}

// Boolean is the GraphQL boolean type definition
var Boolean = NewScalar(ScalarConfig{
	Name:        "Boolean",
	Description: "The `Boolean` scalar type represents `true` or `false`.",
	Serialize:   coerceBool,
	ParseValue:  coerceBool,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.BooleanValue:
			return valueAST.Value
		}
		return nil
	},
	AppendJSON: appendBoolJSON,
})

// ID is the GraphQL id type definition
var ID = NewScalar(ScalarConfig{
	Name: "ID",
	Description: "The `ID` scalar type represents a unique identifier, often used to " +
		"refetch an object or as key for a cache. The ID type appears in a JSON " +
		"response as a String; however, it is not intended to be human-readable. " +
		"When expected as an input type, any string (such as `\"4\"`) or integer " +
		"(such as `4`) input value will be accepted as an ID.",
	Serialize:  coerceString,
	ParseValue: coerceString,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.IntValue:
			return valueAST.Value
		case *ast.StringValue:
			return valueAST.Value
		}
		return nil
	},
	AppendJSON: appendStringJSON,
})

func serializeDateTime(value interface{}) interface{} {
	switch value := value.(type) {
	case time.Time:
		buff, err := value.MarshalText()
		if err != nil {
			return nil
		}

		return string(buff)
	case *time.Time:
		if value == nil {
			return nil
		}
		return serializeDateTime(*value)
	default:
		return nil
	}
}

func unserializeDateTime(value interface{}) interface{} {
	switch value := value.(type) {
	case []byte:
		t := time.Time{}
		err := t.UnmarshalText(value)
		if err != nil {
			return nil
		}

		return t
	case string:
		return unserializeDateTime([]byte(value))
	case *string:
		if value == nil {
			return nil
		}
		return unserializeDateTime([]byte(*value))
	case time.Time:
		return value
	default:
		return nil
	}
}

var DateTime = NewScalar(ScalarConfig{
	Name: "DateTime",
	Description: "The `DateTime` scalar type represents a DateTime." +
		" The DateTime is serialized as an RFC 3339 quoted string",
	Serialize:  serializeDateTime,
	ParseValue: unserializeDateTime,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			return unserializeDateTime(valueAST.Value)
		}
		return nil
	},
	AppendJSON: appendDateTimeJSON,
})

// appendIntJSON writes a coerceInt-style result as a JSON integer.
// Matches the contract that Serialize returns int (or nil); other
// kinds round-trip through coerceInt to preserve the slow-path
// behavior (e.g. float64 → truncated int).
func appendIntJSON(dst []byte, value interface{}) []byte {
	if n, ok := value.(int); ok {
		return strconv.AppendInt(dst, int64(n), 10)
	}
	coerced := coerceInt(value)
	if coerced == nil {
		return append(dst, "null"...)
	}
	return strconv.AppendInt(dst, int64(coerced.(int)), 10)
}

// appendFloatJSON writes a Float-typed value as a JSON number.
// Mirrors encoding/json's float behavior: NaN / +-Inf are not
// representable in JSON and yield null. Format follows json's
// strategy (use 'e' for very small/large magnitudes, 'f' otherwise)
// to keep byte-level parity with json.Marshal for the common cases.
func appendFloatJSON(dst []byte, value interface{}) []byte {
	var f float64
	switch v := value.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	default:
		coerced := coerceFloat(value)
		if coerced == nil {
			return append(dst, "null"...)
		}
		f = coerced.(float64)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(dst, "null"...)
	}
	abs := math.Abs(f)
	fmtByte := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		fmtByte = 'e'
	}
	dst = strconv.AppendFloat(dst, f, fmtByte, -1, 64)
	if fmtByte == 'e' {
		// strconv may emit "1e+09"; encoding/json emits "1e+09" too,
		// so no normalization needed here.
		return dst
	}
	return dst
}

// appendBoolJSON writes a Boolean-typed value as JSON true/false.
func appendBoolJSON(dst []byte, value interface{}) []byte {
	switch v := value.(type) {
	case bool:
		if v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case *bool:
		if v == nil {
			return append(dst, "null"...)
		}
		if *v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	}
	coerced := coerceBool(value)
	b, ok := coerced.(bool)
	if !ok {
		return append(dst, "null"...)
	}
	if b {
		return append(dst, "true"...)
	}
	return append(dst, "false"...)
}

// appendStringJSON writes a String / ID value as a JSON string.
// Falls back to coerceString for non-string types (matching the
// Serialize-then-emit pipeline).
func appendStringJSON(dst []byte, value interface{}) []byte {
	switch v := value.(type) {
	case string:
		return appendJSONString(dst, v)
	case *string:
		if v == nil {
			return append(dst, "null"...)
		}
		return appendJSONString(dst, *v)
	}
	coerced := coerceString(value)
	if coerced == nil {
		return append(dst, "null"...)
	}
	if s, ok := coerced.(string); ok {
		return appendJSONString(dst, s)
	}
	return append(dst, "null"...)
}

// appendDateTimeJSON renders a time.Time / *time.Time as the JSON
// quoted RFC 3339 form serializeDateTime would produce. Formats
// directly into dst to avoid the MarshalText + string conversion
// allocations.
func appendDateTimeJSON(dst []byte, value interface{}) []byte {
	var t time.Time
	switch v := value.(type) {
	case time.Time:
		t = v
	case *time.Time:
		if v == nil {
			return append(dst, "null"...)
		}
		t = *v
	default:
		return append(dst, "null"...)
	}
	// time.MarshalText produces RFC 3339 with nanosecond precision
	// (time.RFC3339Nano). Format directly to avoid the []byte + string
	// allocations of MarshalText. MarshalText never errors for a
	// time.Time value, so we skip the error check.
	dst = append(dst, '"')
	dst = t.AppendFormat(dst, time.RFC3339Nano)
	return append(dst, '"')
}

const hexChars = "0123456789abcdef"

// appendJSONString writes a JSON-quoted string to dst with HTML-safe
// escaping (matches encoding/json's default for <, >, &). UTF-8 is
// passed through; control bytes (< 0x20) get \u escapes; U+2028 and
// U+2029 get   /   to stay valid as JS embeds. The fast
// path streams contiguous safe runs in a single append.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			if jsonSafeSet[c] {
				i++
				continue
			}
			if start < i {
				dst = append(dst, s[start:i]...)
			}
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexChars[c>>4], hexChars[c&0xF])
			}
			i++
			start = i
			continue
		}
		// Multibyte UTF-8: detect U+2028 / U+2029 (E2 80 A8 / A9).
		if c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			if start < i {
				dst = append(dst, s[start:i]...)
			}
			dst = append(dst, '\\', 'u', '2', '0', '2', hexChars[s[i+2]&0xF])
			i += 3
			start = i
			continue
		}
		i++
	}
	if start < len(s) {
		dst = append(dst, s[start:]...)
	}
	return append(dst, '"')
}

// jsonStringEncodedLen returns exactly len(appendJSONString(nil, s)) —
// the encoded byte count including both quotes — without allocating.
// It mirrors appendJSONString's branches one for one; the two are
// pinned together by TestJSONStringEncodedLenMatchesAppend, because a
// drift here would silently under-size a caller's buffer.
func jsonStringEncodedLen(s string) int {
	n := 2 // the enclosing quotes
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			switch {
			case jsonSafeSet[c]:
				n++
			case c == '"' || c == '\\' || c == '\n' || c == '\r' ||
				c == '\t' || c == '\b' || c == '\f':
				n += 2
			default:
				n += 6 // \u00XX
			}
			i++
			continue
		}
		if c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			n += 6 // \u2028 / \u2029
			i += 3
			continue
		}
		n++
		i++
	}
	return n
}

// jsonSafeSet[c] is true when byte c may appear unescaped inside a
// JSON string under HTML-safe encoding (matches encoding/json's
// htmlSafeSet).
var jsonSafeSet = [128]bool{
	' ': true, '!': true /* '"' escaped */, '#': true, '$': true, '%': true,
	/* '&' escaped */ '\'': true, '(': true, ')': true, '*': true, '+': true,
	',': true, '-': true, '.': true, '/': true,
	'0': true, '1': true, '2': true, '3': true, '4': true,
	'5': true, '6': true, '7': true, '8': true, '9': true,
	':': true, ';': true /* '<' escaped */, '=': true, /* '>' escaped */
	'?': true, '@': true,
	'A': true, 'B': true, 'C': true, 'D': true, 'E': true, 'F': true, 'G': true,
	'H': true, 'I': true, 'J': true, 'K': true, 'L': true, 'M': true, 'N': true,
	'O': true, 'P': true, 'Q': true, 'R': true, 'S': true, 'T': true, 'U': true,
	'V': true, 'W': true, 'X': true, 'Y': true, 'Z': true,
	'[': true /* '\\' escaped */, ']': true, '^': true, '_': true, '`': true,
	'a': true, 'b': true, 'c': true, 'd': true, 'e': true, 'f': true, 'g': true,
	'h': true, 'i': true, 'j': true, 'k': true, 'l': true, 'm': true, 'n': true,
	'o': true, 'p': true, 'q': true, 'r': true, 's': true, 't': true, 'u': true,
	'v': true, 'w': true, 'x': true, 'y': true, 'z': true,
	'{': true, '|': true, '}': true, '~': true,
}
