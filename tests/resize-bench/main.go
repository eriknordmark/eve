// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// Command resize-bench times the two phases of the EVE-kvm -> EVE-k repartition
// independently so we can judge how long the device is "dark" (no EVE-OS, no
// controller reporting) and how much could instead run online:
//
//	SHRINK phase  e2fsck + resize2fs on a filled ext4 /persist (P3)
//	GROW phase    mkfs.fat ESP2 + copy ESP content + copy each IMGx image
//
// It shells out to the same utilities the real resizer uses (e2fsprogs,
// dosfstools, mtools), so the numbers reflect real tool + I/O cost. All scratch
// I/O happens under --workdir, which MUST sit on the medium being measured; the
// tool refuses a tmpfs/ramfs workdir (that would measure RAM, not flash).
//
// Run on real storage with sudo (for in-place fill + cold caches):
//
//	sudo ./resize-bench --workdir /var/scratch --persist-size 100G --fill 60 --shrink 22G
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	tmpfsMagic = 0x01021994
	ramfsMagic = 0x858458f6
)

type step struct {
	Name     string
	Bytes    int64
	Duration time.Duration
}

func (s step) String() string {
	if s.Bytes > 0 && s.Duration > 0 {
		mbps := float64(s.Bytes) / (1 << 20) / s.Duration.Seconds()
		return fmt.Sprintf("  %-22s %8s  (%s, %.0f MiB/s)", s.Name, round(s.Duration), human(s.Bytes), mbps)
	}
	return fmt.Sprintf("  %-22s %8s", s.Name, round(s.Duration))
}

// MarshalJSON emits a human-readable duration (string + seconds) instead of raw
// nanoseconds, plus throughput.
func (s step) MarshalJSON() ([]byte, error) {
	o := struct {
		Name      string  `json:"name"`
		Bytes     int64   `json:"bytes,omitempty"`
		Duration  string  `json:"duration"`
		Seconds   float64 `json:"seconds"`
		MiBPerSec float64 `json:"mibPerSec,omitempty"`
	}{
		Name:     s.Name,
		Bytes:    s.Bytes,
		Duration: round(s.Duration),
		Seconds:  math.Round(s.Duration.Seconds()*1000) / 1000,
	}
	if s.Bytes > 0 && s.Duration > 0 {
		o.MiBPerSec = math.Round(float64(s.Bytes) / (1 << 20) / s.Duration.Seconds())
	}
	return json.Marshal(o)
}

type report struct {
	Workdir       string `json:"workdir"`
	FSType        string `json:"workdirFsType"`
	Phase         string `json:"phase"`
	PersistSize   int64  `json:"persistSizeBytes"`
	FillPct       int    `json:"fillPercentOfCapacity"`
	CapacityBytes int64  `json:"usableCapacityBytes"`
	FilledBytes   int64  `json:"filledBytes"`
	ShrinkBy      int64  `json:"shrinkByBytes"`
	TargetBytes   int64  `json:"targetBytes"`
	Root          bool   `json:"root"`
	DroppedCache  bool   `json:"droppedCache"`
	Setup         []step `json:"setup,omitempty"`
	Shrink        []step `json:"shrink,omitempty"`
	Grow          []step `json:"grow,omitempty"`
}

func usage() {
	fmt.Fprintln(os.Stderr, "resize-bench — time the shrink vs grow phases of the EVE-kvm<->EVE-k repartition")
	fmt.Fprintln(os.Stderr, "usage: resize-bench [flags]   (run on REAL storage, not tmpfs; sudo for cold-cache numbers)")
	fmt.Fprintln(os.Stderr, "  --workdir DIR        scratch dir on the medium to measure (REQUIRED; NOT tmpfs)")
	fmt.Fprintln(os.Stderr, "  --persist-size SIZE  ext4 P3 size to create (default 64G)")
	fmt.Fprintln(os.Stderr, "  --fill PCT           percent of the ext4 USABLE CAPACITY to fill (default 40)")
	fmt.Fprintln(os.Stderr, "  --shrink SIZE        amount to shrink P3 by (default 22G)")
	fmt.Fprintln(os.Stderr, "  --phase WHICH        shrink | grow | both (default both)")
	fmt.Fprintln(os.Stderr, "  --small-files N      number of small (4KiB-1MiB) files in the fill mix (default 2000)")
	fmt.Fprintln(os.Stderr, "  --esp-size/--esp-copy/--img-copy/--img-count   grow-phase sizes (2G/36M/300M/2)")
	fmt.Fprintln(os.Stderr, "  --fill-method auto|mount|mke2fs   (auto: mount if root, else mke2fs)")
	fmt.Fprintln(os.Stderr, "  --drop-caches / --keep / --json")
}

func main() {
	flag.Usage = usage
	workdir := flag.String("workdir", "", "scratch dir on the medium to measure (REQUIRED; NOT tmpfs)")
	persistStr := flag.String("persist-size", "64G", "ext4 P3 size to create")
	fillPct := flag.Int("fill", 40, "percent of the ext4 usable capacity to fill")
	shrinkStr := flag.String("shrink", "22G", "amount to shrink P3 by")
	phase := flag.String("phase", "both", "which phase to measure: shrink|grow|both")
	smallFiles := flag.Int("small-files", 2000, "number of small files in the fill mix")
	espSizeStr := flag.String("esp-size", "2G", "ESP2 filesystem size (mkfs.fat target)")
	espCopyStr := flag.String("esp-copy", "36M", "ESP content bytes copied during grow")
	imgCopyStr := flag.String("img-copy", "300M", "each IMGx content bytes copied during grow")
	imgCount := flag.Int("img-count", 2, "number of IMGx images copied during grow")
	fillMethod := flag.String("fill-method", "auto", "auto|mount|mke2fs")
	dropCache := flag.Bool("drop-caches", true, "drop page cache before each timed phase (needs root)")
	keep := flag.Bool("keep", false, "keep scratch files")
	asJSON := flag.Bool("json", false, "emit JSON")
	flag.Parse()

	if *workdir == "" {
		fmt.Fprintln(os.Stderr, "resize-bench: --workdir is required (a dir on the medium to measure; not tmpfs)")
		usage()
		os.Exit(2)
	}
	doShrink := *phase == "shrink" || *phase == "both"
	doGrow := *phase == "grow" || *phase == "both"
	if !doShrink && !doGrow {
		fatal(fmt.Errorf("bad --phase %q (want shrink|grow|both)", *phase))
	}

	persistSize := mustSize(*persistStr)
	shrinkBy := mustSize(*shrinkStr)
	espSize := mustSize(*espSizeStr)
	espCopy := mustSize(*espCopyStr)
	imgCopy := mustSize(*imgCopyStr)
	target := persistSize - shrinkBy

	if err := checkRequiredTools(); err != nil {
		fatal(err)
	}
	scratch := filepath.Join(*workdir, "resize-bench-scratch")
	if !dirEmptyOrAbsent(scratch) {
		fatal(fmt.Errorf("%s already exists and is not empty; remove it or pick another --workdir", scratch))
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		fatal(err)
	}
	// From here the scratch dir exists and is ours: clean it up (and umount any
	// loop mount) on every exit path — normal return, fatal(), or ^C.
	scratchDir = scratch
	keepScratch = *keep
	defer cleanupScratch()
	ctx, cancel := context.WithCancel(context.Background())
	benchCtx = ctx
	defer cancel()
	installSignalCleanup(cancel)

	fsType, magic := fsTypeOf(scratch)
	if magic == tmpfsMagic || magic == ramfsMagic {
		fatal(fmt.Errorf("workdir %s is %s (RAM-backed) — measurements would be meaningless; pick a --workdir on the medium under test", scratch, fsType))
	}

	root := os.Geteuid() == 0
	method := *fillMethod
	if method == "auto" {
		if root {
			method = "mount"
		} else {
			method = "mke2fs"
		}
	}
	if method == "mount" && !root {
		fatal(fmt.Errorf("--fill-method mount needs root; re-run with sudo or use --fill-method mke2fs"))
	}

	// Free-space precheck (estimate; --fill is resolved against capacity later).
	needed := int64(256 << 20)
	if doShrink {
		needed += persistSize * int64(*fillPct) / 100
		if method == "mke2fs" {
			needed += persistSize * int64(*fillPct) / 100 // source tree, copied in then removed
		}
	}
	if doGrow {
		needed += espCopy + imgCopy*int64(*imgCount)
	}
	if avail := availableBytes(scratch); avail >= 0 && avail < needed {
		fatal(fmt.Errorf("workdir has %s available but the run needs ~%s; free space or reduce --persist-size/--fill", human(avail), human(needed)))
	}

	rep := report{
		Workdir: scratch, FSType: fsType, Phase: *phase,
		PersistSize: persistSize, FillPct: *fillPct, ShrinkBy: shrinkBy, Root: root,
	}
	fmt.Fprintf(os.Stderr, "workdir %s (%s), phase=%s persist=%s method=%s root=%v\n",
		scratch, fsType, *phase, human(persistSize), method, root)

	img := filepath.Join(scratch, "persist.img")

	if doShrink {
		rep.TargetBytes = target
		var capacity, filled int64
		rep.Setup, capacity, filled = buildFilledExt4(img, persistSize, *fillPct, method, scratch, *smallFiles)
		rep.CapacityBytes, rep.FilledBytes = capacity, filled
		fmt.Fprintf(os.Stderr, "ext4 usable capacity %s; filled %s (%d%% of capacity); shrink to %s\n",
			human(capacity), human(filled), *fillPct, human(target))

		rep.DroppedCache = maybeDropCaches(*dropCache, root)
		rep.Shrink = runShrink(img, target)
	}

	if doGrow {
		maybeDropCaches(*dropCache, root)
		rep.Grow = runGrow(scratch, espSize, espCopy, imgCopy, *imgCount)
	}

	emit(rep, *asJSON)
}

// buildFilledExt4 creates an ext4 image, measures its usable capacity, and fills
// fillPct of that capacity. Returns the setup steps, the usable capacity, and
// the bytes filled.
func buildFilledExt4(img string, size int64, fillPct int, method, scratch string, smallFiles int) ([]step, int64, int64) {
	var steps []step
	truncateFile(img, size)
	buf := randBuf(4 << 20)

	// Empty mkfs first, to learn the usable capacity (total - ext4 overhead).
	steps = append(steps, timed("mkfs.ext4", 0, func() error {
		return runOK("mkfs.ext4", "-F", "-q", "-b", "4096", img)
	}))
	bs, _, freeBlocks, err := readExt4SB(img)
	if err != nil {
		fatal(fmt.Errorf("read ext4 superblock: %w", err))
	}
	capacity := bs * freeBlocks
	filled := capacity * int64(fillPct) / 100

	switch method {
	case "mke2fs":
		// root-free: build a source tree of `filled`, then re-create the fs with
		// mkfs.ext4 -d copying it in.
		tree := filepath.Join(scratch, "filltree")
		_ = os.MkdirAll(tree, 0o755)
		steps = append(steps, timed("fill: write source tree", filled, func() error {
			return fillDir(tree, filled, smallFiles, buf)
		}))
		steps = append(steps, timed("mkfs.ext4 -d (copy in)", filled, func() error {
			return runOK("mkfs.ext4", "-F", "-q", "-b", "4096", "-d", tree, img)
		}))
		_ = os.RemoveAll(tree)
	default: // mount (root): fill the empty fs in place
		mnt := filepath.Join(scratch, "mnt")
		_ = os.MkdirAll(mnt, 0o755)
		steps = append(steps, timed("fill: write files", filled, func() error {
			if err := runOK("mount", "-o", "loop", img, mnt); err != nil {
				return err
			}
			mountPoint = mnt // so cleanupScratch/^C can umount it if we're interrupted
			defer func() {
				_ = exec.Command("umount", mnt).Run()
				mountPoint = ""
			}()
			if err := fillDir(mnt, filled, smallFiles, buf); err != nil {
				return err
			}
			return runOK("sync")
		}))
	}
	return steps, capacity, filled
}

// runShrink times e2fsck + resize2fs on the image, as the real resizer does.
func runShrink(img string, target int64) []step {
	targetK := target / 1024
	return []step{
		timed("e2fsck -f", 0, func() error { return runFsck("e2fsck", "-f", "-y", img) }),
		timed("resize2fs (shrink)", 0, func() error { return runResize2fs(img, fmt.Sprintf("%dK", targetK)) }),
	}
}

// runGrow times creating ESP2 + copying ESP content + copying each IMGx image.
func runGrow(scratch string, espSize, espCopy, imgCopy int64, imgCount int) []step {
	var steps []step
	buf := randBuf(4 << 20)

	esp := filepath.Join(scratch, "esp2.img")
	truncateFile(esp, espSize)
	steps = append(steps, timed("mkfs.fat ESP2", 0, func() error {
		if err := runFat(esp); err != nil {
			return err
		}
		return fsyncFile(esp)
	}))

	espSrc := filepath.Join(scratch, "esp-src.bin")
	writeFile(espSrc, espCopy, buf)
	steps = append(steps, timed("copy ESP content", espCopy, func() error {
		if err := runOK("mcopy", "-i", esp, espSrc, "::ESPDATA"); err != nil {
			return err
		}
		return fsyncFile(esp)
	}))

	imgSrc := filepath.Join(scratch, "img-src.bin")
	writeFile(imgSrc, imgCopy, buf)
	for i := 0; i < imgCount; i++ {
		dst := filepath.Join(scratch, fmt.Sprintf("img%d.bin", i))
		steps = append(steps, timed(fmt.Sprintf("copy IMG%d (%s)", i, human(imgCopy)), imgCopy, func() error {
			return copyFileSync(dst, imgSrc)
		}))
	}
	return steps
}

func emit(rep report, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}
	sum := func(ss []step) time.Duration {
		var d time.Duration
		for _, s := range ss {
			d += s.Duration
		}
		return d
	}
	if len(rep.Setup) > 0 {
		fmt.Printf("\nSETUP (not counted): capacity=%s filled=%s (%d%%)\n",
			human(rep.CapacityBytes), human(rep.FilledBytes), rep.FillPct)
		for _, s := range rep.Setup {
			fmt.Println(s)
		}
	}
	shrinkT, growT := sum(rep.Shrink), sum(rep.Grow)
	if len(rep.Shrink) > 0 {
		fmt.Printf("\nSHRINK phase  total %s%s:\n", round(shrinkT), cacheNote(rep))
		for _, s := range rep.Shrink {
			fmt.Println(s)
		}
	}
	if len(rep.Grow) > 0 {
		fmt.Printf("\nGROW phase    total %s:\n", round(growT))
		for _, s := range rep.Grow {
			fmt.Println(s)
		}
	}
	if len(rep.Shrink) > 0 && len(rep.Grow) > 0 {
		fmt.Printf("\nSUMMARY: shrink=%s grow=%s (shrink is %.2fx grow)\n", round(shrinkT), round(growT), float64(shrinkT)/float64(growT))
	}
	if (len(rep.Shrink) > 0) && !rep.DroppedCache {
		fmt.Println("NOTE: page cache NOT dropped (not root) — numbers are cache-warm; run with sudo for cold-cache figures.")
	}
}

func cacheNote(rep report) string {
	if rep.DroppedCache {
		return " (cold cache)"
	}
	return " (warm cache)"
}
