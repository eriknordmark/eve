/*
 * Fuzz target for eve_overrides with a property oracle.
 *
 * Built with clang -fsanitize=fuzzer it is a libFuzzer target. Built without
 * it (FUZZ_STANDALONE), main() mutates the seed files given on the command
 * line for -i iterations, so the target also runs under gcc and ASan.
 *
 * Besides memory errors (ASan), every run checks properties that must hold
 * for any input, using a key list taken from docs/GRUB-OVERRIDES.md rather
 * than from the command's own tables:
 *  - only the override variables, timeout and eve_overrides_* change;
 *  - a changed override variable holds only documented keys and none of the
 *    characters or words the doc forbids;
 *  - an append keeps the previous value as its prefix;
 *  - the file is measured first, into PCR 9 (one PCR 8 refusal event instead
 *    when it is over 64 KiB); at most 16 rejection events
 *    follow, in PCR 8, with printable descriptions of bounded length;
 *  - with --require-tpm and no measurement, only eve_overrides_status changes.
 */
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

#include "shim/recorder.h"

static const char *const override_vars[] = {
  "hv_dom0_mem_settings", "hv_eve_mem_settings", "hv_ctrd_mem_settings",
  "hv_dom0_cpu_settings", "hv_eve_cpu_settings", "hv_ctrd_cpu_settings",
  "dom0_console", "dom0_platform_tweaks", "dom0_extra_args", NULL
};

static const char *const doc_keys[] = {
  "dom0_mem", "eve_mem", "ctrd_mem", "dom0_max_vcpus", "eve_max_vcpus",
  "ctrd_max_vcpus", "dom0_vcpus_pin", "console", "eve_no_dirsync",
  "eve_blackbox", "eve_pause_before_install", "eve_pause_after_install",
  "eve_reboot_after_install", "eve_install_skip_config",
  "eve_install_skip_persist", "eve_install_skip_rootfs",
  "eve_install_skip_zfs_checks", "eve_nuke_all_disks", "eve_install_disk",
  "eve_persist_disk", "eve_nuke_disks", "eve_install_zfs_with_raid_level",
  "eve_install_k3s_etcd_sizeGB", "eve_install_server", "isolcpus",
  "nohz_full", "video", "pci", "pcie_aspm", "pcie_port_pm",
  "acpi_no_watchdog", "rootwait", "rootdelay", "log_buf_len",
  "modprobe.blacklist", "kempld_core.force_device_id",
  "vfio_pci.disable_idle_d3", "nvethernet.macsec_enable", NULL
};

static const char *const presets[][2] = {
  {"dom0_console", "console=tty0 console=ttyS0,115200"},
  {"dom0_platform_tweaks", "pcie_aspm=off"},
  {"dom0_extra_args", ""},
  {"hv_dom0_mem_settings", "dom0_mem=800M,max:800M"},
};

static int
in (const char *const *l, const char *s, size_t n)
{
  for (; *l; l++)
    if (strlen (*l) == n && strncmp (*l, s, n) == 0)
      return 1;
  return 0;
}

static const unsigned char *cur_data;
static size_t cur_len;
static unsigned cur_mode;

/* Saves the input that broke a property to crash.cfg (mode in crash.mode).  */
static void
fail (const char *what, const char *detail)
{
  FILE *f = fopen ("crash.cfg", "wb");
  if (f)
    {
      fwrite (cur_data, 1, cur_len, f);
      fclose (f);
    }
  f = fopen ("crash.mode", "w");
  if (f)
    {
      fprintf (f, "%u\n", cur_mode);
      fclose (f);
    }
  fprintf (stderr, "oracle: %s: %s (input in crash.cfg, mode %u)\n", what,
	   detail ? detail : "", cur_mode);
  abort ();
}

static const char *
preset_of (const char *name)
{
  size_t i;
  for (i = 0; i < ARRAY_SIZE (presets); i++)
    if (strcmp (presets[i][0], name) == 0)
      return presets[i][1];
  return NULL;
}

static void
check_value (const char *name, const char *v)
{
  const char *pre = preset_of (name);
  const unsigned char *c;
  const char *t;

  /* Appends keep the preset; anything after it is new and must be clean.
     Tab separates tokens, as it does for GRUB and the kernel.  */
  if (pre && strncmp (v, pre, strlen (pre)) == 0
      && (v[strlen (pre)] == 0 || v[strlen (pre)] == ' '
	  || v[strlen (pre)] == '\t'))
    v += strlen (pre);
  for (c = (const unsigned char *) v; *c; c++)
    if ((*c < 0x20 && *c != '\t') || *c >= 0x7f || strchr ("\"'\\;$#{}`", *c))
      fail ("forbidden character", name);
  if (strstr (v, "getty") || strstr (v, "init=") || strstr (v, "eve_root")
      || strstr (v, "eve_disable_verify") || strstr (v, "eve_soft_serial"))
    fail ("forbidden word", v);
  for (t = v; *t;)
    {
      const char *e = t, *k;
      while (*t == ' ' || *t == '\t')
	t++;
      if (!*t)
	break;
      e = t;
      while (*e && *e != ' ' && *e != '\t')
	e++;
      k = t;
      while (k < e && *k != '=')
	k++;
      if (!in (doc_keys, t, k - t))
	fail ("undocumented key", t);
      t = e;
    }
  if (strlen (v) > 1024)
    fail ("value too long", name);
}

static void
check (const unsigned char *data, size_t len, int require)
{
  int i, nrej = 0;

  for (i = 0; i < shim.nvars; i++)
    {
      struct shim_var *v = &shim.vars[i];
      if (!v->changed)
	continue;
      if (strcmp (v->name, "eve_overrides_status") == 0)
	{
	  if (strcmp (v->value, "applied") && strcmp (v->value, "no-tpm"))
	    fail ("status", v->value);
	  continue;
	}
      if (require && (!shim.tpm_present || shim.tpm_fail))
	fail ("changed without measurement", v->name);
      if (strcmp (v->name, "eve_overrides_rejected") == 0)
	continue;
      if (strcmp (v->name, "timeout") == 0)
	{
	  size_t n = strlen (v->value);
	  if (n < 1 || n > 3 || strspn (v->value, "0123456789") != n)
	    fail ("timeout", v->value);
	  continue;
	}
      if (!in (override_vars, v->name, strlen (v->name)))
	fail ("variable outside the allow-list", v->name);
      check_value (v->name, v->value);
    }

  if (shim.tpm_present && !shim.tpm_fail && len > 65536)
    {
      if (shim.nevents != 1 || shim.events[0].pcr != 8
	  || strncmp (shim.events[0].desc,
		      "eve_overrides: rejected file ", 29))
	fail ("oversized file not recorded in PCR 8", NULL);
    }
  else if (shim.tpm_present && !shim.tpm_fail)
    {
      if (shim.nevents < 1 || shim.events[0].pcr != 9
	  || shim.events[0].len != len
	  || memcmp (shim.events[0].data, data, len))
	fail ("file not measured first into PCR 9", NULL);
      for (i = 1; i < shim.nevents; i++)
	{
	  const char *d = shim.events[i].desc;
	  const unsigned char *c;
	  if (shim.events[i].pcr != 8)
	    fail ("rejection event not in PCR 8", d);
	  if (strncmp (d, "eve_overrides: rejected line ", 29))
	    fail ("rejection event description", d);
	  if (strlen (d) > 167)
	    fail ("rejection description too long", d);
	  for (c = (const unsigned char *) d; *c; c++)
	    if (*c < 0x20 || *c >= 0x7f)
	      fail ("unescaped byte in description", d);
	  nrej++;
	}
      if (nrej > 16)
	fail ("more than 16 rejection events", NULL);
    }
  else if (shim.nevents)
    fail ("event without a TPM", NULL);
}

static void
one (const unsigned char *data, size_t len, unsigned mode)
{
  size_t i;
  int require = !(mode & 4);

  cur_data = data;
  cur_len = len;
  cur_mode = mode;
  shim_reset ();
  shim.tpm_present = !(mode & 1);
  shim.tpm_fail = !!(mode & 2);
  for (i = 0; i < ARRAY_SIZE (presets); i++)
    shim_preset (presets[i][0], presets[i][1]);
  shim_run ("/config/grub.cfg", data, len, require, NULL);
  check (data, len, require);
  shim_reset ();
}

int
LLVMFuzzerTestOneInput (const unsigned char *data, size_t len)
{
  unsigned mode;
  /* The first byte picks the TPM mode, the rest is the file.  */
  if (!len)
    return 0;
  mode = data[0];
  one (data + 1, len - 1, mode);
  return 0;
}

#ifdef FUZZ_STANDALONE
static const char *const dict[] = {
  "set_global ", "set ", "export ", "\"", "$", "${", "}", "=", " ", "\n",
  "\r", "#", ";", "\\", "'", "dom0_extra_args", "dom0_console",
  "dom0_platform_tweaks", "hv_dom0_mem_settings", "hv_dom0_cpu_settings",
  "console=ttyS0,115200n8", "dom0_mem=", ",max:", "getty", "eve_root=",
  "init=", "pci=noaer,", "isolcpus=", "timeout", "set_no_dirsync",
  "set_isolcpus", "$dom0_extra_args", "${dom0_console}", "\0",
  "eve_install_disk=", "modprobe.blacklist=wdat_wdt",
};

static size_t
mutate (unsigned char *buf, size_t len, size_t max)
{
  int n = 1 + rand () % 4;
  while (n--)
    {
      size_t pos = len ? (size_t) rand () % (len + 1) : 0;
      switch (rand () % 5)
	{
	case 0:		/* flip a byte */
	  if (len)
	    buf[pos % len] = rand ();
	  break;
	case 1:		/* delete a run */
	  if (len)
	    {
	      size_t k = 1 + rand () % 8;
	      pos %= len;
	      if (k > len - pos)
		k = len - pos;
	      memmove (buf + pos, buf + pos + k, len - pos - k);
	      len -= k;
	    }
	  break;
	case 2:		/* insert a dictionary word */
	case 3:
	  {
	    const char *w = dict[rand () % ARRAY_SIZE (dict)];
	    size_t k = strlen (w) ? strlen (w) : 1;
	    if (len + k <= max)
	      {
		memmove (buf + pos + k, buf + pos, len - pos);
		memcpy (buf + pos, w, k);
		len += k;
	      }
	  }
	  break;
	case 4:		/* duplicate a run */
	  if (len)
	    {
	      size_t from = rand () % len, k = 1 + rand () % 64;
	      if (k > len - from)
		k = len - from;
	      if (len + k <= max)
		{
		  memmove (buf + pos + k, buf + pos, len - pos);
		  memmove (buf + pos, buf + from + (from >= pos ? k : 0), k);
		  len += k;
		}
	    }
	  break;
	}
    }
  return len;
}

int
main (int argc, char **argv)
{
  enum { MAX = 70000 };
  static unsigned char seeds[64][4096];
  static size_t seedlen[64];
  static unsigned char buf[MAX + 1];
  int nseeds = 0, c;
  long iters = 100000, i;
  unsigned seed = 1;

  while ((c = getopt (argc, argv, "i:s:")) != -1)
    if (c == 'i')
      iters = atol (optarg);
    else if (c == 's')
      seed = atoi (optarg);
    else
      return 2;
  for (; optind < argc && nseeds < 64; optind++)
    {
      FILE *f = fopen (argv[optind], "rb");
      if (!f)
	continue;
      seedlen[nseeds] = fread (seeds[nseeds], 1, sizeof (seeds[0]), f);
      fclose (f);
      nseeds++;
    }
  if (!nseeds)
    return 2;
  srand (seed);
  for (i = 0; i < iters; i++)
    {
      int s = rand () % nseeds;
      size_t len = seedlen[s];
      memcpy (buf, seeds[s], len);
      len = mutate (buf, len, MAX);
      one (buf, len, rand () % 8);
    }
  printf ("fuzz: %ld iterations, %d seeds, seed %u: no oracle failure\n",
	  iters, nseeds, seed);
  return 0;
}
#endif
