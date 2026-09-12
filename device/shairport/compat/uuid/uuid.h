/*
 * uuid/uuid.h — the three pieces of libuuid that shairport-sync uses, for a
 * platform that has no libuuid.
 *
 * bionic ships none, and util-linux — where libuuid actually lives — is a
 * large autotools tree with its own Android quirks. shairport-sync uses
 * exactly `uuid_t`, `uuid_generate_random` and `uuid_unparse_lower`, at one
 * site (shairport.c:556), to mint the AirPlay `pi` identifier. Cross-building
 * util-linux for that is the wrong trade; see android_uuid.c.
 *
 * Named uuid/uuid.h so `#include <uuid/uuid.h>` resolves against -I/compat and
 * no upstream source is patched — the same rule the rest of compat/ follows.
 */

#ifndef REVOICE_UUID_UUID_H
#define REVOICE_UUID_UUID_H

#ifdef __cplusplus
extern "C" {
#endif

/* libuuid's uuid_t is an array type, and that is load-bearing: callers declare
 * `uuid_t binuuid;` and pass it bare, relying on array-to-pointer decay. A
 * struct or a pointer typedef would compile at the declaration and fail at the
 * call. */
typedef unsigned char uuid_t[16];

/* RFC 4122 version 4. Cannot fail in a way the caller could act on — libuuid's
 * signature returns void — so a failure to read entropy is reported on stderr
 * and answered with a weaker source rather than a predictable constant. */
void uuid_generate_random(uuid_t out);

/* 36 characters plus a NUL. The caller supplies the space, exactly as libuuid
 * requires; UUID_STR_LEN is the size to declare. */
#define UUID_STR_LEN 37
void uuid_unparse_lower(const uuid_t uu, char *out);

#ifdef __cplusplus
}
#endif

#endif /* REVOICE_UUID_UUID_H */
