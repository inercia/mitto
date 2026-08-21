# Streaming Benchmark Corpus

This document simulates a long agent answer: prose, nested lists, fenced
code, a table and a block quote, repeated to reach a realistic streaming
length for the mitto-pscc.8.1 benchmark.

Understanding the request required reading through several files before any
change could be made safely. The surrounding code follows a consistent style
that should be preserved rather than reworked wholesale, since unrelated
refactors make a review harder to reason about and increase the risk of
regressions that have nothing to do with the actual task at hand.

## Plan

1. Read the existing implementation end to end.
2. Identify the smallest coherent change that satisfies the request.
3. Write the change, matching existing conventions.
4. Verify with the existing test suite plus any new coverage needed.

The following snippet shows the shape of the change:

```go
func Example() string {
	// A representative code block, long enough to matter for wrapping and
	// syntax highlighting cost, but not so long that the corpus becomes
	// unwieldy to read in a diff.
	values := []int{1, 2, 3, 4, 5}
	total := 0
	for _, v := range values {
		total += v
	}
	return fmt.Sprintf("total=%d", total)
}
```

A short table summarises the before/after behavior:

| Aspect        | Before                          | After                         |
|---------------|----------------------------------|-------------------------------|
| Re-render     | Whole message on every chunk     | Only the trailing partial     |
| Complexity    | O(n^2) over a long answer        | Amortized O(n)                |
| Correctness   | Always correct (baseline)        | Falls back to full render     |

> Note: any construct where the streamed result cannot be proven identical
> to a full render should fall back to a full render rather than accept a
> visual regression for the sake of speed.

Once the change lands, a short follow-up paragraph explains any deviations
from the original plan and why they were necessary, so a reviewer does not
have to reconstruct that reasoning from the diff alone. This paragraph is
deliberately long-ish, wrapping across multiple terminal-width lines when
rendered, to exercise glamour's word-wrap logic the same way a real
multi-sentence explanation from an agent would.

- First follow-up item: confirm the build is clean.
- Second follow-up item: confirm the relevant tests pass.
- Third follow-up item: confirm no unrelated files changed.

Finally, a closing paragraph restates the outcome in one or two sentences,
giving the reader a clear signal that the answer is complete and does not
require any further action before moving on to review.
