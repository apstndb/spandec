package spandec

import (
	"fmt"
	"reflect"

	"cloud.google.com/go/spanner"
	"github.com/apstndb/structfields/spannertag"
)

// ToStruct decodes a row into a struct using the client's `spanner` tag
// field listing (via [github.com/apstndb/structfields/spannertag]) and
// [Decode] for each column, so the extension destination shapes from the
// package documentation work as struct fields. Options apply to every
// column; see [WithValueDecoder].
//
// Mirroring [cloud.google.com/go/spanner.Row.ToStruct] strictness: every
// row column must have a corresponding field ([ErrNoMatchingField];
// matching is exact first, then case-insensitive) and duplicate column
// names are an error. Fields without a matching column keep their values.
func ToStruct(row *spanner.Row, ptr any, opts ...DecodeOption) error {
	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("%w: %T", ErrNotStructPointer, ptr)
	}
	dst := rv.Elem()
	fl, err := spannertag.Cache.Fields(dst.Type())
	if err != nil {
		return err
	}
	cfg := newDecodeConfig(opts)
	seen := make(map[string]bool, row.Size())
	for i, name := range row.ColumnNames() {
		if seen[name] {
			return fmt.Errorf("spandec: duplicate column name %q", name)
		}
		seen[name] = true
		f := fl.Match(name)
		if f == nil {
			return fmt.Errorf("%w: %q in %v", ErrNoMatchingField, name, dst.Type())
		}
		var gcv spanner.GenericColumnValue
		if err := row.Column(i, &gcv); err != nil {
			return fmt.Errorf("spandec: column %q: %w", name, err)
		}
		if err := decode(gcv, dst.FieldByIndex(f.Index).Addr().Interface(), cfg); err != nil {
			return fmt.Errorf("spandec: column %q: %w", name, err)
		}
	}
	return nil
}
