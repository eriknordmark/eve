/*
 * Host implementations of the GRUB interfaces in grub/shim.h, recording what
 * the command does (environment, measurements, console output) so a test
 * driver can print and compare it.
 */
#include <stdio.h>
#include <stdlib.h>

#include <grub/shim.h>
#include "recorder.h"

grub_err_t grub_errno;

struct shim_state shim;

grub_err_t
grub_error (grub_err_t n, const char *fmt, ...)
{
  (void) fmt;
  grub_errno = n;
  return n;
}

/* GRUB 2.12 grub_vsnprintf returns 0 when n is 0 and otherwise the length
   the output would have had, as C does. libc returns that length for n == 0
   too, so the n == 0 case must not reach vsnprintf.  */
int
grub_snprintf (char *str, grub_size_t n, const char *fmt, ...)
{
  va_list ap;
  int ret;

  if (!n)
    return 0;
  va_start (ap, fmt);
  ret = vsnprintf (str, n, fmt, ap);
  va_end (ap);
  return ret;
}

char *
grub_xasprintf (const char *fmt, ...)
{
  va_list ap;
  char *s;

  va_start (ap, fmt);
  if (vasprintf (&s, fmt, ap) < 0)
    s = NULL;
  va_end (ap);
  if (!s)
    grub_errno = GRUB_ERR_OUT_OF_MEMORY;
  return s;
}

int
grub_printf (const char *fmt, ...)
{
  va_list ap;
  int ret;

  va_start (ap, fmt);
  ret = vsnprintf (shim.out + shim.outlen, sizeof (shim.out) - shim.outlen,
		   fmt, ap);
  va_end (ap);
  if (ret > 0)
    {
      shim.outlen += ret;
      if (shim.outlen >= sizeof (shim.out))
	shim.outlen = sizeof (shim.out) - 1;
    }
  return ret;
}

unsigned long
grub_strtoul (const char *s, const char **const end, int base)
{
  return strtoul (s, (char **) end, base);
}

void *
grub_zalloc (grub_size_t size)
{
  void *p = calloc (1, size);
  if (!p)
    grub_errno = GRUB_ERR_OUT_OF_MEMORY;
  return p;
}

void
grub_free (void *p)
{
  free (p);
}

static struct shim_var *
find (const char *name)
{
  int i;
  for (i = 0; i < shim.nvars; i++)
    if (strcmp (shim.vars[i].name, name) == 0)
      return &shim.vars[i];
  return NULL;
}

grub_err_t
grub_env_set (const char *name, const char *val)
{
  struct shim_var *v = find (name);

  if (!v)
    {
      if (shim.nvars == SHIM_MAX_VARS)
	abort ();
      v = &shim.vars[shim.nvars++];
      v->name = strdup (name);
      v->value = NULL;
      v->exported = 0;
    }
  free (v->value);
  v->value = strdup (val);
  v->changed = 1;
  return GRUB_ERR_NONE;
}

const char *
grub_env_get (const char *name)
{
  struct shim_var *v = find (name);
  return v ? v->value : NULL;
}

grub_err_t
grub_env_export (const char *name)
{
  struct shim_var *v = find (name);
  if (v)
    v->exported = 1;
  return GRUB_ERR_NONE;
}

struct grub_file
{
  unsigned char *data;
  grub_size_t size;
  grub_size_t off;
};

grub_file_t
grub_file_open (const char *name, int type)
{
  struct grub_file *f;

  shim.open_type = type;
  if (strcmp (name, shim.file_name) != 0)
    {
      grub_errno = GRUB_ERR_FILE_NOT_FOUND;
      return NULL;
    }
  f = calloc (1, sizeof (*f));
  f->data = malloc (shim.file_len ? shim.file_len : 1);
  memcpy (f->data, shim.file_data, shim.file_len);
  f->size = shim.file_len;
  return f;
}

grub_off_t
grub_file_size (grub_file_t f)
{
  return f->size;
}

grub_ssize_t
grub_file_read (grub_file_t f, void *buf, grub_size_t len)
{
  grub_size_t n = f->size - f->off;
  if (n > len)
    n = len;
  memcpy (buf, f->data + f->off, n);
  f->off += n;
  return n;
}

grub_err_t
grub_file_close (grub_file_t f)
{
  free (f->data);
  free (f);
  return GRUB_ERR_NONE;
}

int
grub_tpm_present (void)
{
  return shim.tpm_present;
}

grub_err_t
grub_tpm_measure (unsigned char *buf, grub_size_t size, grub_uint8_t pcr,
		  const char *description)
{
  struct shim_event *e;

  if (shim.tpm_fail)
    return grub_error (GRUB_ERR_BAD_ARGUMENT, "measure failed");
  if (shim.nevents == SHIM_MAX_EVENTS)
    abort ();
  e = &shim.events[shim.nevents++];
  e->pcr = pcr;
  e->len = size;
  e->data = malloc (size ? size : 1);
  memcpy (e->data, buf, size);
  e->desc = strdup (description);
  return GRUB_ERR_NONE;
}

static grub_extcmd_func_t registered_func;

grub_extcmd_t
grub_register_extcmd (const char *name, grub_extcmd_func_t func, int flags,
		      const char *summary, const char *description,
		      const struct grub_arg_option *parser)
{
  (void) flags;
  (void) summary;
  (void) description;
  shim.cmd_name = name;
  shim.options = parser;
  registered_func = func;
  return (grub_extcmd_t) &registered_func;
}

void
grub_unregister_extcmd (grub_extcmd_t cmd)
{
  (void) cmd;
  registered_func = NULL;
}

void grub_mod_init_eve_overrides (void);
void grub_mod_fini_eve_overrides (void);

/* Runs eve_overrides on data as FILE, as rootfs.cfg does, with the options
   GRUB's extcmd parser would have filled in.  */
grub_err_t
shim_run (const char *file, const void *data, grub_size_t len,
	  int require_tpm, const char *pcr)
{
  struct grub_arg_list state[2];
  struct grub_extcmd_context ctxt = { NULL, state, NULL };
  char *args[1] = { (char *) file };
  grub_err_t err;

  shim.file_name = file;
  shim.file_data = data;
  shim.file_len = len;
  memset (state, 0, sizeof (state));
  state[0].set = require_tpm;
  state[1].set = pcr != NULL;
  state[1].arg = (char *) pcr;

  grub_mod_init_eve_overrides ();
  if (strcmp (shim.cmd_name, "eve_overrides") != 0
      || strcmp (shim.options[0].longarg, "require-tpm") != 0
      || strcmp (shim.options[1].longarg, "pcr") != 0)
    abort ();
  grub_errno = GRUB_ERR_NONE;
  err = registered_func (&ctxt, 1, args);
  grub_mod_fini_eve_overrides ();
  return err;
}

void
shim_reset (void)
{
  int i;

  for (i = 0; i < shim.nvars; i++)
    {
      free (shim.vars[i].name);
      free (shim.vars[i].value);
    }
  for (i = 0; i < shim.nevents; i++)
    {
      free (shim.events[i].data);
      free (shim.events[i].desc);
    }
  memset (&shim, 0, sizeof (shim));
  shim.tpm_present = 1;
}

/* Sets an initial variable, as rootfs.cfg would have before the call.  */
void
shim_preset (const char *name, const char *val)
{
  grub_env_set (name, val);
  grub_env_export (name);
  shim.vars[shim.nvars - 1].changed = 0;
}
