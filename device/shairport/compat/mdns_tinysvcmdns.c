/*
 * mdns_tinysvcmdns.c — Revoice's replacement for shairport-sync's bundled
 * tinysvcmdns backend, so that AirPlay 2 can be discovered.
 *
 * This file REPLACES the upstream file of the same name at build time
 * (build.sh copies it over the checkout). It is not a patch, deliberately:
 * `--with-tinysvcmdns` already compiles this filename, so nothing in
 * configure.ac, Makefile.am or mdns.c has to be touched and the 4.3.7 pin
 * stays as movable as it was. A patch against upstream's body would conflict
 * on every bump; a whole file we own does not. The one thing that CAN drift
 * under us is the `mdns_backend` struct's shape — and that is a compile
 * error, not a silent one.
 *
 * # Why it exists
 *
 * AirPlay 2 is advertised as TWO services: `_raop._tcp` exactly as classic
 * AirPlay, and `_airplay._tcp` carrying the features, the public key and the
 * group state. Upstream's backend takes `ap2name` and `secondary_txt_records`
 * and declares both `__attribute__((unused))`, and sets no `mdns_update` at
 * all. So a build configured `--with-airplay-2 --with-tinysvcmdns`
 * configures, compiles, links and RUNS — and is never once offered to a
 * phone as an AirPlay 2 device, with nothing logged at either end. Only
 * `mdns_avahi.c` implements the second service upstream, which is where the
 * "AirPlay 2 requires Avahi" belief comes from; configure.ac enforces no such
 * thing. See #79.
 *
 * # Why an update tears the responder down and starts it again
 *
 * This is the part worth reading before "optimising" it.
 *
 * `rtsp.c` calls `mdns_update(NULL, secondary_txt_records)` at four points,
 * and every one of them is a change to the AirPlay 2 group state: `flags`
 * gains and loses DeviceSupportsRelay on SETUP and TEARDOWN, and `gid` /
 * `gcgl` / `isGroupLeader` change as the speaker joins and leaves a group.
 *
 * The obvious implementation is to find the TXT record and edit it in place.
 * tinysvcmdns even makes that *look* available: `struct rr_entry` is a
 * complete type in the public header and `rr_add_txt` is exported. It is
 * still wrong, for a reason that has nothing to do with the data race
 * (`struct mdnsd` is opaque, so its `data_lock` is out of reach anyway):
 * **tinysvcmdns announces on registration and at no other time, and its
 * records carry a 4500-second TTL.** A record edited in place is a record
 * every client on the network goes on ignoring for up to seventy-five
 * minutes, because it already has the old copy and no reason to ask again.
 * The edit would appear to work, in the sense that the responder's answers
 * would be correct, and grouping would still be broken.
 *
 * Re-announcing is the thing that has to happen, and `mdnsd_start()` is the
 * only code here that announces. So an update is a restart. It costs the
 * responder thread a stop and a start — bounded by mdnsd_stop's 500ms poll —
 * at a moment that is already a session boundary.
 *
 * # What is deliberately NOT changed
 *
 * The primary service's TXT records are still built from mdns.h's
 * MDNS_RECORD_* macros rather than from the `txt_records` argument, exactly
 * as upstream does. The two sets are not the same — the macros carry `am=`,
 * `vs=`, `sf=`, `fv=` and `tp=`, which `build_bonjour_strings` does not put in
 * the array — so using the argument would quietly change what classic AirPlay
 * advertises. This file is here to ADD the second service, not to renegotiate
 * the first.
 */

#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
#include <pthread.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "common.h"
#include "mdns.h"

#include "mdns_ap2.h"
#include "tinysvcmdns.h"

static struct mdnsd *svr = NULL;

/* What is on the air. Held across an update because shairport resends only the
 * secondary half, and because a restart has to rebuild the whole
 * advertisement from something. */
static struct em_ad ad;

/* register / update / unregister are called from different threads — the
 * first from the main thread at startup, the others from RTSP connection
 * threads — and an update frees and replaces `svr`. Without this, a TEARDOWN
 * racing a SETUP can stop the responder twice. */
static pthread_mutex_t ad_lock = PTHREAD_MUTEX_INITIALIZER;

/* Give the responder this host's name and every non-loopback address it has.
 * Lifted from upstream's version of this file, with its two leaks fixed: it
 * returned on failure without freeing the list, and its final freeifaddrs()
 * was handed the loop variable, which is NULL by then. Ours frees the head.
 */
static int set_hostname_and_addresses(void) {
  char hostname[128];
  char withlocal[160];
  struct ifaddrs *ifalist = NULL, *ifa = NULL, *primary = NULL;

  if (gethostname(hostname, sizeof(hostname) - 1) != 0) {
    warn("tinysvcmdns: gethostname() failed");
    return -1;
  }
  hostname[sizeof(hostname) - 1] = 0; /* POSIX permits truncation with no NUL */

  if (em_hostname_local(hostname, withlocal, sizeof(withlocal)) != 0) {
    warn("tinysvcmdns: cannot form a .local hostname from \"%s\"", hostname);
    return -1;
  }

  if (getifaddrs(&ifalist) < 0) {
    warn("tinysvcmdns: getifaddrs() failed");
    return -1;
  }

  /* The first non-loopback address becomes the responder's own; the rest are
   * added as extra A/AAAA records for the same name. */
  for (ifa = ifalist; ifa != NULL; ifa = ifa->ifa_next) {
    if (config.interface != NULL && strcmp(config.interface, ifa->ifa_name) != 0)
      continue;
    if ((ifa->ifa_flags & IFF_LOOPBACK) || ifa->ifa_addr == NULL)
      continue;

    if (ifa->ifa_addr->sa_family == AF_INET) {
      uint32_t ip = ((struct sockaddr_in *)ifa->ifa_addr)->sin_addr.s_addr;
      mdnsd_set_hostname(svr, withlocal, ip);
      primary = ifa;
      break;
    } else if (ifa->ifa_addr->sa_family == AF_INET6) {
      struct in6_addr *a6 = &((struct sockaddr_in6 *)ifa->ifa_addr)->sin6_addr;
      mdnsd_set_hostname_v6(svr, withlocal, a6);
      primary = ifa;
      break;
    }
  }

  if (primary == NULL) {
    warn("tinysvcmdns: no non-loopback ipv4 or ipv6 interface found");
    freeifaddrs(ifalist);
    return -1;
  }

  for (ifa = primary->ifa_next; ifa != NULL; ifa = ifa->ifa_next) {
    if (ifa->ifa_addr == NULL || (ifa->ifa_flags & IFF_LOOPBACK))
      continue;
    if (config.interface != NULL && strcmp(config.interface, ifa->ifa_name) != 0)
      continue;

    switch (ifa->ifa_addr->sa_family) {
    case AF_INET: {
      uint32_t ip = ((struct sockaddr_in *)ifa->ifa_addr)->sin_addr.s_addr;
      mdnsd_add_rr(svr, rr_create_a(create_nlabel(withlocal), ip));
    } break;
    case AF_INET6: {
      struct in6_addr *a6 = &((struct sockaddr_in6 *)ifa->ifa_addr)->sin6_addr;
      mdnsd_add_rr(svr, rr_create_aaaa(create_nlabel(withlocal), a6));
    } break;
    default:
      break;
    }
  }

  freeifaddrs(ifalist);
  return 0;
}

static int register_one(const char *instance, const char *regtype, const char *txt[]) {
  char type[128];
  if (regtype == NULL || em_regtype_local(regtype, type, sizeof(type)) != 0) {
    warn("tinysvcmdns: cannot form a service type from \"%s\"", regtype ? regtype : "(null)");
    return -1;
  }
  struct mdns_service *svc = mdnsd_register_svc(svr, instance, type, ad.port, NULL, txt);
  if (svc == NULL) {
    warn("tinysvcmdns: could not register %s as \"%s\"", type, instance);
    return -1;
  }
  /* Frees the wrapper only — the records themselves belong to the responder
   * and are freed by mdnsd_stop. Upstream does the same. */
  mdns_service_destroy(svc);
  return 0;
}

/* Start a responder and put the whole current advertisement on it. Called for
 * the first registration and again for every update. Caller holds ad_lock. */
static int bring_up(void) {
  svr = mdnsd_start();
  if (svr == NULL) {
    warn("tinysvcmdns: mdnsd_start() failed");
    return -1;
  }

  if (set_hostname_and_addresses() != 0) {
    mdnsd_stop(svr); /* upstream leaked the responder and its thread here */
    svr = NULL;
    return -1;
  }

  /* The primary service's records, upstream's set verbatim — see the header
   * comment for why the passed-in txt_records are not used. These are built
   * here rather than returned from a helper because the macros are not
   * constant expressions (config.password decides the last one), so they
   * cannot initialise anything with static storage. */
  char *txt_without[] = {MDNS_RECORD_WITHOUT_METADATA, NULL};
#ifdef CONFIG_METADATA
  char *txt_with[] = {MDNS_RECORD_WITH_METADATA, NULL};
#endif
  char **primary = txt_without;
#ifdef CONFIG_METADATA
  if (config.metadata_enabled)
    primary = txt_with;
#endif

  if (register_one(ad.ap1name, config.regtype, (const char **)primary) != 0) {
    mdnsd_stop(svr);
    svr = NULL;
    return -1;
  }

  /* The second service is the whole point of this file, and it is also the
   * half that must not take the first one down with it: a device that answers
   * classic AirPlay is worth more than one that answers nothing. So a failure
   * here is loud and survivable. */
  if (em_ad_has_second_service(&ad, config.regtype2)) {
    if (register_one(ad.ap2name, config.regtype2, (const char **)ad.secondary) != 0)
      warn("tinysvcmdns: %s is advertised but %s is NOT — this device will be found for "
           "classic AirPlay and not for AirPlay 2",
           config.regtype, config.regtype2);
    else
      debug(1, "tinysvcmdns: advertised %s as \"%s\" and %s as \"%s\".", config.regtype,
            ad.ap1name, config.regtype2, ad.ap2name);
  } else {
    debug(1, "tinysvcmdns: advertised %s as \"%s\"; no AirPlay 2 service.", config.regtype,
          ad.ap1name);
  }

  return 0;
}

/* Caller holds ad_lock. */
static void tear_down(void) {
  if (svr != NULL) {
    mdnsd_stop(svr);
    svr = NULL;
  }
}

static int mdns_tinysvcmdns_register(char *ap1name, char *ap2name, int port, char **txt_records,
                                     char **secondary_txt_records) {
  (void)txt_records; /* see the header comment */
  int rc;

  pthread_mutex_lock(&ad_lock);
  tear_down(); /* mdns_register is called once, but make it idempotent */
  if (em_ad_set(&ad, ap1name, ap2name, port, secondary_txt_records) != 0) {
    warn("tinysvcmdns: out of memory building the advertisement");
    pthread_mutex_unlock(&ad_lock);
    return -1;
  }
  rc = bring_up();
  pthread_mutex_unlock(&ad_lock);
  return rc;
}

static int mdns_tinysvcmdns_update(char **txt_records, char **secondary_txt_records) {
  (void)txt_records;
  int rc;

  pthread_mutex_lock(&ad_lock);
  if (ad.ap1name == NULL) {
    /* Nothing registered. Upstream's mdns.c only calls update through
     * config.mdns, which is set by a successful register, so this is a
     * can't-happen — and a can't-happen that restarts a responder from an
     * empty advertisement would take the device off the air. */
    pthread_mutex_unlock(&ad_lock);
    debug(1, "tinysvcmdns: update before register, ignored.");
    return -1;
  }

  if (em_ad_update(&ad, secondary_txt_records) != 0) {
    warn("tinysvcmdns: out of memory updating the advertisement; keeping the old records");
    pthread_mutex_unlock(&ad_lock);
    return -1;
  }

  /* Restart, because announcing is what makes the change visible — an edited
   * record with a 4500-second TTL is not. */
  tear_down();
  rc = bring_up();
  if (rc != 0)
    warn("tinysvcmdns: the responder did not come back after an update — this device is no "
         "longer advertised");
  pthread_mutex_unlock(&ad_lock);
  return rc;
}

static void mdns_tinysvcmdns_unregister(void) {
  pthread_mutex_lock(&ad_lock);
  tear_down();
  em_ad_free(&ad);
  pthread_mutex_unlock(&ad_lock);
}

mdns_backend mdns_tinysvcmdns = {.name = "tinysvcmdns",
                                 .mdns_register = mdns_tinysvcmdns_register,
                                 .mdns_update = mdns_tinysvcmdns_update,
                                 .mdns_unregister = mdns_tinysvcmdns_unregister,
                                 .mdns_dacp_monitor_start = NULL,
                                 .mdns_dacp_monitor_set_id = NULL,
                                 .mdns_dacp_monitor_stop = NULL};
