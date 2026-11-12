# Type tests

- `sigs/*.navi`: a line with `// sig: text` declares an item whose collected
  signature must render exactly as `text`. The files must have no errors.
- `err/*.navi`: `// error: text` on a line expects an error there whose
  message contains `text`; every error must be expected.
