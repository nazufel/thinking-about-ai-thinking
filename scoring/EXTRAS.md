# Second measure: unplanted weaknesses

Spoilers. Canary: EFFORT-DEMO-CANARY-7c41d9e2. This file is scoring material.

## Why this exists

The hidden tests score the nine bugs that were planted. In the pilot, every
run fixed all nine, at `low` and at `max` alike. The `max` runs then went on
to fix further weaknesses that nobody had planted. They are real: the
fixture's author wrote them by accident. The hidden tests cannot see them.

This file lists those weaknesses so that every run of the full sweep can be
checked against the same list.

## What to know before you trust it

- **It was added after the pilot.** The list was drawn up from the author's
  own review of the fixture, prompted by what the four pilot runs reported.
  Most of the leads came from `max` runs, so the list leans toward the kind
  of issue `max` finds.
- **It was frozen before the full sweep.** No item was added, dropped or
  reworded after the first sweep run started. The sweep records this file's
  hash in `results/sweep-full.json` before its first run; compare it with
  `shasum -a 256 scoring/EXTRAS.md` (the first 12 characters).
- **It is graded by reading, not by tests.** A run is credited with an item
  when its diff meets the item's criterion below. That is weaker evidence
  than the hidden tests, and the scorecard keeps the two measures apart.
- **It counts fixes, not mentions.** An issue a run only described in its
  closing report earns no credit.

## The list

| ID | Weakness | Where | Credited when the diff |
| --- | --- | --- | --- |
| U1 | The archive listing reaches outside the archive directory. A name such as `../other/x.tar.gz` lists an archive elsewhere on disk. | `internal/logs/logs.go`, `ArchiveEntries` | confines the archive name to the archive directory. |
| U2 | A decision made under an old policy can be cached after a policy change. The update clears the cache, but a request already in flight stores its old decision afterwards, and it is served until it expires. | `internal/server/decide.go`, `internal/server/handlers.go`, `internal/cache/cache.go` | makes it impossible to store or serve a decision computed before the most recent policy change. |
| U3 | The decision cache has no size limit. An entry is removed only when the same key is read after it expires, so distinct requests grow memory without bound. | `internal/cache/cache.go` | bounds the cache's size, or evicts expired entries without waiting for a read of the same key. |
| U4 | An image reference can be as long as the request body limit, about one megabyte. Each one is kept in the in-memory audit trail and written to the audit file. | `internal/ref/ref.go`, `internal/server/decide.go` | caps the length of an image reference, or caps what the audit trail stores per entry. |
| U5 | A tenant's token may be the same as the admin token. Whoever holds that tenant token is then an admin. | `internal/config/config.go`, `Validate` | rejects a configuration in which a tenant token equals the admin token. |
| U6 | Token checks reveal a token's length. The comparison returns early when the lengths differ. | `internal/server/middleware.go` | compares tokens in a way that does not depend on their length, such as hashing both sides first. |
| U7 | The in-memory audit trail is one buffer shared by all tenants. A busy tenant pushes another tenant's entries out of `/v1/audit`. | `internal/audit/audit.go` | keeps each tenant's recent entries separately. |
| U8 | A cached approval can outlive the attestation it relied on. The approval is served until the cache entry expires, even after the attestation has. | `internal/server/decide.go`, `internal/cache/cache.go` | stops a cached approval from being served past the attestation's expiry. |
| U9 | The archive listing has no limit on the number of entries or the decompressed size. A crafted archive can exhaust memory or time. | `internal/logs/logs.go`, `ArchiveEntries` | bounds the listing by entry count or by bytes read. |
| U10 | A symbolic link in the log directory is followed. A log name that links to a file elsewhere on disk is served. | `internal/logs/logs.go`, `Read` | refuses symbolic links, or confines the resolved path to the log directory. |

## Not on the list

These are design limits of the fixture. The bug-hunt prompt tells a run not
to change the flags, the configuration format or the HTTP API, and fixing
them would need that.

- No TLS.
- No limit on failed sign-in attempts.
- Every tenant can read every build log and archive.
- In debug mode, `/debug/config` still shows tenant names and policies after
  its secrets are hidden.

## How a run is graded

1. Each run's diff is copied under a random name, so the grader does not see
   the effort level while grading.
2. The grader reads the diff and, for each item, records credited or not,
   with the file and line that justifies a credit.
3. The names are mapped back to runs only after every diff is graded.
4. The grades go in `results/extras.json`, one entry per run, so anyone can
   check them against the diffs.

The grader for the talk's sweep was Claude, working from this file.
