/*
 * Host stand-ins for the GRUB 2.12 interfaces eve_overrides.c uses, so the
 * command can be compiled and run as an ordinary process. Every definition
 * follows the GRUB 2.12 semantics the command depends on; where libc
 * differs, the GRUB behavior is reimplemented (see grub_snprintf).
 */
#ifndef EVE_OVERRIDES_SHIM_H
#define EVE_OVERRIDES_SHIM_H

#include <stdarg.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <sys/types.h>

typedef size_t grub_size_t;
typedef ssize_t grub_ssize_t;
typedef uint64_t grub_off_t;
typedef uint8_t grub_uint8_t;

typedef enum
{
  GRUB_ERR_NONE = 0,
  GRUB_ERR_OUT_OF_MEMORY,
  GRUB_ERR_BAD_ARGUMENT,
  GRUB_ERR_FILE_NOT_FOUND,
  GRUB_ERR_FILE_READ_ERROR,
} grub_err_t;

extern grub_err_t grub_errno;
grub_err_t grub_error (grub_err_t n, const char *fmt, ...);

#define GRUB_MOD_LICENSE(l)
#define GRUB_MOD_INIT(name) void grub_mod_init_##name (void)
#define GRUB_MOD_FINI(name) void grub_mod_fini_##name (void)
#define N_(s) s
#define ARRAY_SIZE(a) (sizeof (a) / sizeof ((a)[0]))

/* GRUB's ctype helpers are plain range checks, defined for any int.  */
static inline int grub_isdigit (int c) { return c >= '0' && c <= '9'; }
static inline int grub_isupper (int c) { return c >= 'A' && c <= 'Z'; }
static inline int grub_islower (int c) { return c >= 'a' && c <= 'z'; }
static inline int grub_isalpha (int c) { return grub_isupper (c) || grub_islower (c); }
static inline int grub_isalnum (int c) { return grub_isalpha (c) || grub_isdigit (c); }

#define grub_strlen strlen
#define grub_strcmp strcmp
#define grub_strncmp strncmp
#define grub_strchr strchr
#define grub_strstr strstr
#define grub_memcpy memcpy

int grub_snprintf (char *str, grub_size_t n, const char *fmt, ...);
char *grub_xasprintf (const char *fmt, ...);
int grub_printf (const char *fmt, ...);
#define grub_dprintf(cond, ...) ((void) 0)
unsigned long grub_strtoul (const char *s, const char **const end, int base);

void *grub_zalloc (grub_size_t size);
void grub_free (void *p);

grub_err_t grub_env_set (const char *name, const char *val);
const char *grub_env_get (const char *name);
grub_err_t grub_env_export (const char *name);

#define GRUB_FILE_TYPE_CONFIG 0x1
#define GRUB_FILE_TYPE_SKIP_SIGNATURE 0x10000
struct grub_file;
typedef struct grub_file *grub_file_t;
grub_file_t grub_file_open (const char *name, int type);
grub_off_t grub_file_size (grub_file_t f);
grub_ssize_t grub_file_read (grub_file_t f, void *buf, grub_size_t len);
grub_err_t grub_file_close (grub_file_t f);

#define GRUB_STRING_PCR 8
#define GRUB_BINARY_PCR 9
int grub_tpm_present (void);
grub_err_t grub_tpm_measure (unsigned char *buf, grub_size_t size,
			     grub_uint8_t pcr, const char *description);

typedef enum { ARG_TYPE_NONE, ARG_TYPE_STRING, ARG_TYPE_INT } grub_arg_type_t;
struct grub_arg_option
{
  const char *longarg;
  int shortarg;
  int flags;
  const char *doc;
  const char *arg;
  grub_arg_type_t type;
};
struct grub_arg_list
{
  int set;
  union
  {
    char *arg;
    char **args;
  };
};
struct grub_extcmd_context
{
  void *extcmd;
  struct grub_arg_list *state;
  void *script;
};
typedef struct grub_extcmd_context *grub_extcmd_context_t;
typedef grub_err_t (*grub_extcmd_func_t) (grub_extcmd_context_t, int, char **);
typedef struct grub_extcmd *grub_extcmd_t;
grub_extcmd_t grub_register_extcmd (const char *name, grub_extcmd_func_t func,
				    int flags, const char *summary,
				    const char *description,
				    const struct grub_arg_option *parser);
void grub_unregister_extcmd (grub_extcmd_t cmd);

#endif
