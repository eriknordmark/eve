# Host tests for the GRUB eve_overrides command (docs/GRUB-OVERRIDES.md).
#
#   make check          case table (run.sh) under ASan and UBSan
#   make fuzz           standalone mutation fuzzer with the property oracle
#   make check SANITIZE= SRC=/grub/grub-core/commands/eve_overrides.c
#                       inside the pkg/grub build, against the patched tree
#
# By default the source is extracted from the GRUB patch, so the test always
# runs the patch in this tree.

PATCH ?= ../../patches-2.12/0015-commands-Add-eve_overrides-command.patch
SRC ?= build/eve_overrides.c
CC ?= gcc
SANITIZE ?= -fsanitize=address,undefined -fno-sanitize-recover=all
CFLAGS ?= -O1 -g -Wall -Wextra -Wno-unused-parameter -Wno-sign-compare
FUZZ_ITERS ?= 200000
FUZZ_SEED ?= 1

ALL_CFLAGS = $(CFLAGS) $(SANITIZE) -Ishim
SHIM = shim/shim.c

.PHONY: check fuzz clean

build/eve_overrides.c: $(PATCH)
	mkdir -p build
	awk '/^\+\+\+ b\/grub-core\/commands\/eve_overrides.c/ {on=1; next} \
	     on && /^@@/ {next} \
	     on && /^(diff --git|-- $$)/ {on=0} \
	     on {sub(/^\+/, ""); print}' $< > $@
	test -s $@

build/run: run.c $(SHIM) $(SRC) shim/grub/shim.h shim/recorder.h
	mkdir -p build
	$(CC) $(ALL_CFLAGS) -o $@ run.c $(SHIM) $(SRC)

build/fuzz: fuzz.c $(SHIM) $(SRC) shim/grub/shim.h shim/recorder.h
	mkdir -p build
	$(CC) $(ALL_CFLAGS) -DFUZZ_STANDALONE -o $@ fuzz.c $(SHIM) $(SRC)

check: build/run
	./run.sh build/run

fuzz: build/fuzz
	build/fuzz -i $(FUZZ_ITERS) -s $(FUZZ_SEED) cases/*.cfg

clean:
	rm -rf build
