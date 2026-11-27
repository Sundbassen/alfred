# Review loop: every implementation gets an independent review

After implementing a change, and before opening or updating its PR, have it reviewed by a **new review agent**, in up to three review rounds, each by a fresh agent (round 1 is the first review). Major findings stop that PR and go to the user. This applies to every change, docs and rules included, and to each PR of a stack.

## The loop

1. **Implement** the change and run the checks in `backend/`: `make vet test layers build`, plus `make sqlc-diff` and `make proto-diff` when generated code changes. **Commit** the change, and later each round's fixes, before spawning the reviewer, so the diff it reads is complete.
2. **Review:** spawn a fresh agent, not a fork, so it reads the code without the implementation's assumptions. Give it the diff (`git diff <base>...HEAD`), the goal of the change, what earlier rounds found and fixed, and the review brief below.
3. **Act on the findings:**
   - **Only minor findings** (see below): fix them, rerun the checks, commit, and spawn a new review of the updated diff, up to **3 review rounds in total**.
   - **Any major finding:** **stop** that PR. Don't fix the major finding, and don't open or update the PR. Present all findings from the round, minor ones included, to the user with the reviewer's reasoning and your view, so you can plan what to do together. Other PRs in a stack can carry on where they don't depend on the decision.
   - **No findings:** done; open or update the PR.
4. **After round 3:** fix the minor findings that are clear-cut without a fourth review, open or update the PR, and tell the user (and note in the PR) what was fixed unreviewed and what is left.
5. **In the PR description,** add a short "Review" note: how many rounds, what was fixed, and what was decided with the user.

## Minor vs. major

| Minor: fix and re-review | Major: stop and ask the user |
|---|---|
| A bug with an obvious local fix | A flaw in the design or approach, or a bug whose fix changes the design |
| Naming, comments, doc wording | A layer or dependency-rule violation (`project-patterns.md`) that needs restructuring |
| A missing or weak test for code that's otherwise right | Missing behaviour the plan promised, or scope that differs from the plan |
| Error wrapping or categories per `go-errors.md` | An API or interface that should be shaped differently |
| Small idiom fixes (see the brief) | Concurrency or data-loss risks that need a different structure |
| Formatting, dead code, duplication in one place | Anything the reviewer and you disagree on |

When unsure, treat a finding as **major**.

## Review brief (give this to the review agent)

> Review this diff for: **[goal of the change]**. Read the changed files in full, plus enough of the surrounding code to judge them. Check:
>
> 1. **Correctness:** bugs, edge cases, error handling, concurrency (races, leaks, goroutines without a clear way to stop), resource cleanup, and behaviour on failure.
> 2. **The repository's established architecture:** `.claude/rules/project-patterns.md` (layers and the dependency rule, what goes where, interfaces with their consumer, providers vs. `<skill>store`, wiring as the composition root, configuration through `wiring.ConfigFromEnv`) and `.claude/rules/go-errors.md` (tozd errors, categories in `fault`). Compare with how existing code solves the same kind of problem.
> 3. **Idiomatic Go:** Effective Go and the Go Code Review Comments, plus this repository's conventions. For example:
>    - small interfaces defined where they're used; accept interfaces, return concrete types;
>    - `context.Context` as the first parameter, never stored in structs;
>    - errors handled once: returned with context per `go-errors.md`, or logged, not both; error strings lowercase, without trailing punctuation;
>    - no needless abstraction, globals or `init()`;
>    - zero values that work;
>    - clear names without stutter; short, consistent receiver names;
>    - table-driven tests where they fit;
>    - doc comments on exported identifiers;
>    - the standard library before dependencies.
> 4. **Tests:** whether they cover the behaviour and its failure modes, and whether they'd catch a regression.
> 5. **Docs:** README, `project-patterns.md` and comments kept in step.
>
> Report each finding with file:line, what's wrong, why, and a suggested fix, classified **minor** or **major** by the table in `.claude/rules/review-loop.md`. Say plainly if you find nothing. Don't edit files, commit, push or comment on GitHub.
