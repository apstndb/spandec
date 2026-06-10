# Agent instructions for `spandec`

Go library (**MIT**): decode-direction companion to
[spanenc](https://github.com/apstndb/spanenc). Decodes GCVs/Rows like the
client (`GenericColumnValue.Decode` / `Row.ToStruct`) but extended for
destination shapes the client rejects; everything else DELEGATES to the
client — this is an extension layer, not a re-implementation. Struct field
listing via `structfields/spannertag` (upstream-derived code lives there).

## Extensions (each tied to an upstream issue)

| Shape | Upstream |
|-------|----------|
| `*T` named-scalar pointers (string/int64/bool/float64/float32 kinds) | googleapis/google-cloud-go#12576 |
| `[]T` struct slices for ARRAY<STRUCT> (`ErrNullStructElement` on NULL elements) | #11090 |
| `json.RawMessage` for JSON columns (scoped by Type code) | #10720 |

Rules: only intercept shapes the client cannot handle (`decodeExtended`
returns false → client path). Tests pin each upstream gap with a
"client still fails" subtest — when a client release fixes one natively,
retire the extension for that version. ToStruct mirrors client strictness
(unmatched column / duplicate column = error; exact then case-insensitive
matching via fields.List.Match).

## Commands

`mise.toml` owns tasks/tools; prefer `mise run check`; Makefile delegates.

## Conventions

Versioning: stay on v0; breaking = minor, otherwise patch. GitHub Releases
are the per-version truth; never re-tag. English only on github.com.
