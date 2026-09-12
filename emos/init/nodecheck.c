/* Prove init.c takes its device numbers from the kernel when the kernel has
 * them, and from the table only when it does not.
 *
 * emOS creates every node by hand, because this kernel has no devtmpfs and we
 * do not run ueventd. The numbers were read off a running FireOS device, so
 * they are correct for biscuit and a guess anywhere else — and the failure is
 * silent in the worst way: `mknod` succeeds whatever numbers it is handed, and
 * `open` on the resulting node succeeds too. `event2` is the volume button
 * here and the touchscreen on checkers, so a second board gets a device that
 * boots, registers, and has dead buttons.
 *
 * Every way the resolution can be wrong is invisible without hardware:
 *
 *   - not reading sysfs at all leaves the table load-bearing and looks
 *     identical to sysfs agreeing with it;
 *   - preferring the table over sysfs is the same thing with more code;
 *   - accepting a malformed `dev` attribute produces a node with a plausible
 *     number that nothing can open, which reads as a driver that failed to
 *     load rather than as a parse error.
 *
 * init.c is included whole, the same trick ringsim.c, pwcheck.c, tmoutcheck.c
 * and pathcheck.c use, so this drives the real functions rather than a copy.
 *
 *   cc -O2 -o nodecheck nodecheck.c && ./nodecheck
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>

#define LEDDIR "/tmp/emos-nodecheck-led"
#define main   init_main_unused

#include "init.c"

#undef main

#define FAKESYS "/tmp/emos-nodecheck-sys"

static int failures;

static void ok(const char *label, int pass, const char *detail)
{
    if (pass) {
        printf("ok    %-52s %s\n", label, detail);
    } else {
        printf("FAIL  %-52s %s\n", label, detail);
        failures++;
    }
}

/* Place a fake /sys/class/<cls>/<name>/dev holding `body`, or remove it when
 * `body` is NULL. Directories are created one level at a time — there is no
 * mkdir -p in C and pulling one in for three levels is not worth it. */
static void put_dev(const char *cls, const char *name, const char *body)
{
    char dir[256], file[320];
    mkdir(FAKESYS, 0755);
    snprintf(dir, sizeof dir, "%s/class", FAKESYS);
    mkdir(dir, 0755);
    snprintf(dir, sizeof dir, "%s/class/%s", FAKESYS, cls);
    mkdir(dir, 0755);
    snprintf(dir, sizeof dir, "%s/class/%s/%s", FAKESYS, cls, name);
    mkdir(dir, 0755);
    snprintf(file, sizeof file, "%s/dev", dir);

    if (!body) {
        unlink(file);
        return;
    }
    FILE *f = fopen(file, "w");
    if (!f) {
        printf("FAIL  could not write %s\n", file);
        failures++;
        return;
    }
    fputs(body, f);
    fclose(f);
}

/* `body` NULL means the attribute is absent. `want_rc` 0 means the read must
 * succeed with exactly want_major:want_minor; -1 means it must refuse and
 * leave the caller's values untouched, because the caller's values are the
 * table entry and refusing has to mean "keep what shipped". */
static void check(const char *label, const char *body, int want_rc,
                  int want_major, int want_minor)
{
    put_dev("sound", "pcmC0D23p", body);

    /* Seeded with the table's own numbers for the speaker, so a refusal is
     * verifiably a no-op rather than a zeroing. */
    int maj = 116, min = 37;
    int rc = read_devnum(FAKESYS, "sound", "pcmC0D23p", &maj, &min);

    char detail[128];
    snprintf(detail, sizeof detail, "rc=%d %d:%d", rc, maj, min);
    ok(label, rc == want_rc && maj == want_major && min == want_minor, detail);
}

int main(void)
{
    /* The ordinary good case: the kernel has registered the device and says
     * which numbers it got. */
    check("sysfs answers", "116:37\n", 0, 116, 37);

    /* **sysfs wins when it disagrees**, and this is the whole point of the
     * change. The table describes the board it was read off; sysfs describes
     * the kernel that is running. A second board renumbers and only one of
     * those two follows. */
    check("sysfs disagrees with the table and wins", "116:99\n", 0, 116, 99);
    check("a different major is taken too", "42:7\n", 0, 42, 7);

    /* No trailing newline: sysfs always writes one, but a parser that needs
     * it is one kernel change away from failing closed. */
    check("no trailing newline", "116:37", 0, 116, 37);

    /* The attribute is not there — the device has not been registered yet,
     * which at this point in the boot is the ordinary state for anything whose
     * driver has not probed. Must fall back, not fail. */
    check("attribute absent", NULL, -1, 116, 37);

    /* Empty file. Seen when a driver is mid-registration. */
    check("attribute empty", "", -1, 116, 37);

    /* Malformed, four ways. Each must refuse rather than salvage a number:
     * a node made from half a parse is one that exists and cannot be opened,
     * which reads as a driver that failed to load. */
    check("no colon", "11637\n", -1, 116, 37);
    check("not a number", "sound:37\n", -1, 116, 37);
    check("trailing junk", "116:37x\n", -1, 116, 37);
    check("second colon", "116:37:1\n", -1, 116, 37);

    /* Major 0 is reserved. A node built from it exists and cannot be opened,
     * so accepting it would turn a missing driver into a mystery. */
    check("major zero is refused", "0:37\n", -1, 116, 37);

    /* Minor 0 is perfectly legal — /dev/wmtdetect is 154:0 in the table. */
    check("minor zero is accepted", "154:0\n", 0, 154, 0);

    /* The class mapping. Derived from the path, so it is worth pinning that
     * the cases are the ones that exist — and that the MediaTek chrdevs
     * resolve to NULL, which is what keeps them on their compiled-in numbers
     * rather than being looked up before the chip is detected. */
    ok("input class", sysfs_class("/dev/input/event2")
       && !strcmp(sysfs_class("/dev/input/event2"), "input"), "input");
    ok("sound class", sysfs_class("/dev/snd/pcmC0D24c")
       && !strcmp(sysfs_class("/dev/snd/pcmC0D24c"), "sound"), "sound");
    ok("combo chrdev has no class", sysfs_class("/dev/stpbt") == NULL, "NULL");
    ok("block node has no class", sysfs_class("/dev/block/mmcblk0p10") == NULL,
       "NULL");

    ok("node_name takes the last component",
       !strcmp(node_name("/dev/snd/pcmC0D23p"), "pcmC0D23p"), "pcmC0D23p");

    /* Every row of the real table must produce a name sysfs could file it
     * under, or no class at all. A row whose path ends in a slash, or a prefix
     * that stopped matching, would resolve nothing and fall back for ever —
     * exactly the silent no-op this check exists to rule out. */
    int classed = 0;
    for (unsigned i = 0; i < sizeof nodes / sizeof nodes[0]; i++) {
        const char *cls = sysfs_class(nodes[i].path);
        if (!cls)
            continue;
        classed++;
        if (node_name(nodes[i].path)[0] == '\0') {
            printf("FAIL  row %u (%s) has no name for sysfs\n", i, nodes[i].path);
            failures++;
        }
    }
    char detail[64];
    snprintf(detail, sizeof detail, "%d of %u rows", classed,
             (unsigned)(sizeof nodes / sizeof nodes[0]));
    /* If this ever reads 0, the path prefixes in sysfs_class stopped matching
     * the table and nothing else would have said so. */
    ok("the table has rows sysfs can answer for", classed > 0, detail);

    put_dev("sound", "pcmC0D23p", NULL);

    if (failures) {
        printf("\n%d check(s) failed\n", failures);
        return 1;
    }
    printf("\ndevice node resolution prefers the kernel and falls back safely\n");
    return 0;
}
