/* mdnsprobe — ask the network an mDNS question and print who answers.
 *
 * Freestanding: no libc at all, only raw ARM EABI syscalls. Two reasons.
 * It has to cross the device's shell plane as base64, and a static glibc
 * build is 455KB where this is a few KB. And it has to run on bionic, so
 * depending on no libc at all removes the question entirely.
 *
 * It binds an EPHEMERAL port and sets the unicast-response (QU) bit, so it
 * never binds 5353 and cannot disturb the responders it is measuring — which
 * matters, because whether two responders on one host interfere is exactly
 * the question being asked.
 */

#define SYS_exit 1
#define SYS_write 4
/* ARM EABI has DIRECT socket syscalls; it does not go through socketcall the
 * way i386 does. Using 102 (socketcall) returns ENOSYS here, which surfaced
 * as a bare "socket failed" on the device. */
#define SYS_socket 281
#define SYS_bind 282
#define SYS_sendto 290
#define SYS_recvfrom 292
#define SYS_setsockopt 294

static inline long sys3(long n, long a, long b, long c) {
	register long r7 __asm__("r7") = n;
	register long r0 __asm__("r0") = a;
	register long r1 __asm__("r1") = b;
	register long r2 __asm__("r2") = c;
	__asm__ volatile("svc 0" : "+r"(r0) : "r"(r7), "r"(r1), "r"(r2) : "memory");
	return r0;
}

static inline long sys6(long n, long a, long b, long c, long d, long e, long f) {
	register long r7 __asm__("r7") = n;
	register long r0 __asm__("r0") = a;
	register long r1 __asm__("r1") = b;
	register long r2 __asm__("r2") = c;
	register long r3 __asm__("r3") = d;
	register long r4 __asm__("r4") = e;
	register long r5 __asm__("r5") = f;
	__asm__ volatile("svc 0" : "+r"(r0)
		: "r"(r7), "r"(r1), "r"(r2), "r"(r3), "r"(r4), "r"(r5) : "memory");
	return r0;
}

static void wr(const char *s, int n) { sys3(SYS_write, 1, (long)s, n); }
static int slen(const char *s) { int n = 0; while (s[n]) n++; return n; }
static void w(const char *s) { wr(s, slen(s)); }
/* Divide by ten by hand. ARM has no hardware division, so `v / 10` emits a
 * call to __aeabi_uidivmod, and libgcc's copy of that drags in `raise` — a
 * libc symbol this freestanding binary deliberately does not have. */
static unsigned long div10(unsigned long v, unsigned long *rem) {
	unsigned long q = 0, bit = 1, d = 10;
	while (d <= v && !(d & 0x80000000UL)) { d <<= 1; bit <<= 1; }
	while (bit) {
		if (v >= d) { v -= d; q |= bit; }
		d >>= 1; bit >>= 1;
	}
	*rem = v;
	return q;
}
static void wn(unsigned long v) {
	char b[24]; int i = 24;
	if (!v) { w("0"); return; }
	while (v) { unsigned long r; v = div10(v, &r); b[--i] = '0' + (char)r; }
	wr(b + i, 24 - i);
}
static void wip(const unsigned char *p) {
	for (int i = 0; i < 4; i++) { wn(p[i]); if (i < 3) w("."); }
}

struct sa_in { unsigned short fam; unsigned short port; unsigned char addr[4]; unsigned char pad[8]; };
struct tv { long sec; long usec; };

static unsigned char buf[9000];

static int rdname(unsigned char *m, int len, int off, char *out, int outsz) {
	int jumped = 0, end = off, n = 0, guard = 0;
	out[0] = 0;
	while (off < len && guard++ < 128) {
		int l = m[off];
		if (l == 0) { off++; if (!jumped) end = off; break; }
		if ((l & 0xc0) == 0xc0) {
			if (off + 1 >= len) break;
			int ptr = ((l & 0x3f) << 8) | m[off + 1];
			if (!jumped) end = off + 2;
			jumped = 1; off = ptr; continue;
		}
		if (off + 1 + l > len) break;
		if (n && n < outsz - 1) out[n++] = '.';
		for (int i = 0; i < l && n < outsz - 1; i++) out[n++] = m[off + 1 + i];
		out[n] = 0;
		off += 1 + l;
		if (!jumped) end = off;
	}
	return end;
}

static const char *tname(int t) {
	if (t == 1) return "A"; if (t == 12) return "PTR"; if (t == 16) return "TXT";
	if (t == 28) return "AAAA"; if (t == 33) return "SRV";
	return "?";
}

void _start_c(int argc, char **argv) {
	if (argc < 2) { w("usage: probe <service> [seconds]\n"); sys3(SYS_exit, 2, 0, 0); }
	int secs = 6;
	if (argc > 2) { secs = 0; for (char *p = argv[2]; *p >= '0' && *p <= '9'; p++) secs = secs * 10 + (*p - '0'); }
	if (secs < 1) secs = 6;

	int s = sys3(SYS_socket, 2 /*AF_INET*/, 2 /*SOCK_DGRAM*/, 0);
	if (s < 0) { w("socket failed, errno "); wn(-s); w("\n"); sys3(SYS_exit, 1, 0, 0); }

	struct sa_in me; for (unsigned i = 0; i < sizeof me; i++) ((char *)&me)[i] = 0;
	me.fam = 2;
	long rc = sys3(SYS_bind, s, (long)&me, sizeof me);
	if (rc < 0) { w("bind failed, errno "); wn(-rc); w("\n"); sys3(SYS_exit, 1, 0, 0); }

	/* One-second receive timeout, so the loop can re-ask and still finish. */
	struct tv t1; t1.sec = 1; t1.usec = 0;
	sys6(SYS_setsockopt, s, 1 /*SOL_SOCKET*/, 20 /*SO_RCVTIMEO*/,
	     (long)&t1, sizeof t1, 0);

	unsigned char q[512]; int n = 0;
	for (int i = 0; i < 12; i++) q[i] = 0;
	q[5] = 1; n = 12;
	char *p = argv[1];
	while (*p) {
		int l = 0; while (p[l] && p[l] != '.') l++;
		if (l) { q[n++] = l; for (int i = 0; i < l; i++) q[n++] = p[i]; }
		p += l; if (*p == '.') p++;
	}
	q[n++] = 0;
	q[n++] = 0; q[n++] = 12;        /* PTR */
	q[n++] = 0x80; q[n++] = 1;      /* IN + unicast-response bit */

	struct sa_in to; for (unsigned i = 0; i < sizeof to; i++) ((char *)&to)[i] = 0;
	to.fam = 2; to.port = (5353 >> 8) | ((5353 & 0xff) << 8);
	to.addr[0] = 224; to.addr[1] = 0; to.addr[2] = 0; to.addr[3] = 251;
	/* A third argument aims the query at ONE responder instead of the group.
	 * Multicast leaving the box and a responder choosing to answer are two
	 * different failures, and they look identical from here without this. */
	if (argc > 3) {
		char *ip = argv[3]; int oct = 0, v = 0, any = 0;
		for (char *c = ip;; c++) {
			if (*c >= '0' && *c <= '9') { v = v * 10 + (*c - '0'); any = 1; }
			else if ((*c == '.' || *c == 0) && any) {
				if (oct < 4) to.addr[oct++] = (unsigned char)v;
				v = 0; any = 0;
				if (*c == 0) break;
			} else if (*c == 0) break;
		}
		w("aiming at "); wip(to.addr); w("\n");
	}

	w("probing "); w(argv[1]); w(" for "); wn(secs); w("s\n");

	int replies = 0;
	for (int round = 0; round < secs; round++) {
		/* Re-ask for the first three seconds: mDNS is lossy by design and this
		 * link has been measured at 4.6-7.1% loss, so a single unanswered
		 * query proves nothing. */
		if (round < 3) {
			long sent = sys6(SYS_sendto, s, (long)q, n, 0, (long)&to, sizeof to);
			if (sent < 0) { w("sendto failed, errno "); wn(-sent); w("\n"); }
		}
		for (;;) {
			struct sa_in from; int fl = sizeof from;
			int r = sys6(SYS_recvfrom, s, (long)buf, sizeof buf, 0,
			             (long)&from, (long)&fl);
			if (r < 12) break;
			replies++;
			int qd = (buf[4] << 8) | buf[5], an = (buf[6] << 8) | buf[7];
			int ns = (buf[8] << 8) | buf[9], ar = (buf[10] << 8) | buf[11];
			w("\n=== "); wn(r); w(" bytes from "); wip(from.addr);
			w(" - "); wn(an); w(" answers, "); wn(ar); w(" additional\n");
			int off = 12; char nm[300];
			for (int i = 0; i < qd && off < r; i++) { off = rdname(buf, r, off, nm, sizeof nm); off += 4; }
			for (int i = 0; i < an + ns + ar && off < r; i++) {
				off = rdname(buf, r, off, nm, sizeof nm);
				if (off + 10 > r) break;
				int ty = (buf[off] << 8) | buf[off + 1];
				int dl = (buf[off + 8] << 8) | buf[off + 9];
				off += 10;
				if (off + dl > r) break;
				w("  -> "); w(tname(ty)); w(" "); w(nm); w("  ");
				if (ty == 12) { char d[300]; rdname(buf, r, off, d, sizeof d); w(d); }
				else if (ty == 33 && dl >= 7) {
					char d[300]; rdname(buf, r, off + 6, d, sizeof d);
					w("port "); wn((buf[off + 4] << 8) | buf[off + 5]); w(" target "); w(d);
				} else if (ty == 1 && dl == 4) { wip(buf + off); }
				else if (ty == 16) {
					int pp = off, lim = off + dl, shown = 0;
					while (pp < lim && shown < 10) {
						int l = buf[pp];
						if (!l || pp + 1 + l > lim) break;
						wr((char *)buf + pp + 1, l); w(" | ");
						pp += 1 + l; shown++;
					}
				} else { wn(dl); w(" bytes"); }
				w("\n");
				off += dl;
			}
		}
	}
	w("\n"); wn(replies); w(" reply packet(s)\n");
	sys3(SYS_exit, 0, 0, 0);
}

__asm__(
	".global _start\n"
	"_start:\n"
	"  ldr r0, [sp]\n"          /* argc */
	"  add r1, sp, #4\n"        /* argv */
	"  bl _start_c\n"
	"  mov r7, #1\n"
	"  mov r0, #0\n"
	"  svc 0\n"
);
