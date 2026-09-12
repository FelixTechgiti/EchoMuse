/* Prove init.c finds the console records across the rename.
 *
 * The console password and the idle timeout are written by the FIRMWARE and
 * read by INIT, and the two arrive by completely different means: the
 * firmware by OTA, a new init only when somebody flashes a boot partition.
 * The rename moved `/data/local/etc/echomuse/` to `/data/local/etc/revoice/`
 * on both sides in one commit, so every combination of old and new has to
 * work — and every way it can be wrong is silent:
 *
 *   - a NEW init that does not fall back reads no record at all, so the
 *     console has no password while the dashboard shows one configured;
 *   - an init that preferred the LEGACY path would keep enforcing a stale
 *     record after the firmware had written a new one, which in the clearing
 *     direction means a password belonging to a previous owner stays in front
 *     of the console after its removal was reported as done.
 *
 * Neither is visible without a device in front of you and the password you
 * are trying to prove is gone.
 *
 * init.c is included whole, the same trick ringsim.c, pwcheck.c and
 * tmoutcheck.c use, so this drives the real functions rather than a copy.
 *
 *   cc -O2 -o pathcheck pathcheck.c && ./pathcheck
 *
 * This is a separate tool from pwcheck rather than more cases inside it
 * because pwcheck's stdout is PARSED line by line by ci.yml as
 * `<password>\t<record>`; anything else printed there fails the hash check
 * with a message about the wrong thing entirely.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define LEDDIR "/tmp/emos-pathcheck"
/* The records live under /data on a device. Redirected here so the check runs
 * anywhere — as a non-root CI runner on a machine with no /data. */
#define CONSOLE_PW           "/tmp/emos-pathcheck.current.pw"
#define CONSOLE_PW_LEGACY    "/tmp/emos-pathcheck.legacy.pw"
#define CONSOLE_TMOUT        "/tmp/emos-pathcheck.current.timeout"
#define CONSOLE_TMOUT_LEGACY "/tmp/emos-pathcheck.legacy.timeout"
#define main init_main_unused

#include "init.c"

#undef main

static int failures;

/* A record of the shape pw_load parses. The hash is never verified by
 * pw_load — it only has to be 32 bytes of hex — so the ITERATION COUNT is
 * used as the marker saying which file was read.
 *
 * The caller supplies the buffer, and that is not style: a shared static one
 * would make `f(record(7), record(11))` pass the SAME pointer twice, and the
 * case it breaks is exactly the one that matters here — both records present
 * and disagreeing, which would then silently compare a record against itself.
 */
static void record_with_iters(char *buf, size_t n, int iters)
{
    snprintf(buf, n, "%d:0f1e2d3c4b5a6978:"
             "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
             iters);
}

static void put(const char *path, const char *body)
{
    if (!body) {
        unlink(path);
        return;
    }
    FILE *f = fopen(path, "w");
    if (!f) {
        printf("FAIL  could not write %s\n", path);
        failures++;
        return;
    }
    fprintf(f, "%s\n", body);
    fclose(f);
}

/* `current` and `legacy` are the record bodies to place, NULL for absent.
 * `want` is the iteration count pw_load must come back with, or 0 for "no
 * password to enforce". */
static void check_pw(const char *label, const char *current,
                     const char *legacy, long want)
{
    put(CONSOLE_PW, current);
    put(CONSOLE_PW_LEGACY, legacy);

    long iters = 0;
    unsigned char salt[32], hash[32];
    int saltlen = 0;
    int loaded = pw_load(&iters, salt, &saltlen, hash);

    long got = loaded ? iters : 0;
    if (got != want) {
        printf("FAIL  %-46s got %ld, want %ld\n", label, got, want);
        failures++;
    } else {
        printf("ok    %-46s %ld\n", label, got);
    }
}

static void check_tmout(const char *label, const char *current,
                        const char *legacy, long want)
{
    put(CONSOLE_TMOUT, current);
    put(CONSOLE_TMOUT_LEGACY, legacy);

    long got = console_timeout_secs();
    if (got != want) {
        printf("FAIL  %-46s got %lds, want %lds\n", label, got, want);
        failures++;
    } else {
        printf("ok    %-46s %lds\n", label, got);
    }
}

int main(void)
{
    char current[160], legacy[160];
    record_with_iters(current, sizeof current, 7);
    record_with_iters(legacy, sizeof legacy, 11);

    /* Never configured. The ordinary case for most devices, and it must read
     * as no password rather than as an error — a console nobody can open is
     * the one failure this file must never cause. */
    check_pw("no record anywhere", NULL, NULL, 0);

    /* A device that has taken firmware since the rename. */
    check_pw("current only", current, NULL, 7);

    /* NEW init, OLD firmware: the firmware writes only the pre-rename path.
     * This is the case the fallback exists for. */
    check_pw("legacy only", NULL, legacy, 11);

    /* Both present, and they DISAGREE. Current wins: once the firmware has
     * written both, the legacy copy is at best a duplicate and at worst the
     * record somebody just changed. */
    check_pw("both present, current wins", current, legacy, 7);

    /* An EMPTY current record must not fall through to the legacy one.
     *
     * The firmware's clear removes every path, so this shape should not
     * occur — but "should not occur" is what the rename said about mixed
     * versions too. What init can hold on its own is that a record it CAN
     * open and cannot parse reads as no password, rather than as a reason to
     * go looking for an older one. */
    check_pw("empty current does not fall through", "", legacy, 0);

    check_tmout("no record anywhere", NULL, NULL, 0);
    check_tmout("current only", "15", NULL, 900);
    check_tmout("legacy only", NULL, "30", 1800);
    check_tmout("both present, current wins", "15", "30", 900);

    /* Tidy up rather than leaving four files in /tmp on a developer's
     * machine, and so a second run cannot pass on the first run's leftovers. */
    unlink(CONSOLE_PW);
    unlink(CONSOLE_PW_LEGACY);
    unlink(CONSOLE_TMOUT);
    unlink(CONSOLE_TMOUT_LEGACY);

    if (failures) {
        printf("\n%d check(s) failed\n", failures);
        return 1;
    }
    printf("\nall console record paths resolve correctly\n");
    return 0;
}
