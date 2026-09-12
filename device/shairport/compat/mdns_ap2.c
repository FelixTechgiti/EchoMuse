/*
 * mdns_ap2.c — see mdns_ap2.h.
 *
 * Pure: no sockets, no threads, no shairport-sync headers. Driven on the host
 * by mdnscheck.c.
 */

#include "mdns_ap2.h"

#include <stdlib.h>
#include <string.h>

/* Deep-copy a NULL-terminated array of C strings. Returns NULL for a NULL or
 * empty input, which the caller reads as "nothing to advertise" — an empty
 * array and no array mean the same thing here, and collapsing them removes a
 * case from every reader. */
static char **txt_dup(char **txt) {
  size_t n = 0;
  if (txt == NULL)
    return NULL;
  while (txt[n] != NULL)
    n++;
  if (n == 0)
    return NULL;

  char **out = calloc(n + 1, sizeof(char *));
  if (out == NULL)
    return NULL;
  for (size_t i = 0; i < n; i++) {
    out[i] = strdup(txt[i]);
    if (out[i] == NULL) { /* unwind rather than hand back a partial array */
      for (size_t j = 0; j < i; j++)
        free(out[j]);
      free(out);
      return NULL;
    }
  }
  return out;
}

static void txt_free(char **txt) {
  if (txt == NULL)
    return;
  for (size_t i = 0; txt[i] != NULL; i++)
    free(txt[i]);
  free(txt);
}

int em_ad_set(struct em_ad *ad, const char *ap1name, const char *ap2name, int port,
              char **secondary) {
  if (ad == NULL || ap1name == NULL)
    return -1;

  /* Build the new state completely before touching the old, so a failure
   * leaves the caller advertising what it already was. */
  char *n1 = strdup(ap1name);
  char *n2 = (ap2name != NULL) ? strdup(ap2name) : NULL;
  char **sec = txt_dup(secondary);

  if (n1 == NULL || (ap2name != NULL && n2 == NULL) || (secondary != NULL && secondary[0] != NULL &&
                                                        sec == NULL)) {
    free(n1);
    free(n2);
    txt_free(sec);
    return -1;
  }

  em_ad_free(ad);
  ad->ap1name = n1;
  ad->ap2name = n2;
  ad->port = port;
  ad->secondary = sec;
  return 0;
}

int em_ad_update(struct em_ad *ad, char **secondary) {
  if (ad == NULL)
    return -1;
  /* NULL means "unchanged", not "none". shairport's four update call sites all
   * pass NULL for the primary records and only ever resend the secondary set;
   * reading NULL as "clear it" would retire _raop._tcp's TXT on the first
   * group change. */
  if (secondary == NULL)
    return 0;

  char **sec = txt_dup(secondary);
  if (secondary[0] != NULL && sec == NULL)
    return -1;

  txt_free(ad->secondary);
  ad->secondary = sec;
  return 0;
}

void em_ad_free(struct em_ad *ad) {
  if (ad == NULL)
    return;
  free(ad->ap1name);
  free(ad->ap2name);
  txt_free(ad->secondary);
  ad->ap1name = NULL;
  ad->ap2name = NULL;
  ad->secondary = NULL;
  ad->port = 0;
}

int em_ad_has_second_service(const struct em_ad *ad, const char *regtype2) {
  if (ad == NULL || regtype2 == NULL || regtype2[0] == '\0')
    return 0;
  if (ad->ap2name == NULL || ad->ap2name[0] == '\0')
    return 0;
  /* No records means nothing to say about the service, and an _airplay._tcp
   * instance with an empty TXT is worse than none: a client finds it, asks
   * what it can do, and gets an answer with no features in it. */
  if (ad->secondary == NULL || ad->secondary[0] == NULL)
    return 0;
  return 1;
}

/* Append `suffix` to `in` unless it is already there. Case-sensitive, which is
 * correct for the two callers below: both compare against a literal that
 * shairport and tinysvcmdns themselves write in lower case. */
static int suffix_once(const char *in, const char *suffix, char *out, size_t outlen) {
  if (in == NULL || out == NULL)
    return -1;
  size_t li = strlen(in), ls = strlen(suffix);
  if (li == 0)
    return -1;

  int have = (li >= ls) && (strcmp(in + li - ls, suffix) == 0);
  size_t need = li + (have ? 0 : ls) + 1;
  if (need > outlen)
    return -1;

  memcpy(out, in, li);
  if (!have)
    memcpy(out + li, suffix, ls);
  out[need - 1] = '\0';
  return 0;
}

int em_regtype_local(const char *regtype, char *out, size_t outlen) {
  return suffix_once(regtype, ".local", out, outlen);
}

int em_hostname_local(const char *in, char *out, size_t outlen) {
  return suffix_once(in, ".local", out, outlen);
}
