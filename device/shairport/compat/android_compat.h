/*
 * android_compat.h — the POSIX that bionic does not have, for shairport-sync
 * =========================================================================
 *
 * Injected into every shairport-sync translation unit with `-include`, so
 * UPSTREAM SOURCES ARE NOT PATCHED. That is the point of doing it this way:
 * the 4.3.7 pin has a reason (5.x removed --with-tinysvcmdns) but it is still
 * a pin, and a patch series against upstream .c files has to be re-landed on
 * every bump. A header that supplies what the platform is missing survives a
 * bump untouched.
 *
 * Two independent gaps, see #20:
 *
 *   1. Thread cancellation. bionic has NO pthread_cancel at any API level —
 *      a design decision, not a missing __INTRODUCED_IN — and no
 *      pthread_setcancelstate or pthread_testcancel either. It DOES have
 *      pthread_cleanup_push/pop, which is what makes this tractable: the
 *      cleanup handler stack exists, and pthread_exit unwinds it.
 *
 *   2. getifaddrs/freeifaddrs, which bionic declares only from API 24. We
 *      build at 22, because that is what FireOS 5 is. See android_ifaddrs.c.
 *
 * ── How cancellation is implemented, and why not more simply ──────────────
 *
 * pthread_cancel sends a signal; its handler calls pthread_exit, which runs
 * the cleanup handlers bionic already supports. The signal is also what makes
 * this work at all rather than merely compile: shairport cancels threads that
 * are BLOCKED IN read()/poll()/accept(), and a signal is what brings those
 * back — a cooperative flag would never be looked at by a thread parked in a
 * syscall.
 *
 * **pthread_setcancelstate is implemented for real and must stay that way.**
 * The tempting version returns 0 and does nothing, and it is actively worse
 * than not building: shairport calls setcancelstate(DISABLE) around critical
 * sections precisely so it cannot be torn down inside them, and a no-op hands
 * the shim permission to do exactly that. So the state lives in TLS, a cancel
 * arriving while disabled is recorded as PENDING rather than acted on, and it
 * is delivered at the next point cancellation is enabled or tested. That is
 * deferred cancellation, faithfully, and it is the difference between a
 * working port and one that corrupts state under load.
 *
 * **Stated limit:** calling pthread_exit from a signal handler is not
 * async-signal-safe by the letter of POSIX. It is the same hazard real
 * asynchronous cancellation carries, and honouring DISABLE is what keeps it
 * out of the regions where it would matter. If a future failure looks like a
 * thread dying with a lock held, this is the first place to look.
 */

#ifndef REVOICE_ANDROID_COMPAT_H
#define REVOICE_ANDROID_COMPAT_H

#ifdef __ANDROID__

#include <pthread.h>
#include <signal.h>

/* ── getifaddrs ───────────────────────────────────────────────────────────
 *
 * bionic ships <ifaddrs.h> with the struct and marks the two functions
 * __INTRODUCED_IN(24), so the struct is usable and the functions are not.
 * Ours are declared here and defined in android_ifaddrs.c, then mapped onto
 * the standard names so upstream call sites need no change.
 *
 * Guarded on the API level rather than made unconditional: on an API 24+
 * build bionic provides them and two definitions would collide.
 */
#if __ANDROID_API__ < 24
#include <ifaddrs.h>
int em_getifaddrs(struct ifaddrs **ifap);
void em_freeifaddrs(struct ifaddrs *ifa);
#define getifaddrs em_getifaddrs
#define freeifaddrs em_freeifaddrs
#endif

/* ── Cancellation ─────────────────────────────────────────────────────────
 *
 * Values match glibc, so code that compares against them or stores one in an
 * int behaves the same way it does everywhere else.
 */
#ifndef PTHREAD_CANCEL_ENABLE
#define PTHREAD_CANCEL_ENABLE 0
#define PTHREAD_CANCEL_DISABLE 1
#endif

#ifndef PTHREAD_CANCEL_DEFERRED
#define PTHREAD_CANCEL_DEFERRED 0
#define PTHREAD_CANCEL_ASYNCHRONOUS 1
#endif

#ifndef PTHREAD_CANCELED
#define PTHREAD_CANCELED ((void *)-1)
#endif

/*
 * The signal carrying a cancel.
 *
 * A real-time signal rather than SIGUSR1/2, because those belong to the
 * application: shairport-sync installs its own SIGUSR handling, and quietly
 * taking one would break whatever uses it in a way that looks like anything
 * but this header. Resolved at runtime from SIGRTMIN, which on bionic is a
 * function call rather than a constant — the low real-time signals are
 * reserved by the C library itself.
 */
int em_cancel_signal(void);

int em_pthread_cancel(pthread_t thread);
int em_pthread_setcancelstate(int state, int *oldstate);
int em_pthread_setcanceltype(int type, int *oldtype);
void em_pthread_testcancel(void);

#define pthread_cancel em_pthread_cancel
#define pthread_setcancelstate em_pthread_setcancelstate
#define pthread_setcanceltype em_pthread_setcanceltype
#define pthread_testcancel em_pthread_testcancel

#endif /* __ANDROID__ */
#endif /* REVOICE_ANDROID_COMPAT_H */
