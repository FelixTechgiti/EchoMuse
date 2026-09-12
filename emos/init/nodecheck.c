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

/* ── The partition table half ───────────────────────────────────────────────
 *
 * The block nodes are numbered by hand too, and there the number is the
 * board-specific part — so the report init makes about them reads the GPT.
 * Nothing acts on it yet (#131); what has to be right is that the reader
 * either answers correctly or says nothing, because a plausible wrong label
 * would be a measurement somebody later trusts.
 */
#define FAKEGPT "/tmp/emos-nodecheck.img"

static void put_u32(unsigned char *b, unsigned v)
{
    b[0] = v & 0xff; b[1] = (v >> 8) & 0xff;
    b[2] = (v >> 16) & 0xff; b[3] = (v >> 24) & 0xff;
}

static void put_u64(unsigned char *b, unsigned long long v)
{
    for (int i = 0; i < 8; i++)
        b[i] = (unsigned char)((v >> (8 * i)) & 0xff);
}

/* Write a synthetic GPT. `names[i]` is the label of partition i+1; NULL means
 * an unused slot, which gets an all-zero type GUID. A name is written as
 * UTF-16LE exactly as the spec has it, so the decoder is exercised rather
 * than accommodated. */
static void make_gpt(const char *sig, unsigned entries, unsigned entry_size,
                     unsigned long long entry_lba, const char **names, int n)
{
    FILE *f = fopen(FAKEGPT, "wb");
    if (!f) {
        printf("FAIL  could not write %s\n", FAKEGPT);
        failures++;
        return;
    }
    unsigned char lba0[512];
    memset(lba0, 0, sizeof lba0);
    fwrite(lba0, 1, sizeof lba0, f);          /* protective MBR, unread */

    unsigned char hdr[512];
    memset(hdr, 0, sizeof hdr);
    memcpy(hdr, sig, 8);
    put_u64(hdr + 72, entry_lba);
    put_u32(hdr + 80, entries);
    put_u32(hdr + 84, entry_size);
    fwrite(hdr, 1, sizeof hdr, f);            /* LBA 1 */

    /* Pad to the entry array. */
    for (unsigned long long l = 2; l < entry_lba; l++)
        fwrite(lba0, 1, sizeof lba0, f);

    unsigned esz = entry_size ? entry_size : 128;
    unsigned char *ent = malloc(esz);
    if (!ent) {
        printf("FAIL  out of memory building a %u-byte entry\n", esz);
        failures++;
        fclose(f);
        return;
    }
    for (int i = 0; i < n; i++) {
        memset(ent, 0, esz);
        /* Guarded, because the deliberately malformed cases pass an entry_size
         * far below the spec minimum — writing the name at its fixed offset
         * would run off this buffer and take the whole check with it. */
        if (names[i] && esz >= 16)
            memset(ent, 0xab, 16);            /* a non-zero type GUID */
        if (names[i] && esz >= 56 + 36 * 2) {
            for (int k = 0; names[i][k] && k < 36; k++) {
                ent[56 + k * 2]     = (unsigned char)names[i][k];
                ent[56 + k * 2 + 1] = 0;      /* UTF-16LE high byte */
            }
        }
        fwrite(ent, 1, esz, f);
    }
    free(ent);
    fclose(f);
}

/* `want` is the partition number asked about; `expect` the label it must come
 * back with, or "" for "no answer". `want_rc` is the entry count the header
 * declares, or 0 when the table is unreadable. */
static void check_gpt(const char *label, int want, int want_rc,
                      const char *expect)
{
    int w[1] = { want };
    char out[1][GPT_LABEL_MAX];
    int rc = gpt_labels(FAKEGPT, w, out, 1);

    char detail[128];
    snprintf(detail, sizeof detail, "rc=%d label=%s", rc,
             out[0][0] ? out[0] : "(none)");
    ok(label, rc == want_rc && !strcmp(out[0], expect), detail);
}

static void gpt_cases(void)
{
    static const char *names[] = { "boot_a_amonet", "system", NULL, "cache",
                                   "userdata" };
    /* Five entries, so partition 5 is the last one addressable. */
    make_gpt("EFI PART", 5, 128, 2, names, 5);

    check_gpt("label of partition 1", 1, 5, "boot_a_amonet");
    check_gpt("label of partition 2", 2, 5, "system");
    check_gpt("an unused slot answers nothing", 3, 5, "");
    check_gpt("label of partition 5", 5, 5, "userdata");

    /* Past the declared count. Asking about p16 on a table with five entries
     * has to be silence, not the last entry or a read off the end. */
    check_gpt("past the entry count", 16, 5, "");
    check_gpt("partition 0 is not a partition", 0, 5, "");

    /* A larger entry_size must be honoured as the STRIDE. Getting this wrong
     * walks the array and yields names that look real — the failure that
     * would make the whole report untrustworthy. */
    make_gpt("EFI PART", 5, 256, 2, names, 5);
    check_gpt("entry_size 256 is the stride", 5, 5, "userdata");

    /* The entry array is where the header says. A fixed offset would be the
     * same board-specific assumption this is meant to remove. */
    make_gpt("EFI PART", 5, 128, 34, names, 5);
    check_gpt("entry array at LBA 34", 2, 5, "system");

    /* Refusals. Each must report an unreadable table rather than salvage
     * something: rc 0 and no label. */
    make_gpt("NOT PART", 5, 128, 2, names, 5);
    check_gpt("wrong signature", 2, 0, "");

    make_gpt("EFI PART", 5, 7, 2, names, 5);
    check_gpt("entry_size below the spec minimum", 2, 0, "");

    make_gpt("EFI PART", 5, 99999, 2, names, 5);
    check_gpt("entry_size absurdly large", 2, 0, "");

    make_gpt("EFI PART", 0, 128, 2, names, 5);
    check_gpt("zero entries", 2, 0, "");

    make_gpt("EFI PART", 5, 128, 1, names, 5);
    check_gpt("entry array inside the header", 2, 0, "");

    /* A non-ASCII label is reported as absent rather than mangled: a
     * half-decoded name that happens to match something is worse than none. */
    make_gpt("EFI PART", 5, 128, 2, names, 5);
    {
        /* The high byte of partition 2's first code unit: LBA 2 is byte 1024,
         * entry index 1 adds 128, the name field adds 56, and the high byte
         * is one past that. */
        FILE *f = fopen(FAKEGPT, "r+b");
        if (!f) {
            printf("FAIL  could not reopen %s\n", FAKEGPT);
            failures++;
        } else {
            fseek(f, 512 * 2 + 128 * 1 + 56 + 1, SEEK_SET);
            fputc(0x04, f);                   /* U+04xx — Cyrillic, not ASCII */
            fclose(f);
            check_gpt("a non-ASCII label is not mangled", 2, 5, "");
        }
    }

    /* No table at all. The ordinary case on a device whose eMMC node could
     * not be created, and it must not be an error. */
    unlink(FAKEGPT);
    check_gpt("no image at all", 2, 0, "");
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

    gpt_cases();

    put_dev("sound", "pcmC0D23p", NULL);

    if (failures) {
        printf("\n%d check(s) failed\n", failures);
        return 1;
    }
    printf("\ndevice node resolution prefers the kernel and falls back safely\n");
    return 0;
}
