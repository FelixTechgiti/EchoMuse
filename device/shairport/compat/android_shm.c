/*
 * android_shm.c — POSIX shared memory objects, for a libc that has none
 * =====================================================================
 *
 * bionic has no `shm_open` and no `shm_unlink`, at any API level. They are
 * the ONLY interface between nqptp and shairport-sync: nqptp writes the
 * master clock into a `struct shm_structure` and shairport reads it, and
 * that is the whole of the AirPlay 2 clock path (#79).
 *
 * Everything else in that path bionic already has — `ftruncate`, `mmap`,
 * `munmap`, `MAP_SHARED`. **There is no process-shared mutex and no
 * semaphore anywhere in it**: nqptp writes the record twice and the reader
 * `memcmp`s the two copies and retries until they agree (ptp-utilities.c).
 * That is what makes this a shim of ninety lines rather than a port — if
 * either side had reached for `pthread_mutexattr_setpshared` the answer
 * would be different, and it is worth re-checking that on an nqptp bump
 * rather than assuming.
 *
 * So a POSIX shared memory object is a file that both processes mmap, and
 * that is *literally* what glibc does: `shm_open` opens a path under
 * /dev/shm. Android has no /dev/shm, so this picks the directory instead.
 *
 * ── The directory has to be a tmpfs, and this checks ──────────────────────
 *
 * nqptp rewrites the struct at PTP rate. A MAP_SHARED mapping of a file on
 * **flash** has its dirty pages written back by the kernel on its own
 * schedule, for ever, for the life of the daemon — so backing this with
 * /data/local/tmp is not a slower version of the right answer, it is
 * continuous writes to the eMMC of a 2015 speaker that has to keep working.
 * Nothing would report it and nothing would fail; the device would simply
 * age.
 *
 * `/dev` is a tmpfs on Android (init mounts it before anything else runs),
 * so `/dev/revoice-shm` is RAM, is root-owned, and is gone on reboot —
 * which is correct for a clock record that means nothing across a boot.
 *
 * It is checked rather than assumed: `statfs` reports TMPFS_MAGIC or it does
 * not, and a directory that is not tmpfs gets one loud line on stderr. It
 * does not refuse — an operator pointing REVOICE_SHM_DIR somewhere
 * deliberately is entitled to — but the hazard is stated where somebody
 * reading a log can find it, rather than left to be discovered as wear.
 *
 * ── Name mapping ─────────────────────────────────────────────────────────
 *
 * POSIX says a shared memory name should begin with a slash and contain no
 * others; nqptp uses "/nqptp" and builds client names by appending. The
 * leading slashes are stripped and any remaining slash becomes an underscore,
 * so a name can never escape the directory — `shm_open("/../../etc/x")` lands
 * on `<dir>/.._.._etc_x` and not on /etc. That is a security property, not
 * tidiness, because the name reaches here from a config file.
 *
 * ── What is NOT implemented, deliberately ────────────────────────────────
 *
 * No `shm_open` on a name of "" or "/" (EINVAL, as POSIX requires), and no
 * emulation of the O_EXCL-across-a-reboot semantics glibc gets from /dev/shm
 * persisting: the directory is tmpfs, so a reboot clears it, which is what
 * both programs want anyway.
 *
 * This file is compiled on the host by shmcheck.c, which #includes it whole
 * and drives these functions — the emos/init/pwcheck.c pattern, for the same
 * reason: the alternative is a shim nobody can run until it is on a device,
 * and the device is the one place a mistake here is expensive.
 */

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <sys/types.h>
#include <unistd.h>

#include "android_shm.h"

#ifndef EM_SHM_DEFAULT_DIR
#define EM_SHM_DEFAULT_DIR "/dev/revoice-shm"
#endif

/* linux/magic.h is not in the NDK sysroot at every API level; the constant
 * is a kernel ABI value and has not moved since tmpfs existed. */
#ifndef EM_TMPFS_MAGIC
#define EM_TMPFS_MAGIC 0x01021994
#endif

const char *em_shm_dir(void) {
  const char *dir = getenv("REVOICE_SHM_DIR");
  if (dir != NULL && dir[0] != '\0')
    return dir;
  return EM_SHM_DEFAULT_DIR;
}

/*
 * Build <dir>/<flattened name>. Returns 0, or -1 with errno set.
 *
 * EINVAL for a name that is empty or nothing but slashes — POSIX's own
 * answer, and the one nqptp's die() message is written against.
 * ENAMETOOLONG rather than a truncated path, because a truncated path is a
 * DIFFERENT shared memory object that both sides would then disagree about.
 */
int em_shm_path(const char *name, char *out, size_t outlen) {
  if (name == NULL || out == NULL) {
    errno = EINVAL;
    return -1;
  }
  while (*name == '/')
    name++;
  if (*name == '\0') {
    errno = EINVAL;
    return -1;
  }

  const char *dir = em_shm_dir();
  int n = snprintf(out, outlen, "%s/%s", dir, name);
  if (n < 0 || (size_t)n >= outlen) {
    errno = ENAMETOOLONG;
    return -1;
  }

  /* Flatten only the part after the directory — the directory itself is
   * ours and may legitimately contain slashes. */
  for (char *p = out + strlen(dir) + 1; *p != '\0'; p++)
    if (*p == '/')
      *p = '_';
  return 0;
}

/*
 * Make the directory, once, and say so if it is not RAM.
 *
 * The warning is emitted at most once per process: nqptp opens the interface
 * plus one object per client, and a line repeated per client is a line that
 * reads as noise rather than as the one thing worth knowing.
 */
static int em_shm_ensure_dir(void) {
  static int checked = 0;
  const char *dir = em_shm_dir();

  if (mkdir(dir, 0755) == -1 && errno != EEXIST)
    return -1;

  if (!checked) {
    checked = 1;
    struct statfs sfs;
    if (statfs(dir, &sfs) == 0 && (unsigned long)sfs.f_type != EM_TMPFS_MAGIC)
      fprintf(stderr,
              "revoice: shared memory directory \"%s\" is not a tmpfs. The "
              "PTP clock record is rewritten continuously, so this will write "
              "to flash for as long as the daemon runs. Point REVOICE_SHM_DIR "
              "at a tmpfs (the default is %s).\n",
              dir, EM_SHM_DEFAULT_DIR);
  }
  return 0;
}

int em_shm_open(const char *name, int oflag, mode_t mode) {
  char path[PATH_MAX];

  if (em_shm_path(name, path, sizeof(path)) == -1)
    return -1;
  if ((oflag & O_CREAT) != 0 && em_shm_ensure_dir() == -1)
    return -1;

  /* O_CLOEXEC is what glibc's shm_open does and is not optional here:
   * shairport-sync forks helper processes, and a descriptor onto the clock
   * record leaking into one is a mapping nobody can account for. */
  return open(path, oflag | O_CLOEXEC, mode);
}

int em_shm_unlink(const char *name) {
  char path[PATH_MAX];

  if (em_shm_path(name, path, sizeof(path)) == -1)
    return -1;
  return unlink(path);
}
