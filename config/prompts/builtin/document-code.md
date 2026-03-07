---
name: "Document Code"
description: "Add inline documentation and comments to the code we just wrote"
group: "Documentation"
backgroundColor: "#B39DDB"
---

<investigate_before_answering>
Before adding documentation, read the code we wrote to understand its behavior,
edge cases, and non-obvious design decisions. Also check the project's existing
documentation style by reviewing similar files.
</investigate_before_answering>

<task>
Add inline documentation and comments to the code we just wrote.
</task>

<scope>
Only document code that was actually changed or created. Do not add docstrings,
comments, or type annotations to code that was not modified.
</scope>

<instructions>

### Add:

1. **Package/module docs**: Purpose, main types, usage examples
2. **Function/method docs**: What it does, parameters, return values, errors
3. **Complex logic comments**: Explain non-obvious algorithms or business rules
4. **TODO/FIXME**: Mark known limitations or future improvements
5. **Examples**: Usage examples for public APIs

### Guidelines:

- Explain WHY, not just WHAT — the code already shows what it does
- Keep comments close to the code they describe
- Update existing comments if code changed
- Use the documentation style standard for this language/project
- Skip obvious code — avoid comments like `// increment i` for `i++`

</instructions>
