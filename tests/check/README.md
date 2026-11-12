# Type checking tests

- `ok/*.navi` must type-check without errors. `// type: T` on a line with a
  `let name` checks the inferred type of the first binding on that line.
- `err/*.navi`: `// error: text` expects an error on that line containing
  `text`; every error must be expected.
