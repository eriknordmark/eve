/* Recorder state shared by the shim and the test drivers.  */
#ifndef EVE_OVERRIDES_RECORDER_H
#define EVE_OVERRIDES_RECORDER_H

#include <grub/shim.h>

#define SHIM_MAX_VARS 64
#define SHIM_MAX_EVENTS 64

struct shim_var
{
  char *name;
  char *value;
  int exported;
  int changed;
};

struct shim_event
{
  int pcr;
  grub_size_t len;
  unsigned char *data;
  char *desc;
};

struct shim_state
{
  int tpm_present;
  int tpm_fail;
  const char *file_name;
  const void *file_data;
  grub_size_t file_len;
  int open_type;
  const char *cmd_name;
  const struct grub_arg_option *options;
  struct shim_var vars[SHIM_MAX_VARS];
  int nvars;
  struct shim_event events[SHIM_MAX_EVENTS];
  int nevents;
  char out[4096];
  grub_size_t outlen;
};

extern struct shim_state shim;

void shim_reset (void);
void shim_preset (const char *name, const char *val);
grub_err_t shim_run (const char *file, const void *data, grub_size_t len,
		     int require_tpm, const char *pcr);

#endif
