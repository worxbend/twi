# TUI workload performance

Measured on 2026-10-12 with Go 1.27.2, Linux amd64, an AMD Ryzen 7 5700X, two Go workers and a 120x40 viewport. These are reproducible local stress measurements, not Twitch throughput promises or a cross-hardware SLA.

The collector builds one test binary for every benchmark and profile in a cohort. It records a source digest, runtime settings, raw samples, medians, budgets and whether source changed during collection. No credentials, saved config, network services or terminal captures enter the fixtures.

## Workloads

| Case | Channels | Backlog per channel | Arrivals | Animation |
| --- | ---: | ---: | --- | --- |
| quiet-one | 1 | 0 | none | off |
| quiet-many | 100 | 0 | none | fast |
| busy-one | 1 | 2,000 | ten per model frame | off |
| busy-many | 10 | 2,000 | ten per model frame; runtime offers 100/s across ten channels | off |
| busy-animated | 10 | 2,000 | same busy arrivals | fast |

Messages use 64 author identities, changing IDs, representative text and Unicode (`視聴者 😀`). Each model benchmark iteration performs all updates plus one view. Animated iterations advance the 100ms chrome clock and five 20ms reveal steps. Model iterations use a deterministic clock; the native runtime uses the production wall clock and actual Bubble Tea commands. The scrollback cap includes its existing bounded trimming margin.

The runtime suite runs the real Bubble Tea event loop and renderer for three seconds per scenario. A separate producer offers one normalized event every 10ms into a bounded 128-event queue. The normal writer discards output bytes. A fifth case delays each terminal write by 25ms. This makes backlog and loss visible without quietly slowing the offered rate to match the UI. CPU uses process user+system time; 100% means one fully occupied CPU core.

## Model and retained-memory baseline

Values are medians of three samples. MB means 1,000,000 bytes. Retained memory is heap still reachable after GC, measured with the model and warmed active-channel row cache alive. It includes shared model/cache overhead; bytes per channel are an average, not the marginal cost of another channel. Empty channels and inactive backlog are substantially cheaper than an active rendered backlog.

| Case | Update+view ms | Allocated MB/op | Allocations/op | GC-live total MB | GC-live MB/channel |
| --- | ---: | ---: | ---: | ---: | ---: |
| quiet-one | 1.80 | 0.80 | 22,353 | 0.0396 | 0.0396 |
| quiet-many | 3.28 | 1.40 | 41,091 | 0.1151 | 0.0012 |
| busy-one | 14.80 | 13.37 | 50,201 | 4.8717 | 4.8717 |
| busy-many | 12.10 | 12.35 | 51,134 | 15.7523 | 1.5752 |
| busy-animated | 17.63 | 17.95 | 69,618 | 15.7523 | 1.5752 |

## Native event-loop and renderer baseline

View calls and terminal writes differ because Bubble Tea coalesces screen updates. The quiet/off case has no continuous frame command; its single measured view comes from shutdown. Shutdown views and terminal restoration writes are included in the reported rates. Arrival latency measures arrival to model processing, including queue wait; it does not measure physical terminal-emulator display latency.

| Case | CPU/core % | Views/s | Terminal writes/s | View p95 ms | Allocated MB/s |
| --- | ---: | ---: | ---: | ---: | ---: |
| quiet-one | 0.4 | 0.3 | 0.7 | 2.38 | 0.3 |
| quiet-many | 5.9 | 10.3 | 8.0 | 4.63 | 14.4 |
| busy-many | 70.1 | 100.0 | 60.5 | 6.00 | 353.6 |
| busy-animated | 101.9 | 150.1 | 58.6 | 5.80 | 514.2 |
| busy-many-slow-terminal | 38.9 | 54.6 | 29.9 | 6.20 | 194.2 |

| Busy case | Offered | Processed | Overflow drops | Pending at shutdown | Queue peak/128 | Arrival p95 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| busy-many | 300 | 300 | 0 | 0 | 0 | 3.1 |
| busy-animated | 299 | 299 | 0 | 0 | 10 | 78.0 |
| busy-many-slow-terminal | 299 | 169 | 4 | 126 | 128 | 1271.0 |

The delayed terminal reaches the queue bound and drops events. Pending events at intentional shutdown are reported separately from overflow. This fixture has no recorder: it evaluates UI pressure, while recorder durability/slow-output evidence lives in `docs/history-performance.md`. Production capture precedes the UI queue; these numbers do not assert durable-history loss.

## Profiles and regression budgets

The CPU profile attributes about 28% cumulative CPU to `visibleChatRowBlocks`, 33% to Lip Gloss style rendering and 19% to ANSI width calculation. The allocation profile attributes about 55% cumulative allocation to `visibleChatRowBlocks`, with string builders adding about 25%. This is evidence for future work on rebuilding whole-history row blocks and repeated width/style work. No speculative rendering cache or layout refactor was added for this measurement.

Numeric upper bounds below round up measured busy-case baselines with roughly twofold headroom, with practical floors for small idle measurements. Slow-terminal overflow varied from about 2% to 12% across stable samples because screen writes coalesce differently; its 25% ceiling rounds up twice the worst measured value and accompanies the minimum processed fraction. Normal-writer overflow remains limited to 1%. The native timing/CPU/throughput gates require comparable idle hardware; CI on slower or heavily shared machines should collect evidence without `--check`, then compare allocation/memory medians and repeat marginal timing failures. They are regression tripwires, not new product guarantees.

| Model case | Max ms/op | Max MB/op | Max allocations/op | Max retained MB/channel |
| --- | ---: | ---: | ---: | ---: |
| quiet-one | 5 | 1.5 | 45,000 | 0.096 |
| quiet-many | 9 | 3.0 | 90,000 | 0.003 |
| busy-one | 40 | 30.0 | 130,000 | 12.000 |
| busy-many | 35 | 28.0 | 125,000 | 4.000 |
| busy-animated | 45 | 40.0 | 170,000 | 4.000 |

| Native case | Max CPU/core % | Max view p95 ms | Max allocated MB/s | Min processed fraction | Max overflow fraction | Max arrival p95 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| quiet-one | 5 | 10 | 3 | — | — | — |
| quiet-many | 20 | 15 | 35 | — | — | — |
| busy-many | 150 | 25 | 850 | 0.8 | 0.01 | 200 |
| busy-animated | 180 | 25 | 1200 | 0.8 | 0.01 | 200 |
| busy-many-slow-terminal | 100 | 25 | 600 | 0.35 | 0.25 | 3000 |

The collector also requires the fixture queue to remain at capacity 128 and its measured peak never to exceed that bound. The quiet/off unit test verifies that continuous redraw is disabled. Per-channel echo tracking now follows retained scrollback: synthetic IRC IDs are never treated as expected server echoes, acknowledged IDs are pruned on trimming/clear, and either ordering of EventSub observation and HTTP acknowledgement retains one richer observed row. A 1,000-send regression covers the bound.

## Reproduce and inspect

On a Unix host with Go, Python 3 and `rg`:

```sh
python3 scripts/profile-tui.py --output /tmp/twi-tui-performance --check
go tool pprof /tmp/twi-tui-performance/app.test /tmp/twi-tui-performance/runtime.cpu.pprof
go tool pprof -alloc_space /tmp/twi-tui-performance/app.test /tmp/twi-tui-performance/runtime.allocs.pprof
```

The script pins `GOMAXPROCS=2`, `GOGC=100`, `GOMEMLIMIT=off` and clears inherited runtime debug instrumentation. `benchmarks.txt` contains three one-second samples per model case; `memory.txt` contains three GC-live samples. `runtime.json` contains per-scenario counters and latency, `cpu-top.txt`/`alloc-top.txt` identify hotspots, and `cpu-workloads.txt` records workload labels. `summary.json` combines the results and explicit budgets. CPU sampling and allocation sampling have overhead, so native-profile results should be compared against other profile runs with the same settings. The runtime `post_run_live_heap_delta_bytes` is a post-shutdown GC-live delta against the warmed program, so it may be negative when startup buffers are reclaimed; absolute model retention is reported by the separate memory benchmark.

Recorded final cohort after module tidy: `/tmp/twi-tui-performance-final-module`; source SHA-256 `803a91335b31614b24891bfc15f5cd7cf3a7baae5a4c40ec2391481f6e1d7c10`. All measured budgets passed and source was unchanged during this final cohort.
