// Extract the body of a `const NAME = useCallback(...)` arrow function from
// source text, so hook tests can execute the real callback with stubbed
// bindings instead of importing the whole browser hook.
//
// The callback's dependency array is deliberately NOT part of the extracted
// source: it is an implementation detail that changes whenever the hook gains
// or loses a dependency. Scanning for the matching brace keeps these tests
// working across such refactors, which a literal `}, []);` search does not.

export function useCallbackSource(source, name) {
  const marker = `const ${name} = useCallback(`;
  const start = source.indexOf(marker);
  if (start === -1) throw new Error(`useCallback ${name} not found in source`);
  const from = start + marker.length;
  const arrow = source.indexOf("=>", from);
  const open = arrow === -1 ? -1 : source.indexOf("{", arrow);
  if (open === -1) {
    throw new Error(`useCallback ${name} has no block-bodied arrow function`);
  }
  return source.slice(from, endOfBlock(source, open) + 1);
}

// Index of the `}` matching the block opened at `open`.
function endOfBlock(source, open) {
  let depth = 0;
  for (let i = open; i < source.length; i++) {
    const char = source[i];
    if (char === '"' || char === "'" || char === "`") {
      i = endOfQuoted(source, i);
    } else if (char === "/" && source[i + 1] === "/") {
      i = source.indexOf("\n", i);
      if (i === -1) break;
    } else if (char === "/" && source[i + 1] === "*") {
      i = source.indexOf("*/", i + 2);
      if (i === -1) break;
      i += 1;
    } else if (char === "{") {
      depth += 1;
    } else if (char === "}") {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  throw new Error("unbalanced callback body");
}

// Index of the quote closing the string/template literal at `start`.
function endOfQuoted(source, start) {
  const quote = source[start];
  for (let i = start + 1; i < source.length; i++) {
    const char = source[i];
    if (char === "\\") {
      i += 1;
    } else if (quote === "`" && char === "$" && source[i + 1] === "{") {
      i = endOfBlock(source, i + 1);
    } else if (char === quote) {
      return i;
    }
  }
  throw new Error("unterminated string in callback source");
}
