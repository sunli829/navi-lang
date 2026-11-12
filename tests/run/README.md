# Run tests (golden tests)

Each `*.navi` file is a program. Lines `// out: text` give the expected
standard output line by line (`// out:` alone is an empty line). `// exit: N`
gives the expected exit code (default 0) and `// stderr: text` requires text
in the standard error output. The same files run on the MIR interpreter and,
from M3 on, as native binaries on every target.
