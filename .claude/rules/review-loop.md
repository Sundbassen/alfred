# Review loop: every implementation gets an independent review

After implementing a change, and before opening or updating its PR, spawn a
**new review agent** to review it. Iterate with it up to three times. Major
findings stop the work and go to the user. This applies to each PR of a
stack, reviewed before that PR is opened.

## The loop

1. **Implement** the change and run the checks (`make vet test layers build`,
   plus `sqlc-diff`/`proto-diff` where relevant).
2. **Review:** spawn a fresh agent, not a fork, so it reads the code without
   the implementation's assumptions. Give it the diff (`git diff <base>...HEAD`),
   the goal of the change, and the review brief below.
3. **Act on the findings:**
   - **Only minor findings** (see below): fix them, rerun the checks, and spawn
     a new review of the updated diff. Repeat **at most 3 review rounds in total**.
   - **Any major finding:** **stop.** Don't fix it and don't open the PR.
     Present the findings to the user, with the reviewer's reasoning and your
     view, so you can plan what to do together.
   - **No findings:** done; open or update the PR.
4. **Still finding minor issues after 3 rounds:** stop and present what's left
   to the user instead of looping further.
5. **In the PR description,** add a short "Review" note: how many rounds, and
   what was fixed.

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

> Review this diff for: **[goal of the change]**. Read the changed files in
> full, plus enough of the surrounding code to judge them. Check:
>
> 1. **Correctness:** bugs, edge cases, error handling, concurrency (races,
>    leaks, goroutine lifetimes), resource cleanup, and behaviour on failure.
> 2. **The repository's established architecture:** `.claude/rules/project-patterns.md`
>    (layers and the dependency rule, what goes where, interfaces with their
>    consumer, providers vs. `<skill>store`, wiring as the composition root,
>    follow-up questions, configuration through `wiring.ConfigFromEnv`) and
>    `.claude/rules/go-errors.md` (tozd errors, categories in `fault`). Compare
>    with how existing code solves the same kind of problem.
> 3. **Idiomatic Go:** Effective Go and the Go Code Review Comments. For
>    example:
>    - small interfaces defined where they're used; accept interfaces, return
>      concrete types;
>    - `context.Context` as the first parameter, never stored in structs;
>    - errors wrapped with context, not logged and returned twice;
>    - no needless abstraction, globals or `init()`;
>    - zero values that work;
>    - clear names without stutter;
>    - table-driven tests where they fit;
>    - doc comments on exported identifiers;
>    - standard library before dependencies.
> 4. **Tests:** whether they cover the behaviour and its failure modes, and
>    whether they'd catch a regression.
> 5. **Docs:** README, `project-patterns.md` and comments kept in step.
>
> Report each finding with file:line, what's wrong, why, and a suggested fix,
> classified **minor** or **major** by the table in `.claude/rules/review-loop.md`.
> Say plainly if you find nothing. Don't edit files.
