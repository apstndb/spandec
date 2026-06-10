# spandec

[![Go Reference](https://pkg.go.dev/badge/github.com/apstndb/spandec.svg)](https://pkg.go.dev/github.com/apstndb/spandec)

Decode Cloud Spanner values into Go values like the official client library
(`GenericColumnValue.Decode` / `Row.ToStruct`), **extended to cover
destination shapes the client rejects today**:

| Extension | Upstream gap |
|-----------|--------------|
| `*T` fields where `T` is a named scalar type (`type CustomString string`) | [googleapis/google-cloud-go#12576](https://github.com/googleapis/google-cloud-go/issues/12576) |
| `[]T` struct slices for `ARRAY<STRUCT>` (not only `[]*T`) | [googleapis/google-cloud-go#11090](https://github.com/googleapis/google-cloud-go/issues/11090) |
| `json.RawMessage` destinations for JSON columns | [googleapis/google-cloud-go#10720](https://github.com/googleapis/google-cloud-go/issues/10720) |

Everything else delegates to the client's own decoding, so behavior stays
identical where the client already works — spandec is a thin extension
layer, not a re-implementation.

```go
type Row struct {
    ID      int64           `spanner:"Id"`
    Display *CustomString   // nil on NULL (client rejects this field type)
    Payload json.RawMessage `spanner:"payload"` // raw JSON wire text
}

var r Row
err := spandec.ToStruct(row, &r) // client tag semantics via structfields/spannertag
```

- `Decode(gcv, ptr)` — one value; `ToStruct(row, ptr)` — one row with the
  client's `spanner` tag field listing (exact match first, then
  case-insensitive; unmatched columns and duplicates error, mirroring
  `Row.ToStruct` strictness).
- `[]T` decoding cannot represent NULL STRUCT elements; it returns
  `ErrNullStructElement` (use `[]*T` for nullable elements).
- A test pins each upstream gap: when a client release fixes one natively,
  the corresponding extension can be retired.

The companion encode-direction package is
[spanenc](https://github.com/apstndb/spanenc); struct tag semantics come
from [structfields](https://github.com/apstndb/structfields).

**Status: experimental.**

## License

MIT
