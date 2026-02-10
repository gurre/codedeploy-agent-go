# Benchmark Tracker

Platform: Apple M4 Pro, darwin/arm64, Go 1.25.2

## 2026-02-10 Transport-layer proxy refactor benchmarks

Changes: Refactored proxy support from shared `*http.Client` to shared `http.RoundTripper`. Each adaptor now builds its own `*http.Client` with adaptor-specific timeout (codedeployctl=80s, githubdownload=60s, imds=10s). Struct field alignment on `codedeployctl.Client`.

### adaptor/codedeployctl

| Benchmark          | ops   | ns/op | B/op  | allocs/op |
| ------------------ | ----- | ----- | ----- | --------- |
| BenchmarkDoRequest | 24860 | 47884 | 18734 | 198       |

No regression. Allocs unchanged (198). B/op slightly lower from struct alignment (-54 B/op).

## 2026-02-10 Log rotation, proxy, version header benchmarks

Changes: Added `adaptor/logfile` rotating writer, proxy URI support on all HTTP clients, agent version header on CodeDeploy Commands requests, IMDS retry on startup.

### adaptor/logfile

| Benchmark      | ops     | ns/op | B/op | allocs/op |
| -------------- | ------- | ----- | ---- | --------- |
| BenchmarkWrite | 1207160 | 971.3 | 0    | 0         |

Zero allocations on the Write hot path. Rotation allocates via `fmt.Sprintf` for file naming only when triggered (~1 per maxBytes written). Mutex contention is the dominant cost.

Memory profile: all allocations from `rotate()` file naming and runtime infrastructure, zero from `Write()`.

### adaptor/codedeployctl

| Benchmark          | ops   | ns/op | B/op  | allocs/op |
| ------------------ | ----- | ----- | ----- | --------- |
| BenchmarkDoRequest | 24568 | 47545 | 18788 | 198       |

+7 allocs vs previous (191→198) from version header `req.Header.Set` and `runtime/debug.ReadBuildInfo` at construction time. Negligible per-request overhead.

## 2026-02-10 Retry storm prevention benchmarks

Changes: Added `logic/backoff` package with jittered exponential backoff. Pure computation — no I/O.

### logic/backoff

| Benchmark         | ops       | ns/op | B/op | allocs/op |
| ----------------- | --------- | ----- | ---- | --------- |
| BenchmarkDuration | 257761969 | 4.508 | 0    | 0         |

Zero allocations. Single `rand.Int64N` call + bit shift. Called on every poll error so allocation-free is important.

Memory profile: all allocations from `runtime.allocm` (goroutine scheduling), zero from `backoff.Duration`.

## 2026-02-10 Self-install reconciliation benchmarks

Changes: Added `logic/selfinstall` package with declarative reconciliation for the `codedeploy-install` binary. Pure computation — no I/O.

### logic/selfinstall

| Benchmark                        | ops      | ns/op | B/op | allocs/op |
| -------------------------------- | -------- | ----- | ---- | --------- |
| BenchmarkReconcile_CleanSystem   | 16447640 | 72.65 | 512  | 1         |
| BenchmarkReconcile_FullyInstalled| 13185212 | 93.54 | 512  | 1         |

Single allocation (pre-sized step slice). Clean-system case is faster because it skips fewer map lookups on the empty `DirsExist` map. Fully-installed case checks all map entries and evaluates more branches.

Memory profile: 100% of allocations from `Reconcile` — the `make([]Step, 0, N)` slice. No optimization needed.

## 2026-02-09 Post-feature-parity benchmarks

Changes: deployment-logs directory creation, per-deployment logs dir, script log writes during hook execution. S3 URL parsing, custom event hook mapping, appspec fallback fix.

### orchestration/executor

| Benchmark                   | ops    | ns/op | B/op | allocs/op |
| --------------------------- | ------ | ----- | ---- | --------- |
| BenchmarkExecuteHook        | 175724 | 6272  | 2256 | 20        |
| BenchmarkCleanupOldArchives | 81985  | 14702 | 3032 | 29        |

Hook dispatch: 20 allocs unchanged. Script log writes add I/O overhead (~3% ns/op) but zero additional allocations in the hot path.
Cleanup: unchanged.

### logic/appspec

| Benchmark                         | ops      | ns/op | B/op  | allocs/op |
| --------------------------------- | -------- | ----- | ----- | --------- |
| BenchmarkParse                    | 77019    | 15499 | 19302 | 288       |
| BenchmarkPermissionMatchesPattern | 48106636 | 24.55 | 16    | 1         |

FindAppSpecFile custom-filename-no-fallback change is zero-cost on the parse benchmark path.

## 2026-02-09 Post-optimization benchmarks

Changes: struct alignment, pre-sized maps/slices, cached os.Stat in sort, buffered cleanup writes, `io.LimitReader` on response bodies, reversed `fillMissingAncestors` allocation pattern.

### orchestration/executor

| Benchmark                   | ops    | ns/op | B/op | allocs/op |
| --------------------------- | ------ | ----- | ---- | --------- |
| BenchmarkExecuteHook        | 193725 | 6068  | 2256 | 20        |
| BenchmarkCleanupOldArchives | 83468  | 14865 | 3032 | 29        |

Hook dispatch: 20 allocs dominated by env map construction and pointer file reads.
Cleanup: os.Stat cached before sort eliminates N² stat calls.

### orchestration/installer

| Benchmark        | ops  | ns/op  | B/op  | allocs/op |
| ---------------- | ---- | ------ | ----- | --------- |
| BenchmarkInstall | 3340 | 359016 | 94876 | 544       |

50-file install with buffered cleanup writes and reversed `fillMissingAncestors`.

### orchestration/hookrunner

| Benchmark    | ops   | ns/op | B/op  | allocs/op |
| ------------ | ----- | ----- | ----- | --------- |
| BenchmarkRun | 51605 | 22027 | 15155 | 172       |

Appspec parse (172 allocs) dominated by `yaml.v3` (78% of allocations). Pre-sized `buildEnv` map avoids 1 runtime grow.

### adaptor/codedeployctl

| Benchmark          | ops   | ns/op | B/op  | allocs/op |
| ------------------ | ----- | ----- | ----- | --------- |
| BenchmarkDoRequest | 25966 | 46406 | 17862 | 191       |

Full round-trip: JSON marshal + SigV4 sign + HTTP + unmarshal. Response body reads capped at 4 MB via `io.LimitReader`.

### logic/appspec

| Benchmark                         | ops      | ns/op | B/op  | allocs/op |
| --------------------------------- | -------- | ----- | ----- | --------- |
| BenchmarkParse                    | 72365    | 16199 | 19301 | 288       |
| BenchmarkPermissionMatchesPattern | 48272401 | 24.63 | 16    | 1         |

Top allocation source: `yaml.v3` parsing dominates (78% YAML internal, 22% appspec logic). `Script` struct field reorder (Timeout before Sudo) saves 8 bytes padding per instance.

### logic/instruction

| Benchmark                                  | ops    | ns/op | B/op  | allocs/op |
| ------------------------------------------ | ------ | ----- | ----- | --------- |
| BenchmarkBuilderCopyHeavy (100 files)      | 80305  | 14971 | 45380 | 216       |
| BenchmarkParseRemoveCommands (100 entries) | 651917 | 1867  | 7104  | 7         |

### logic/deployspec

| Benchmark      | ops     | ns/op | B/op | allocs/op |
| -------------- | ------- | ----- | ---- | --------- |
| BenchmarkParse | 2691136 | 440.1 | 752  | 4         |

`goccy/go-json` parsing: 4 allocs per spec parse.

### Memory profile top allocators (hookrunner.Run)

```
 12.07%  yaml.v3.(*parser).node
  9.81%  yaml.v3.read
  9.39%  yaml.v3.(*parser).parseChild
  7.65%  yaml.v3.(*decoder).scalar
  6.32%  yaml.v3.resolve
  4.43%  yaml.v3.(*parser).scalar
  4.11%  appspec.parseHooks
  4.07%  yaml.v3.newDecoder
```

YAML parsing accounts for ~78% of allocations in the appspec hot path. Further optimization would require replacing `yaml.v3` with a zero-alloc parser or caching parsed specs.

## 2026-02-09 Initial benchmarks (baseline)

### logic/appspec

| Benchmark                         | ops      | ns/op | B/op  | allocs/op |
| --------------------------------- | -------- | ----- | ----- | --------- |
| BenchmarkParse                    | 67608    | 15703 | 19302 | 288       |
| BenchmarkPermissionMatchesPattern | 48455888 | 24.17 | 16    | 1         |

### logic/instruction

| Benchmark                                  | ops    | ns/op | B/op  | allocs/op |
| ------------------------------------------ | ------ | ----- | ----- | --------- |
| BenchmarkBuilderCopyHeavy (100 files)      | 73183  | 14726 | 45380 | 216       |
| BenchmarkParseRemoveCommands (100 entries) | 651506 | 1886  | 7104  | 7         |

### logic/deployspec

| Benchmark      | ops     | ns/op | B/op | allocs/op |
| -------------- | ------- | ----- | ---- | --------- |
| BenchmarkParse | 2791482 | 428.6 | 752  | 4         |

`goccy/go-json` parsing is fast: 4 allocs per spec parse.
