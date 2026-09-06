/*
 * android_compat.c — deferred pthread cancellation on bionic
 *
 * The design, the reasoning and the stated limit are in android_compat.h.
 * This file is the mechanism.
 */

#ifdef __ANDROID__

#define _GNU_SOURCE
#include <errno.h>
#include <pthread.h>
#include <signal.h>
#include <stddef.h>

/*
 * The header's macros rename the pthread_* names onto ours. In THIS file we
 * need the real ones — em_pthread_cancel has to call the genuine
 * pthread_kill, and nothing here may recurse into itself — so the header is
 * deliberately not included and the prototypes are repeated instead.
 */
int em_cancel_signal(void);
int em_pthread_cancel(pthread_t thread);
int em_pthread_setcancelstate(int state, int *oldstate);
int em_pthread_setcanceltype(int type, int *oldtype);
void em_pthread_testcancel(void);

#define EM_CANCEL_ENABLE 0
#define EM_CANCEL_DISABLE 1
#define EM_CANCELED ((void *)-1)

/*
 * Per-thread cancellation state.
 *
 * __thread rather than pthread_key_create plus a lookup: it needs no
 * initialisation call, so there is no ordering question about whether a
 * thread has been through a setup function before it is first cancelled —
 * and a thread created by a library we do not control would lose that race.
 * Zero-initialised means every thread starts ENABLE with nothing pending,
 * which is exactly the POSIX default.
 *
 * `pending` is what makes DISABLE mean something. A cancel arriving while
 * cancellation is off is recorded here and acted on later, rather than
 * dropped (which leaks the thread) or obeyed at once (which is the
 * tear-down-inside-a-critical-section the caller was protecting against).
 */
static __thread int em_cancel_state = EM_CANCEL_ENABLE;
static __thread int em_cancel_pending = 0;

/*
 * SIGRTMIN is a FUNCTION on bionic rather than a constant, so the signal
 * number cannot be a compile-time #define and is resolved here. +2 leaves
 * the first two real-time signals to the C library, which reserves some.
 */
int em_cancel_signal(void) { return SIGRTMIN + 2; }

/*
 * Exits the thread, running the pthread_cleanup_push handlers bionic does
 * support. This is the not-async-signal-safe step named in the header; it is
 * reached only with cancellation ENABLED, which is the whole reason
 * em_pthread_setcancelstate is implemented properly rather than stubbed.
 */
static void em_cancel_handler(int sig) {
  (void)sig;
  if (em_cancel_state == EM_CANCEL_DISABLE) {
    /*
     * Deferred: remember it and return, so the interrupted syscall reports
     * EINTR and the thread carries on to the end of its critical section.
     * Delivery happens at setcancelstate(ENABLE) or testcancel().
     */
    em_cancel_pending = 1;
    return;
  }
  pthread_exit(EM_CANCELED);
}

static pthread_once_t em_cancel_once = PTHREAD_ONCE_INIT;

/*
 * SA_RESTART is deliberately NOT set. Restarting is the opposite of what is
 * wanted: every one of shairport's cancel targets is a thread parked in
 * read(), poll() or accept(), and interrupting that syscall is how the
 * thread gets far enough to notice. With SA_RESTART the kernel resumes the
 * call and a deferred cancel sits unnoticed until the socket happens to say
 * something — which for an idle RTSP listener is never.
 */
static void em_cancel_install(void) {
  struct sigaction sa;
  sigemptyset(&sa.sa_mask);
  sa.sa_flags = 0;
  sa.sa_handler = em_cancel_handler;
  sigaction(em_cancel_signal(), &sa, NULL);
}

int em_pthread_cancel(pthread_t thread) {
  pthread_once(&em_cancel_once, em_cancel_install);
  /*
   * ESRCH from a thread that has already exited is the ordinary case here,
   * not a fault: shairport cancels threads that may have returned on their
   * own, and glibc answers the same way. Returned rather than logged — the
   * callers check it or ignore it exactly as they did before.
   */
  return pthread_kill(thread, em_cancel_signal());
}

int em_pthread_setcancelstate(int state, int *oldstate) {
  if (state != EM_CANCEL_ENABLE && state != EM_CANCEL_DISABLE)
    return EINVAL;
  if (oldstate)
    *oldstate = em_cancel_state;
  em_cancel_state = state;
  /*
   * Re-enabling is a delivery point. Without this, a cancel that arrived
   * during a critical section would wait for a testcancel that most of these
   * call sites never make — the thread would survive its own cancellation
   * and the endpoint would not stop, which is the failure this shim exists
   * to avoid.
   */
  if (state == EM_CANCEL_ENABLE && em_cancel_pending) {
    em_cancel_pending = 0;
    pthread_exit(EM_CANCELED);
  }
  return 0;
}

/*
 * Accepted and recorded nowhere, because the distinction cannot be honoured
 * here: delivery is always at a signal, which is closer to ASYNCHRONOUS than
 * to DEFERRED, and no bookkeeping in this file changes when the kernel runs
 * the handler. Returning success with a plausible old type is still right —
 * every caller in shairport sets a type and ignores the result, and failing
 * would turn a preference into an error on a platform that has no
 * cancellation to configure at all.
 */
int em_pthread_setcanceltype(int type, int *oldtype) {
  (void)type;
  if (oldtype)
    *oldtype = 0; /* PTHREAD_CANCEL_DEFERRED */
  return 0;
}

void em_pthread_testcancel(void) {
  if (em_cancel_pending && em_cancel_state == EM_CANCEL_ENABLE) {
    em_cancel_pending = 0;
    pthread_exit(EM_CANCELED);
  }
}

#endif /* __ANDROID__ */
