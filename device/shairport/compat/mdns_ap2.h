/*
 * mdns_ap2.h — the part of the AirPlay 2 mDNS advertisement that can be
 * decided without a network.
 *
 * The backend itself (mdns_tinysvcmdns.c, which we replace wholesale) is
 * sockets and threads and cannot run in CI. Everything it has to get RIGHT
 * is here instead: what is currently advertised, how an update merges into
 * it, and the names that come out of it. mdnscheck.c drives these functions
 * on the host, the way shmcheck.c drives the shm shim.
 *
 * Nothing here includes a shairport-sync header, deliberately — that is what
 * keeps the test free of a stub `struct configuration`.
 */

#ifndef REVOICE_MDNS_AP2_H
#define REVOICE_MDNS_AP2_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* What is on the air right now. Held because an AirPlay 2 update arrives as
 * `mdns_update(NULL, secondary)` — the primary half is NOT resent — and
 * because re-announcing means building the whole advertisement again from
 * something. Upstream's backend kept no state at all, which is exactly why it
 * could not implement update. */
struct em_ad {
  char *ap1name;    /* "XXXXXXXXXXXX@Kitchen" -> _raop._tcp    */
  char *ap2name;    /* "Kitchen"              -> _airplay._tcp */
  int port;
  char **secondary; /* owned deep copy, NULL-terminated; NULL when classic */
};

/* Replace everything. Any of ap2name/secondary may be NULL (a classic build
 * passes both). 0 on success, -1 on allocation failure with the struct left
 * empty rather than half-populated. */
int em_ad_set(struct em_ad *ad, const char *ap1name, const char *ap2name, int port,
              char **secondary);

/* Merge an update. `secondary == NULL` KEEPS the current set rather than
 * clearing it: shairport calls mdns_update(NULL, secondary) for the AirPlay 2
 * records and would otherwise silently retire the ones it did not resend.
 * 0 on success, -1 on allocation failure with the previous set still intact. */
int em_ad_update(struct em_ad *ad, char **secondary);

void em_ad_free(struct em_ad *ad);

/* Whether the second service should go on the air at all. False for a classic
 * build, where config.regtype2 is never set and there are no secondary
 * records — so one file is correct for both flavours with no #ifdef. */
int em_ad_has_second_service(const struct em_ad *ad, const char *regtype2);

/* "_raop._tcp" -> "_raop._tcp.local". tinysvcmdns wants the trailing domain;
 * upstream's backend appended it by hand for the one service it registered. */
int em_regtype_local(const char *regtype, char *out, size_t outlen);

/* "kitchen" -> "kitchen.local", and "kitchen.local" unchanged. tinysvcmdns
 * will not answer for a name that does not end in .local — a bug upstream's
 * own release notes record fixing once already. */
int em_hostname_local(const char *in, char *out, size_t outlen);

#ifdef __cplusplus
}
#endif

#endif /* REVOICE_MDNS_AP2_H */
