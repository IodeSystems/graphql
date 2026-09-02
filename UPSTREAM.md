# Upstream sync ledger

This fork tracks `graphql-go/graphql` (remote `upstream`). Per-commit
decisions live in `upstream.jsonl`, one JSON object per line; this file
carries the procedure, the fork's divergence points, and the open-PR
triage. `bin/upstream` reads the ledger — start there:

```
bin/upstream            # commits upstream has that nobody here has judged
bin/upstream --all      # every commit since the sync point, with its decision
bin/upstream --ledger   # every recorded decision, newest first
bin/upstream --check    # exit 1 if anything is undecided or the ledger is malformed
```

**Last absorbed:** `6acef3563ff762c0f5cd14b759ed2cca405ac8fa` (2026-06-22,
"Merge pull request #753 from graphql-go/ast-definitions-unit-tests"). Not
maintained by hand — `bin/upstream` derives the sync point from
`git merge-base HEAD upstream/master`.

## Sync procedure

```
git fetch upstream
bin/upstream            # the work queue
```

For each commit listed, cherry-pick it (`git cherry-pick -n <sha>`), resolve
against this fork's divergence, and commit with the upstream SHA in a
`(cherry picked from commit <sha>)` trailer so provenance survives the
adaptation. Then record the decision:

```
bin/upstream record <sha> absorbed "what was adapted and why" --local <our-sha>
bin/upstream record <sha> skipped  "why it is not worth taking"
bin/upstream record <sha> net-zero "what it cancels against"
```

Record refusals too. An unrecorded commit is indistinguishable from one
nobody has looked at, so the next sync re-derives the same decision.

Then close the gap so `git rev-list --count HEAD..upstream/master` stays a
real signal:

- If nothing conflicts, `git merge upstream/master`.
- If the content is already absorbed via cherry-pick, `git merge -s ours
  upstream/master`.

### Why `-s ours` needs the ledger

`-s ours` records upstream as an ancestor while taking none of its content.
That is accurate when the content arrived by cherry-pick, but it also means
anything skipped can never return through a later merge — git considers it
already merged. `upstream.jsonl` is the only remaining record, which is why
`bin/upstream` keys off the merge-base rather than `HEAD..upstream/master`:
after the merge that range is empty even for commits whose content was
never taken.

## Fork divergence (what makes cherry-picks conflict)

- Import paths: `github.com/graphql-go/graphql` → `github.com/IodeSystems/graphql-go/v2`.
  Every upstream commit touching an import block conflicts on this alone.
- `plan.go` / `executor.go` carry the append-mode pipeline (`PlanQuery`,
  `ExecutePlan`, `ExecutePlanAppend`, `ResolveAppend`). See `docs/plan.md`.
- `ResolveInfo.Path` and the `ResponsePath` type were removed (682320e), so
  upstream code passing a `path` argument needs it dropped.
- `DefaultResolveFn` resolves struct fields through the cached
  `resolveDefaultStructField` rather than upstream's per-call field scan,
  and follows Go's promotion rules into embedded structs (9e89e97).
- Introspection returns `__schema.types` and `__type.fields` in sorted order.

## Per-commit decisions

In `upstream.jsonl`. Run `bin/upstream --ledger` to read them, or
`bin/upstream --all` to see them against the commits they describe.

## Open upstream issues

Triaged 2026-08-17 against the then-open issues on `graphql-go/graphql`.
Re-check with `gh issue list --repo graphql-go/graphql --state open`.

Already fixed here: `#742` (overlapping-fields stack overflow on cyclic
fragment spreads, `49e909d`), `#699` (multi-byte runes in comments — the
lexer rewrite handles them), `#700` (`DefaultResolveFn` with a custom
string-keyed map type), `#629` (explicit nulls missing from
`ResolveParams.Args`).

Acted on: `#754` (unbounded parser recursion). Triaging it turned up a
separate and more reachable problem — syntax-error formatting was
quadratic in the error's column, so one malformed 80KB single-line query
cost ~2.7s of CPU with no nesting involved. Fixed in `3a8f62e`. The
recursion depth cap the issue actually proposes is **still open here**:
the parser descends one frame per nesting level with no limit, so a
sufficiently deep document can exhaust the goroutine stack, which is a
fatal runtime error that `recover` cannot catch. Adding a cap rejects
documents that parse today, so it needs a decision on the limit and on
whether it is configurable.

Not acted on: `#750` (return partial data alongside errors) reads as a
feature request rather than a defect; the executor already emits partial
data with field errors. The rest are questions, feature requests, or
upstream-workflow issues.

## Open upstream PRs

Triaged 2026-08-17 against the 38 then-open PRs on `graphql-go/graphql`.
Re-check with `gh pr list --repo graphql-go/graphql --state open`; only
PRs opened since that date need a fresh look.

Already in this fork, verified by matching the actual source hunks rather
than the titles — several arrived through our own independent fixes:
`#739` (non-null args with defaults, ours is `36ecd3f`), `#730`
(nullable variable into a defaulted non-null arg), `#737` (custom
string-based map key types), `#717`, `#706` (preserve Extensions),
`#636` (deterministic introspection order), `#631` (explicit-null input
object fields), `#602`, `#605` (multi-byte comments — moot here, the
lexer rewrite already handles them), `#555`, `#550`, `#547` (optional
`=` in union definitions), `#518`, `#465`, `#445`.

Acted on: `#371` — fields promoted from embedded structs resolved to
null. Fixed in `9e89e97`, implemented against Go's promotion rules rather
than cherry-picked; see that commit for why the PR's own version is not
the one to take.

Not worth taking:

- `#696`, `#683`, `#536`, `#401` — four competing null-literal
  implementations, 260-409 lines each, unmerged for years and never
  reconciled with each other. Adopting one means owning that choice.
- `#630` — adds `ResolveInfo.FieldNameAlias` so `DefaultResolveFn` reads
  the source map by alias. An alias is a response key, not a source key;
  this changes which data a field resolves to based on how it was named
  in the query.
- `#639`, `#428` (tracing), `#559` (auto-bind), `#589`, `#552`, `#473`,
  `#475`, `#398`, `#479`, `#277`, `#253` — features and reworks, not
  fixes. Revisit only if we want the capability.
