package spandec_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/apstndb/spantype/typector"
	"github.com/apstndb/spanvalue/gcvctor"
	"github.com/google/go-cmp/cmp"

	"github.com/apstndb/spandec"
)

// int64ToUint32 is the canonical use case: a built-in Go destination the
// client's decoding does not support, with a range check.
func int64ToUint32(gcv spanner.GenericColumnValue, dst *uint32) error {
	var v int64
	if err := gcv.Decode(&v); err != nil {
		return err
	}
	if v < 0 || v > math.MaxUint32 {
		return fmt.Errorf("INT64 %d out of uint32 range", v)
	}
	*dst = uint32(v)
	return nil
}

func TestWithValueDecoder(t *testing.T) {
	t.Parallel()

	t.Run("uint32 destination", func(t *testing.T) {
		t.Parallel()
		var got uint32
		if err := spandec.Decode(gcvctor.Int64Value(7), &got, spandec.WithValueDecoder(int64ToUint32)); err != nil {
			t.Fatal(err)
		}
		if got != 7 {
			t.Errorf("got = %d, want 7", got)
		}
	})

	t.Run("range error propagates", func(t *testing.T) {
		t.Parallel()
		var got uint32
		err := spandec.Decode(gcvctor.Int64Value(-1), &got, spandec.WithValueDecoder(int64ToUint32))
		if err == nil || !strings.Contains(err.Error(), "out of uint32 range") {
			t.Errorf("error = %v, want range error", err)
		}
	})

	t.Run("time.Duration destination", func(t *testing.T) {
		t.Parallel()
		var got time.Duration
		err := spandec.Decode(gcvctor.Int64Value(2e9), &got, spandec.WithValueDecoder(func(gcv spanner.GenericColumnValue, dst *time.Duration) error {
			var v int64
			if err := gcv.Decode(&v); err != nil {
				return err
			}
			*dst = time.Duration(v)
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got != 2*time.Second {
			t.Errorf("got = %v, want 2s", got)
		}
	})

	t.Run("overrides the extension shape for json.RawMessage", func(t *testing.T) {
		t.Parallel()
		var got json.RawMessage
		err := spandec.Decode(gcvctor.MustJSONValue(`{"a":1}`), &got, spandec.WithValueDecoder(func(spanner.GenericColumnValue, *json.RawMessage) error {
			return errors.New("registered decoder wins")
		}))
		if err == nil || err.Error() != "registered decoder wins" {
			t.Errorf("error = %v, want registered decoder error", err)
		}
	})

	t.Run("fallthrough defers to built-in decoding", func(t *testing.T) {
		t.Parallel()
		var got string
		err := spandec.Decode(gcvctor.StringValue("plain"), &got, spandec.WithValueDecoder(func(gcv spanner.GenericColumnValue, dst *string) error {
			if gcv.Type.GetCode() == sppb.TypeCode_JSON {
				*dst = "json:" + gcv.Value.GetStringValue()
				return nil
			}
			return spandec.ErrFallthrough
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got != "plain" {
			t.Errorf("got = %q, want %q", got, "plain")
		}
	})

	t.Run("last registration wins", func(t *testing.T) {
		t.Parallel()
		var got uint32
		err := spandec.Decode(gcvctor.Int64Value(1), &got,
			spandec.WithValueDecoder(func(spanner.GenericColumnValue, *uint32) error {
				return errors.New("first")
			}),
			spandec.WithValueDecoder(int64ToUint32),
		)
		if err != nil {
			t.Fatal(err)
		}
		if got != 1 {
			t.Errorf("got = %d, want 1", got)
		}
	})

	t.Run("interface destination panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("WithValueDecoder[error]: want panic, got none")
			}
		}()
		var got string
		_ = spandec.Decode(gcvctor.StringValue("x"), &got, spandec.WithValueDecoder(func(spanner.GenericColumnValue, *error) error {
			return nil
		}))
	})
}

func TestWithValueDecoderSlice(t *testing.T) {
	t.Parallel()

	arr := func(vs ...int64) spanner.GenericColumnValue {
		elems := make([]spanner.GenericColumnValue, len(vs))
		for i, v := range vs {
			elems[i] = gcvctor.Int64Value(v)
		}
		return gcvctor.MustArrayValueOf(typector.Int64(), elems...)
	}

	t.Run("elements decode through the registration", func(t *testing.T) {
		t.Parallel()
		var got []uint32
		if err := spandec.Decode(arr(1, 2), &got, spandec.WithValueDecoder(int64ToUint32)); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]uint32{1, 2}, got); diff != "" {
			t.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("NULL ARRAY yields nil slice", func(t *testing.T) {
		t.Parallel()
		got := []uint32{9}
		if err := spandec.Decode(gcvctor.NullArrayOf(typector.Int64()), &got, spandec.WithValueDecoder(int64ToUint32)); err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
	})

	t.Run("element error carries the index", func(t *testing.T) {
		t.Parallel()
		var got []uint32
		err := spandec.Decode(arr(1, -1), &got, spandec.WithValueDecoder(int64ToUint32))
		if err == nil || !strings.Contains(err.Error(), "element 1") {
			t.Errorf("error = %v, want element 1 error", err)
		}
	})
}

func TestToStructWithValueDecoder(t *testing.T) {
	t.Parallel()

	type row struct {
		Name  string `spanner:"name"`
		Count uint32 `spanner:"count"`
	}
	r, err := spanner.NewRow([]string{"name", "count"}, []any{
		gcvctor.StringValue("x"),
		gcvctor.Int64Value(42),
	})
	if err != nil {
		t.Fatal(err)
	}

	var got row
	if err := spandec.ToStruct(r, &got, spandec.WithValueDecoder(int64ToUint32)); err != nil {
		t.Fatal(err)
	}
	if got.Name != "x" || got.Count != 42 {
		t.Errorf("got = %+v, want {x 42}", got)
	}

	// Without the option the uint32 field fails in the client decode.
	var bare row
	if err := spandec.ToStruct(r, &bare); err == nil {
		t.Error("ToStruct without options: want error for uint32 field, got nil")
	}
}
