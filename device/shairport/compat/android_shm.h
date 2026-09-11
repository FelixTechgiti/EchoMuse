/*
 * android_shm.h — declarations for the shm_open shim. See android_shm.c.
 *
 * Separate from android_compat.h on purpose. That header is injected into
 * every shairport-sync translation unit with `-include`; this one is needed
 * by **nqptp too**, which is a different program with its own build, and a
 * header that pulls in pthread cancellation is not something to hand a
 * daemon that does not use threads.
 *
 * The rename to the standard names is guarded on __ANDROID__ so the same
 * sources compile on the host, where shm_open is real and shmcheck.c drives
 * the em_* functions directly.
 */

#ifndef REVOICE_ANDROID_SHM_H
#define REVOICE_ANDROID_SHM_H

#include <stddef.h>
#include <sys/types.h>

#ifdef __cplusplus
extern "C" {
#endif

/* The directory backing shared memory objects: $REVOICE_SHM_DIR, else
 * /dev/revoice-shm. Exposed so a caller can log what it resolved to. */
const char *em_shm_dir(void);

/* <dir>/<name with slashes flattened>. 0 on success; -1 with errno EINVAL
 * (empty name) or ENAMETOOLONG. Exposed for the test. */
int em_shm_path(const char *name, char *out, size_t outlen);

int em_shm_open(const char *name, int oflag, mode_t mode);
int em_shm_unlink(const char *name);

#ifdef __ANDROID__
#define shm_open em_shm_open
#define shm_unlink em_shm_unlink
#endif

#ifdef __cplusplus
}
#endif

#endif /* REVOICE_ANDROID_SHM_H */
