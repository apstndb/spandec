// Package spandec decodes Cloud Spanner values into Go values like the
// official client library ([cloud.google.com/go/spanner.GenericColumnValue]
// Decode and Row.ToStruct), extended to cover destination shapes the client
// rejects today:
//
//   - pointers to named scalar types as fields
//     (https://github.com/googleapis/google-cloud-go/issues/12576)
//   - []T struct slices for ARRAY<STRUCT>, not only []*T
//     (https://github.com/googleapis/google-cloud-go/issues/11090)
//   - [encoding/json.RawMessage] destinations for JSON columns
//     (https://github.com/googleapis/google-cloud-go/issues/10720)
//
// Everything else delegates to the client's own decoding, so behavior stays
// identical where the client already works. Struct field listing uses
// [github.com/apstndb/structfields/spannertag], the exported port of the
// client's `spanner` tag semantics.
//
// Destination types outside the client's coverage (uint32 and the other
// integer width variants, [time.Duration], external types that cannot
// implement [cloud.google.com/go/spanner.Decoder]) can be supported per call
// with [WithValueDecoder]; registered decoders run before the built-in
// decoding and may return [ErrFallthrough] to defer to it. There is
// deliberately no package-global registry; injection is per call, mirroring
// spanenc's WithValueEncoder on the encode side.
package spandec

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/protobuf/types/known/structpb"
)

var (
	// ErrNotStructPointer is returned by [ToStruct] when the destination is
	// not a non-nil pointer to a struct.
	ErrNotStructPointer = errors.New("spandec: destination must be a non-nil pointer to a struct")
	// ErrNoMatchingField is returned by [ToStruct] when a row column has no
	// corresponding struct field, mirroring the client's ToStruct strictness.
	ErrNoMatchingField = errors.New("spandec: no corresponding struct field for column")
	// ErrNullStructElement is returned when an ARRAY<STRUCT> NULL element is
	// decoded into a []T struct slice, which cannot represent NULL elements;
	// use []*T for arrays that may contain NULL STRUCTs.
	ErrNullStructElement = errors.New("spandec: NULL STRUCT element cannot be decoded into a non-pointer struct slice")
)

// Decode decodes a [spanner.GenericColumnValue] into ptr. Destinations the
// client supports are delegated to the client's Decode unchanged; the
// extension shapes listed in the package documentation are handled here.
// Decoders registered via [WithValueDecoder] are consulted first (including
// per element when decoding an ARRAY into a slice of a registered type);
// [ErrFallthrough] defers to the built-in path, so without options the
// behavior is unchanged.
func Decode(gcv spanner.GenericColumnValue, ptr any, opts ...DecodeOption) error {
	return decode(gcv, ptr, newDecodeConfig(opts))
}

// decode is the option-threaded core of [Decode].
func decode(gcv spanner.GenericColumnValue, ptr any, cfg decodeConfig) error {
	if cfg.valueDecoders != nil {
		if t := reflect.TypeOf(ptr); t != nil && t.Kind() == reflect.Pointer {
			if dec, ok := cfg.valueDecoders[t.Elem()]; ok {
				err := dec(gcv, ptr)
				if err == nil || !errors.Is(err, ErrFallthrough) {
					return err
				}
				// ErrFallthrough: defer to the built-in path below.
			} else if t.Elem().Kind() == reflect.Slice && gcv.Type.GetCode() == sppb.TypeCode_ARRAY {
				if _, ok := cfg.valueDecoders[t.Elem().Elem()]; ok {
					return decodeCustomSlice(cfg, gcv, reflect.ValueOf(ptr).Elem())
				}
			}
		}
	}
	if done, err := decodeExtended(gcv, ptr); done {
		return err
	}
	return gcv.Decode(ptr)
}

// decodeCustomSlice decodes an ARRAY value element-wise into a slice
// destination whose element type has a [WithValueDecoder] registration:
// a SQL NULL ARRAY yields a nil slice, and each element goes through
// [Decode] again, so per-element [ErrFallthrough] still reaches the
// built-in decoding.
func decodeCustomSlice(cfg decodeConfig, gcv spanner.GenericColumnValue, dst reflect.Value) error {
	if isNullValue(gcv) {
		dst.SetZero()
		return nil
	}
	lv := gcv.Value.GetListValue()
	if lv == nil {
		return fmt.Errorf("spandec: ARRAY value carries %T, not a list value", gcv.Value.GetKind())
	}
	elemType := gcv.Type.GetArrayElementType()
	values := lv.GetValues()
	out := reflect.MakeSlice(dst.Type(), len(values), len(values))
	for i, v := range values {
		egcv := spanner.GenericColumnValue{Type: elemType, Value: v}
		if err := decode(egcv, out.Index(i).Addr().Interface(), cfg); err != nil {
			return fmt.Errorf("spandec: element %d: %w", i, err)
		}
	}
	dst.Set(out)
	return nil
}

// decodeExtended reports whether ptr is one of the extension shapes, and if
// so decodes into it.
func decodeExtended(gcv spanner.GenericColumnValue, ptr any) (bool, error) {
	// json.RawMessage destination for JSON columns (#10720).
	if raw, ok := ptr.(*json.RawMessage); ok && gcv.Type.GetCode() == sppb.TypeCode_JSON {
		return true, decodeJSONRaw(gcv, raw)
	}

	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false, nil // let the client produce its usual error
	}
	dst := rv.Elem()

	// Pointer-to-named-scalar destination (*T fields decoded via **T) (#12576).
	if dst.Kind() == reflect.Pointer {
		if base, ok := scalarBaseType(dst.Type().Elem()); ok {
			return true, decodeNamedScalarPtr(gcv, dst, base)
		}
	}

	// []T struct slice for ARRAY<STRUCT> (#11090).
	if dst.Kind() == reflect.Slice && dst.Type().Elem().Kind() == reflect.Struct &&
		gcv.Type.GetCode() == sppb.TypeCode_ARRAY &&
		gcv.Type.GetArrayElementType().GetCode() == sppb.TypeCode_STRUCT {
		return true, decodeStructSlice(gcv, dst)
	}

	return false, nil
}

// decodeJSONRaw stores the JSON wire text as-is; a SQL NULL JSON yields a
// nil RawMessage.
func decodeJSONRaw(gcv spanner.GenericColumnValue, raw *json.RawMessage) error {
	if isNullValue(gcv) {
		*raw = nil
		return nil
	}
	*raw = json.RawMessage(gcv.Value.GetStringValue())
	return nil
}

// scalarBaseType reports the base scalar type a named type converts to,
// limited to the scalar kinds the client's custom-type decoding supports.
func scalarBaseType(t reflect.Type) (reflect.Type, bool) {
	switch t.Kind() {
	case reflect.String:
		return reflect.TypeFor[string](), t != reflect.TypeFor[string]()
	case reflect.Int64:
		return reflect.TypeFor[int64](), t != reflect.TypeFor[int64]()
	case reflect.Bool:
		return reflect.TypeFor[bool](), t != reflect.TypeFor[bool]()
	case reflect.Float64:
		return reflect.TypeFor[float64](), t != reflect.TypeFor[float64]()
	case reflect.Float32:
		return reflect.TypeFor[float32](), t != reflect.TypeFor[float32]()
	}
	return nil, false
}

// decodeNamedScalarPtr decodes into a *T destination where T is a named
// scalar type: NULL yields a nil pointer, a value yields a freshly
// allocated, converted T.
func decodeNamedScalarPtr(gcv spanner.GenericColumnValue, dst reflect.Value, base reflect.Type) error {
	basePtr := reflect.New(reflect.PointerTo(base)) // **base
	if err := gcv.Decode(basePtr.Interface()); err != nil {
		return err
	}
	decoded := basePtr.Elem() // *base
	if decoded.IsNil() {
		dst.SetZero()
		return nil
	}
	out := reflect.New(dst.Type().Elem()) // *T
	out.Elem().Set(decoded.Elem().Convert(dst.Type().Elem()))
	dst.Set(out)
	return nil
}

// decodeStructSlice decodes ARRAY<STRUCT> into a []T destination by
// delegating to the client's []*T decoding and dereferencing the elements.
func decodeStructSlice(gcv spanner.GenericColumnValue, dst reflect.Value) error {
	elemType := dst.Type().Elem()
	ptrSlice := reflect.New(reflect.SliceOf(reflect.PointerTo(elemType)))
	if err := gcv.Decode(ptrSlice.Interface()); err != nil {
		return err
	}
	src := ptrSlice.Elem()
	if src.IsNil() {
		dst.SetZero()
		return nil
	}
	out := reflect.MakeSlice(dst.Type(), src.Len(), src.Len())
	for i := 0; i < src.Len(); i++ {
		p := src.Index(i)
		if p.IsNil() {
			return fmt.Errorf("%w: element %d", ErrNullStructElement, i)
		}
		out.Index(i).Set(p.Elem())
	}
	dst.Set(out)
	return nil
}

// isNullValue reports whether the GCV holds a protobuf NullValue (SQL NULL).
func isNullValue(gcv spanner.GenericColumnValue) bool {
	_, ok := gcv.Value.GetKind().(*structpb.Value_NullValue)
	return ok
}
