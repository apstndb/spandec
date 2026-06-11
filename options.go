package spandec

import (
	"errors"
	"fmt"
	"reflect"

	"cloud.google.com/go/spanner"
)

// ErrFallthrough is returned by a decoder registered with
// [WithValueDecoder] to defer the value to the built-in decoding (the
// extension shapes of this package, then the client's Decode) — the same
// contract as spanenc's WithValueEncoder and spanvalue's complex format
// plugins. It is never returned by this package's own functions.
var ErrFallthrough = errors.New("spandec: fallthrough to built-in decoding")

// DecodeOption configures [Decode] and [ToStruct].
type DecodeOption func(*decodeConfig)

type decodeConfig struct {
	valueDecoders map[reflect.Type]func(spanner.GenericColumnValue, any) error
}

func newDecodeConfig(opts []DecodeOption) decodeConfig {
	var cfg decodeConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithValueDecoder registers f as the decoder for destinations of exactly
// type *T, consulted BEFORE the built-in decoding: with it a call can decode
// into Go types the client does not support (uint32, [time.Duration],
// external types that cannot implement [spanner.Decoder]) or override
// built-in handling, including this package's own extension shapes.
// Returning [ErrFallthrough] from f defers the value to the built-in
// decoding. The last registration for a given T wins, following the usual
// functional-options convention.
//
// A registered decoder for T also applies per element when decoding an
// ARRAY-typed value into *[]T: a SQL NULL ARRAY yields a nil slice, and each
// element is decoded as if passed to [Decode] individually.
//
// Matching is by exact destination type only; T must not be an interface
// type (the returned option panics when applied).
func WithValueDecoder[T any](f func(spanner.GenericColumnValue, *T) error) DecodeOption {
	t := reflect.TypeFor[T]()
	return func(cfg *decodeConfig) {
		if t.Kind() == reflect.Interface {
			panic(fmt.Sprintf("spandec.WithValueDecoder: interface type %v cannot match a destination type; register concrete types instead", t))
		}
		if cfg.valueDecoders == nil {
			cfg.valueDecoders = make(map[reflect.Type]func(spanner.GenericColumnValue, any) error)
		}
		cfg.valueDecoders[t] = func(gcv spanner.GenericColumnValue, ptr any) error {
			return f(gcv, ptr.(*T))
		}
	}
}
