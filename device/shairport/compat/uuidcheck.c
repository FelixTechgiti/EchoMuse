/* Prove the uuid shim on a host, because the one place its output is read is
 * an iPhone's AirPlay picker.
 *
 * android_uuid.c is included whole — the pwcheck.c trick, as shmcheck.c and
 * mdnscheck.c do — so this drives the real functions rather than a copy.
 *
 *   cc -O2 -o uuidcheck uuidcheck.c && ./uuidcheck
 *
 * The failure this is here to catch is a quiet one. shairport-sync puts the
 * result in `pi=` on the _airplay._tcp TXT records; a malformed or repeated
 * identifier does not produce an error anywhere, it produces a speaker that
 * behaves oddly in a picker on somebody's phone.
 *
 * Exits non-zero on the first failure and says which. Run by CI beside
 * shmcheck, mdnscheck, ringsim --check and pwcheck.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "android_uuid.c"

static int failures = 0;

static void ok(int cond, const char *what) {
  printf("%-64s %s\n", what, cond ? "ok" : "FAIL");
  if (!cond)
    failures++;
}

/* Exactly the shape shairport.c:556 uses, so a mistake in the header's array
 * typedef shows up here as a compile error rather than on a device. */
static void mint(char *out) {
  uuid_t binuuid;
  uuid_generate_random(binuuid);
  uuid_unparse_lower(binuuid, out);
}

static int is_hex_lower(char c) {
  return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f');
}

int main(void) {
  char s[UUID_STR_LEN];

  /* ---- formatting ---------------------------------------------------- */
  mint(s);
  printf("  sample: %s\n", s);
  ok(strlen(s) == 36, "unparse: 36 characters");
  ok(s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-',
     "unparse: dashes at 8, 13, 18 and 23");

  int hexcount = 0, bad = 0;
  for (int i = 0; i < 36; i++) {
    if (i == 8 || i == 13 || i == 18 || i == 23)
      continue;
    if (is_hex_lower(s[i]))
      hexcount++;
    else
      bad++;
  }
  ok(hexcount == 32 && bad == 0, "unparse: 32 lower-case hex digits and nothing else");

  /* A known vector, so the nibble order cannot silently reverse. */
  uuid_t fixed = {0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
                  0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10};
  uuid_unparse_lower(fixed, s);
  ok(strcmp(s, "01234567-89ab-cdef-fedc-ba9876543210") == 0,
     "unparse: a known vector renders byte for byte");
  if (strcmp(s, "01234567-89ab-cdef-fedc-ba9876543210") != 0)
    printf("    got: %s\n", s);

  /* ---- RFC 4122 bits -------------------------------------------------- */
  /* An AirPlay client reads `pi` as an identifier and does not validate it, so
   * nothing downstream would complain about a wrong version nibble. */
  int version_ok = 1, variant_ok = 1;
  for (int i = 0; i < 512; i++) {
    uuid_t u;
    uuid_generate_random(u);
    if ((u[6] & 0xF0) != 0x40)
      version_ok = 0;
    if ((u[8] & 0xC0) != 0x80)
      variant_ok = 0;
  }
  ok(version_ok, "generate: version 4 in the high nibble of octet 6, 512/512");
  ok(variant_ok, "generate: variant 10x in the top bits of octet 8, 512/512");

  /* ---- it actually varies --------------------------------------------- */
  /* The failure that matters is two Echos minting the same identifier. A
   * constant or a zeroed buffer would pass every check above. */
  enum { N = 256 };
  static char seen[N][UUID_STR_LEN];
  int dupes = 0, all_same_as_first = 1;
  for (int i = 0; i < N; i++) {
    mint(seen[i]);
    for (int j = 0; j < i; j++)
      if (strcmp(seen[i], seen[j]) == 0)
        dupes++;
    if (strcmp(seen[i], seen[0]) != 0)
      all_same_as_first = 0;
  }
  ok(dupes == 0, "generate: 256 draws, no repeats");
  ok(!all_same_as_first, "generate: draws differ from one another");

  /* Every bit position must move across the draws, or a source that fills only
   * part of the buffer passes the uniqueness check on the rest. The version
   * and variant octets are excluded: they are fixed BY DESIGN. */
  unsigned char ones[16], zeros[16];
  memset(ones, 0, sizeof ones);
  memset(zeros, 0, sizeof zeros);
  for (int i = 0; i < 512; i++) {
    uuid_t u;
    uuid_generate_random(u);
    for (int b = 0; b < 16; b++) {
      ones[b] |= u[b];
      zeros[b] |= (unsigned char)~u[b];
    }
  }
  int stuck = 0;
  for (int b = 0; b < 16; b++) {
    unsigned char mask = 0xFF;
    if (b == 6)
      mask = 0x0F; /* version nibble is fixed */
    if (b == 8)
      mask = 0x3F; /* variant bits are fixed */
    if ((ones[b] & mask) != mask || (zeros[b] & mask) != mask)
      stuck++;
  }
  ok(stuck == 0, "generate: every free bit takes both values over 512 draws");

  /* ---- the fallback --------------------------------------------------- */
  /* It is not a CSPRNG and is not claimed to be, but it must still give two
   * devices different identifiers — which is the only property it exists for. */
  unsigned char a[16], b[16];
  weak_fill(a, sizeof a);
  weak_fill(b, sizeof b);
  ok(memcmp(a, b, sizeof a) != 0, "fallback: two consecutive fills differ");
  int nonzero = 0;
  for (size_t i = 0; i < sizeof a; i++)
    if (a[i])
      nonzero++;
  ok(nonzero > 8, "fallback: fills the whole buffer, not a few bytes");

  printf("\n%s\n", failures ? "FAILURES" : "all checks passed");
  return failures ? 1 : 0;
}
