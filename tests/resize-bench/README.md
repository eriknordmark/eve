# resize-bench

Times the two phases of the EVE-kvm → EVE-k disk repartition independently, to
judge how long the device is "dark" (no EVE-OS running, no controller reporting)
versus how much work could run online:

- **SHRINK** — `e2fsck` + `resize2fs` on a filled ext4 `/persist` (P3).
- **GROW** — `mkfs.fat` ESP2 + copy ESP content (36 MB) + copy each IMGx image (300 MB).

It shells out to the same utilities the real resizer uses, so the numbers
reflect real tool + I/O cost. The GPT partition-table writes themselves are
sub-second and excluded. Required tools and the Alpine packages that provide
them:

| tool | Alpine package |
|------|----------------|
| `mkfs.ext4`, `e2fsck` | `e2fsprogs` |
| `resize2fs` | `e2fsprogs-extra` (**not** the base `e2fsprogs`) |
| `mkfs.fat` / `mkfs.vfat` | `dosfstools` |
| `mcopy` | `mtools` |

```sh
apk add e2fsprogs e2fsprogs-extra dosfstools mtools
```

The tool checks for these at startup and, if any are missing, prints exactly
which `apk add` to run.

## Important: measure on real storage

All scratch I/O happens under `--workdir`. **It must sit on the medium you want
to measure** (the device's eMMC/SSD). The tool detects a tmpfs/ramfs workdir via
`statfs` and refuses — measuring RAM would be meaningless. It also prints the
detected filesystem type in its first status line (e.g. `workdir … (ext2/3/4)`)
so you can verify the medium — it does not prompt; there is no interactive
confirmation.

## Build

Pure Go, no cgo — `CGO_ENABLED=0` gives a fully static binary that runs on
Alpine/musl as well as glibc (no musl cross-toolchain needed):

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-s -w" -o resize-bench .   # arm64 device
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o resize-bench .   # amd64
```

## Run (use sudo for in-place fill + cold caches)

```sh
sudo ./resize-bench --workdir /var/scratch --persist-size 100G --fill 60 --shrink 22G
```

`sudo` matters: with root the fill is written in place via a loop mount (no
double space) and the page cache is dropped before each timed phase, giving
cold-cache (realistic) numbers. Without root it falls back to `mkfs.ext4 -d`
(needs ~`fill` extra scratch space) and cannot drop caches (numbers are
cache-warm) — fine for a quick relative check, not for absolute figures.

## Flags

| flag | default | meaning |
|------|---------|---------|
| `--workdir` | *(required)* | scratch location — put on the medium under test |
| `--persist-size` | `64G` | ext4 P3 size to create |
| `--fill` | `40` | percent of the ext4 **usable capacity** to fill (see below) |
| `--shrink` | `22G` | amount to shrink P3 by (target = persist-size − shrink) |
| `--phase` | `both` | which phase to measure: `shrink`, `grow`, or `both` |
| `--small-files` | `2000` | number of small files in the fill mix |
| `--esp-size` | `2G` | ESP2 filesystem size (`mkfs.fat` target) |
| `--esp-copy` | `36M` | ESP content copied in grow |
| `--img-copy` | `300M` | each IMGx content copied |
| `--img-count` | `2` | number of IMGx copied |
| `--fill-method` | `auto` | `mount` (root, in-place) or `mke2fs` (root-free) |
| `--drop-caches` | `true` | drop page cache before each phase (needs root) |
| `--json` | `false` | machine-readable output (durations as `s`/string, not ns) |

(Flags are GNU-style `--word`; Go's `flag` also accepts a single dash.)

## `--fill` is relative to usable capacity

`--fill 75` fills 75% of the **filesystem's usable capacity**, not 75% of the
partition. ext4 reserves space the data can't use — inode tables, the journal,
group descriptors/bitmaps, the 5% root-reserved blocks, `lost+found` — so the
usable capacity is several percent below the partition size. The tool mkfs's an
empty fs first, reads the real capacity, and fills that fraction; it prints
`ext4 usable capacity X; filled Y (N% of capacity)`. This way a `--fill 75` run
results in an fs that is ~75% full (what `df` would show), and `resize2fs`'s
*minimum* (data + non-freeable overhead) stays close to the filled size.

If the shrink target is below that minimum, `resize2fs` cannot fit the data and
the tool reports *"filesystem is too full to shrink to … — lower --fill or
--shrink, or raise --persist-size"* (instead of the raw `New size smaller than
minimum`). Shrink cost is driven by how much data must be relocated below the new
boundary, so sweep `--fill` to find where shrink time starts to dominate grow.

## Fill size distribution

To resemble a real `/persist` rather than a few identical blobs, the fill is a
two-tier mix (fixed-seed PRNG, so runs are comparable):

- a bounded number of **small files** (4 KiB–1 MiB: logs, configs, certs) for
  realistic inode/extent pressure — count set by `--small-files`;
- the remaining bytes in **large files** (64–512 MiB: volume images, container
  blobs).

## Suggested sweep

Across fill levels and on each medium of interest (laptop SSD vs device eMMC):

```sh
for f in 30 50 70 85; do
  sudo ./resize-bench --workdir /var/scratch --persist-size 100G --fill $f --shrink 22G --json
done
```

## TODO: characterize the ext4 shrink floor

The shrink-margin policy in `storage-resizer` (the `--max-full 90` bound) is a
placeholder, not a measured safe point. `resize2fs` refuses to shrink below a
filesystem's *minimum* size — data blocks plus non-relocatable metadata (inode
tables, group descriptors, journal) — which is **not** a fixed percentage: it
rises with fragmentation and file count, and the metadata floor has a
size-independent component. Manual runs already hit `New size smaller than
minimum` and fragmentation failures at fill levels below the nominal margin.

The open question (storage-resizer README, the `--max-full` section): is a single
percentage enough, or must the bound be a percentage **plus** a fixed reserve
(some MB that does not scale with size), and does the constant vary across
`/persist` size, fragmentation, and ext4 feature set? Set up an exploratory sweep
to find out (unless authoritative ext4 documentation already specifies the
floor). Vary, independently:

- **fill level**: 30 … 95% in steps, to find where shrink starts failing;
- **`/persist` size**: 8G, 32G, 64G, 100G, 256G — to separate the
  size-proportional part of the floor from any fixed component;
- **fragmentation / aging**: the two-tier mix vs many small files vs an aged
  filesystem (create+delete churn before filling) — fragmentation raises the
  relocatable-block count and the floor;
- **ext4 feature set**: default `mkfs` vs EVE's actual `mkfs.ext4` options, and
  with/without journal, since features change the metadata footprint.

For each cell capture: `df` used, the `resize2fs -P` reported minimum, the
*actual* smallest size `resize2fs` accepts (binary-search the target), and
whether the requested 22 GB shrink succeeds. From the gap between "used" and the
real floor across the matrix, derive a robust margin model (fixed MB + percent)
and feed it back into `storage-resizer`'s `check`. Record results in `~/notes/`.
