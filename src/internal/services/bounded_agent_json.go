package services

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxAgentJSONDepth = 64
	maxAgentJSONNodes = 32 * 1024
)

var (
	agentJSONMarshalerType   = reflect.TypeFor[json.Marshaler]()
	agentTextMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	agentJSONTimeType        = reflect.TypeFor[time.Time]()
	agentJSONTimePointerType = reflect.PointerTo(agentJSONTimeType)
)

type agentJSONBudget struct {
	remaining int64
	nodes     int
	visiting  map[agentJSONVisit]struct{}
}

type agentJSONVisit struct {
	typ reflect.Type
	ptr uintptr
}

// agentJSONWithinMarshalBudget walks the already-materialized value without
// invoking custom marshalers and computes a conservative upper bound for the
// compact encoding/json output. This prevents Marshal from first allocating an
// attacker-sized output only to have it discarded by a post-encoding limit.
func agentJSONWithinMarshalBudget(value any, limit int) bool {
	if limit < 0 {
		return false
	}
	budget := &agentJSONBudget{
		remaining: int64(limit),
		visiting:  make(map[agentJSONVisit]struct{}),
	}
	return budget.visit(reflect.ValueOf(value), 0)
}

func (budget *agentJSONBudget) visit(value reflect.Value, depth int) bool {
	if budget == nil || depth > maxAgentJSONDepth {
		return false
	}
	budget.nodes++
	if budget.nodes > maxAgentJSONNodes {
		return false
	}
	if !value.IsValid() {
		return budget.consume(4) // null
	}
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return budget.consume(4)
		}
		value = value.Elem()
	}
	if value.Type() == agentJSONTimeType || value.Type() == agentJSONTimePointerType {
		return budget.consume(64)
	}

	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return budget.consume(4)
		}
		if agentJSONUsesCustomMarshaler(value.Type()) {
			return false
		}
		return budget.visitReference(value, depth)
	case reflect.Map:
		if value.IsNil() {
			return budget.consume(4)
		}
		if agentJSONUsesCustomMarshaler(value.Type()) || !agentJSONMapKeySupported(value.Type().Key()) {
			return false
		}
		return budget.visitMap(value, depth)
	case reflect.Slice:
		if value.IsNil() {
			return budget.consume(4)
		}
		if agentJSONUsesCustomMarshaler(value.Type()) {
			return false
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			encoded := int64(2) + 4*((int64(value.Len())+2)/3)
			return budget.consume(encoded)
		}
		return budget.visitSequence(value, depth)
	case reflect.Array:
		if agentJSONUsesCustomMarshaler(value.Type()) {
			return false
		}
		return budget.visitSequence(value, depth)
	}

	if agentJSONUsesCustomMarshaler(value.Type()) {
		return false
	}
	switch value.Kind() {
	case reflect.Bool:
		return budget.consume(5)
	case reflect.String:
		return budget.consume(agentJSONStringSize(value.String()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return budget.consume(21)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return budget.consume(21)
	case reflect.Float32, reflect.Float64:
		return budget.consume(32)
	case reflect.Struct:
		return budget.visitStruct(value, depth)
	default:
		// Channels, functions, complex values, and unsafe pointers are not JSON
		// values. Return the fixed safe encoding error without formatting them.
		return false
	}
}

func (budget *agentJSONBudget) visitReference(value reflect.Value, depth int) bool {
	visit := agentJSONVisit{typ: value.Type(), ptr: value.Pointer()}
	if _, cycle := budget.visiting[visit]; cycle {
		return false
	}
	budget.visiting[visit] = struct{}{}
	defer delete(budget.visiting, visit)
	return budget.visit(value.Elem(), depth+1)
}

func (budget *agentJSONBudget) visitMap(value reflect.Value, depth int) bool {
	visit := agentJSONVisit{typ: value.Type(), ptr: value.Pointer()}
	if _, cycle := budget.visiting[visit]; cycle {
		return false
	}
	budget.visiting[visit] = struct{}{}
	defer delete(budget.visiting, visit)
	if !budget.consume(2) { // {}
		return false
	}
	iterator := value.MapRange()
	first := true
	for iterator.Next() {
		if !first && !budget.consume(1) {
			return false
		}
		first = false
		if !budget.visitMapKey(iterator.Key()) || !budget.consume(1) || !budget.visit(iterator.Value(), depth+1) {
			return false
		}
	}
	return true
}

func (budget *agentJSONBudget) visitMapKey(value reflect.Value) bool {
	for value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		return budget.consume(agentJSONStringSize(value.String()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return budget.consume(23) // quoted maximum-width integer
	default:
		return false
	}
}

func (budget *agentJSONBudget) visitSequence(value reflect.Value, depth int) bool {
	var visit agentJSONVisit
	if value.Kind() == reflect.Slice {
		visit = agentJSONVisit{typ: value.Type(), ptr: value.Pointer()}
		if _, cycle := budget.visiting[visit]; cycle {
			return false
		}
		budget.visiting[visit] = struct{}{}
		defer delete(budget.visiting, visit)
	}
	if !budget.consume(2) { // []
		return false
	}
	for index := 0; index < value.Len(); index++ {
		if index > 0 && !budget.consume(1) {
			return false
		}
		if !budget.visit(value.Index(index), depth+1) {
			return false
		}
	}
	return true
}

func (budget *agentJSONBudget) visitStruct(value reflect.Value, depth int) bool {
	if !budget.consume(2) { // {}
		return false
	}
	first := true
	typ := value.Type()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" { // unexported
			if field.Anonymous {
				// encoding/json can promote exported members from an unexported
				// anonymous struct. Reject the uncommon shape rather than risk
				// under-counting its flattened output or invoking hidden methods.
				return false
			}
			continue
		}
		tagParts := strings.Split(field.Tag.Get("json"), ",")
		name := tagParts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if !first && !budget.consume(1) {
			return false
		}
		first = false
		nameSize := agentJSONStringSize(name)
		if fallbackSize := agentJSONStringSize(field.Name); fallbackSize > nameSize {
			// encoding/json ignores syntactically invalid tag names. Charge the
			// larger of the tag and Go field name without reproducing that parser.
			nameSize = fallbackSize
		}
		fieldValue := value.Field(index)
		if !budget.consume(nameSize) || !budget.consume(1) {
			return false
		}
		for _, option := range tagParts[1:] {
			if option == "string" && !budget.consume(agentJSONStringOptionExtra(fieldValue)) {
				return false
			}
		}
		if !budget.visit(fieldValue, depth+1) {
			return false
		}
	}
	return true
}

func (budget *agentJSONBudget) consume(size int64) bool {
	if budget == nil || size < 0 || size > budget.remaining {
		return false
	}
	budget.remaining -= size
	return true
}

func agentJSONUsesCustomMarshaler(typ reflect.Type) bool {
	if typ == nil || typ == agentJSONTimeType || typ == agentJSONTimePointerType {
		return false
	}
	if typ.Implements(agentJSONMarshalerType) || typ.Implements(agentTextMarshalerType) {
		return true
	}
	return typ.Kind() != reflect.Pointer && (reflect.PointerTo(typ).Implements(agentJSONMarshalerType) || reflect.PointerTo(typ).Implements(agentTextMarshalerType))
}

func agentJSONMapKeySupported(typ reflect.Type) bool {
	if typ == nil {
		return false
	}
	if typ.Kind() != reflect.String && typ.Implements(agentTextMarshalerType) {
		// encoding/json invokes TextMarshaler for non-string map keys before
		// integer formatting; custom output cannot be preflight-bounded.
		return false
	}
	switch typ.Kind() {
	case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

// agentJSONStringSize matches encoding/json's compact string escaping without
// allocating the escaped form. HTML-sensitive ASCII bytes and control bytes
// are charged at their maximum emitted width.
func agentJSONStringSize(value string) int64 {
	size := int64(2) // quotes
	for index := 0; index < len(value); {
		character := value[index]
		if character < utf8.RuneSelf {
			switch character {
			case '\\', '"', '\b', '\f', '\n', '\r', '\t':
				size += 2
			case '<', '>', '&':
				size += 6
			default:
				if character < 0x20 {
					size += 6
				} else {
					size++
				}
			}
			index++
			continue
		}
		runeValue, width := utf8.DecodeRuneInString(value[index:])
		if runeValue == utf8.RuneError && width == 1 {
			size += 6 // encoding/json emits the ASCII escape \ufffd
			index++
			continue
		}
		if runeValue == '\u2028' || runeValue == '\u2029' {
			size += 6
		} else {
			size += int64(width)
		}
		index += width
	}
	return size
}

func agentJSONStringOptionExtra(value reflect.Value) int64 {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return 0
	}
	switch value.Kind() {
	case reflect.String:
		// The ordinary visit charges the inner JSON string. The `string`
		// option encodes that representation as another JSON string, which
		// can at most double every inner byte and add its outer quotes.
		return agentJSONStringSize(value.String()) + 2
	case reflect.Bool:
		return 7
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return 23
	case reflect.Float32, reflect.Float64:
		return 34
	default:
		return 0
	}
}
