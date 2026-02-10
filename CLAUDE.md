# CLAUDE.MD

## Application Architecture (IMPORTANT MUST FOLLOW)

### Dependency Law

No upward imports. No lateral imports within the same layer unless listed above.

```
logic         --> nothing
state         --> nothing
adaptor       --> logic + state
orchestration --> logic + state (interfaces from logic, injected by cmd)
entrypoint    --> orchestration + adaptor
cmd           --> entrypoint + orchestration + adaptor (construction only)
```

- logic — Pure Computation
  - Validation, derivation, decisions. Domain types, interfaces, and parsing.
- state — schemas and records
  - Struct definitions, invariants, constraints, serialization formats, migrations.
- adaptor — Contact with Reality
  - Translate external to internal, internal to external. Adaptors do not own meaning. HTTP handlers are adaptors because they translate HTTP requests/responses to/from domain types and depend on a web server (vendor/IO).
- orchestration — Use-Case Coordination
  - Compose logic and adaptors into use cases. Own timeouts, sequencing, and error policy.
- entrypoint — Wiring and Lifecycle
  - Route wiring, middleware composition, config, lifecycle. Thin layer that wires adaptors to orchestration.
- cmd — Entry Points (Composition Root)
  - Construction and wiring only. No business logic, no conditionals based on config values. As the composition root, cmd may import any layer for wiring but must contain no domain decisions.

## Golang

- Replace code as needed to create a elegant implementation. No backwards compatibility is required.
- Make sure tests and linter passes, otherwise fix the tests. Run tests with -race flag.
- Use Dependency inversion principle and declare local interfaces for dependencies.
- Comment code based on behavior and design constraints, exported methods should have usage examples.
- Consider service boundries and separation of concern. Packages must not have leaky abstractions.
- Add comments to all tests. Explain what it does and motivate why the test should exist.
- Lint and test code before calling done.
  - golangci-lint run --config ./.golangci.yml ./...
  - go test -memprofile=mem.out ./... && go tool pprof -top -alloc_objects mem.out
- Identify hot paths and create selected benchmarks; update BENCH_TRACKER.md with results.
- Profile memory with `go test -bench=. -memprofile=mem.out && go tool pprof -top -alloc_objects mem.out`.
- Clean-up once done, remove unused and temporary code, if you have started background processes make sure they are terminated.
- Check package local documentation for drift. Remove irrelevant, overly verbose and code blobs doom to become outdated from docs.
- Always align Go structs. It's free to implement and often leads to better memory efficiency without changing any logic—only field order needs to be adjusted. https://goperf.dev/01-common-patterns/fields-alignment/
- Use pointers when assigning to Go interfaces.
- Use `go doc` cli to research Go libraries and their API.
- Avoid returning nil values from functions, instead return empty structs or slices.
- Prefer duplicated code over convoluted DRY refactoring.
- Amundsen's Maxim states that when designing a Web API you must treat your data model, object model, resource model and message model as distinct layers.

## Performance Optimization

- When building performant Go applications start with profiling (`go test -bench=. -cpuprofile=cpu.prof -memprofile=mem.out`) to identify bottlenecks before optimizing.
- Focus on reducing allocations in hot paths: use `sync.Pool` for short-lived objects, preallocate slices/maps when size is known, and avoid interface boxing of large structs.
- Always align struct fields from largest to smallest for better memory efficiency. For networking, drain HTTP response bodies, set reasonable timeouts, and use context for cancellation.
- Batch operations when possible and use buffered I/O for repeated small writes. Avoid premature optimization—measure first with benchmarks and profiling, then optimize based on data.

## Tests

- IMPORTANT: ALWAYS test the actual implementation through imports. NEVER duplicate the implementation
- Each test has a clear, descriptive name describing the behavior
- Each test verifies one specific behavior
- Tests only public interfaces (no unexported fields/methods)
- No tests for standard library behavior (JSON marshaling, etc.)
- No tests for unexported helper functions
- Minimal assertions per test (ideally 1-2)
- Common setup extracted to helper functions marked with `t.Helper()`
- Test mocks/doubles moved to end of file or separate file
- All tests still pass after refactoring


## Design for metastability (down-but-up states)

- Do recognize the pattern: a trigger pushes the system into a low-goodput state sustained by feedback loops (e.g., work amplification). Plan explicit "kicks" (load drops, resets) to exit that basin.
- Do identify and track a characteristic metric per risk (e.g., queueing delay, timeout rate, cache hit rate) that rises with the trigger and only normalizes after recovery.
- Avoid fixating on triggers (the spark) instead of the sustaining loop (the fuel). Your remedies should weaken or break the loop.

## Rules

- Always print CLI commands on a single line, avoid \\n.
- Read README.md of every folder worked within.
- Use go doc to get a glimpse of the codebase, prefer it over grep, eg `go doc --all`.
