# Mock the UI/UX Before You Write Code

Any change that affects how output is rendered to the user — a new column, a new annotation, a new table, a reformatted line — must be mocked up with representative real-world data BEFORE any implementation code is written. This is not optional and the mock is not "write the code and then look at it."

A mock is:

1. A plain-text block showing what the new format looks like with 3-5 rows of realistic data (not contrived short examples). For table changes, pick rows from an actual inbox, contact book, or MIME structure — whatever matches the command. Include worst-case content: long display names, long addresses, wide annotations, truncated subjects.
2. A column-by-column width calculation against the 80-character budget, including the 3-char row prefix and 2-char separators. Show the math.
3. A side-by-side with the current format so the regression surface is visible at a glance.

The mock is reviewed with the CEO (or the person who will see the output) BEFORE the implementation ticket is claimed. If the mock shows the row overflows 80 chars, or the variable column gets crushed to its minimum, the design is wrong and the code is not written yet — the design is revised until the mock fits the budget.

This rule exists because beadle-0he (EMAIL column) and beadle-z34 ((via X) annotation) were implemented and shipped without either being rendered against a real inbox first. The combination blew the 80-col budget by 10 characters and crushed the SUBJECT column to a 10-char stub that showed "Re: [pun..." for every GitHub PR notification — making the output useless for the exact workflow it was meant to serve. Caught only after 82 notifications were listed in a demo. Cost: both PRs merged, a release bundle shipped, and then the regression discovered post-merge.

Table changes specifically: compute `row_width = prefix_len + sum(col_widths) + sep_len * (n_cols - 1)` using `column.minWidth` for fixed columns and the widest realistic content for variable columns. If `row_width > 80`, stop and redesign. Do not commit tests that only validate "the substring appears in the output" — write tests that assert `len(line) <= 80` on realistic inputs.
