# Container resource measurements

Keep the app limit at **512 MiB** and reduce PostgreSQL from **384 MiB to
256 MiB**. The production Compose file records these final defaults. This is
a configuration change for the next deployment; no production host was changed.

## Reproduce

After `task setup`, with Docker's Linux engine and cgroup v2 available:

```text
task resources
```

The task builds the runtime image and generates a patterned synthetic
4032×3024 image (12,192,768 pixels), encoded as JPEG quality 85 and HEIC quality 80.
Each batch contains ten distinct files. Legal BMFF free boxes vary HEIC hashes,
so all ten files actually convert instead of being deduplicated.

The isolated app uses local image storage, fake extraction, a read-only root,
64 MiB `/tmp`, a disposable disk-backed image volume and the production pool
limit of five. PostgreSQL 18.4 uses `shared_buffers=64MB`, `max_connections=20`.
Both containers have their proposed memory cap with swap disabled. The task
checks all twenty uploads were created and processed. No external provider or
production data is used. Fixture generation and the HTTP sender run outside
the measured containers.

The runner samples cgroup `memory.current`, anonymous memory and working set
(`current - inactive_file`, clamped to zero), pausing 200 ms between samples.
Docker command time adds to that interval. Kernel `memory.peak` captures the
whole-run high-water mark, including reclaimable file cache and probe overhead.
Per-phase sampled peaks can miss short spikes; use the kernel peak when
assessing the cap. Raw metrics are written to ignored `bin/resource-report.json`.
Only this run's labeled containers, volumes/network and verified fixture
directory are removed; no Docker prune is used.

## Recorded run

Measured **2026-11-16 02:48 UTC**, application revision
`7f949da3dd2d0c2cd700a9772c9b64149c0ee145`, Docker 29.9.3/overlayfs.
The verification tooling was in the working tree; it adds no runtime code.
The [raw report](resource-report.json) retains exact byte values and image id.
Each JPEG was 5,580,674 bytes; each HEIC was 10,989,558 bytes before its uniqueness suffix.

| Phase | App working set | PostgreSQL working set |
| --- | ---: | ---: |
| Idle after sign-in | 8.2 MiB | 37.7 MiB |
| Ten JPEG uploads, sampled peak | 40.5 MiB | 43.4 MiB |
| Ten HEIC uploads, sampled peak | 180.7 MiB | 44.5 MiB |
| Whole-run kernel peak, including file cache | 364.8 MiB | 89.0 MiB |
| Enforced limit | 512 MiB | 256 MiB |

Neither container was OOM-killed. The HEIC batch fits the retained app limit;
the measured database peak leaves headroom at 256 MiB. The app cap is retained
because decode buffers and file cache exceed the sampled working set.

The separate [OCR regression probes](HARDENING.md#findings-and-fixes) caught
and fixed an oversized 16-bit PNG memory failure. They also demonstrated a
24 MP 16-bit PNG completing at 278.0 MiB after the fix. They measure one image
operation at a time, not all possible overlapping requests.

## Image size

| Engine/report | Bytes | Result |
| --- | ---: | --- |
| Local Docker Desktop 29.9.3/overlayfs `image inspect .Size` | 60,878,822 | Recorded engine-reported size |
| Ubuntu CI image job, final application fix | 42,541,697 | Passes the 60,000,000-byte CI cap |

The [CI log](https://github.com/enesoglu/receipt-tracker/actions/runs/43793436971/job/133056803757)
contains the size assertion and runtime smoke test. Engine-reported figures
differ and are not interchangeable; the local result is not represented as
passing the CI-size threshold.

These are small-database synthetic measurements. Monitor memory after deployment
and as data grows; they do not establish capacity for arbitrary concurrent
clients, every camera format, or real provider latency.
