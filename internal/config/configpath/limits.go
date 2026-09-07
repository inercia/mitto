package configpath

// Limits bound parser input to keep it fuzz-safe: no panics and no
// unbounded allocation regardless of adversarial input.
const (
	// MaxRawInputBytes bounds a single raw --set/--set-string/--set-json
	// flag value. Chosen well above any realistic settings.json value while
	// staying small enough that pathological nested-bracket inputs cannot
	// produce meaningful recursion depth.
	MaxRawInputBytes = 16 * 1024

	// MaxFileValueBytes bounds a --set-file value that the caller already
	// read from disk/stdin and hands to the parser verbatim.
	MaxFileValueBytes = 1 * 1024 * 1024

	// MaxPathDepth bounds the number of segments (map keys + array indices)
	// in one path.
	MaxPathDepth = 32

	// MaxKeySegmentLength bounds one map-key segment's length.
	MaxKeySegmentLength = 256

	// MaxArrayIndex bounds an accepted array index value, rejecting sparse
	// or huge indexes that would otherwise force large slice allocations
	// downstream.
	MaxArrayIndex = 10000

	// MaxListLength bounds elements accepted in a brace list (`{a,b,c}`) or
	// a JSON array.
	MaxListLength = 1000

	// MaxJSONObjectKeys bounds keys accepted in one JSON object.
	MaxJSONObjectKeys = 1000

	// MaxJSONNestingDepth bounds nested `{}`/`[]` levels in a --set-json
	// value.
	MaxJSONNestingDepth = 32

	// MaxOperations bounds the number of assignments accepted from one
	// raw flag value, and the number resolved into one OpSet.
	MaxOperations = 500
)
