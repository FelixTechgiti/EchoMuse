/* Prove the shm_open shim behaves like the thing nqptp and shairport-sync
 * expect, on a host, before either of them is cross-compiled.
 *
 * android_shm.c is included whole — the emos/init/pwcheck.c trick — so this
 * drives the real functions rather than a copy. Without it the shim could
 * only be exercised on a rooted Echo, which is the one place a mistake in it
 * is expensive: a clock interface that silently maps the wrong object gives
 * shairport a record that never changes, which presents as AirPlay 2 audio
 * that will not synchronise rather than as anything to do with memory.
 *
 *   cc -O2 -o shmcheck shmcheck.c && ./shmcheck
 *
 * Exits non-zero on the first failure and says which. Run by CI beside
 * ringsim --check and pwcheck.
 */
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#include "android_shm.c"

static int failures = 0;

static void ok(int cond, const char *what) {
  printf("%-58s %s\n", what, cond ? "ok" : "FAIL");
  if (!cond)
    failures++;
}

static void eq(const char *got, const char *want, const char *what) {
  int good = got != NULL && strcmp(got, want) == 0;
  printf("%-58s %s\n", what, good ? "ok" : "FAIL");
  if (!good) {
    printf("    got:  %s\n", got ? got : "(null)");
    printf("    want: %s\n", want);
    failures++;
  }
}

/* The record both programs share. Only the shape matters here — two copies
 * and a counter — because that shape is what makes a lock unnecessary and
 * therefore what makes this shim sufficient. */
struct fake_shm {
  unsigned long long counter;
  unsigned long long a;
  unsigned long long b;
};

static void test_paths(void) {
  char p[PATH_MAX];

  ok(em_shm_path("/nqptp", p, sizeof p) == 0, "a leading slash is accepted");
  eq(p, "/tmp/revoice-shmcheck/nqptp", "  and stripped, not doubled");

  ok(em_shm_path("nqptp", p, sizeof p) == 0, "a name without a slash works");
  eq(p, "/tmp/revoice-shmcheck/nqptp", "  and lands in the same place");

  /* nqptp builds per-client names by appending to the interface name, so a
   * name containing a slash is ordinary traffic and not an attack. */
  ok(em_shm_path("/nqptp/client3", p, sizeof p) == 0, "an inner slash is kept");
  eq(p, "/tmp/revoice-shmcheck/nqptp_client3", "  and flattened, not nested");

  /* The security property: a name reaches here from a config file, so it must
   * not be able to name a file outside the directory. */
  ok(em_shm_path("/../../etc/passwd", p, sizeof p) == 0, "traversal parses");
  eq(p, "/tmp/revoice-shmcheck/.._.._etc_passwd",
     "  and cannot escape the directory");

  errno = 0;
  ok(em_shm_path("", p, sizeof p) == -1 && errno == EINVAL,
     "an empty name is EINVAL, as POSIX says");
  errno = 0;
  ok(em_shm_path("///", p, sizeof p) == -1 && errno == EINVAL,
     "a name of nothing but slashes is EINVAL too");

  /* Truncation would silently produce a DIFFERENT object, which the two
   * programs would then disagree about with nothing failing. */
  char small[12];
  errno = 0;
  ok(em_shm_path("/nqptp", small, sizeof small) == -1 && errno == ENAMETOOLONG,
     "a path that will not fit fails rather than truncating");
}

static void test_round_trip(void) {
  const char *name = "/nqptp-shmcheck";

  (void)em_shm_unlink(name); /* from an earlier run */

  int wfd = em_shm_open(name, O_RDWR | O_CREAT, 0644);
  ok(wfd >= 0, "the writer can create the object");
  ok(ftruncate(wfd, sizeof(struct fake_shm)) == 0,
     "ftruncate sizes it (bionic has this; only shm_open was missing)");

  struct fake_shm *w = mmap(NULL, sizeof *w, PROT_READ | PROT_WRITE,
                            MAP_SHARED, wfd, 0);
  ok(w != MAP_FAILED, "the writer can map it read-write");

  /* The reader is a second open of the same name, which is exactly what
   * shairport-sync does — a different process, O_RDONLY, PROT_READ. */
  int rfd = em_shm_open(name, O_RDONLY, 0);
  ok(rfd >= 0, "a second opener finds the SAME object by name");
  struct fake_shm *r = mmap(NULL, sizeof *r, PROT_READ, MAP_SHARED, rfd, 0);
  ok(r != MAP_FAILED, "the reader can map it read-only");

  /* The thing that makes the whole interface lock-free: the writer updates
   * two copies, the reader compares them and retries. If the shim handed the
   * two sides different objects this reads zeroes for ever and looks like a
   * clock that never ticks. */
  w->counter = 42;
  w->a = 0x0123456789abcdefULL;
  w->b = w->a;
  ok(r->counter == 42, "the reader sees the writer's update");
  ok(r->a == r->b, "the double-buffered copies agree, so no lock is needed");

  w->a = 0x1111111111111111ULL; /* a torn write, mid-update */
  ok(r->a != r->b, "and disagree mid-update, which is what the retry detects");
  w->b = w->a;
  ok(r->a == r->b, "  then agree once the update completes");

  ok(munmap(w, sizeof *w) == 0, "the writer can unmap");
  ok(munmap(r, sizeof *r) == 0, "the reader can unmap");
  close(wfd);
  close(rfd);

  ok(em_shm_unlink(name) == 0, "unlink removes it");
  ok(em_shm_open(name, O_RDONLY, 0) == -1,
     "and it is gone afterwards, so a stale record cannot be read");
}

static void test_dir_is_configurable(void) {
  /* The default has to be a tmpfs — /dev is one on Android and /dev/shm does
   * not exist there. The test cannot mount anything, so it checks the
   * default is what the comment says and that the override is honoured. */
  unsetenv("REVOICE_SHM_DIR");
  eq(em_shm_dir(), "/dev/revoice-shm", "the default directory is the tmpfs one");
  setenv("REVOICE_SHM_DIR", "/tmp/revoice-shmcheck", 1);
  eq(em_shm_dir(), "/tmp/revoice-shmcheck", "REVOICE_SHM_DIR overrides it");
  setenv("REVOICE_SHM_DIR", "", 1);
  eq(em_shm_dir(), "/dev/revoice-shm", "an EMPTY override is not an override");
  setenv("REVOICE_SHM_DIR", "/tmp/revoice-shmcheck", 1);
}

int main(void) {
  test_dir_is_configurable();
  test_paths();
  test_round_trip();

  /* /tmp is not a tmpfs everywhere, so the warning this run may or may not
   * have printed is not asserted on — what is asserted is that it did not
   * stop anything, which is the behaviour that matters: an operator who
   * points this somewhere deliberately is entitled to. */
  printf("\n%s\n", failures ? "shmcheck: FAILURES" : "shmcheck: all ok");
  return failures ? 1 : 0;
}
