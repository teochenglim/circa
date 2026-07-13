# Circa — Design 03: Storage engine (the RRD-like core)

## 3.1 Model

Per metric (identified by name + label set), maintain **multiple round-robin archives (RRAs) at different resolutions**, RRD-style:

- Tier 0: raw resolution (e.g. 1s or 10s, matching collection interval), short retention (e.g. 2 hours)
- Tier 1: 1-minute averages (min/avg/max triplet), medium retention (e.g. 7 days)
- Tier 2: 1-hour averages, long retention (e.g. 1 year)

Each tier is a fixed-length circular buffer — write position wraps, so **disk usage per metric is constant regardless of how long Circa runs**. This is the direct MRTG/RRDtool inheritance and is what keeps a "single binary, no DB" story credible at scale (thousands of metrics × months of history without unbounded growth).

## 3.2 On-disk layout and compression

Rather than RRD's fixed 8-byte float per slot, use point-pair (timestamp, value) compression modeled on Facebook's Gorilla algorithm: Gorilla compresses data points within a time series with no additional compression used across time series — each data point is a pair of 64-bit values representing the timestamp and value at that time. The technique uses delta-of-delta to compress timestamps, so with a fixed interval the majority of timestamps encode as a single bit, and XORs successive float values, storing only the changed bits. This typically gives ~10x size reduction over raw storage for stable metrics like CPU% or memory, which matters when trying to keep months of tier-1/tier-2 data inside a small file. Go has existing implementations of this (`dgryski/go-tsz`, `tsenart/go-tsz` fork) to prototype against rather than writing the bit-packing from scratch.

Each fixed-size tier can be a memory-mapped file (mmap) so reads/writes avoid syscall overhead and the OS page cache does the caching for you — this is close to how Netdata's dbengine behaves: netdata memory is written very infrequently — if you have 24 hours of metrics, each byte is updated just once per day — which is friendly to the kernel's dedup/caching.

## 3.3 Practical footprint

Using Netdata's own numbers as a sanity check for uncompressed storage: for each dimension of a chart you need 4 bytes for the value times the number of entries; for a day of 1-second data across 1,000 dimensions that's 86,400 seconds × 4 bytes × 1,000 dimensions = 345MB. With Gorilla-style compression this typically drops several-fold. Build a sizing calculator early (inputs: metric count, resolution, retention per tier) so users can reason about disk budget the way Netdata's dbengine calculator does.

## 3.4 Aggregation on write-down

When rolling from tier N to tier N+1, store **min/avg/max** (not just avg) per bucket — this preserves spike visibility even at coarse resolution, which plain-average RRD rollups lose. This is a cheap addition over vanilla RRD and worth doing from day one since it directly affects chart usefulness at zoomed-out ranges.
