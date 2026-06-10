package spandec_test

import (
	"encoding/json"
	"errors"
	"testing"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/apstndb/spanvalue/gcvctor"
	"github.com/google/go-cmp/cmp"

	"github.com/apstndb/spandec"
)

type customString string

func ptr[T any](v T) *T { return &v }

func TestDecodeDelegatesToClient(t *testing.T) {
	t.Parallel()

	var s string
	if err := spandec.Decode(gcvctor.StringValue("foo"), &s); err != nil {
		t.Fatal(err)
	}
	if s != "foo" {
		t.Errorf("s = %q, want %q", s, "foo")
	}

	var n spanner.NullInt64
	if err := spandec.Decode(gcvctor.NullFromCode(sppb.TypeCode_INT64), &n); err != nil {
		t.Fatal(err)
	}
	if n.Valid {
		t.Errorf("n = %v, want invalid", n)
	}

	// Named scalar through a single pointer is already client-supported.
	var c customString
	if err := spandec.Decode(gcvctor.StringValue("bar"), &c); err != nil {
		t.Fatal(err)
	}
	if c != "bar" {
		t.Errorf("c = %q, want %q", c, "bar")
	}
}

// TestDecodeNamedScalarPointer covers googleapis/google-cloud-go#12576: a
// *T destination where T is a named scalar type.
func TestDecodeNamedScalarPointer(t *testing.T) {
	t.Parallel()

	t.Run("value", func(t *testing.T) {
		t.Parallel()
		var c *customString
		if err := spandec.Decode(gcvctor.StringValue("foobar"), &c); err != nil {
			t.Fatal(err)
		}
		if c == nil || *c != "foobar" {
			t.Errorf("c = %v, want pointer to %q", c, "foobar")
		}
	})

	t.Run("null yields nil", func(t *testing.T) {
		t.Parallel()
		c := ptr(customString("stale"))
		if err := spandec.Decode(gcvctor.NullFromCode(sppb.TypeCode_STRING), &c); err != nil {
			t.Fatal(err)
		}
		if c != nil {
			t.Errorf("c = %v, want nil", c)
		}
	})

	t.Run("client still fails without spandec", func(t *testing.T) {
		t.Parallel()
		// Pin the upstream gap: if this starts passing, the extension can
		// be retired for the linked client version.
		gcv := gcvctor.StringValue("foobar")
		var c *customString
		if err := gcv.Decode(&c); err == nil {
			t.Log("client now decodes *customString natively; consider retiring the extension")
		}
	})

	t.Run("type mismatch", func(t *testing.T) {
		t.Parallel()
		var c *customString
		if err := spandec.Decode(gcvctor.Int64Value(1), &c); err == nil {
			t.Error("want error decoding INT64 into *customString, got nil")
		}
	})
}

// TestDecodeStructSlice covers googleapis/google-cloud-go#11090: []T struct
// slices for ARRAY<STRUCT>.
func TestDecodeStructSlice(t *testing.T) {
	t.Parallel()

	type entry struct {
		Name  string
		Value int64
	}
	structGCV := func(name string, value int64) spanner.GenericColumnValue {
		return gcvctor.MustStructValueOf([]string{"Name", "Value"}, []spanner.GenericColumnValue{
			gcvctor.StringValue(name), gcvctor.Int64Value(value),
		})
	}
	arr := gcvctor.MustArrayValueOf(structGCV("a", 1).Type, structGCV("Hello", 1000), structGCV("World", 2000))

	t.Run("values", func(t *testing.T) {
		t.Parallel()
		var got []entry
		if err := spandec.Decode(arr, &got); err != nil {
			t.Fatal(err)
		}
		want := []entry{{"Hello", 1000}, {"World", 2000}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("null array yields nil slice", func(t *testing.T) {
		t.Parallel()
		got := []entry{{"stale", 0}}
		if err := spandec.Decode(gcvctor.NullArrayOf(structGCV("a", 1).Type), &got); err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("got = %v, want nil", got)
		}
	})

	t.Run("null element rejected", func(t *testing.T) {
		t.Parallel()
		withNull := gcvctor.MustArrayValueOf(structGCV("a", 1).Type,
			structGCV("Hello", 1000), gcvctor.NullOf(structGCV("a", 1).Type))
		var got []entry
		if err := spandec.Decode(withNull, &got); !errors.Is(err, spandec.ErrNullStructElement) {
			t.Errorf("error = %v, want ErrNullStructElement", err)
		}
	})

	t.Run("pointer slices still delegate to client", func(t *testing.T) {
		t.Parallel()
		var got []*entry
		if err := spandec.Decode(arr, &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] == nil || got[0].Name != "Hello" {
			t.Errorf("got = %v, want 2 decoded pointers", got)
		}
	})
}

// TestDecodeJSONRawMessage covers googleapis/google-cloud-go#10720:
// json.RawMessage destinations for JSON columns.
func TestDecodeJSONRawMessage(t *testing.T) {
	t.Parallel()

	t.Run("value", func(t *testing.T) {
		t.Parallel()
		var raw json.RawMessage
		if err := spandec.Decode(gcvctor.MustJSONValue(map[string]any{"a": 1}), &raw); err != nil {
			t.Fatal(err)
		}
		if string(raw) != `{"a":1}` {
			t.Errorf("raw = %s, want {\"a\":1}", raw)
		}
	})

	t.Run("null yields nil", func(t *testing.T) {
		t.Parallel()
		raw := json.RawMessage(`stale`)
		if err := spandec.Decode(gcvctor.NullFromCode(sppb.TypeCode_JSON), &raw); err != nil {
			t.Fatal(err)
		}
		if raw != nil {
			t.Errorf("raw = %s, want nil", raw)
		}
	})

	t.Run("non-JSON column unaffected", func(t *testing.T) {
		t.Parallel()
		// A STRING column into json.RawMessage keeps the client's behavior
		// (an error), since the extension is scoped to JSON columns.
		var raw json.RawMessage
		if err := spandec.Decode(gcvctor.StringValue("not json column"), &raw); err == nil {
			t.Error("want client error for STRING into json.RawMessage, got nil")
		}
	})
}

func TestToStruct(t *testing.T) {
	t.Parallel()

	type row struct {
		ID      int64 `spanner:"Id"`
		Display *customString
		Payload json.RawMessage `spanner:"payload"`
		Skipped string          `spanner:"-"`
	}

	newRow := func(t *testing.T) *spanner.Row {
		t.Helper()
		r, err := spanner.NewRow(
			[]string{"Id", "Display", "payload"},
			[]any{int64(7), "shown", spanner.NullJSON{Value: map[string]any{"k": "v"}, Valid: true}},
		)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	t.Run("decodes extension fields", func(t *testing.T) {
		t.Parallel()
		var got row
		if err := spandec.ToStruct(newRow(t), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != 7 || got.Display == nil || *got.Display != "shown" {
			t.Errorf("got = %+v, want ID=7 Display=shown", got)
		}
		var payload map[string]any
		if err := json.Unmarshal(got.Payload, &payload); err != nil || payload["k"] != "v" {
			t.Errorf("Payload = %s (err %v), want {\"k\":\"v\"}", got.Payload, err)
		}
	})

	t.Run("case-insensitive fallback", func(t *testing.T) {
		t.Parallel()
		r, err := spanner.NewRow([]string{"id"}, []any{int64(1)})
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			ID int64 `spanner:"Id"`
		}
		if err := spandec.ToStruct(r, &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != 1 {
			t.Errorf("ID = %d, want 1", got.ID)
		}
	})

	t.Run("no matching field", func(t *testing.T) {
		t.Parallel()
		r, err := spanner.NewRow([]string{"Nope"}, []any{int64(1)})
		if err != nil {
			t.Fatal(err)
		}
		var got row
		if err := spandec.ToStruct(r, &got); !errors.Is(err, spandec.ErrNoMatchingField) {
			t.Errorf("error = %v, want ErrNoMatchingField", err)
		}
	})

	t.Run("non-struct destination", func(t *testing.T) {
		t.Parallel()
		var n int
		if err := spandec.ToStruct(newRow(t), &n); !errors.Is(err, spandec.ErrNotStructPointer) {
			t.Errorf("error = %v, want ErrNotStructPointer", err)
		}
	})
}
