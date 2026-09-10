/*
 * ifaddrs_alias.c — the real getifaddrs/freeifaddrs names, for librespot.
 *
 * device/shairport/compat/android_ifaddrs.c implements both over netlink,
 * because bionic declares them __INTRODUCED_IN(24) and FireOS 5 is API 22.
 * But it defines them as `em_getifaddrs` / `em_freeifaddrs`, and shairport
 * reaches them through `#define getifaddrs em_getifaddrs` in
 * android_compat.h, injected with -include at compile time.
 *
 * A C MACRO CANNOT REACH RUST. librespot's `if_addrs` crate — pulled in by
 * with-libmdns — emits a call to the real symbol, so the preprocessor trick
 * that serves shairport leaves the link with exactly the undefined references
 * it was meant to fix. Measured: the object was on the linker command line
 * and the symbols were still missing, which is what sent this looking at the
 * NAMES rather than at the link order.
 *
 * So: real definitions, forwarding. Deliberately NOT `--defsym` at link time,
 * which looks tidier and is a trap on ARM — an absolute symbol alias does not
 * carry the Thumb bit, and a call through it lands in ARM state on a Thumb
 * function. A wrapper is two instructions and cannot be wrong that way.
 *
 * Not moved into android_ifaddrs.c: shairport's build injects the macro
 * header, so defining these names there would collide with its own #define.
 */

#include <ifaddrs.h>

int em_getifaddrs(struct ifaddrs **ifap);
void em_freeifaddrs(struct ifaddrs *ifa);

int getifaddrs(struct ifaddrs **ifap) { return em_getifaddrs(ifap); }

void freeifaddrs(struct ifaddrs *ifa) { em_freeifaddrs(ifa); }
