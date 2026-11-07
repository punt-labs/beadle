# Exploratory Testing

Investigating a product by using it, with the expected outcome written
down before the run.

- Test as questioning: each test is a question whose answer changes the next test
- Oracle first: decide how you would recognize failure before you look
- Drive the real entry point (CLI, MCP tool, daemon), never only the test suite
- Standard heuristics, applied by name: boundary, invalid input, missing dependency,
  interrupted operation, stale state, concurrent use, empty case, large case
- Risk-ordered coverage: trust boundaries, credential paths, first-run journeys first
- Reproduction as the unit of a bug report: steps, input, actual, expected, delta
- Scope is part of every finding: "no problems in X, Y, Z" — never bare "no problems"
- Verified / inferred / unknown kept separate in every report
