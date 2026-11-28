/*
 * run [-n] [-F] [-r] [-p PCR] [-e NAME=VALUE]... FILE
 *
 * Runs eve_overrides on FILE (presented to the command as /config/grub.cfg)
 * and prints what it did, one record per line, for run.sh to compare:
 *
 *   open TYPE                        flags passed to grub_file_open
 *   event PCR LEN DATA | DESC        each TPM measurement (DATA escaped,
 *                                    shown only for PCR 8)
 *   out TEXT                         each line printed to the console
 *   ret ERR                          the command's return value
 *   set[*] NAME=VALUE                each variable the command set, sorted;
 *                                    * if exported
 *
 * -n: no TPM. -F: measurement fails. -r: --require-tpm. -p: --pcr PCR.
 * -e: a variable set before the call, as rootfs.cfg sets the board defaults.
 */
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

#include "shim/recorder.h"

static void
print_escaped (const unsigned char *s, size_t len)
{
  size_t i;
  for (i = 0; i < len; i++)
    if (s[i] >= 0x20 && s[i] < 0x7f && s[i] != '\\')
      putchar (s[i]);
    else
      printf ("\\x%02x", s[i]);
}

static int
cmp_var (const void *a, const void *b)
{
  return strcmp (((const struct shim_var *) a)->name,
		 ((const struct shim_var *) b)->name);
}

int
main (int argc, char **argv)
{
  int c, i, require = 0;
  const char *pcr = NULL;
  FILE *f;
  char *data;
  long len;
  grub_err_t err;
  char *p, *line;

  shim_reset ();
  while ((c = getopt (argc, argv, "nFrp:e:")) != -1)
    switch (c)
      {
      case 'n':
	shim.tpm_present = 0;
	break;
      case 'F':
	shim.tpm_fail = 1;
	break;
      case 'r':
	require = 1;
	break;
      case 'p':
	pcr = optarg;
	break;
      case 'e':
	p = strchr (optarg, '=');
	if (!p)
	  return 2;
	*p = 0;
	shim_preset (optarg, p + 1);
	break;
      default:
	return 2;
      }
  if (optind != argc - 1)
    return 2;

  f = fopen (argv[optind], "rb");
  if (!f)
    return 2;
  fseek (f, 0, SEEK_END);
  len = ftell (f);
  fseek (f, 0, SEEK_SET);
  data = malloc (len ? len : 1);
  if (fread (data, 1, len, f) != (size_t) len)
    return 2;
  fclose (f);

  err = shim_run ("/config/grub.cfg", data, len, require, pcr);

  printf ("open 0x%x\n", shim.open_type);
  for (i = 0; i < shim.nevents; i++)
    {
      struct shim_event *e = &shim.events[i];
      printf ("event %d %zu ", e->pcr, e->len);
      if (e->pcr == GRUB_STRING_PCR)
	print_escaped (e->data, e->len);
      printf (" | %s\n", e->desc);
    }
  for (line = strtok (shim.out, "\n"); line; line = strtok (NULL, "\n"))
    printf ("out %s\n", line);
  printf ("ret %d\n", err);
  qsort (shim.vars, shim.nvars, sizeof (shim.vars[0]), cmp_var);
  for (i = 0; i < shim.nvars; i++)
    if (shim.vars[i].changed)
      printf ("set%s %s=%s\n", shim.vars[i].exported ? "*" : "",
	      shim.vars[i].name, shim.vars[i].value);
  free (data);
  shim_reset ();
  return 0;
}
