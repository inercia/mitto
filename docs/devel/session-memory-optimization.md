# Session History Memory Optimization

## Problem

Sessions with 10,000+ events were loading all events into memory when reading the last N events, causing high memory usage and CPU pressure.

## Solution

Implemented an optimized tail-reading approach in `internal/session/store.go` that avoids loading the entire file into memory for large sessions.

### Implementation Details

#### ReadEventsLast Optimization

The `ReadEventsLast()` method now uses two different strategies:

1. **Simple Forward Scan** (for small files or when filtering by `beforeSeq`):
   - Used when file size < 1MB or when `beforeSeq > 0`
   - Reads the entire file sequentially (acceptable for small files)
   - Required for `beforeSeq` filtering since we need to filter while reading

2. **Optimized Tail Reading** (for large files):
   - Used when file size >= 1MB and no `beforeSeq` filtering
   - Reads 256KB chunks from the end of the file backwards
   - Stops as soon as enough events are collected
   - Avoids loading the entire file into memory

#### Key Functions

- `readEventsLastSimple()`: Forward scan approach for small files
- `readEventsLastOptimized()`: Tail-reading approach for large files
- `splitLinesReverse()`: Helper to split chunks into lines in reverse order

### Memory Impact

For a session with 10,000 events (~10MB file):

- **Before**: Loaded all 10,000 events into memory (~10MB+)
- **After**: Loads only the last 200 events (~200KB) plus a few chunks (~512KB max)

**Memory savings**: ~95% reduction for large sessions

### Performance

- **Small files** (<1MB): No change in performance
- **Large files** (>1MB): Significantly faster for reading last N events
  - Only reads the necessary portion of the file
  - Stops as soon as enough events are collected

### Backward Compatibility

- All existing tests pass
- No changes to the API or event format
- Works with existing session files on disk

### Testing

Added `TestStore_ReadEventsLast_LargeFile` to verify:
- Correct number of events returned
- Events in chronological order (oldest first)
- Correct sequence numbers (last N events)
- Filtering with `beforeSeq` still works

## Files Modified

- `internal/session/store.go`: Added optimized tail-reading implementation
- `internal/session/store_test.go`: Added test for large file optimization

## Future Improvements

Potential enhancements for even better memory efficiency:

1. **Memory-mapped files**: Use `mmap` for zero-copy access to event data
2. **Event index**: Build an index of event offsets for O(1) random access
3. **Streaming API**: Add iterator-based API for processing events without loading into memory
4. **Compression**: Compress old events to reduce disk usage and memory footprint

